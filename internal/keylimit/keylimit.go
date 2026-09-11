// Package keylimit 下游 API 密钥级的并发上限与 RPM 限速。
//
// 与渠道级限速(internal/relay/channellimit.go)同构: 并发用连续 active 计数 +
// 动态 limit, RPM 用滑动窗口记录窗口内的放行时刻。语义不同: 渠道级面向内部
// 调度采用阻塞排队, 本包面向外部客户端采用 fail-fast —— 达到上限立即返回错误
// 并给出 Retry-After, 由调用方(中间件)下发 429。阻塞不可信客户端只会占住
// goroutine 与内存, 还会放大并发压力。
//
// 并发上限的关键性质: active 计数在配置变更(升额/降额/不限/重新限额)期间
// 始终连续累加, 绝不因 limit 变化而替换计数容器。因此降额不会"遗忘"旧在途
// 请求——旧请求自然归还使 active 回落, 在 active 降到新 limit 之前新请求
// 立即被拒绝(fail-fast), 无法通过换代绕过限制(见 STA-13)。
//
// 全部状态由同一把互斥锁保护, 临界区只有 map 读写与短切片扫描, 无 IO。
// 状态为进程内存态: 重启清零, 多实例部署各实例独立计数(与渠道级限速一致)。
package keylimit

import (
	"errors"
	"math"
	"sync"
	"time"
)

var (
	// ErrConcurrencyFull 并发槽位已满; 调用方应下发 429。
	ErrConcurrencyFull = errors.New("api key concurrency limit reached")
	// ErrRateLimited RPM 窗口已满; 调用方应下发 429 并携带返回的 Retry-After 秒数。
	ErrRateLimited = errors.New("api key rate limit exceeded")
)

// rpmWindow 滑动窗口长度; 变量以便测试注入短窗口。
var rpmWindow = time.Minute

// concurrencyState 单个密钥的连续并发计数状态。
// active 在请求生命周期内只增不减(领取 +1 / 归还 -1), limit 变化时不重置,
// 从而保证降额期间旧在途请求仍被计入, 新请求无法绕过限制。
type concurrencyState struct {
	// mu 保护 active; 与包级 mu 相互独立, 临界区无 IO。
	mu     sync.Mutex
	active int // 当前持有槽位的请求数; 归还幂等保证不会低于 0
}

// release 归还一个槽位: active 递减(不低于 0)。
// 由 AcquireConcurrency 返回的幂等闭包调用, 不会因重复调用而把 active 推到负值。
func (st *concurrencyState) release() {
	st.mu.Lock()
	if st.active > 0 {
		st.active--
	}
	st.mu.Unlock()
}

var (
	// mu 保护以下两张表; 所有访问必须持锁。
	mu sync.Mutex

	// states 按密钥保存连续并发计数状态。
	// 配置变更时只更新传入的 limit(由 AcquireConcurrency 即时生效), 绝不替换状态对象,
	// 因此旧在途请求的归还仍作用于同一 active 计数, 不会与在途生命周期脱节。
	// 密钥删除时整体移除条目: 已发放槽位的归还闭包持有原状态对象, 不依赖本表存活,
	// 直接删除是安全的; 同 ID 重建会得到全新的零计数状态。
	states = make(map[int]*concurrencyState)

	// windows 按密钥记录窗口内的放行时刻, 时间升序; 长度不超过 RPM 上限。
	windows = make(map[int][]time.Time)
)

// AcquireConcurrency 以 fail-fast 语义领取指定密钥的一个并发槽位, 返回归还函数。
// limit 非正值表示不限制, 直接返回空操作; 已满时不等待, 立即返回 ErrConcurrencyFull。
// 被拒绝的请求不占用槽位。归还函数幂等: 多次调用只归还一次。
//
// limit 即时生效: 升额后新请求立即按更宽的 limit 放行; 降额后旧在途请求
// 不被杀死, 但在 active 回落到新 limit 之前新请求被立即拒绝, 无法绕过。
func AcquireConcurrency(keyID, limit int) (func(), error) {
	if limit <= 0 {
		return func() {}, nil
	}
	mu.Lock()
	st, ok := states[keyID]
	if !ok {
		st = &concurrencyState{}
		states[keyID] = st
	}
	mu.Unlock()

	st.mu.Lock()
	if st.active >= limit {
		st.mu.Unlock()
		return func() {}, ErrConcurrencyFull
	}
	st.active++
	st.mu.Unlock()

	var once sync.Once
	return func() { once.Do(st.release) }, nil
}

// AcquireRPMPermit 以 fail-fast 语义尝试为密钥记录一次放行。
// 未达上限时记录当前时刻并返回 (0, nil); 已达上限时不放行,
// 返回 (Retry-After 秒数, ErrRateLimited), 秒数即最近一次名额腾出的等待时长。
// rpm 非正值表示不限制, 同时清除遗留窗口避免条目滞留。
func AcquireRPMPermit(keyID, rpm int) (int, error) {
	if rpm <= 0 {
		mu.Lock()
		delete(windows, keyID)
		mu.Unlock()
		return 0, nil
	}
	now := time.Now()
	cutoff := now.Add(-rpmWindow)
	mu.Lock()
	defer mu.Unlock()

	window := windows[keyID]
	start := 0
	for start < len(window) && !window[start].After(cutoff) {
		start++
	}
	window = window[start:]
	if len(window) >= rpm {
		// 窗口内已有 rpm 个放行: 最早可放行时刻即第 len-rpm 个时间戳加满一个窗口。
		admitAt := window[len(window)-rpm].Add(rpmWindow)
		windows[keyID] = window
		retryAfter := int(math.Ceil(admitAt.Sub(now).Seconds()))
		if retryAfter < 1 {
			retryAfter = 1
		}
		return retryAfter, ErrRateLimited
	}
	windows[keyID] = append(window, now)
	return 0, nil
}

// Cleanup 清除指定密钥的并发计数状态与 RPM 窗口, 密钥删除时调用。
// 已发放槽位的归还闭包持有原状态对象, 不依赖本表的存活, 直接删除条目是安全的。
func Cleanup(keyID int) {
	mu.Lock()
	defer mu.Unlock()
	delete(states, keyID)
	delete(windows, keyID)
}

// Reset 清空全部限流状态; 仅供测试隔离包级全局状态。
func Reset() {
	mu.Lock()
	states = make(map[int]*concurrencyState)
	windows = make(map[int][]time.Time)
	mu.Unlock()
}
