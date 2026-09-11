package keylimit

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAcquireConcurrencyLimit(t *testing.T) {
	t.Cleanup(Reset)

	release, err := AcquireConcurrency(1, 1)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// 已满: fail-fast, 不等待。
	if _, err := AcquireConcurrency(1, 1); !errors.Is(err, ErrConcurrencyFull) {
		t.Fatalf("second acquire = %v, want ErrConcurrencyFull", err)
	}
	// 归还后可再次领取。
	release()
	if _, err := AcquireConcurrency(1, 1); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestAcquireConcurrencyIdempotentRelease(t *testing.T) {
	t.Cleanup(Reset)

	release, err := AcquireConcurrency(2, 1)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// 幂等归还: 重复释放不得把余额推回超过持有量, 否则并发超额。
	release()
	release()
	if _, err := AcquireConcurrency(2, 1); err != nil {
		t.Fatalf("acquire after double release: %v", err)
	}
	if _, err := AcquireConcurrency(2, 1); !errors.Is(err, ErrConcurrencyFull) {
		t.Fatal("slot leaked by non-idempotent release")
	}
}

func TestAcquireConcurrencyUnlimited(t *testing.T) {
	t.Cleanup(Reset)

	for range 5 {
		release, err := AcquireConcurrency(3, 0)
		if err != nil {
			t.Fatalf("limit<=0 must be unlimited: %v", err)
		}
		release()
	}
}

// TestAcquireConcurrencyConfigReplace 验证 STA-13 修复: 配置变更不替换计数容器,
// active 连续累加。升额后旧持有者仍计入 active, 新请求无法通过换代绕过限制。
func TestAcquireConcurrencyConfigReplace(t *testing.T) {
	t.Cleanup(Reset)

	// 旧上限 1, 领一个槽位。
	oldRelease, err := AcquireConcurrency(4, 1)
	if err != nil {
		t.Fatalf("acquire under old limit: %v", err)
	}
	// 升额到 2: 旧持有者仍计入 active(连续计数, 不换代), 只剩 1 个名额。
	if _, err := AcquireConcurrency(4, 2); err != nil {
		t.Fatalf("acquire 2/2 after widen (old holder must count): %v", err)
	}
	if _, err := AcquireConcurrency(4, 2); !errors.Is(err, ErrConcurrencyFull) {
		t.Fatal("active=2, limit=2 must be full; new request must not bypass via replacement")
	}
	// 旧持有者归还使 active 回落到 1, 新名额腾出。
	oldRelease()
	if _, err := AcquireConcurrency(4, 2); err != nil {
		t.Fatalf("after old release, slot should free: %v", err)
	}
	if _, err := AcquireConcurrency(4, 2); !errors.Is(err, ErrConcurrencyFull) {
		t.Fatal("active=2, limit=2 must be full again")
	}
}

// TestAcquireConcurrencyLowerLimitRejectsNewWhileInFlight 验证降额期间旧在途请求
// 仍被计入 active, 新请求在 active 未降到新 limit 之前被 fail-fast 拒绝, 无法绕过。
func TestAcquireConcurrencyLowerLimitRejectsNewWhileInFlight(t *testing.T) {
	t.Cleanup(Reset)
	const key = 1001

	// 旧上限 3, 领满 3 个槽位。
	var releases [3]func()
	for i := range releases {
		r, err := AcquireConcurrency(key, 3)
		if err != nil {
			t.Fatalf("acquire %d under limit 3: %v", i, err)
		}
		releases[i] = r
	}
	// 降额到 1: 旧在途 3 仍计入, 新请求应被拒绝, 不能绕过。
	if _, err := AcquireConcurrency(key, 1); !errors.Is(err, ErrConcurrencyFull) {
		t.Fatalf("lower limit must reject new request while active(3) > limit(1), got %v", err)
	}
	// 归还 2 个旧槽位: active=1, 仍 active>=limit, 继续拒绝。
	releases[0]()
	releases[1]()
	if _, err := AcquireConcurrency(key, 1); !errors.Is(err, ErrConcurrencyFull) {
		t.Fatalf("active(1) >= limit(1) must still reject, got %v", err)
	}
	// 归还最后一个: active=0, 新请求放行。
	releases[2]()
	r, err := AcquireConcurrency(key, 1)
	if err != nil {
		t.Fatalf("after active drops to 0, acquire should succeed: %v", err)
	}
	r()
}

// TestAcquireConcurrencyRaiseLimitAdmitsMore 验证升额后按更宽的 limit 放行,
// active 连续不重置。
func TestAcquireConcurrencyRaiseLimitAdmitsMore(t *testing.T) {
	t.Cleanup(Reset)
	const key = 1002

	r1, err := AcquireConcurrency(key, 1)
	if err != nil {
		t.Fatalf("acquire 1/1: %v", err)
	}
	if _, err := AcquireConcurrency(key, 1); !errors.Is(err, ErrConcurrencyFull) {
		t.Fatal("limit 1 should be full")
	}
	// 升额到 3: active=1, 可再放 2 个。
	r2, err := AcquireConcurrency(key, 3)
	if err != nil {
		t.Fatalf("acquire 2/3 after raise: %v", err)
	}
	r3, err := AcquireConcurrency(key, 3)
	if err != nil {
		t.Fatalf("acquire 3/3 after raise: %v", err)
	}
	if _, err := AcquireConcurrency(key, 3); !errors.Is(err, ErrConcurrencyFull) {
		t.Fatal("limit 3 should be full")
	}
	r1()
	r2()
	r3()
}

// TestAcquireConcurrencyUnlimitedThenRelimit 验证切到不限速时新请求直接放行且不计入
// active, 重新限额后旧在途仍计入, 从当前 active 起按新 limit 计数。
func TestAcquireConcurrencyUnlimitedThenRelimit(t *testing.T) {
	t.Cleanup(Reset)
	const key = 1003

	// 先限额 1 领一个: active=1。
	held, err := AcquireConcurrency(key, 1)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// 切换不限: 新请求直接放行, 不计入 active。
	for range 3 {
		r, err := AcquireConcurrency(key, 0)
		if err != nil {
			t.Fatalf("unlimited must pass: %v", err)
		}
		r()
	}
	// 重新限额 2: 旧 held 仍计入(active=1), 可再放 1 个。
	r, err := AcquireConcurrency(key, 2)
	if err != nil {
		t.Fatalf("re-limit acquire 2/2: %v", err)
	}
	if _, err := AcquireConcurrency(key, 2); !errors.Is(err, ErrConcurrencyFull) {
		t.Fatal("re-limit should be full: active=2, limit=2")
	}
	held()
	r()
}

// TestAcquireConcurrencyCleanupRecreateFresh 验证密钥删除后同 ID 重建得到全新零计数
// 状态, 旧在途归还作用于孤立状态, 不挤占新容量。
func TestAcquireConcurrencyCleanupRecreateFresh(t *testing.T) {
	t.Cleanup(Reset)
	const key = 1004

	held, err := AcquireConcurrency(key, 2)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	Cleanup(key)
	// 清理后归还旧槽位: 作用于孤立状态, 不影响新状态。
	held()
	// 新状态可领满 2 个。
	r1, err := AcquireConcurrency(key, 2)
	if err != nil {
		t.Fatalf("acquire 1/2 after cleanup: %v", err)
	}
	r2, err := AcquireConcurrency(key, 2)
	if err != nil {
		t.Fatalf("acquire 2/2 after cleanup: %v", err)
	}
	if _, err := AcquireConcurrency(key, 2); !errors.Is(err, ErrConcurrencyFull) {
		t.Fatal("fresh state should be full at 2")
	}
	r1()
	r2()
}

// TestAcquireConcurrencyConcurrent 在 -race 下并发施压, 验证无数据竞争且
// 峰值并发不超过配置上限。
func TestAcquireConcurrencyConcurrent(t *testing.T) {
	t.Cleanup(Reset)
	const (
		key     = 1005
		limit   = 4
		workers = 60
	)

	var (
		wg     sync.WaitGroup
		active int32
		peak   int32
		mu     sync.Mutex
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := AcquireConcurrency(key, limit)
			if err != nil {
				return
			}
			mu.Lock()
			active++
			if active > peak {
				peak = active
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			r()
		}()
	}
	wg.Wait()
	if peak > limit {
		t.Fatalf("peak concurrency %d exceeds limit %d", peak, limit)
	}
}

func TestAcquireRPMPermitWindow(t *testing.T) {
	t.Cleanup(Reset)
	oldWindow := rpmWindow
	rpmWindow = 20 * time.Millisecond
	t.Cleanup(func() { rpmWindow = oldWindow })

	for range 2 {
		if retryAfter, err := AcquireRPMPermit(5, 2); err != nil {
			t.Fatalf("permit within window: retryAfter=%d err=%v", retryAfter, err)
		}
	}
	retryAfter, err := AcquireRPMPermit(5, 2)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("third permit = %v, want ErrRateLimited", err)
	}
	if retryAfter < 1 || retryAfter > 2 {
		t.Fatalf("retryAfter = %d, want 1..2 for 20ms window", retryAfter)
	}
	// 窗口滑出后名额恢复。
	time.Sleep(25 * time.Millisecond)
	if _, err := AcquireRPMPermit(5, 2); err != nil {
		t.Fatalf("permit after window slide: %v", err)
	}
}

func TestAcquireRPMPermitUnlimitedClearsWindow(t *testing.T) {
	t.Cleanup(Reset)
	oldWindow := rpmWindow
	rpmWindow = time.Hour
	t.Cleanup(func() { rpmWindow = oldWindow })

	if _, err := AcquireRPMPermit(6, 1); err != nil {
		t.Fatalf("first permit: %v", err)
	}
	// 改为不限时清理遗留窗口, 恢复后旧记录不得继续限制。
	if _, err := AcquireRPMPermit(6, 0); err != nil {
		t.Fatalf("unlimited permit: %v", err)
	}
	if _, err := AcquireRPMPermit(6, 1); err != nil {
		t.Fatalf("permit after unlimited reset: %v", err)
	}
}

func TestCleanup(t *testing.T) {
	t.Cleanup(Reset)

	release, err := AcquireConcurrency(7, 1)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := AcquireRPMPermit(7, 1); err != nil {
		t.Fatalf("permit: %v", err)
	}
	Cleanup(7)
	// 已发放槽位的归还闭包持有原状态对象, 清理后归还依然安全。
	release()
	// 清理后全新状态: 并发与 RPM 均不再受限。
	if _, err := AcquireConcurrency(7, 1); err != nil {
		t.Fatalf("acquire after cleanup: %v", err)
	}
	if _, err := AcquireRPMPermit(7, 1); err != nil {
		t.Fatalf("permit after cleanup: %v", err)
	}
}
