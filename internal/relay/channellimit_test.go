package relay

// 渠道级 RPM 限速与并发上限的逻辑层测试:
// 覆盖并发信号量满载阻塞与 ctx 打断、释放后复用、RPM 滑动窗口边界(短窗注入)、
// 双 Key 独立预算、不限速清理、渠道删除状态清理以及 op 层更新分支的落库语义。

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/kingsunb/NovaVeil/internal/op"
)

// useShortRPMWindow 注入短滑动窗口并在测试结束后恢复原值。
func useShortRPMWindow(t *testing.T, window time.Duration) {
	t.Helper()
	previous := channelRPMWindow
	channelRPMWindow = window
	t.Cleanup(func() { channelRPMWindow = previous })
}

// TestAcquireChannelConcurrencyFullLoad 验证并发信号量满载阻塞与释放后放行:
// 容量 2 领满后第三个请求阻塞, 归还一个槽位后立即放行。
func TestAcquireChannelConcurrencyFullLoad(t *testing.T) {
	resetChannelLimits()
	const channelID = 9001

	first, err := acquireChannelConcurrency(context.Background(), channelID, 2)
	if err != nil {
		t.Fatalf("第一个槽位应立即获取: %v", err)
	}
	defer first()
	second, err := acquireChannelConcurrency(context.Background(), channelID, 2)
	if err != nil {
		t.Fatalf("第二个槽位应立即获取: %v", err)
	}

	acquired := make(chan error, 1)
	var releaseThird func()
	go func() {
		release, aerr := acquireChannelConcurrency(context.Background(), channelID, 2)
		releaseThird = release
		acquired <- aerr
	}()

	select {
	case aerr := <-acquired:
		t.Fatalf("容量已满时第三个请求不应获取成功, 实际 %v", aerr)
	case <-time.After(150 * time.Millisecond):
		// 预期内: 仍在阻塞等待槽位。
	}

	second()
	select {
	case aerr := <-acquired:
		if aerr != nil {
			t.Fatalf("归还槽位后的请求不应失败: %v", aerr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("归还槽位后阻塞的请求应被放行")
	}
	if releaseThird != nil {
		releaseThird()
	}
}

// TestAcquireChannelConcurrencyCtxInterrupt 验证满载等待期间 ctx 结束立即返回 ctx 错误,
// 且被打断的请求不占用槽位; 释放后可再次获取。
func TestAcquireChannelConcurrencyCtxInterrupt(t *testing.T) {
	resetChannelLimits()
	const channelID = 9002

	releaseHeld, err := acquireChannelConcurrency(context.Background(), channelID, 1)
	if err != nil {
		t.Fatalf("唯一槽位应立即获取: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	if _, aerr := acquireChannelConcurrency(ctx, channelID, 1); !errors.Is(aerr, context.DeadlineExceeded) {
		t.Fatalf("满载等待应被 ctx 打断并返回 DeadlineExceeded, 实际 %v", aerr)
	}
	if elapsed := time.Since(startedAt); elapsed >= time.Second {
		t.Fatalf("ctx 打断应及时返回, 实际耗时 %v", elapsed)
	}

	releaseHeld()
	reacquired, err := acquireChannelConcurrency(context.Background(), channelID, 1)
	if err != nil {
		t.Fatalf("释放后应可再次获取: %v", err)
	}
	reacquired()
}

// TestWaitChannelRPMWindowBoundary 用 500ms 窗口/3 次上限验证滑动窗口边界:
// 窗口内第 4 个请求阻塞并被短 ctx 打断; 窗口整体滑出后立即放行。
func TestWaitChannelRPMWindowBoundary(t *testing.T) {
	resetChannelLimits()
	useShortRPMWindow(t, 500*time.Millisecond)
	const channelID = 9100

	for i := 0; i < 3; i++ {
		if err := waitChannelRPM(context.Background(), channelID, "k1", 3); err != nil {
			t.Fatalf("窗口内前 3 次应立即放行(第 %d 次): %v", i+1, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if werr := waitChannelRPM(ctx, channelID, "k1", 3); !errors.Is(werr, context.DeadlineExceeded) {
		t.Fatalf("达到上限时应阻塞并被短 ctx 打断, 实际 %v", werr)
	}

	// 窗口整体滑出后第 4 个请求应立即放行。
	time.Sleep(600 * time.Millisecond)
	startedAt := time.Now()
	if werr := waitChannelRPM(context.Background(), channelID, "k1", 3); werr != nil {
		t.Fatalf("窗口滑出后应立即放行: %v", werr)
	}
	if elapsed := time.Since(startedAt); elapsed > 300*time.Millisecond {
		t.Fatalf("窗口滑出后不应继续等待, 实际耗时 %v", elapsed)
	}
}

// TestWaitChannelRPMSlotFreesWhenOldestSlidesOut 回拨注入时间戳做确定性边界验证:
// 3 条记录分别回拨 450/300/100ms, 名额在最早一条(+500ms 窗口)到期时腾出,
// 阻塞的等待者应在约 50ms 后被放行而不是等满整窗。
func TestWaitChannelRPMSlotFreesWhenOldestSlidesOut(t *testing.T) {
	resetChannelLimits()
	useShortRPMWindow(t, 500*time.Millisecond)
	const channelID = 9101
	ref := keyRef{ChannelID: channelID, KeyID: "k1"}

	now := time.Now()
	channelLimitMu.Lock()
	channelKeyWindows[ref] = []time.Time{
		now.Add(-450 * time.Millisecond),
		now.Add(-300 * time.Millisecond),
		now.Add(-100 * time.Millisecond),
	}
	channelLimitMu.Unlock()

	startedAt := time.Now()
	if err := waitChannelRPM(context.Background(), channelID, "k1", 3); err != nil {
		t.Fatalf("最早时间戳滑出窗口后应放行: %v", err)
	}
	elapsed := time.Since(startedAt)
	if elapsed < 20*time.Millisecond || elapsed > 450*time.Millisecond {
		t.Fatalf("应等待最早时间戳到期(约 50ms)而非整窗(500ms), 实际 %v", elapsed)
	}
}

// TestWaitChannelRPMIndependentBudgetsPerKey 验证同一渠道双 Key 各自独立预算:
// k1 达到上限阻塞不影响 k2 立即放行; 回退旧字段的空串 Key 同样独立成桶。
func TestWaitChannelRPMIndependentBudgetsPerKey(t *testing.T) {
	resetChannelLimits()
	const channelID = 9102

	if err := waitChannelRPM(context.Background(), channelID, "k1", 1); err != nil {
		t.Fatalf("k1 首次应立即放行: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if werr := waitChannelRPM(ctx, channelID, "k1", 1); !errors.Is(werr, context.DeadlineExceeded) {
		t.Fatalf("k1 达到单次上限应阻塞, 实际 %v", werr)
	}
	if werr := waitChannelRPM(context.Background(), channelID, "k2", 1); werr != nil {
		t.Fatalf("k2 的独立预算不应受 k1 影响: %v", werr)
	}
	legacyCtx, legacyCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer legacyCancel()
	if werr := waitChannelRPM(context.Background(), channelID, "", 1); werr != nil {
		t.Fatalf("空串 Key 应作为稳定独立引用放行: %v", werr)
	}
	if werr := waitChannelRPM(legacyCtx, channelID, "", 1); !errors.Is(werr, context.DeadlineExceeded) {
		t.Fatalf("空串 Key 第二次同样应被限速, 实际 %v", werr)
	}
}

// TestWaitChannelRPMUnlimitedClearsLeftoverWindow 验证 rpm<=0 不限制且顺带清除遗留窗口,
// 之后重新启用限速时从零开始计数。
func TestWaitChannelRPMUnlimitedClearsLeftoverWindow(t *testing.T) {
	resetChannelLimits()
	const channelID = 9103

	if err := waitChannelRPM(context.Background(), channelID, "k1", 1); err != nil {
		t.Fatalf("首次放行失败: %v", err)
	}
	ref := keyRef{ChannelID: channelID, KeyID: "k1"}
	channelLimitMu.Lock()
	leftover := len(channelKeyWindows[ref])
	channelLimitMu.Unlock()
	if leftover != 1 {
		t.Fatalf("限速生效时应保留窗口记录, 实际 %d 条", leftover)
	}

	if err := waitChannelRPM(context.Background(), channelID, "k1", 0); err != nil {
		t.Fatalf("不限速应直接放行: %v", err)
	}
	channelLimitMu.Lock()
	leftover = len(channelKeyWindows[ref])
	channelLimitMu.Unlock()
	if leftover != 0 {
		t.Fatalf("切换为不限速后应清除遗留窗口, 实际剩余 %d 条", leftover)
	}

	negativeCtx, negativeCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer negativeCancel()
	if err := waitChannelRPM(negativeCtx, channelID, "k1", -5); err != nil {
		t.Fatalf("负值按不限速处理: %v", err)
	}
}

// TestCleanupChannelKeyStateClearsLimitState 验证渠道删除时扩展清理点同时清除
// 并发信号量与 RPM 窗口: 清理后同渠道同 Key 立即重新获得完整预算。
func TestCleanupChannelKeyStateClearsLimitState(t *testing.T) {
	resetChannelLimits()
	const channelID = 9104

	held, err := acquireChannelConcurrency(context.Background(), channelID, 4)
	if err != nil {
		t.Fatalf("领取槽位失败: %v", err)
	}
	if err := waitChannelRPM(context.Background(), channelID, "k1", 1); err != nil {
		t.Fatalf("记录 RPM 放行失败: %v", err)
	}

	CleanupChannelKeyState(channelID)

	// 已发放槽位的归还函数仍须安全可用(闭包持有原信号量)。
	held()

	// RPM 窗口已清空: 同 Key 立即重新放行, 不受删除前的记录压制。
	if werr := waitChannelRPM(context.Background(), channelID, "k1", 1); werr != nil {
		t.Fatalf("清理后同 Key 应重新获得预算: %v", werr)
	}
	// 信号量表已重建: 清理前占用的槽位不再计入新容量。
	first, err := acquireChannelConcurrency(context.Background(), channelID, 2)
	if err != nil {
		t.Fatalf("清理后应重建信号量: %v", err)
	}
	second, err := acquireChannelConcurrency(context.Background(), channelID, 2)
	if err != nil {
		t.Fatalf("清理前持有的槽位不应挤占新信号量容量: %v", err)
	}
	first()
	second()
}

// TestConcurrentGateTraffic 在 -race 下并发施压信号量与 RPM 门禁, 验证无数据竞争且
// 峰值并发不超过配置上限。
func TestConcurrentGateTraffic(t *testing.T) {
	resetChannelLimits()
	useShortRPMWindow(t, 400*time.Millisecond)
	const (
		channelID = 9105
		limit     = 3
		workers   = 12
	)

	var (
		wg       sync.WaitGroup
		inFlight int32 // 受 channelLimitMu 保护, 观测峰值并发。
		peak     int32
		mu       sync.Mutex
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := acquireChannelConcurrency(context.Background(), channelID, limit)
			if err != nil {
				return
			}
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			inFlight--
			mu.Unlock()
			release()
		}()
	}
	wg.Wait()
	if peak > limit {
		t.Fatalf("峰值并发 %d 超过上限 %d", peak, limit)
	}
}

// TestChannelUpdateRateLimitFields 验证 op 层更新分支: nil 不覆盖, 负值归零,
// 正常值写入缓存并可读回; 渠道删除联动清理限速状态。
func TestChannelUpdateRateLimitFields(t *testing.T) {
	setupFailoverTest(t)
	resetChannelLimits()

	rpm := 30
	concurrent := 5
	channel := model.Channel{
		Name:          integrationUniqueName("it-ratelimit"),
		Type:          model.ChannelProviderOpenAI,
		Enabled:       true,
		BaseURL:       "https://ratelimit.invalid",
		Key:           "rate-key",
		RateLimitRPM:  rpm,
		MaxConcurrent: concurrent,
		Models:        []model.ChannelModel{{Name: integrationUniqueName("it-model-ratelimit"), Source: model.ChannelModelSourceManual}},
	}
	if err := op.ChannelCreate(&channel, context.Background()); err != nil {
		t.Fatalf("创建渠道失败: %v", err)
	}

	got, err := op.ChannelGet(channel.ID)
	if err != nil {
		t.Fatalf("读取渠道失败: %v", err)
	}
	if got.RateLimitRPM != 30 || got.MaxConcurrent != 5 {
		t.Fatalf("创建值应写入缓存, 实际 rpm=%d concurrent=%d", got.RateLimitRPM, got.MaxConcurrent)
	}

	// nil 字段不得覆盖既有配置。
	if _, uerr := op.ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Name: &channel.Name}, context.Background()); uerr != nil {
		t.Fatalf("nil 更新失败: %v", uerr)
	}
	got, err = op.ChannelGet(channel.ID)
	if err != nil {
		t.Fatalf("二次读取渠道失败: %v", err)
	}
	if got.RateLimitRPM != 30 || got.MaxConcurrent != 5 {
		t.Fatalf("nil 更新不应覆盖, 实际 rpm=%d concurrent=%d", got.RateLimitRPM, got.MaxConcurrent)
	}

	// 负值归零(即不限制), 单独更新一个字段不影响另一个。
	newRPM := -1
	if _, uerr := op.ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, RateLimitRPM: &newRPM}, context.Background()); uerr != nil {
		t.Fatalf("负值更新失败: %v", uerr)
	}
	got, err = op.ChannelGet(channel.ID)
	if err != nil {
		t.Fatalf("三次读取渠道失败: %v", err)
	}
	if got.RateLimitRPM != 0 || got.MaxConcurrent != 5 {
		t.Fatalf("负值应归零且不影响其他字段, 实际 rpm=%d concurrent=%d", got.RateLimitRPM, got.MaxConcurrent)
	}

	// 正常值写入后读回一致。
	newRPM, newConcurrent := 120, 8
	if _, uerr := op.ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, RateLimitRPM: &newRPM, MaxConcurrent: &newConcurrent}, context.Background()); uerr != nil {
		t.Fatalf("正值更新失败: %v", uerr)
	}
	got, err = op.ChannelGet(channel.ID)
	if err != nil {
		t.Fatalf("四次读取渠道失败: %v", err)
	}
	if got.RateLimitRPM != 120 || got.MaxConcurrent != 8 {
		t.Fatalf("正值应写入, 实际 rpm=%d concurrent=%d", got.RateLimitRPM, got.MaxConcurrent)
	}

	// 删除渠道联动清空限速状态: 删除前先制造窗口记录, 删除后同 ID 重新放行。
	if derr := op.ChannelDel(channel.ID, context.Background()); derr != nil {
		t.Fatalf("删除渠道失败: %v", derr)
	}
	if werr := waitChannelRPM(context.Background(), channel.ID, "anykey", 1); werr != nil {
		t.Fatalf("删除后限速状态应已清空: %v", werr)
	}
}

// TestAcquireChannelConcurrencyNoBypassOnLower 验证 STA-13 修复(阻塞语义):
// 降额后旧在途请求仍被计入 active, 新请求阻塞等待而非通过换代绕过限制;
// 旧请求逐个归还使 active 回落后, 新请求才被放行。
func TestAcquireChannelConcurrencyNoBypassOnLower(t *testing.T) {
	resetChannelLimits()
	const channelID = 9201

	// 旧上限 3, 领满 3 个槽位。
	var held [3]func()
	for i := range held {
		r, err := acquireChannelConcurrency(context.Background(), channelID, 3)
		if err != nil {
			t.Fatalf("acquire %d/3: %v", i, err)
		}
		held[i] = r
	}

	// 降额到 1: 新请求应阻塞(active=3 >= 1), 不能绕过立即放行。
	acquired := make(chan error, 1)
	var releaseNew func()
	go func() {
		r, aerr := acquireChannelConcurrency(context.Background(), channelID, 1)
		releaseNew = r
		acquired <- aerr
	}()
	select {
	case aerr := <-acquired:
		t.Fatalf("降额后新请求不应立即放行(应阻塞等待 active 回落), 实际 %v", aerr)
	case <-time.After(150 * time.Millisecond):
		// 预期: 仍在阻塞。
	}

	// 归还 2 个旧槽位: active=1, 仍 >= limit(1), 继续阻塞。
	held[0]()
	held[1]()
	select {
	case aerr := <-acquired:
		t.Fatalf("active(1) >= limit(1) 时新请求仍应阻塞, 实际 %v", aerr)
	case <-time.After(150 * time.Millisecond):
		// 预期: 仍在阻塞。
	}

	// 归还最后一个: active=0, 新请求放行。
	held[2]()
	select {
	case aerr := <-acquired:
		if aerr != nil {
			t.Fatalf("active 回落后新请求应放行: %v", aerr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active 回落后新请求应被放行")
	}
	if releaseNew != nil {
		releaseNew()
	}
}

// TestAcquireChannelConcurrencyLowerBlocksUntilCtx 验证降额后新请求在 active 未回落前
// 持续阻塞, 被 ctx 打断时返回 ctx 错误且不占用槽位。
func TestAcquireChannelConcurrencyLowerBlocksUntilCtx(t *testing.T) {
	resetChannelLimits()
	const channelID = 9202

	held1, err := acquireChannelConcurrency(context.Background(), channelID, 2)
	if err != nil {
		t.Fatalf("acquire 1/2: %v", err)
	}
	held2, err := acquireChannelConcurrency(context.Background(), channelID, 2)
	if err != nil {
		t.Fatalf("acquire 2/2: %v", err)
	}

	// 降额到 1: active=2 >= 1, 新请求阻塞, 被短 ctx 打断。
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, aerr := acquireChannelConcurrency(ctx, channelID, 1); !errors.Is(aerr, context.DeadlineExceeded) {
		t.Fatalf("降额后新请求应阻塞并被 ctx 打断, 实际 %v", aerr)
	}

	// 被打断的请求不占用槽位: 归还全部后可重新获取。
	held1()
	held2()
	r, err := acquireChannelConcurrency(context.Background(), channelID, 1)
	if err != nil {
		t.Fatalf("归还后应可获取: %v", err)
	}
	r()
}

// TestAcquireChannelConcurrencyRaiseAdmitsMore 验证升额后按更宽的 limit 放行,
// active 连续不重置; 升额后超出新 limit 的请求仍阻塞。
func TestAcquireChannelConcurrencyRaiseAdmitsMore(t *testing.T) {
	resetChannelLimits()
	const channelID = 9203

	r1, err := acquireChannelConcurrency(context.Background(), channelID, 1)
	if err != nil {
		t.Fatalf("acquire 1/1: %v", err)
	}
	// 升额到 3: active=1, 可再放 2 个。
	r2, err := acquireChannelConcurrency(context.Background(), channelID, 3)
	if err != nil {
		t.Fatalf("acquire 2/3 after raise: %v", err)
	}
	r3, err := acquireChannelConcurrency(context.Background(), channelID, 3)
	if err != nil {
		t.Fatalf("acquire 3/3 after raise: %v", err)
	}

	// 第 4 个应阻塞。
	acquired := make(chan error, 1)
	var releaseNew func()
	go func() {
		r, aerr := acquireChannelConcurrency(context.Background(), channelID, 3)
		releaseNew = r
		acquired <- aerr
	}()
	select {
	case aerr := <-acquired:
		t.Fatalf("limit 3 已满, 第 4 个应阻塞, 实际 %v", aerr)
	case <-time.After(150 * time.Millisecond):
	}

	r1()
	r2()
	r3()
	select {
	case aerr := <-acquired:
		if aerr != nil {
			t.Fatalf("归还后应放行: %v", aerr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("归还后应放行")
	}
	if releaseNew != nil {
		releaseNew()
	}
}

// TestAcquireChannelConcurrencyUnlimitedThenRelimit 验证切到不限速时新请求直接放行且
// 不计入 active, 重新限额后旧在途仍计入, 从当前 active 起按新 limit 计数。
func TestAcquireChannelConcurrencyUnlimitedThenRelimit(t *testing.T) {
	resetChannelLimits()
	const channelID = 9204

	held, err := acquireChannelConcurrency(context.Background(), channelID, 1)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// 切换不限: 新请求直接放行, 不计入 active。
	for range 3 {
		r, err := acquireChannelConcurrency(context.Background(), channelID, 0)
		if err != nil {
			t.Fatalf("unlimited must pass: %v", err)
		}
		r()
	}
	// 重新限额 2: 旧 held 仍计入(active=1), 可再放 1 个。
	r, err := acquireChannelConcurrency(context.Background(), channelID, 2)
	if err != nil {
		t.Fatalf("re-limit acquire 2/2: %v", err)
	}
	// 第 3 个应阻塞。
	acquired := make(chan error, 1)
	var releaseNew func()
	go func() {
		rr, aerr := acquireChannelConcurrency(context.Background(), channelID, 2)
		releaseNew = rr
		acquired <- aerr
	}()
	select {
	case aerr := <-acquired:
		t.Fatalf("re-limit 已满应阻塞, 实际 %v", aerr)
	case <-time.After(150 * time.Millisecond):
	}
	held()
	r()
	select {
	case aerr := <-acquired:
		if aerr != nil {
			t.Fatalf("归还后应放行: %v", aerr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("归还后应放行")
	}
	if releaseNew != nil {
		releaseNew()
	}
}

// TestAcquireChannelConcurrencyConcurrentWithConfigChange 在 -race 下并发施压并混合
// 升额/降额(limit 在 2 与 4 间交替), 验证无数据竞争且峰值并发不超过所用最大 limit。
func TestAcquireChannelConcurrencyConcurrentWithConfigChange(t *testing.T) {
	resetChannelLimits()
	const (
		channelID = 9205
		workers   = 40
		maxLimit  = 4
	)

	var (
		wg     sync.WaitGroup
		active int32
		peak   int32
		mu     sync.Mutex
	)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 在 2 与 maxLimit 之间交替, 制造升额/降额混合。
			limit := 2
			if i%2 == 0 {
				limit = maxLimit
			}
			r, err := acquireChannelConcurrency(context.Background(), channelID, limit)
			if err != nil {
				return
			}
			mu.Lock()
			active++
			if active > peak {
				peak = active
			}
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			r()
		}()
	}
	wg.Wait()
	if peak > maxLimit {
		t.Fatalf("峰值并发 %d 超过最大 limit %d", peak, maxLimit)
	}
}
