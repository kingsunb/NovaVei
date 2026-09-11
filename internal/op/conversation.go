package op

// 对话留存: 把终态请求的完整报文按天追加到 data/conversations/YYYY-MM-DD.jsonl,
// 跨天时把前一天压缩为 .gz, 供对话审计与本地模型训练直接使用。
//
// 保留天数与目录硬预算从 settings 实时读取, 设置项缺失或非法时回退到 model 包里的默认常量;
// 三道防线: 目录硬预算 / 开关默认关 / 磁盘剩余 <15% 暂停写入。

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/kingsunb/NovaVei/internal/model"
	"github.com/tidwall/gjson"
)

const (
	conversationFlushInterval = 5 * time.Second
	conversationFilePerm      = 0o600 // 留存内容含用户对话明文, 收紧文件权限
)

var (
	conversationMu sync.Mutex // 保护以下全部可变状态; 临界区只有内存操作与文件 append
	// conversationFlushMu 串行化关闭钩子, 防止两个并发调用同时 close 同一个 stop channel。
	conversationFlushMu sync.Mutex
	conversationDir     string

	conversationPending []model.ConversationRecord
	conversationBytes   int

	conversationDay    string        // 当前活跃文件对应的日期(UTC 与文件名一致用本地日期)
	conversationFile   *os.File      // 当前活跃的 JSONL 文件句柄
	conversationWriter *bufio.Writer // 带缓冲写入, 减少 syscall 次数

	conversationCompactDay string // 待压缩的前一日归档名: ensure 检测到跨天时记录, 由写入协程在锁外执行压缩

	conversationNotify chan struct{} // 容量 1: 有新数据或需要轮转/清理时的唤醒信号
	conversationStop   chan struct{}
	conversationDone   chan struct{}

	conversationDropped     int
	conversationDroppedLast time.Time

	// conversationDiskHasRoom is injectable so tests do not depend on the CI host's
	// current free-space percentage. Production keeps the real statfs implementation.
	conversationDiskHasRoom = diskHasRoomFor

	// conversationFlushIOHook is called during the lock-free I/O phase of a batch
	// flush (conversationFlushBatch), after the pending batch has been swapped out
	// under the short lock and before encoding/writing begins. Tests use it to
	// simulate slow disks (sleep) and observe that the producer is not blocked.
	// Returning a non-nil error aborts the flush and restores the batch, simulating
	// a write failure. Production leaves it nil (no-op).
	conversationFlushIOHook func() error

	// compactHookAfterCopy / compactHookAfterSync / compactHookAfterRename are
	// injectable hooks for testing crash safety of compactYesterday. Each is called
	// at the named point inside compressGzipAtomic; returning a non-nil error
	// aborts at that point, simulating a crash/failure. Production leaves them nil.
	compactHookAfterCopy   func() error
	compactHookAfterSync   func() error
	compactHookAfterRename func() error
)

var ErrConversationNotInitialized = errors.New("conversation store not initialized")

// InitConversationStore 设置留存目录并确保后台写入协程存活; 由 start 在数据库初始化后调用。
// 可重入: 协程已停止(如关闭钩子触发过排空)时自动重建。
func InitConversationStore(dir string) {
	conversationMu.Lock()
	defer conversationMu.Unlock()
	conversationDir = dir
	if conversationAlive() {
		return
	}
	conversationNotify = make(chan struct{}, 1)
	conversationStop = make(chan struct{})
	conversationDone = make(chan struct{})
	go conversationWriterLoop()
}

// ConversationEnabled 返回留存开关(设置项实时生效)。
func ConversationEnabled() bool {
	enabled, err := SettingGetBool(model.SettingKeyConversationLog)
	return err == nil && enabled
}

// CaptureConversation 把一条终态对话移交给留存协程。
// 字符串是不可变的, 直接移交调用方的引用即可, 零拷贝且与后续截断互不影响。
// 关闭状态或目录未初始化时静默丢弃; 缓冲超预算时驱逐最旧条目。
func CaptureConversation(record model.ConversationRecord) {
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}
	conversationMu.Lock()
	defer conversationMu.Unlock()
	if conversationDir == "" || !ConversationEnabled() {
		return
	}
	// FlushConversations 关闭 writer 后，直到 InitConversationStore 重建 writer 前拒绝新记录，
	// 避免数据进入已无人消费的 pending 队列。
	if !conversationAlive() {
		conversationDropLocked("writer stopped")
		return
	}

	size := len(record.RawRequest) + len(record.Response)
	// 单条记录本身超过总预算时直接拒收: 即使队列清空也无法容纳, 计入 dropped 指标。
	// 这使 conversationBytes 成为严格硬上限: 任何时刻 conversationBytes <= ConversationPendingMaxBytes。
	if size > model.ConversationPendingMaxBytes {
		conversationDropLocked("single record exceeds pending budget")
		return
	}
	for len(conversationPending) > 0 && conversationBytes+size > model.ConversationPendingMaxBytes {
		evicted := conversationPending[0]
		conversationBytes -= len(evicted.RawRequest) + len(evicted.Response)
		conversationPending = conversationPending[1:]
		conversationDropLocked("pending budget")
	}
	conversationPending = append(conversationPending, record)
	conversationBytes += size

	select {
	case conversationNotify <- struct{}{}:
	default:
	}
}

// conversationRetentionDays 返回运行期生效的归档保留天数, 设置项缺失或非法时回退默认。
// 调用方需无锁(只读 settings 与 module-level default 常量), 高频热路径请按需缓存。
func conversationRetentionDays() int {
	v, err := SettingGetInt(model.SettingKeyConversationRetentionDays)
	if err != nil || v < model.ConversationRetentionDaysMin {
		return model.DefaultConversationRetentionDays
	}
	if v > model.ConversationRetentionDaysMax {
		return model.ConversationRetentionDaysMax
	}
	return v
}

// conversationDirMaxBytes 返回运行期生效的目录硬预算(字节), 设置项缺失或非法时回退默认。
// 设置项为非数字字符串时 SettingGetInt 返回错误, 此时也回退默认; 超出有效区间时
// 同样按上限或默认值兜底, 避免一次误配置让硬预算失效。
func conversationDirMaxBytes() int64 {
	v, err := SettingGetInt(model.SettingKeyConversationDirMaxGB)
	if err != nil || v < int(model.ConversationDirMaxGBMin) {
		return model.DefaultConversationDirMaxGB * 1024 * 1024 * 1024
	}
	if v > int(model.ConversationDirMaxGBMax) {
		return model.ConversationDirMaxGBMax * 1024 * 1024 * 1024
	}
	return int64(v) * 1024 * 1024 * 1024
}

// FlushConversations 停止后台协程并把待写内容落盘, 供优雅关闭钩子调用(LIFO 先于 db.Close)。
// 幂等: 协程已停止时仅同步补写残留条目; 后续 Capture 会自动重启协程。
//
// 死锁修复要点: 旧实现在持有 conversationMu 的情况下 close(stop) 再等待 writerDone,
// 而 writer 收尾分支同样需要重新加锁, 形成自等待死锁。本实现先把停止信号与等待分离,
// 等待结束后再短暂加锁完成最后排空与文件关闭。
func FlushConversations(ctx context.Context) error {
	conversationFlushMu.Lock()
	defer conversationFlushMu.Unlock()

	// 阶段 1: 在持锁状态下发出停止信号, 随即释放 conversationMu 让 writer 能完成收尾。
	conversationMu.Lock()
	if conversationDir == "" {
		conversationMu.Unlock()
		return nil
	}
	var done chan struct{}
	if conversationStop != nil && conversationAlive() {
		close(conversationStop)
		done = conversationDone
	}
	conversationMu.Unlock()

	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			log.Warnf("conversation flush on shutdown did not finish: %v", ctx.Err())
			return ctx.Err()
		}
	}

	// 阶段 2: writer 已退出, 短暂加锁完成最终排空与文件关闭; 此时不会再有并发 Capture,
	// 因为 Capture 路径需要先重建句柄并由 conversationAlive 重新判定。
	conversationMu.Lock()
	defer conversationMu.Unlock()
	flushConversationLocked(time.Now())
	return closeConversationFileLocked()
}

func conversationAlive() bool {
	if conversationDone == nil {
		return false
	}
	select {
	case <-conversationDone:
		return false
	default:
		return true
	}
}

func conversationWriterLoop() {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("conversation writer panicked: %v", r)
		}
		close(conversationDone)
	}()

	ticker := time.NewTicker(conversationFlushInterval)
	cleanTicker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	defer cleanTicker.Stop()

	for {
		select {
		case <-conversationNotify:
			conversationFlushBatch()
			runConversationHousekeeping()
		case <-ticker.C:
			conversationFlushBatch()
			runConversationHousekeeping()
		case <-cleanTicker.C:
			runConversationHousekeeping()
		case <-conversationStop:
			conversationMu.Lock()
			flushConversationLocked(time.Now())
			_ = closeConversationFileLocked()
			conversationMu.Unlock()
			return
		}
	}
}

// runConversationHousekeeping 在锁外执行跨天压缩与归档清理。两类操作都是纯文件 IO,
// 只触碰已关闭的历史文件与过期归档, 而写入协程是唯一的追加者与清理者, 无需持有
// conversationMu; 持锁执行会让 CaptureConversation(进而在 relay 全局状态锁内的
// 请求定稿路径)被最大可达数 GB 的 gzip 压缩阻塞数分钟。
func runConversationHousekeeping() {
	conversationMu.Lock()
	day := conversationCompactDay
	conversationCompactDay = ""
	conversationMu.Unlock()
	if day != "" {
		compactYesterday(day)
	}
	cleanupConversations(time.Now())
}

// flushConversationLocked 把待写条目顺序追加到当日 JSONL; 调用方必须持有 conversationMu。
func flushConversationLocked(now time.Time) {
	if len(conversationPending) == 0 {
		return
	}
	day := now.Format("2006-01-02")

	if !conversationDiskHasRoom(conversationDir) {
		conversationDropLocked("disk watermark")
		return
	}
	if err := ensureConversationFileLocked(day); err != nil {
		conversationDropLocked(err.Error())
		return
	}

	succeeded := 0
	writtenBytes := 0
	for i := range conversationPending {
		entry := &conversationPending[i]
		entryBytes := len(entry.RawRequest) + len(entry.Response)
		extractConversationMessages(entry)
		line, err := json.Marshal(entry)
		if err != nil {
			log.Warnf("failed to marshal conversation entry: %v", err)
			break
		}
		line = append(line, '\n')
		if _, err := conversationWriter.Write(line); err != nil {
			log.Warnf("failed to write conversation entry: %v", err)
			break
		}
		succeeded++
		writtenBytes += entryBytes
	}
	if err := conversationWriter.Flush(); err != nil {
		log.Warnf("failed to flush conversation file: %v", err)
		// Flush 失败时不能确认本轮写入已经落盘，保留整个批次等待后续重试。
		return
	}
	if succeeded == 0 {
		return
	}
	conversationBytes -= writtenBytes
	conversationPending = conversationPending[succeeded:]
	if conversationBytes < 0 {
		conversationBytes = 0
	}
}

// conversationFlushBatch 是写入协程热路径的落盘入口, 采用短锁交换 batch + 锁外 I/O,
// 避免慢盘的编码/磁盘写/flush 阻持 conversationMu 而把 CaptureConversation(进而
// relay 定稿路径)卡住。三阶段:
//  1. 短锁: 检查待写、磁盘水位、确保当日文件句柄, 把整个 pending batch 交换到局部
//     切片并清零全局队列/字节计数, 随即释放锁。
//  2. 锁外: 对局部 batch 做编码/磁盘写/flush——这是可能耗时数秒的慢盘操作。
//  3. 短锁: 按成功/失败更新账本。全成功则 batch 已消费; flush 失败则整批回挂队列
//     等待重试; 部分成功则回挂未写尾部。期间 producer 追加的新记录始终在队列尾,
//     回挂时前置, 顺序不乱。
//
// 与 flushConversationLocked 的区别: 后者在锁内完成全部 I/O, 仅供已确认无并发
// 的同步路径(FlushConversations 排空、ClearConversationArchives)使用。
func conversationFlushBatch() {
	// 阶段 1: 短锁交换 batch。
	conversationMu.Lock()
	if len(conversationPending) == 0 {
		conversationMu.Unlock()
		return
	}
	now := time.Now()
	if !conversationDiskHasRoom(conversationDir) {
		conversationDropLocked("disk watermark")
		conversationMu.Unlock()
		return
	}
	if err := ensureConversationFileLocked(now.Format("2006-01-02")); err != nil {
		conversationDropLocked(err.Error())
		conversationMu.Unlock()
		return
	}
	batch := conversationPending
	conversationPending = nil
	batchBytes := conversationBytes
	conversationBytes = 0
	writer := conversationWriter
	conversationMu.Unlock()

	// 阶段 2: 锁外 I/O。注入点: 测试模拟慢盘或写失败。
	if conversationFlushIOHook != nil {
		if herr := conversationFlushIOHook(); herr != nil {
			conversationMu.Lock()
			conversationPending = append(batch, conversationPending...)
			conversationBytes += batchBytes
			conversationMu.Unlock()
			return
		}
	}
	succeeded, writtenBytes, flushOK := writeConversationBatch(writer, batch)

	// 阶段 3: 短锁更新账本。
	conversationMu.Lock()
	defer conversationMu.Unlock()
	if !flushOK {
		// Flush 失败: 不能确认落盘, 整批回挂等待重试(与 flushConversationLocked 一致)。
		conversationPending = append(batch, conversationPending...)
		conversationBytes += batchBytes
		return
	}
	if succeeded == 0 {
		// 无条目成功写入但 flush 通过: 整批回挂。
		conversationPending = append(batch, conversationPending...)
		conversationBytes += batchBytes
		return
	}
	// 部分或全部成功: 未写尾部回挂队列首(保持先来先服务), 已写部分的字节已随
	// 阶段 1 清零而扣除, 此处只把尾部字节加回。
	if succeeded < len(batch) {
		remaining := batch[succeeded:]
		conversationPending = append(remaining, conversationPending...)
		for i := range remaining {
			conversationBytes += len(remaining[i].RawRequest) + len(remaining[i].Response)
		}
	}
	_ = writtenBytes // 已在阶段 1 清零全局计数, 无需再减。
}

// writeConversationBatch 把 batch 逐条 JSON 编码后写入 writer 并 flush。
// 返回 (成功条数, 成功条目的原始字节合计, flush 是否成功)。编码/写入遇错即 break,
// 仍尝试 flush 已缓冲字节。纯函数式: 只操作传入的 writer 与 batch, 不触碰全局状态,
// 调用方负责根据返回值在锁内更新队列账本。
func writeConversationBatch(writer *bufio.Writer, batch []model.ConversationRecord) (succeeded, writtenBytes int, flushOK bool) {
	for i := range batch {
		entry := &batch[i]
		entryBytes := len(entry.RawRequest) + len(entry.Response)
		extractConversationMessages(entry)
		line, err := json.Marshal(entry)
		if err != nil {
			log.Warnf("failed to marshal conversation entry: %v", err)
			break
		}
		line = append(line, '\n')
		if _, err := writer.Write(line); err != nil {
			log.Warnf("failed to write conversation entry: %v", err)
			break
		}
		succeeded++
		writtenBytes += entryBytes
	}
	if err := writer.Flush(); err != nil {
		log.Warnf("failed to flush conversation file: %v", err)
		return succeeded, writtenBytes, false
	}
	return succeeded, writtenBytes, true
}

// extractConversationMessages 从 openai chat 形态的请求体中抽取 messages 数组;
// 抽取后清空原始请求体, 避免与已抽取的 messages 重复存储(JSONL 双份)。
// 其他协议(anthropic/responses)不强行转换, 保留完整原始请求体。
func extractConversationMessages(entry *model.ConversationRecord) {
	if entry.RawRequest == "" {
		return
	}
	if messages := gjson.Get(entry.RawRequest, "messages"); messages.IsArray() {
		entry.Messages = messages.Value()
		// 抽取成功且为 chat 形态时, 原始请求体仅为冗余, 清空以去重。
		entry.RawRequest = ""
		return
	}
	// 非 chat 形态或未抽取到 messages: 不改动 RawRequest, 保留原始请求体兜底。
}

// ensureConversationFileLocked 打开(或随日期切换打开)当日 JSONL 文件;
// 切换时把前一天登记为待压缩归档, 实际 gzip 由写入协程在锁外执行(见 runConversationHousekeeping)。
// 调用方必须持有锁。
func ensureConversationFileLocked(day string) error {
	if conversationFile != nil && conversationDay == day {
		return nil
	}
	if err := closeConversationFileLocked(); err != nil {
		return err
	}
	if err := os.MkdirAll(conversationDir, 0o700); err != nil {
		return fmt.Errorf("创建会话归档目录失败: %w", err)
	}
	// 跨天: 登记昨天的明文归档待压缩, 由写入协程在锁外完成。
	if conversationDay != "" && conversationDay != day {
		conversationCompactDay = conversationDay
	}
	f, err := os.OpenFile(filepath.Join(conversationDir, day+".jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, conversationFilePerm)
	if err != nil {
		return fmt.Errorf("打开会话归档文件失败: %w", err)
	}
	conversationFile = f
	conversationDay = day
	conversationWriter = bufio.NewWriter(f)
	return nil
}

func closeConversationFileLocked() error {
	if conversationWriter != nil {
		if err := conversationWriter.Flush(); err != nil {
			log.Warnf("flush conversation writer: %v", err)
		}
		conversationWriter = nil
	}
	if conversationFile != nil {
		err := conversationFile.Close()
		conversationFile = nil
		if err != nil {
			return fmt.Errorf("关闭会话归档文件失败: %w", err)
		}
	}
	return nil
}

// compactYesterday gzip 压缩指定日期的明文归档, 采用崩溃安全提交。
// 仅由写入协程在锁外调用: 该日的明文文件此时已关闭且不再有写入者。
//
// 提交流程: 同目录临时文件(.jsonl.gz.tmp) → gzip 完整关闭 → fsync → 完整性校验
// → 原子 rename 发布为 .jsonl.gz → 删除源 .jsonl。任一步失败都保留源文件,
// 下次触发时重试。已有目标 .jsonl.gz 不再仅凭存在即认定成功: 先校验完整性,
// 校验通过才删除冗余源; 校验失败(半成品/截断)则删除坏档重新压缩。
//
// 崩溃恢复规则(与 cleanupConversations 的保留期清理正交):
//   - 崩溃在创建临时文件与 rename 之间: 残留 .jsonl.gz.tmp, 下次运行以 O_TRUNC
//     覆盖它, 源 .jsonl 未被触碰。
//   - 崩溃在 rename 与删除源之间: 同时存在 .jsonl 与 .jsonl.gz, 下次运行校验
//     .jsonl.gz 完整后删除冗余 .jsonl。
//   - rename 本身在同目录内 POSIX 原子: 不会产生部分目标, 要么旧名要么新名完整存在。
//   - 压缩或校验任一步失败: 源 .jsonl 始终保留, 下次重试。
func compactYesterday(day string) {
	src := filepath.Join(conversationDir, day+".jsonl")
	dst := src + ".gz"
	tmp := dst + ".tmp"

	if info, err := os.Stat(src); err != nil || info.Size() == 0 {
		// 无明文源可压缩; 顺手清理可能残留的临时文件。
		_ = os.Remove(tmp)
		return
	}

	// 已有压缩档时先校验完整性, 不再仅凭存在即认定成功。
	if _, err := os.Stat(dst); err == nil {
		if verr := verifyGzipArchive(dst); verr == nil {
			// 完整档已存在, 删除冗余明文源即可。
			_ = os.Remove(src)
			return
		}
		// 半成品/截断/损坏: 删除坏档后从源重新压缩。
		log.Warnf("conversation archive %s is corrupt/truncated, re-compressing from source", dst)
		_ = os.Remove(dst)
	}

	if err := compressGzipAtomic(src, dst, tmp); err != nil {
		log.Warnf("failed to compact %s: %v", src, err)
		_ = os.Remove(tmp)
		return
	}
	// 原子 rename 已成功发布; 此时删除源才安全。
	_ = os.Remove(src)
	log.Infof("conversation archive compacted: %s.gz", day)
}

// compressGzipAtomic 把 src 压缩到临时文件 tmp, fsync 后校验完整性, 再原子 rename
// 为 dst。任一步出错即返回错误, 调用方负责清理 tmp; 源文件永不被触碰。
func compressGzipAtomic(src, dst, tmp string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, conversationFilePerm)
	if err != nil {
		return err
	}

	gz := gzip.NewWriter(out)
	if _, err := io.Copy(gz, in); err != nil {
		_ = gz.Close()
		_ = out.Close()
		return fmt.Errorf("gzip copy: %w", err)
	}
	// 注入点: 测试用以模拟 copy 后、Close 前的崩溃。
	if compactHookAfterCopy != nil {
		if herr := compactHookAfterCopy(); herr != nil {
			_ = gz.Close()
			_ = out.Close()
			return herr
		}
	}
	if err := gz.Close(); err != nil {
		_ = out.Close()
		return fmt.Errorf("gzip close: %w", err)
	}
	// fsync 临时文件, 确保 rename 后的字节在崩溃中持久。
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	// 注入点: 测试用以模拟 fsync 后、rename 前的崩溃。
	if compactHookAfterSync != nil {
		if herr := compactHookAfterSync(); herr != nil {
			return herr
		}
	}

	// 发布前校验临时档是完整可读的 gzip 流(验证 CRC32 与 ISIZE 尾部)。
	if err := verifyGzipArchive(tmp); err != nil {
		return fmt.Errorf("verify temp: %w", err)
	}

	// 原子发布: 同目录 rename, POSIX 上原子。
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	// 注入点: 测试用以模拟 rename 后、删除源前的崩溃(此时两文件并存)。
	if compactHookAfterRename != nil {
		if herr := compactHookAfterRename(); herr != nil {
			return herr
		}
	}
	return nil
}

// verifyGzipArchive 打开 path 作为 gzip 流并读至末尾, 强制 gzip.Reader 校验
// CRC32 与 ISIZE 尾部。流完整且校验通过返回 nil, 否则返回错误(截断/损坏/非 gzip)。
func verifyGzipArchive(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return err
	}
	return nil
}

// ClearConversationArchives 立即清空全部对话留存归档(审计日志), 返回已删除文件数量。
// 与 cleanupConversations 的"惰性到期清理"不同, 本函数无条件删除目录下所有 .jsonl 与 .jsonl.gz,
// 并先 flush 内存待写队列、关闭当前文件句柄、重置日期标记, 避免删除后写入协程仍认为当日文件有效而追加到已删文件。
// 调用方(HTTP handler)负责鉴权; 仓库为单管理员, 任意登录者(=admin)均可触发。
func ClearConversationArchives(ctx context.Context) (int, error) {
	conversationMu.Lock()
	defer conversationMu.Unlock()
	if conversationDir == "" {
		return 0, nil
	}

	// 1) 先把内存待写队列落盘, 关闭文件句柄, 让后续写入重新打开新文件。
	flushConversationLocked(time.Now())
	_ = closeConversationFileLocked()
	conversationDay = ""
	conversationCompactDay = ""

	// 2) 删除全部 .jsonl(含压缩档 .jsonl.gz)归档文件。
	entries, err := os.ReadDir(conversationDir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") && !strings.HasSuffix(name, ".jsonl.gz") {
			continue
		}
		if err := os.Remove(filepath.Join(conversationDir, name)); err == nil {
			removed++
		}
	}
	log.Infof("conversation archives cleared: %d file(s) removed under %s", removed, conversationDir)
	return removed, nil
}

// ConversationStats 当前对话留存目录的统计快照, 供设置页展示用量与剩余预算。
// json tag 必须与 web-next 的 api.getConversationStats 消费形状(snake_case)对齐:
// 缺 tag 时 Go 按 PascalCase 字段名序列化, 前端读到的全是 undefined。
type ConversationStats struct {
	Enabled       bool   `json:"enabled"`        // 开关(consversation_log_enabled), 关时不写入新数据。
	Dir           string `json:"dir"`            // 目录绝对路径(未初始化时空)。
	FileCount     int    `json:"file_count"`     // 当前目录中 .jsonl + .jsonl.gz 文件数量。
	TotalBytes    int64  `json:"total_bytes"`    // 当前目录下所有归档文件的总占用字节。
	MaxBytes      int64  `json:"max_bytes"`      // 当前设置项下的目录硬预算(字节)。
	RetentionDays int    `json:"retention_days"` // 当前设置项下的归档保留天数。
	PendingBytes  int    `json:"pending_bytes"`  // 内存待写队列当前占用字节(估算当前写入压力)。
	DroppedTotal  int    `json:"dropped_total"`  // 自启动以来被丢弃的条目累计(缓冲预算/磁盘水线等触发)。
	OldestDay     string `json:"oldest_day"`     // 当前最早归档的日期(YYYY-MM-DD), 无归档时空。
	NewestDay     string `json:"newest_day"`     // 当前最近归档的日期(YYYY-MM-DD), 无归档时空。
}

// ConversationStats 统计当前对话留存目录的占用与设置项下的预算:
// 设置项与内存快照在锁外读取(允许并发), 仅目录扫描串行打开目录与 stat。
func GetConversationStats(ctx context.Context) (*ConversationStats, error) {
	_ = ctx

	enabled := ConversationEnabled()
	conversationMu.Lock()
	pendingBytes := conversationBytes
	droppedTotal := conversationDropped
	dir := conversationDir
	conversationMu.Unlock()

	maxBytes := conversationDirMaxBytes()
	retentionDays := conversationRetentionDays()

	stats := &ConversationStats{
		Enabled:       enabled,
		Dir:           dir,
		MaxBytes:      maxBytes,
		RetentionDays: retentionDays,
		PendingBytes:  pendingBytes,
		DroppedTotal:  droppedTotal,
	}
	if dir == "" {
		return stats, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return stats, nil
		}
		return nil, err
	}
	oldest := ""
	newest := ""
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") && !strings.HasSuffix(name, ".jsonl.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		stats.TotalBytes += info.Size()
		stats.FileCount++
		day := strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".jsonl")
		if oldest == "" || day < oldest {
			oldest = day
		}
		if newest == "" || day > newest {
			newest = day
		}
	}
	stats.OldestDay = oldest
	stats.NewestDay = newest
	return stats, nil
}

// cleanupConversations 按保留天数删除过期归档, 并把目录总量压回硬预算内。
// 仅由写入协程在锁外调用(runConversationHousekeeping): 目录内只有写入协程产生的
// 归档文件, 与内存待写队列互不相干。
func cleanupConversations(now time.Time) {
	// 在锁内读取 conversationDir 的快照, 避免与测试清理的写入产生数据竞争。
	// cleanupConversations 的文件 I/O 仍在锁外执行(持锁执行会让 relay 请求路径被阻塞)。
	conversationMu.Lock()
	dir := conversationDir
	conversationMu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := now.AddDate(0, 0, -conversationRetentionDays())
	type archive struct {
		path string
		size int64
		mod  time.Time
	}
	var archives []archive
	total := int64(0)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") && !strings.HasSuffix(name, ".jsonl.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		total += info.Size()
		day := strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".jsonl")
		if parsed, perr := time.ParseInLocation("2006-01-02", day, time.Local); perr == nil && parsed.Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
			total -= info.Size()
			continue
		}
		archives = append(archives, archive{filepath.Join(dir, name), info.Size(), info.ModTime()})
	}
	if total <= conversationDirMaxBytes() {
		return
	}
	sort.Slice(archives, func(i, j int) bool { return archives[i].mod.Before(archives[j].mod) })
	for _, a := range archives {
		if total <= conversationDirMaxBytes() {
			break
		}
		if err := os.Remove(a.path); err == nil {
			total -= a.size
			log.Infof("conversation budget exceeded, removed %s", filepath.Base(a.path))
		}
	}
}

func conversationDropLocked(reason string) {
	conversationDropped++
	now := time.Now()
	if conversationDropped == 1 || now.Sub(conversationDroppedLast) > time.Minute {
		conversationDroppedLast = now
		log.Warnf("conversation entries dropped (%s), total %d", reason, conversationDropped)
	}
}
