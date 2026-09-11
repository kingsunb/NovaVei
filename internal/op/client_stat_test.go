package op

import (
	"context"
	"testing"
	"time"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
)

// resetClientStatForTest 清空内存缓存与数据库表, 保证用例隔离。
func resetClientStatForTest(t *testing.T) {
	t.Helper()
	clientStatMu.Lock()
	clientStatCache = make(map[string]*model.ClientStat)
	clientStatMu.Unlock()
	if err := db.GetDB().Where("1 = 1").Delete(&model.ClientStat{}).Error; err != nil {
		t.Fatalf("clear client_stats: %v", err)
	}
}

// TestClientStatLoadMergesNotOverwrites 验证运行时重载(ClientStatLoad)不会覆盖
// 内存中尚未 flush 的增量: 内存已有 IP 保留其累计值, DB 独有的 IP 被补入。
// 对应审计 STA-12: 运行中 InitCache→ClientStatLoad 不应覆盖未刷增量。
func TestClientStatLoadMergesNotOverwrites(t *testing.T) {
	resetClientStatForTest(t)
	t.Cleanup(func() { resetClientStatForTest(t) })

	// 内存累计但未 flush 的 IP: TrackClientStat 只写内存, 不落库。
	for i := 0; i < 5; i++ {
		TrackClientStat("1.2.3.4")
	}

	// DB 独有条目(模拟上一进程遗留, 或本进程已淘汰且累计值已在上轮 flush 落库的 IP)。
	dbOnly := model.ClientStat{
		IP:           "5.6.7.8",
		FirstSeen:    time.Now().Add(-time.Hour),
		LastSeen:     time.Now().Add(-time.Hour),
		RequestCount: 42,
	}
	if err := db.GetDB().Create(&dbOnly).Error; err != nil {
		t.Fatalf("create db-only client stat: %v", err)
	}

	if err := ClientStatLoad(context.Background()); err != nil {
		t.Fatalf("ClientStatLoad: %v", err)
	}

	clientStatMu.Lock()
	defer clientStatMu.Unlock()

	inMem, ok := clientStatCache["1.2.3.4"]
	if !ok {
		t.Fatal("内存中未刷增量的 1.2.3.4 应被保留, 不被重载覆盖")
	}
	if inMem.RequestCount != 5 {
		t.Fatalf("1.2.3.4 未刷增量(5)应被保留, 实际 %d", inMem.RequestCount)
	}

	fromDB, ok := clientStatCache["5.6.7.8"]
	if !ok {
		t.Fatal("DB 独有的 5.6.7.8 应被补入缓存")
	}
	if fromDB.RequestCount != 42 {
		t.Fatalf("5.6.7.8 应从 DB 补入累计 42, 实际 %d", fromDB.RequestCount)
	}
}

// TestClientStatLoadStartupLoadsAll 验证启动时(缓存为空)ClientStatLoad 退化为全量加载。
func TestClientStatLoadStartupLoadsAll(t *testing.T) {
	resetClientStatForTest(t)
	t.Cleanup(func() { resetClientStatForTest(t) })

	rows := []model.ClientStat{
		{IP: "10.0.0.1", FirstSeen: time.Now(), LastSeen: time.Now(), RequestCount: 3},
		{IP: "10.0.0.2", FirstSeen: time.Now(), LastSeen: time.Now(), RequestCount: 7},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("create client stats: %v", err)
	}

	if err := ClientStatLoad(context.Background()); err != nil {
		t.Fatalf("ClientStatLoad: %v", err)
	}

	clientStatMu.Lock()
	defer clientStatMu.Unlock()
	if len(clientStatCache) != 2 {
		t.Fatalf("启动空缓存应全量加载 2 条, 实际 %d", len(clientStatCache))
	}
}

// TestClientStatCountSince 验证按时间窗口统计活跃客户端 IP 数:
// 仅计入 last_seen >= since 的去重 IP; since 为零值时返回全表去重数。
func TestClientStatCountSince(t *testing.T) {
	resetClientStatForTest(t)
	t.Cleanup(func() { resetClientStatForTest(t) })

	now := time.Now()
	rows := []model.ClientStat{
		{IP: "10.0.0.1", FirstSeen: now.Add(-2 * time.Hour), LastSeen: now.Add(-30 * time.Minute), RequestCount: 3},
		{IP: "10.0.0.2", FirstSeen: now.Add(-2 * time.Hour), LastSeen: now.Add(-10 * time.Minute), RequestCount: 7},
		{IP: "10.0.0.3", FirstSeen: now.Add(-48 * time.Hour), LastSeen: now.Add(-40 * time.Hour), RequestCount: 1},
	}
	if err := db.GetDB().Create(&rows).Error; err != nil {
		t.Fatalf("create client stats: %v", err)
	}

	// 近 1 小时: 仅 10.0.0.1 与 10.0.0.2 活跃, 10.0.0.3 在 40h 前
	got, err := ClientStatCountSince(context.Background(), now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ClientStatCountSince: %v", err)
	}
	if got != 2 {
		t.Fatalf("近 1 小时活跃 IP 应 2, 实际 %d", got)
	}

	// 近 24 小时: 仍 2(10.0.0.3 在 40h 前, 超出窗口)
	got, err = ClientStatCountSince(context.Background(), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("ClientStatCountSince: %v", err)
	}
	if got != 2 {
		t.Fatalf("近 24 小时活跃 IP 应 2, 实际 %d", got)
	}

	// 近 48 小时: 全部 3 个 IP
	got, err = ClientStatCountSince(context.Background(), now.Add(-48*time.Hour))
	if err != nil {
		t.Fatalf("ClientStatCountSince: %v", err)
	}
	if got != 3 {
		t.Fatalf("近 48 小时活跃 IP 应 3, 实际 %d", got)
	}

	// 零值 since: 全表去重数
	got, err = ClientStatCountSince(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("ClientStatCountSince: %v", err)
	}
	if got != 3 {
		t.Fatalf("全表去重 IP 应 3, 实际 %d", got)
	}
}
