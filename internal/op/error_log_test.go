package op

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kingsunb/NovaVei/internal/db"
	"github.com/kingsunb/NovaVei/internal/model"
)

// errorLogTestContext 返回带超时的独立上下文, 并清空错误日志表保证用例隔离。
func errorLogTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := ErrorLogDeleteAll(ctx); err != nil {
		t.Fatalf("clear error logs: %v", err)
	}
	return ctx
}

func TestErrorLogCreateAndList(t *testing.T) {
	ctx := errorLogTestContext(t)

	now := time.Now()
	entries := []model.ErrorLog{
		{CreatedAt: now.Add(-3 * time.Hour), Model: "gpt-group", ChannelName: "ch-a", TargetModel: "up-a", ClientIP: "203.0.113.1", ErrClass: "timeout", ErrBrief: "first"},
		{CreatedAt: now.Add(-2 * time.Hour), Model: "claude-group", ChannelName: "ch-b", TargetModel: "up-b", ClientIP: "203.0.113.2", ErrClass: "upstream_4xx", ErrBrief: "second"},
		{CreatedAt: now.Add(-1 * time.Hour), Model: "gemini-group", ChannelName: "ch-c", TargetModel: "up-c", ClientIP: "203.0.113.3", ErrClass: "timeout", ErrBrief: "third"},
	}
	for i := range entries {
		if err := ErrorLogCreate(ctx, entries[i]); err != nil {
			t.Fatalf("create error log %d: %v", i, err)
		}
	}

	// 全量按时间新到旧。
	logs, err := ErrorLogList(ctx, 0, "")
	if err != nil {
		t.Fatalf("list error logs: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("listed %d logs, want 3", len(logs))
	}
	if logs[0].ErrBrief != "third" || logs[2].ErrBrief != "first" {
		t.Fatalf("logs must be ordered newest first, got %q..%q", logs[0].ErrBrief, logs[2].ErrBrief)
	}
	first := logs[0]
	if first.ID == 0 || first.Model != "gemini-group" || first.ChannelName != "ch-c" || first.TargetModel != "up-c" || first.ClientIP != "203.0.113.3" {
		t.Fatalf("newest log fields mismatch: %+v", first)
	}
	if first.CreatedAt.IsZero() {
		t.Fatal("created_at must be persisted")
	}

	// limit 截断保留最新。
	limited, err := ErrorLogList(ctx, 2, "")
	if err != nil {
		t.Fatalf("list limited: %v", err)
	}
	if len(limited) != 2 || limited[0].ErrBrief != "third" || limited[1].ErrBrief != "second" {
		t.Fatalf("limit=2 should keep two newest, got %+v", limited)
	}

	// class 过滤。
	filtered, err := ErrorLogList(ctx, 50, "timeout")
	if err != nil {
		t.Fatalf("list by class: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("class filter should return 2, got %d", len(filtered))
	}
	for _, entry := range filtered {
		if entry.ErrClass != "timeout" {
			t.Fatalf("unexpected class %q in filtered result", entry.ErrClass)
		}
	}
}

func TestErrorLogBriefTruncatedTo256Bytes(t *testing.T) {
	ctx := errorLogTestContext(t)

	multibyte := strings.Repeat("汉", 300) // 900 字节, 截断后须落在完整 UTF-8 边界内
	if err := ErrorLogCreate(ctx, model.ErrorLog{Model: "m", ErrBrief: multibyte}); err != nil {
		t.Fatalf("create: %v", err)
	}
	logs, err := ErrorLogList(ctx, 1, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected exactly 1 log, got %d", len(logs))
	}
	stored := logs[0].ErrBrief
	if len(stored) > errBriefMaxBytes {
		t.Fatalf("stored brief = %d bytes, want <= %d", len(stored), errBriefMaxBytes)
	}
	if !utf8.ValidString(stored) {
		t.Fatalf("stored brief must stay valid UTF-8: %q", stored)
	}
	if !strings.HasPrefix(multibyte, stored) {
		t.Fatal("stored brief must be a byte-prefix of the original")
	}

	ascii := strings.Repeat("a", 500)
	if err := ErrorLogCreate(ctx, model.ErrorLog{Model: "m", ErrBrief: ascii}); err != nil {
		t.Fatalf("create ascii: %v", err)
	}
	logs, err = ErrorLogList(ctx, 1, "")
	if err != nil {
		t.Fatalf("list ascii: %v", err)
	}
	if len(logs[0].ErrBrief) != errBriefMaxBytes || !strings.HasPrefix(ascii, logs[0].ErrBrief) {
		t.Fatalf("ascii brief should truncate to exact byte limit, got %d", len(logs[0].ErrBrief))
	}
}

func TestErrorLogCountSince24h(t *testing.T) {
	ctx := errorLogTestContext(t)

	if err := ErrorLogCreate(ctx, model.ErrorLog{Model: "old", CreatedAt: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatalf("create old: %v", err)
	}
	if err := ErrorLogCreate(ctx, model.ErrorLog{Model: "fresh"}); err != nil {
		t.Fatalf("create fresh: %v", err)
	}

	count, err := ErrorLogCountSince(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("count since: %v", err)
	}
	if count != 1 {
		t.Fatalf("error_count_24h = %d, want 1 (仅窗口内的记录)", count)
	}
	total, err := ErrorLogCountSince(ctx, time.Now().Add(-96*time.Hour))
	if err != nil {
		t.Fatalf("count all: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
}

func TestErrorLogCleanExpiredByRetention(t *testing.T) {
	ctx := errorLogTestContext(t)

	// 直接操作进程内设置缓存以控制保留天数, 结束后恢复原值。
	key := model.SettingKeyErrorRetentionDays
	prevValue, hadPrev := settingCache.Get(key)
	setRetention := func(days string) { settingCache.Set(key, days) }
	t.Cleanup(func() {
		if hadPrev {
			settingCache.Set(key, prevValue)
		} else {
			settingCache.Del(key)
		}
	})

	setRetention("3")
	if days := ErrorLogRetentionDays(ctx); days != 3 {
		t.Fatalf("retention = %d, want 3", days)
	}
	if err := ErrorLogCreate(ctx, model.ErrorLog{Model: "expired", CreatedAt: time.Now().AddDate(0, 0, -5)}); err != nil {
		t.Fatalf("create expired: %v", err)
	}
	if err := ErrorLogCreate(ctx, model.ErrorLog{Model: "kept"}); err != nil {
		t.Fatalf("create kept: %v", err)
	}
	removed, err := ErrorLogCleanExpired(ctx)
	if err != nil {
		t.Fatalf("clean expired: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	logs, err := ErrorLogList(ctx, 0, "")
	if err != nil {
		t.Fatalf("list after clean: %v", err)
	}
	if len(logs) != 1 || logs[0].Model != "kept" {
		t.Fatalf("only the fresh row must survive, got %+v", logs)
	}

	// 保留天数为 0 表示永久保留, 不删除任何记录。
	setRetention("0")
	if err := ErrorLogCreate(ctx, model.ErrorLog{Model: "ancient", CreatedAt: time.Now().AddDate(0, 0, -30)}); err != nil {
		t.Fatalf("create ancient: %v", err)
	}
	removed, err = ErrorLogCleanExpired(ctx)
	if err != nil {
		t.Fatalf("clean with permanent retention: %v", err)
	}
	if removed != 0 {
		t.Fatalf("permanent retention must remove nothing, removed %d", removed)
	}

	// 非法负值按永久保留处理, 避免误删数据。
	setRetention("-1")
	if days := ErrorLogRetentionDays(ctx); days != 0 {
		t.Fatalf("negative retention should clamp to 0, got %d", days)
	}
	removed, err = ErrorLogCleanExpired(ctx)
	if err != nil || removed != 0 {
		t.Fatalf("negative retention must remove nothing, removed=%d err=%v", removed, err)
	}
}

func TestErrorLogDeleteAll(t *testing.T) {
	ctx := errorLogTestContext(t)

	for i := 0; i < 3; i++ {
		if err := ErrorLogCreate(ctx, model.ErrorLog{Model: "bulk"}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if err := ErrorLogDeleteAll(ctx); err != nil {
		t.Fatalf("delete all: %v", err)
	}
	logs, err := ErrorLogList(ctx, 0, "")
	if err != nil {
		t.Fatalf("list after delete all: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("table must be empty after delete all, got %d rows", len(logs))
	}
}

func TestErrorLogCreateWithoutDatabaseFailsWithSentinel(t *testing.T) {
	if db.GetDB() != nil {
		t.Skip("需要未初始化数据库的环境才能验证哨兵错误")
	}
	err := ErrorLogCreate(context.Background(), model.ErrorLog{Model: "x"})
	if err == nil || !errors.Is(err, ErrDatabaseNotInitialized) {
		t.Fatalf("want sentinel error, got %v", err)
	}
}

// TestErrorLogDetailTruncatedTo64KB 校验错误详情按 64KB 上限截断且不破坏 UTF-8 边界。
func TestErrorLogDetailTruncatedTo64KB(t *testing.T) {
	ctx := errorLogTestContext(t)

	big := strings.Repeat("错", model.MaxErrDetailBytes) // 每字 3 字节, 远超上限
	entry := model.ErrorLog{Model: "g", ErrClass: "timeout", ErrBrief: "b", ErrDetail: big}
	if err := ErrorLogCreate(ctx, entry); err != nil {
		t.Fatalf("create: %v", err)
	}
	logs, err := ErrorLogList(ctx, 1, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expect 1 row, got %d", len(logs))
	}
	got := logs[0].ErrDetail
	if len(got) > model.MaxErrDetailBytes {
		t.Fatalf("detail 未截断: %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("截断破坏了 UTF-8 边界")
	}
}

// TestErrorLogEnqueueBatchFlush 校验队列写入方攒批落库与字段截断:
// 入队多条后显式触发关闭排空, 记录应全部落库且 detail 超长被截断。
func TestErrorLogEnqueueBatchFlush(t *testing.T) {
	ctx := errorLogTestContext(t)

	for i := range 5 {
		entry := model.ErrorLog{
			Model: "grp", ChannelName: "ch", TargetModel: "up",
			ClientIP: "10.0.0.9", ErrClass: "upstream_5xx",
			ErrBrief:  strings.Repeat("x", errBriefMaxBytes+100),
			ErrDetail: strings.Repeat("y", model.MaxErrDetailBytes+100),
		}
		if i == 0 {
			entry.Model = "first"
		}
		if !ErrorLogEnqueue(entry) {
			t.Fatalf("entry %d 入队失败", i)
		}
	}
	if err := FlushErrorLogQueue(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	logs, err := ErrorLogList(ctx, 0, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(logs) != 5 {
		t.Fatalf("expect 5 rows, got %d", len(logs))
	}
	for _, row := range logs {
		if len(row.ErrBrief) > errBriefMaxBytes {
			t.Fatalf("brief 未截断: %d", len(row.ErrBrief))
		}
		if len(row.ErrDetail) > model.MaxErrDetailBytes {
			t.Fatalf("detail 未截断: %d", len(row.ErrDetail))
		}
	}
	// 列表按新到旧: 最后入队的是最新记录, 最早入队的 "first" 应排在最末。
	if logs[0].Model != "grp" || logs[len(logs)-1].Model != "first" {
		t.Fatalf("排序异常: newest=%q oldest=%q", logs[0].Model, logs[len(logs)-1].Model)
	}

	// 队列已停止: 再次入队应重新启动写入协程并仍可落库。
	if !ErrorLogEnqueue(model.ErrorLog{Model: "again", ErrClass: "timeout", ErrBrief: "b"}) {
		t.Fatal("重启后入队失败")
	}
	if err := FlushErrorLogQueue(ctx); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	logs, err = ErrorLogList(ctx, 1, "")
	if err != nil {
		t.Fatalf("relist: %v", err)
	}
	if len(logs) != 1 || logs[0].Model != "again" {
		t.Fatalf("重启队列后落库异常: %+v", logs)
	}
}

// TestErrorLogTrimToMaxCount 校验按最大条数裁剪: 保留最新 N 条, 最旧记录先被删除。
func TestErrorLogTrimToMaxCount(t *testing.T) {
	ctx := errorLogTestContext(t)

	if err := SettingSetString(model.SettingKeyErrorRetentionMaxCount, "3"); err != nil {
		t.Fatalf("set max count: %v", err)
	}
	t.Cleanup(func() { _ = SettingSetString(model.SettingKeyErrorRetentionMaxCount, "0") })

	now := time.Now()
	for i := range 5 {
		entry := model.ErrorLog{
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
			Model:     fmt.Sprintf("m%d", i), ErrClass: "timeout", ErrBrief: fmt.Sprintf("b%d", i),
		}
		if err := ErrorLogCreate(ctx, entry); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if err := ErrorLogTrimToMaxCount(ctx); err != nil {
		t.Fatalf("trim: %v", err)
	}
	logs, err := ErrorLogList(ctx, 0, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("expect 3 rows after trim, got %d", len(logs))
	}
	// 最新 3 条是 m2/m3/m4; m0/m1 已删。
	for _, row := range logs {
		if row.Model == "m0" || row.Model == "m1" {
			t.Fatalf("最旧记录未被裁剪: %s", row.Model)
		}
	}

	// 上限改回 0(不限): CleanExpired 不应再触发条数裁剪。
	if err := SettingSetString(model.SettingKeyErrorRetentionMaxCount, "0"); err != nil {
		t.Fatalf("reset max count: %v", err)
	}
	removed, err := ErrorLogCleanExpired(ctx)
	if err != nil || removed != 0 {
		t.Fatalf("clean with unlimited count should be no-op: removed=%d err=%v", removed, err)
	}
}
