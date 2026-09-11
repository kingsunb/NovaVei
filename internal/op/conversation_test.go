package op

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kingsunb/NovaVei/internal/db"
	"github.com/kingsunb/NovaVei/internal/model"
)

// conversationTestEnv 返回指向临时目录的已初始化留存存储, 并保证用例间互不残留。
func conversationTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	oldDiskProbe := conversationDiskHasRoom
	conversationDiskHasRoom = func(string) bool { return true }
	InitConversationStore(dir)
	t.Cleanup(func() {
		conversationMu.Lock()
		conversationDiskHasRoom = oldDiskProbe
		conversationDir = ""
		conversationPending = nil
		conversationBytes = 0
		if conversationStop != nil && conversationAlive() {
			close(conversationStop)
		}
		conversationMu.Unlock()
		if conversationDone != nil {
			<-conversationDone
		}
	})
	return dir
}

// TestCaptureConversationDisabled 校验开关关闭时捕获为空操作。
func TestCaptureConversationDisabled(t *testing.T) {
	dir := conversationTestEnv(t)
	_ = db.GetDB() // 数据库由测试框架初始化; 开关默认 false

	CaptureConversation(model.ConversationRecord{RequestID: 1, Outcome: "success", RawRequest: "x"})
	time.Sleep(50 * time.Millisecond)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			t.Fatalf("关闭状态下不应产生文件: %s", e.Name())
		}
	}
}

// TestConversationFlushWritesJSONL 校验开启后捕获的记录落盘为合法 JSONL 且 messages 被抽取。
func TestConversationFlushWritesJSONL(t *testing.T) {
	dir := conversationTestEnv(t)

	if err := SettingSetString(model.SettingKeyConversationLog, "true"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer func() { _ = SettingSetString(model.SettingKeyConversationLog, "false") }()

	body := `{"model":"grp","messages":[{"role":"system","content":"sys"},{"role":"user","content":"hi"}]}`
	CaptureConversation(model.ConversationRecord{
		RequestID: 7, Model: "grp", ChannelName: "ch-a", TargetModel: "up",
		Outcome: "success", ClientFormat: "openai_chat", RelayMode: "passthrough",
		RawRequest: body, Response: "hello!",
		Usage: model.UsageStat{PromptTokens: 10, CompletionTokens: 2},
	})

	day := time.Now().Format("2006-01-02")
	jsonl := filepath.Join(dir, day+".jsonl")

	// 触发落盘: 显式停止协程(与关闭钩子同路径)。
	conversationMu.Lock()
	if conversationStop != nil && conversationAlive() {
		close(conversationStop)
	}
	conversationMu.Unlock()
	if conversationDone != nil {
		select {
		case <-conversationDone:
		case <-time.After(5 * time.Second):
			t.Fatal("writer did not stop")
		}
	}

	data, err := os.ReadFile(jsonl)
	if err != nil {
		t.Fatalf("read %s: %v", jsonl, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expect 1 line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], `"messages"`) || !strings.Contains(lines[0], `"response":"hello!"`) {
		t.Fatalf("记录内容异常: %s", lines[0])
	}
	info, _ := os.Stat(jsonl)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("文件权限应收紧为 0600, 实际 %v", info.Mode().Perm())
	}
}

func TestConversationDiskWatermarkDropsPendingRecord(t *testing.T) {
	dir := conversationTestEnv(t)
	if err := SettingSetString(model.SettingKeyConversationLog, "true"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer func() { _ = SettingSetString(model.SettingKeyConversationLog, "false") }()
	conversationMu.Lock()
	conversationDiskHasRoom = func(string) bool { return false }
	conversationMu.Unlock()

	CaptureConversation(model.ConversationRecord{RequestID: 8, Outcome: "success", RawRequest: `{}`})
	conversationMu.Lock()
	flushConversationLocked(time.Now())
	pending := len(conversationPending)
	conversationMu.Unlock()
	if pending != 1 {
		t.Fatalf("disk watermark should retain the pending record for a later retry, pending=%d", pending)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			t.Fatalf("disk watermark must prevent writes, found %s", entry.Name())
		}
	}
}

// TestClearConversationArchivesRemovesAll 校验 ClearConversationArchives 无条件删除目录下全部 .jsonl / .jsonl.gz,
// 并重置当日文件标记使后续写入重新打开文件; 目录本身保留, 便于后端在最小目录结构上继续工作。
func TestClearConversationArchivesRemovesAll(t *testing.T) {
	dir := conversationTestEnv(t)

	// 准备两个旧日期归档 + 当日文件, 模拟多次跨天滚动后的留存目录。
	oldPlain := filepath.Join(dir, time.Now().AddDate(0, 0, -2).Format("2006-01-02")+".jsonl")
	oldGz := filepath.Join(dir, time.Now().AddDate(0, 0, -1).Format("2006-01-02")+".jsonl.gz")
	today := filepath.Join(dir, time.Now().Format("2006-01-02")+".jsonl")
	for _, p := range []string{oldPlain, oldGz, today} {
		if err := os.WriteFile(p, []byte(`{"request_id":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// conversationDay 由 conversationTestEnv 间接置空, 这里再显式设置, 模拟"已开启当日文件句柄"的状态
	conversationMu.Lock()
	conversationDay = time.Now().Format("2006-01-02")
	conversationMu.Unlock()

	removed, err := ClearConversationArchives(t.Context())
	if err != nil {
		t.Fatalf("ClearConversationArchives: %v", err)
	}
	if removed != 3 {
		t.Fatalf("removed = %d, want 3", removed)
	}

	// 全部归档应消失
	for _, p := range []string{oldPlain, oldGz, today} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("文件应被删除: %s (err=%v)", p, err)
		}
	}

	// 目录本身应保留(空)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("目录应为空, 实际: %v", names)
	}

	// conversationDay 应被重置, 后续 flushConversationLocked 会重新打开文件
	conversationMu.Lock()
	day := conversationDay
	conversationMu.Unlock()
	if day != "" {
		t.Fatalf("conversationDay 应被重置为空, 实际 %q", day)
	}
}

// TestClearConversationArchivesEmptyDir 校验空目录调用不报错且返回 0。
func TestClearConversationArchivesEmptyDir(t *testing.T) {
	dir := conversationTestEnv(t)

	removed, err := ClearConversationArchives(t.Context())
	if err != nil {
		t.Fatalf("ClearConversationArchives: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("目录应保留: %v", err)
	}
}

// TestClearConversationArchivesUninitialized 校验 conversationDir 未初始化时为无操作成功。
func TestClearConversationArchivesUninitialized(t *testing.T) {
	// 绕过 conversationTestEnv, 直接清空 conversationDir 模拟未初始化
	conversationMu.Lock()
	conversationDir = ""
	conversationMu.Unlock()

	removed, err := ClearConversationArchives(t.Context())
	if err != nil {
		t.Fatalf("ClearConversationArchives: %v", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

// TestConversationSettingsRuntimeAware 验证 retention_days 与 dir_max_gb 设置项
// 在运行期生效, 不再写死常量; 非法值与越界值回退默认。
func TestConversationSettingsRuntimeAware(t *testing.T) {
	if got := conversationRetentionDays(); got != model.DefaultConversationRetentionDays {
		t.Fatalf("未设置时应回退默认值, 实际 %d", got)
	}
	if got := conversationDirMaxBytes(); got != model.DefaultConversationDirMaxGB*1024*1024*1024 {
		t.Fatalf("未设置时应回退默认 5GB, 实际 %d", got)
	}

	t.Cleanup(func() {
		SettingSetString(model.SettingKeyConversationRetentionDays, "")
		SettingSetString(model.SettingKeyConversationDirMaxGB, "")
	})

	if err := SettingSetString(model.SettingKeyConversationRetentionDays, "7"); err != nil {
		t.Fatalf("设置保留天数: %v", err)
	}
	if got := conversationRetentionDays(); got != 7 {
		t.Fatalf("运行期保留天数应等于 7, 实际 %d", got)
	}

	if err := SettingSetString(model.SettingKeyConversationDirMaxGB, "10"); err != nil {
		t.Fatalf("设置目录预算: %v", err)
	}
	if got := conversationDirMaxBytes(); got != 10*1024*1024*1024 {
		t.Fatalf("运行期目录预算应等于 10GB, 实际 %d", got)
	}

	// 越界值回退默认
	if err := SettingSetString(model.SettingKeyConversationRetentionDays, "999"); err != nil {
		t.Fatalf("设置越界保留天数: %v", err)
	}
	if got := conversationRetentionDays(); got != model.ConversationRetentionDaysMax {
		t.Fatalf("越界值应回退到上限, 实际 %d", got)
	}
	if err := SettingSetString(model.SettingKeyConversationDirMaxGB, "999"); err != nil {
		t.Fatalf("设置越界目录预算: %v", err)
	}
	if got := conversationDirMaxBytes(); got != model.ConversationDirMaxGBMax*1024*1024*1024 {
		t.Fatalf("越界值应回退到上限, 实际 %d", got)
	}
}

// TestConversationRetentionDaysApplied 验证 cleanup 任务按运行期 retention 生效:
// 设置项为 1 天时, 文件名早于 cutoff 的归档应被删除; 当日的归档必须保留。
// 测试用 cleanupConversations 的净化逻辑: 文件名日期 < cutoff 即删除(按文件名判断),
// 与按 mtime 容量回收正交(后者由 total > MaxBytes 触发)。
func TestConversationRetentionDaysApplied(t *testing.T) {
	dir := conversationTestEnv(t)
	writeArchive := func(name string, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("写入归档失败: %v", err)
		}
	}
	oldDiskProbe := conversationDiskHasRoom
	conversationMu.Lock()
	conversationDiskHasRoom = func(string) bool { return true }
	conversationMu.Unlock()
	t.Cleanup(func() {
		conversationMu.Lock()
		conversationDiskHasRoom = oldDiskProbe
		conversationMu.Unlock()
		SettingSetString(model.SettingKeyConversationRetentionDays, "")
	})

	// 文件名日期是 cleanupConversations 过期判断的依据; retention=1 天时,
	// 昨天的 `yesterday.jsonl` 必须被清理, 当日的 `today.jsonl` 必须保留。
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	today := now.Format("2006-01-02")
	writeArchive(yesterday+".jsonl", "old\n")
	writeArchive(today+".jsonl", "today\n")

	SettingSetString(model.SettingKeyConversationRetentionDays, "1")
	cleanupConversations(now)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Name()] = true
	}
	if names[yesterday+".jsonl"] {
		t.Fatalf("昨天的归档应在 1 天保留策略下被清理: %v", names)
	}
	if !names[today+".jsonl"] {
		t.Fatalf("今天的归档必须保留: %v", names)
	}
}

// TestGetConversationStatsDefaults 验证在未初始化场景下的快照默认值。
func TestGetConversationStatsDefaults(t *testing.T) {
	conversationMu.Lock()
	conversationDir = ""
	conversationMu.Unlock()

	stats, err := GetConversationStats(t.Context())
	if err != nil {
		t.Fatalf("GetConversationStats: %v", err)
	}
	if stats.Enabled {
		t.Fatalf("未启用开关时 stats.Enabled 应为 false")
	}
	if stats.Dir != "" {
		t.Fatalf("未初始化时 stats.Dir 应为空, 实际 %q", stats.Dir)
	}
	if stats.MaxBytes <= 0 {
		t.Fatalf("MaxBytes 应为默认预算, 实际 %d", stats.MaxBytes)
	}
	if stats.RetentionDays <= 0 {
		t.Fatalf("RetentionDays 应为默认保留天数, 实际 %d", stats.RetentionDays)
	}
}

// TestConversationStatsJSONShape 钉死 /conversation/stats 的 JSON 形状:
// web-next 的 api.getConversationStats 按 snake_case 消费, 缺 json tag 时
// Go 按 PascalCase 字段名序列化, 前端读到的全是 undefined。
func TestConversationStatsJSONShape(t *testing.T) {
	stats := ConversationStats{
		Enabled:       true,
		Dir:           "/data/conversations",
		FileCount:     3,
		TotalBytes:    1024,
		MaxBytes:      5 * 1024 * 1024 * 1024,
		RetentionDays: 30,
		PendingBytes:  64,
		DroppedTotal:  2,
		OldestDay:     "2026-08-01",
		NewestDay:     "2026-09-05",
	}
	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]any{
		"enabled":        true,
		"dir":            "/data/conversations",
		"file_count":     float64(3),
		"total_bytes":    float64(1024),
		"max_bytes":      float64(5 * 1024 * 1024 * 1024),
		"retention_days": float64(30),
		"pending_bytes":  float64(64),
		"dropped_total":  float64(2),
		"oldest_day":     "2026-08-01",
		"newest_day":     "2026-09-05",
	}
	if len(got) != len(want) {
		t.Fatalf("字段数不符: got %v, want %v", got, want)
	}
	for key, expected := range want {
		actual, ok := got[key]
		if !ok {
			t.Fatalf("缺少 json key %q (got %v): json tag 与 web-next 契约漂移", key, got)
		}
		if actual != expected {
			t.Fatalf("key %q = %v, want %v", key, actual, expected)
		}
	}
}

// ---------------------------------------------------------------------------
// STA-09: gzip 归档崩溃安全提交测试
// ---------------------------------------------------------------------------

// conversationCompactTestEnv 准备一个隔离的临时目录作为 conversationDir, 不启动写入
// 协程(compactYesterday 只读 conversationDir, 无需协程)。用例间互不残留。
func conversationCompactTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	conversationMu.Lock()
	conversationDir = dir
	conversationMu.Unlock()
	t.Cleanup(func() {
		conversationMu.Lock()
		conversationDir = ""
		conversationMu.Unlock()
	})
	return dir
}

// conversationResetCompactHooks 清理所有压缩注入钩子, 防止用例间残留。
func conversationResetCompactHooks(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		compactHookAfterCopy = nil
		compactHookAfterSync = nil
		compactHookAfterRename = nil
	})
}

// conversationWriteSource 写入指定行数的合法 JSONL 明文归档, 返回文件路径。
func conversationWriteSource(t *testing.T, dir, day string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, day+".jsonl")
	var buf strings.Builder
	for _, l := range lines {
		buf.WriteString(l)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		t.Fatalf("write source archive: %v", err)
	}
	return path
}

// conversationMakeRecords 生成 n 条唯一 JSONL 行(RequestID 1..n)。
func conversationMakeRecords(n int) []string {
	lines := make([]string, n)
	for i := 0; i < n; i++ {
		rec := model.ConversationRecord{RequestID: uint64(i + 1), Outcome: "success", Response: fmt.Sprintf("resp-%d", i)}
		data, err := json.Marshal(rec)
		if err != nil {
			panic(err)
		}
		lines[i] = string(data)
	}
	return lines
}

// conversationReadGzipLines 解压 gzip 文件并返回按行分割的内容(去尾部空行)。
func conversationReadGzipLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open gzip %s: %v", path, err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader %s: %v", path, err)
	}
	defer gz.Close()
	data, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gzip %s: %v", path, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// conversationMakeTruncatedGzip 生成一个合法 gzip 流并截断尾部 4 字节, 模拟压缩中断的半成品。
func conversationMakeTruncatedGzip(t *testing.T, path string, content []byte) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(content); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	full := buf.Bytes()
	if len(full) < 5 {
		t.Fatalf("gzip too short to truncate")
	}
	truncated := full[:len(full)-4]
	if err := os.WriteFile(path, truncated, 0o600); err != nil {
		t.Fatalf("write truncated gzip: %v", err)
	}
}

// TestCompactYesterdayCrashSafeCommit 验证正常压缩: 临时文件 → rename → 删除源,
// 产物可完整解压且记录数一致, 无残留临时文件。
func TestCompactYesterdayCrashSafeCommit(t *testing.T) {
	dir := conversationCompactTestEnv(t)
	conversationResetCompactHooks(t)

	day := "2024-06-15"
	lines := conversationMakeRecords(5)
	src := conversationWriteSource(t, dir, day, lines)
	dst := src + ".gz"

	compactYesterday(day)

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("源文件应在压缩成功后删除: %s (err=%v)", src, err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("压缩档应存在: %v", err)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("临时文件应被清理: %s.tmp", dst)
	}
	got := conversationReadGzipLines(t, dst)
	if len(got) != len(lines) {
		t.Fatalf("记录数不一致: got %d, want %d", len(got), len(lines))
	}
}

// TestCompactYesterdayTruncatedExistingPreservesSource 验证遇到预先存在的截断 gzip 时
// 不能删除源文件, 而应删除坏档重新压缩。这是 STA-09 的核心回归点。
func TestCompactYesterdayTruncatedExistingPreservesSource(t *testing.T) {
	dir := conversationCompactTestEnv(t)
	conversationResetCompactHooks(t)

	day := "2024-06-16"
	lines := conversationMakeRecords(3)
	src := conversationWriteSource(t, dir, day, lines)
	dst := src + ".gz"

	// 预先放置一个截断的半成品 gzip。
	conversationMakeTruncatedGzip(t, dst, []byte("old data\n"))

	compactYesterday(day)

	// 坏档应被替换为完整档。
	got := conversationReadGzipLines(t, dst)
	if len(got) != len(lines) {
		t.Fatalf("重新压缩后记录数不一致: got %d, want %d", len(got), len(lines))
	}
	// 压缩成功后源应被删除。
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("重新压缩成功后源文件应删除: %s", src)
	}
}

// TestCompactYesterdayValidExistingRemovesSource 验证已有完整 gzip 时仅删除冗余源,
// 不重新压缩。
func TestCompactYesterdayValidExistingRemovesSource(t *testing.T) {
	dir := conversationCompactTestEnv(t)
	conversationResetCompactHooks(t)

	day := "2024-06-17"
	lines := conversationMakeRecords(4)
	src := conversationWriteSource(t, dir, day, lines)
	dst := src + ".gz"

	// 预先写入一个完整的 gzip(内容与源不同, 验证不被覆盖)。
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte("existing\n"))
	gz.Close()
	if err := os.WriteFile(dst, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	existingGz := buf.Bytes()

	compactYesterday(day)

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("完整 gzip 已存在时冗余源应删除: %s", src)
	}
	gotData, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("读取 gzip: %v", err)
	}
	if !bytes.Equal(gotData, existingGz) {
		t.Fatalf("已有完整 gzip 不应被重新压缩覆盖")
	}
}

// TestCompactYesterdayFailureAfterCopyPreservesSource 验证压缩过程中(copy 后)注入
// 故障时源文件保留, 不产生半成品 .jsonl.gz。
func TestCompactYesterdayFailureAfterCopyPreservesSource(t *testing.T) {
	dir := conversationCompactTestEnv(t)
	conversationResetCompactHooks(t)

	day := "2024-06-18"
	lines := conversationMakeRecords(3)
	src := conversationWriteSource(t, dir, day, lines)
	dst := src + ".gz"

	compactHookAfterCopy = func() error { return errors.New("simulated crash after copy") }

	compactYesterday(day)

	if _, err := os.Stat(src); err != nil {
		t.Fatalf("压缩失败时源文件必须保留: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("压缩失败不应产生 .jsonl.gz: %s", dst)
	}
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("临时文件应被清理: %s.tmp", dst)
	}
}

// TestCompactYesterdayFailureAfterSyncPreservesSource 验证 fsync 后、rename 前注入
// 故障时源文件保留, 不产生 .jsonl.gz(rename 未发生)。
func TestCompactYesterdayFailureAfterSyncPreservesSource(t *testing.T) {
	dir := conversationCompactTestEnv(t)
	conversationResetCompactHooks(t)

	day := "2024-06-19"
	lines := conversationMakeRecords(2)
	src := conversationWriteSource(t, dir, day, lines)
	dst := src + ".gz"

	compactHookAfterSync = func() error { return errors.New("simulated crash after sync") }

	compactYesterday(day)

	if _, err := os.Stat(src); err != nil {
		t.Fatalf("rename 前故障时源文件必须保留: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("rename 未发生不应存在 .jsonl.gz: %s", dst)
	}
}

// TestCompactYesterdayFailureAfterRenameThenRecover 验证 rename 后、删除源前注入故障
// (模拟崩溃)时两文件并存; 再次调用 compactYesterday 恢复: 校验 gzip 完整后删除冗余源,
// 记录数不丢失、无重复。
func TestCompactYesterdayFailureAfterRenameThenRecover(t *testing.T) {
	dir := conversationCompactTestEnv(t)
	conversationResetCompactHooks(t)

	day := "2024-06-20"
	lines := conversationMakeRecords(6)
	src := conversationWriteSource(t, dir, day, lines)
	dst := src + ".gz"

	// 第一次压缩: rename 成功但删除源前"崩溃"。
	compactHookAfterRename = func() error { return errors.New("simulated crash after rename") }
	compactYesterday(day)

	// 崩溃后: 源与压缩档并存。
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("rename 后崩溃时源文件应仍在: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("rename 已完成, 压缩档应存在: %v", err)
	}

	// 恢复: 再次调用, 校验 gzip 完整后删除冗余源。
	compactHookAfterRename = nil
	compactYesterday(day)

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("恢复后冗余源应删除: %s", src)
	}
	got := conversationReadGzipLines(t, dst)
	if len(got) != len(lines) {
		t.Fatalf("恢复后记录数不一致: got %d, want %d (丢失或重复)", len(got), len(lines))
	}
	// 逐条核对 RequestID, 确认无丢失无重复。
	seen := make(map[uint64]bool, len(got))
	for _, l := range got {
		var rec model.ConversationRecord
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("恢复后解压行不是合法记录: %v", err)
		}
		if seen[rec.RequestID] {
			t.Fatalf("恢复后出现重复 RequestID=%d", rec.RequestID)
		}
		seen[rec.RequestID] = true
	}
	for i := 1; i <= len(lines); i++ {
		if !seen[uint64(i)] {
			t.Fatalf("恢复后丢失 RequestID=%d", i)
		}
	}
}

// TestCompactYesterdayRecordCountPreserved 验证压缩-解压往返保持记录数与内容完全一致。
func TestCompactYesterdayRecordCountPreserved(t *testing.T) {
	dir := conversationCompactTestEnv(t)
	conversationResetCompactHooks(t)

	day := "2024-06-21"
	lines := conversationMakeRecords(10)
	src := conversationWriteSource(t, dir, day, lines)
	dst := src + ".gz"

	compactYesterday(day)

	got := conversationReadGzipLines(t, dst)
	if len(got) != 10 {
		t.Fatalf("记录数不一致: got %d, want 10", len(got))
	}
	for i, l := range got {
		var rec model.ConversationRecord
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("行 %d 不是合法记录: %v", i, err)
		}
		if rec.RequestID != uint64(i+1) {
			t.Fatalf("行 %d RequestID=%d, want %d", i, rec.RequestID, i+1)
		}
	}
}

// TestCompactYesterdayEmptySourceCleansTemp 验证源文件不存在或为空时清理残留临时文件,
// 不误删其他文件。
func TestCompactYesterdayEmptySourceCleansTemp(t *testing.T) {
	dir := conversationCompactTestEnv(t)
	conversationResetCompactHooks(t)

	day := "2024-06-22"
	tmp := filepath.Join(dir, day+".jsonl.gz.tmp")
	if err := os.WriteFile(tmp, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 无源文件; compactYesterday 应清理临时文件后返回。
	compactYesterday(day)
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("残留临时文件应被清理: %s", tmp)
	}
}

// ---------------------------------------------------------------------------
// STA-11: 归档资源边界测试
// ---------------------------------------------------------------------------

// TestConversationOversizedSingleRecordRejected 验证单条记录超过总预算时被拒收,
// 不进入队列, dropped 计数递增, conversationBytes 保持严格硬上限。
func TestConversationOversizedSingleRecordRejected(t *testing.T) {
	_ = conversationTestEnv(t)
	if err := SettingSetString(model.SettingKeyConversationLog, "true"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer func() { _ = SettingSetString(model.SettingKeyConversationLog, "false") }()

	conversationMu.Lock()
	droppedBefore := conversationDropped
	conversationMu.Unlock()

	big := strings.Repeat("x", model.ConversationPendingMaxBytes+1)
	CaptureConversation(model.ConversationRecord{RequestID: 1, Outcome: "success", RawRequest: big})

	conversationMu.Lock()
	pending := len(conversationPending)
	bytes := conversationBytes
	dropped := conversationDropped
	conversationMu.Unlock()

	if pending != 0 {
		t.Fatalf("超限单条记录应被拒收, pending=%d", pending)
	}
	if bytes != 0 {
		t.Fatalf("超限单条记录不应计入字节, bytes=%d", bytes)
	}
	if dropped != droppedBefore+1 {
		t.Fatalf("dropped 应递增 1, got %d, want %d", dropped, droppedBefore+1)
	}
}

// TestConversationPendingBytesHardLimit 验证多次捕获后 conversationBytes 始终不超过
// ConversationPendingMaxBytes(严格硬上限), 超限靠驱逐最旧条目而非溢出。
func TestConversationPendingBytesHardLimit(t *testing.T) {
	_ = conversationTestEnv(t)
	if err := SettingSetString(model.SettingKeyConversationLog, "true"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer func() { _ = SettingSetString(model.SettingKeyConversationLog, "false") }()

	// 每条 5MB, 预算 16MB: 第 4 条会触发驱逐。
	chunk := strings.Repeat("x", 5*1024*1024)
	for i := 0; i < 5; i++ {
		CaptureConversation(model.ConversationRecord{RequestID: uint64(i + 1), Outcome: "success", RawRequest: chunk})
		conversationMu.Lock()
		if conversationBytes > model.ConversationPendingMaxBytes {
			b := conversationBytes
			conversationMu.Unlock()
			t.Fatalf("conversationBytes=%d 超过硬上限 %d (第 %d 条后)", b, model.ConversationPendingMaxBytes, i+1)
		}
		conversationMu.Unlock()
	}
}

// TestConversationFlushBatchDoesNotBlockProducer 验证写入协程在锁外执行 I/O:
// 慢盘(hook 阻塞)期间 CaptureConversation 应快速返回, 不被锁卡住。
func TestConversationFlushBatchDoesNotBlockProducer(t *testing.T) {
	_ = conversationTestEnv(t)
	if err := SettingSetString(model.SettingKeyConversationLog, "true"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer func() { _ = SettingSetString(model.SettingKeyConversationLog, "false") }()

	var enteredOnce sync.Once
	entered := make(chan struct{})
	release := make(chan struct{})
	conversationFlushIOHook = func() error {
		enteredOnce.Do(func() { close(entered) })
		<-release
		return nil
	}
	defer func() {
		conversationFlushIOHook = nil
		close(release)
	}()

	// 捕获一条记录触发 flush; writer 进入锁外 I/O 后在 hook 上阻塞。
	CaptureConversation(model.ConversationRecord{RequestID: 1, Outcome: "success", RawRequest: "trigger"})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("writer 未进入锁外 I/O 阶段")
	}

	// writer 正在锁外阻塞, 此时 CaptureConversation 应快速完成(仅短锁入队)。
	start := time.Now()
	CaptureConversation(model.ConversationRecord{RequestID: 2, Outcome: "success", RawRequest: "second"})
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Fatalf("producer 被慢盘 I/O 阻塞 %v (锁未在 I/O 期间释放)", elapsed)
	}

	// 确认第二条记录已入队等待后续 flush。
	conversationMu.Lock()
	pending := len(conversationPending)
	conversationMu.Unlock()
	if pending == 0 {
		t.Fatal("第二条记录应在队列中等待 flush")
	}
}

// TestConversationFlushBatchSlotRecovery 验证成功 flush 后队列槽位回收(pending 清空),
// flush 失败时整批回挂队列等待重试。
func TestConversationFlushBatchSlotRecovery(t *testing.T) {
	dir := conversationTestEnv(t)
	if err := SettingSetString(model.SettingKeyConversationLog, "true"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer func() { _ = SettingSetString(model.SettingKeyConversationLog, "false") }()

	day := time.Now().Format("2006-01-02")

	// 直接入队一条记录(不经 CaptureConversation 以避免 writer 抢先 flush)。
	rec := model.ConversationRecord{RequestID: 42, Outcome: "success", RawRequest: "slot-test"}
	conversationMu.Lock()
	conversationPending = append(conversationPending, rec)
	conversationBytes += len(rec.RawRequest) + len(rec.Response)
	conversationMu.Unlock()

	// 成功 flush: 槽位应回收。
	conversationFlushBatch()
	conversationMu.Lock()
	pending := len(conversationPending)
	conversationMu.Unlock()
	if pending != 0 {
		t.Fatalf("成功 flush 后 pending 应为 0, got %d", pending)
	}
	// 确认记录已落盘。
	data, err := os.ReadFile(filepath.Join(dir, day+".jsonl"))
	if err != nil {
		t.Fatalf("读取当日归档: %v", err)
	}
	if !strings.Contains(string(data), `"request_id":42`) {
		t.Fatalf("记录未落盘: %s", string(data))
	}

	// 注入 I/O 失败: 整批应回挂队列。
	conversationFlushIOHook = func() error { return errors.New("simulated write failure") }
	rec2 := model.ConversationRecord{RequestID: 43, Outcome: "success", RawRequest: "retry-test"}
	conversationMu.Lock()
	conversationPending = append(conversationPending, rec2)
	conversationBytes += len(rec2.RawRequest) + len(rec2.Response)
	conversationMu.Unlock()

	conversationFlushBatch()
	conversationMu.Lock()
	pending = len(conversationPending)
	bytes := conversationBytes
	conversationMu.Unlock()
	if pending != 1 {
		t.Fatalf("flush 失败后整批应回挂, pending=%d, want 1", pending)
	}
	if bytes == 0 {
		t.Fatalf("flush 失败后字节应非零, got 0")
	}

	// 清除钩子后重试: 记录应成功落盘, 槽位回收。
	conversationFlushIOHook = nil
	conversationFlushBatch()
	conversationMu.Lock()
	pending = len(conversationPending)
	conversationMu.Unlock()
	if pending != 0 {
		t.Fatalf("重试成功后 pending 应为 0, got %d", pending)
	}
	data, err = os.ReadFile(filepath.Join(dir, day+".jsonl"))
	if err != nil {
		t.Fatalf("读取当日归档: %v", err)
	}
	if !strings.Contains(string(data), `"request_id":43`) {
		t.Fatalf("重试后记录未落盘: %s", string(data))
	}
}

// TestConversationFlushBatchDiskFullRetainsBatch 验证磁盘水位不足时批次保留,
// 不丢失记录, dropped 计数递增。
func TestConversationFlushBatchDiskFullRetainsBatch(t *testing.T) {
	dir := conversationTestEnv(t)
	if err := SettingSetString(model.SettingKeyConversationLog, "true"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	defer func() { _ = SettingSetString(model.SettingKeyConversationLog, "false") }()
	conversationMu.Lock()
	conversationDiskHasRoom = func(string) bool { return false }
	conversationMu.Unlock()
	defer func() {
		conversationMu.Lock()
		conversationDiskHasRoom = func(string) bool { return true }
		conversationMu.Unlock()
	}()

	rec := model.ConversationRecord{RequestID: 77, Outcome: "success", RawRequest: "disk-full"}
	conversationMu.Lock()
	conversationPending = append(conversationPending, rec)
	conversationBytes += len(rec.RawRequest) + len(rec.Response)
	conversationMu.Unlock()

	conversationFlushBatch()

	conversationMu.Lock()
	pending := len(conversationPending)
	conversationMu.Unlock()
	if pending != 1 {
		t.Fatalf("磁盘满时批次应保留等待重试, pending=%d, want 1", pending)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			t.Fatalf("磁盘满时不应写入文件: %s", e.Name())
		}
	}
}
