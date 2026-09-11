package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kingsunb/NovaVeil/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 迁移链端到端测试: 先手工搭建旧版(v0.11 时代)库表结构并灌入存量数据,
// 再执行完整 InitDB(迁移 003..012 + AutoMigrate), 验证渠道模型拆表后的新结构:
//   - channel_models 表存在且旧逗号串模型转换为行(手动来源优先于自动来源);
//   - group_items 重建为 ChannelModelID 单外键结构, 引用成员(channel_id=0)转为 RefGroupName;
//   - 同组两条引用成员可以共存(group_items 不再携带数据库唯一索引);
//   - 旧统计表被删除且渠道表不再携带统计列(统计功能已下线);
//   - 渠道级 max_output/thinking_level 转为按模型的 model_limits JSON。

// setupLegacyDB 在临时目录中构造旧版 schema 的数据库文件, 返回文件路径。
func setupLegacyDB(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "nv-migrate-test-")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "legacy.db")

	legacy, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开临时旧版库失败: %v", err)
	}

	stmts := []string{
		`CREATE TABLE channels ( id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, type TEXT, enabled BOOLEAN DEFAULT true, base_url TEXT, key TEXT, model TEXT, custom_model TEXT, proxy BOOLEAN DEFAULT false, auto_sync BOOLEAN DEFAULT false, custom_header TEXT, param_override TEXT, channel_proxy TEXT, match_regex TEXT, max_context INTEGER, max_output INTEGER, thinking_level TEXT, input_token INTEGER DEFAULT 0, output_token INTEGER DEFAULT 0, input_cost REAL DEFAULT 0, output_cost REAL DEFAULT 0, wait_time INTEGER DEFAULT 0, request_success INTEGER DEFAULT 0, request_failed INTEGER DEFAULT 0 )`,
		`INSERT INTO channels(name, type, enabled, base_url, key, model, custom_model, max_output, thinking_level) VALUES('ch-a', 'openai', 1, 'https://a.invalid', 'key-a', 'm1,m2', 'm2,m3', 4096, 'high')`,
		`CREATE TABLE groups ( id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, mode TEXT NOT NULL DEFAULT 'manual', active_item_id INTEGER NOT NULL DEFAULT 0, relay_config TEXT )`,
		`INSERT INTO groups(name, mode) VALUES('gb', 'failover')`,
		`CREATE TABLE group_items ( id INTEGER PRIMARY KEY AUTOINCREMENT, group_id INTEGER NOT NULL, channel_id INTEGER NOT NULL, model_name TEXT NOT NULL, priority INTEGER )`,
		`INSERT INTO group_items(group_id, channel_id, model_name, priority) VALUES(1, 1, 'm1', 0)`,
		`INSERT INTO group_items(group_id, channel_id, model_name, priority) VALUES(1, 1, 'm3', 1)`,
		`INSERT INTO group_items(group_id, channel_id, model_name, priority) VALUES(1, 0, 'gb', 2)`,
		`UPDATE groups SET active_item_id = 3 WHERE id = 1`,
		`CREATE TABLE stats_channels ( channel_id INTEGER PRIMARY KEY, input_token INTEGER DEFAULT 0, output_token INTEGER DEFAULT 0, input_cost REAL DEFAULT 0, output_cost REAL DEFAULT 0, wait_time INTEGER DEFAULT 0, request_success INTEGER DEFAULT 0, request_failed INTEGER DEFAULT 0 )`,
		`INSERT INTO stats_channels(channel_id, input_token, output_token, request_success, request_failed) VALUES(1, 11, 22, 5, 1)`,
		`CREATE TABLE stats_models ( id INTEGER PRIMARY KEY, name TEXT NOT NULL, channel_id INTEGER NOT NULL, input_token INTEGER DEFAULT 0, output_token INTEGER DEFAULT 0, input_cost REAL DEFAULT 0, output_cost REAL DEFAULT 0, wait_time INTEGER DEFAULT 0, request_success INTEGER DEFAULT 0, request_failed INTEGER DEFAULT 0 )`,
		`INSERT INTO stats_models(id, name, channel_id, input_token) VALUES(1, 'm1', 1, 7)`,
	}
	for _, stmt := range stmts {
		if err := legacy.Exec(stmt).Error; err != nil {
			t.Fatalf("初始化旧版库失败(%s): %v", stmt, err)
		}
	}
	if sqlDB, err := legacy.DB(); err != nil {
		t.Fatalf("获取旧版库连接失败: %v", err)
	} else if err := sqlDB.Close(); err != nil {
		t.Fatalf("关闭旧版库连接失败: %v", err)
	}
	return path
}

func TestMigrateToChannelModelSplit(t *testing.T) {
	path := setupLegacyDB(t)
	// 完整走一遍生产启动路径: BeforeAuto + AutoMigrate + AfterAuto。
	if err := InitDB("sqlite", path, os.Getenv("MIGRATE_DEBUG") == "1"); err != nil {
		t.Fatalf("全量迁移失败: %v", err)
	}

	// channel_models 表存在且逗号串按行转换, 手动来源覆盖同名自动来源。
	var count int64
	row := GetDB().Table("channel_models").Count(&count)
	if row.Error != nil {
		t.Fatalf("channel_models 应存在: %v", row.Error)
	}
	if count != 3 {
		t.Fatalf("channel_models 应有 3 行(m1/m2/m3), 实际 %d", count)
	}

	type cmRow struct {
		ChannelID int                      `gorm:"column:channel_id"`
		Name      string                   `gorm:"column:name"`
		Source    model.ChannelModelSource `gorm:"column:source"`
	}
	rows := []cmRow{}
	if err := GetDB().Table("channel_models").Select("channel_id, name, source").Order("name").Find(&rows).Error; err != nil {
		t.Fatalf("读取 channel_models 失败: %v", err)
	}
	sources := map[string]model.ChannelModelSource{}
	for _, r := range rows {
		if r.ChannelID != 1 {
			t.Fatalf("渠道模型应归属渠道 1, 实际 %+v", r)
		}
		sources[r.Name] = r.Source
	}
	for _, name := range []string{"m1", "m2", "m3"} {
		if sources[name] != model.ChannelModelSourceManual && sources[name] != model.ChannelModelSourceAuto {
			t.Fatalf("模型 %s 来源非法: %q", name, sources[name])
		}
	}
	// m2 同时出现在旧 model 与 custom_model 列, 手动优先。
	if sources["m2"] != model.ChannelModelSourceManual || sources["m1"] != model.ChannelModelSourceAuto || sources["m3"] != model.ChannelModelSourceManual {
		t.Fatalf("同名模型手动来源应优先, 实际 %+v", sources)
	}

	// group_items 重建: 叶子成员带 channel_model_id, 引用成员转 RefGroupName 且保留原 ID。
	type giRow struct {
		ID             int    `gorm:"column:id"`
		GroupID        int    `gorm:"column:group_id"`
		ChannelModelID int    `gorm:"column:channel_model_id"`
		RefGroupName   string `gorm:"column:ref_group_name"`
		Priority       int    `gorm:"column:priority"`
	}
	items := []giRow{}
	if err := GetDB().Table("group_items").Select("id, group_id, channel_model_id, ref_group_name, priority").
		Order("priority").Find(&items).Error; err != nil {
		t.Fatalf("读取 group_items 失败: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("group_items 应有 3 行, 实际 %d (%+v)", len(items), items)
	}
	if items[0].ChannelModelID == 0 || items[0].RefGroupName != "" || items[0].ID != 1 {
		t.Fatalf("叶子成员应保留原 ID 且带渠道模型 ID, 实际 %+v", items[0])
	}
	if items[1].ChannelModelID == 0 || items[1].RefGroupName != "" || items[1].ID != 2 {
		t.Fatalf("第二叶子成员应保留原 ID, 实际 %+v", items[1])
	}
	if items[2].ChannelModelID != 0 || items[2].RefGroupName != "gb" || items[2].ID != 3 {
		t.Fatalf("引用成员应转为 RefGroupName 结构并保留原 ID, 实际 %+v", items[2])
	}
	// 指向引用成员的 active_item_id 不因重建丢失。
	active := int64(0)
	if err := GetDB().Table("groups").Select("active_item_id").Where("id = 1").Scan(&active).Error; err != nil {
		t.Fatalf("读取 active_item_id 失败: %v", err)
	}
	if active != 3 {
		t.Fatalf("指向引用成员的 active_item_id 应保留为 3, 实际 %d", active)
	}

	// 同组分列两种引用成员可插入: 唯一索引已移除, 防重复由应用层校验承担。
	dupRefs := [][2]interface{}{{1, "gb"}, {1, "other"}}
	inserted := 0
	for _, pair := range dupRefs {
		if err := GetDB().Exec(
			"INSERT INTO group_items(group_id, channel_model_id, ref_group_name, priority) VALUES(?, 0, ?, 99)",
			pair[0], pair[1]).Error; err != nil {
			t.Fatalf("同组不同名引用成员应可共存(唯一索引已移除): %v", err)
		}
		inserted++
		GetDB().Exec("DELETE FROM group_items WHERE group_id = ? AND ref_group_name = ? AND priority = 99", pair[0], pair[1])
	}
	if inserted != 2 {
		t.Fatalf("防重复合约性检查未完成")
	}

	// 旧统计表已删除, 渠道表不再携带任何统计列(统计功能已下线, 残留列由迁移 12 清理)。
	if GetDB().Migrator().HasTable("stats_channels") || GetDB().Migrator().HasTable("stats_models") {
		t.Fatal("stats_channels / stats_models 应在拆表迁移后被删除")
	}
	for _, column := range []string{"input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed"} {
		if GetDB().Migrator().HasColumn("channels", column) {
			t.Fatalf("channels 表不应再携带统计列 %s", column)
		}
	}

	// 渠道级限制转为按模型的 model_limits JSON, 模型名来自拆表后的 channel_models。
	limitsJSON := ""
	if err := GetDB().Table("channels").Select("model_limits").Where("id = 1").Scan(&limitsJSON).Error; err != nil {
		t.Fatalf("读取 model_limits 失败: %v", err)
	}
	if limitsJSON == "" {
		t.Fatal("旧渠道级 max_output/thinking_level 应转为 model_limits JSON")
	}
	limits := map[string]model.ChannelModelLimit{}
	if err := json.Unmarshal([]byte(limitsJSON), &limits); err != nil {
		t.Fatalf("model_limits 反序列化失败: %v", err)
	}
	limit, ok := limits["m1"]
	if !ok {
		t.Fatalf("model_limits 应覆盖渠道全部模型, 实际 %v", limits)
	}
	if limit.MaxOutput == nil || *limit.MaxOutput != 4096 || limit.ThinkingLevel != "high" {
		t.Fatalf("model_limits 内容不符, 实际 %+v", limit)
	}

	// 迁移记录齐全: 旧版本不重跑, 新增版本各执行一次。
	recorded := []int{}
	if err := GetDB().Table("migration_records").Order("version").Pluck("version", &recorded).Error; err != nil {
		t.Fatalf("读取迁移记录失败: %v", err)
	}
	want := map[int]bool{10: false, 11: false, 12: false}
	for _, v := range recorded {
		if _, hit := want[v]; hit {
			want[v] = true
		}
	}
	for v, ok := range want {
		if !ok {
			t.Fatalf("迁移版本 %d 未登记执行记录", v)
		}
	}
}
