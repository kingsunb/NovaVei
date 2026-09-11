package op

import (
	"sync"

	"github.com/kingsunb/NovaVeil/internal/model"
)

// channelRandomHeaderCache 按渠道索引的随机头名解析缓存(已解析产物 map[int][]string)。
// 由 settingRefreshCache 初始化、SettingSetString 在规则变更时读后即写重建,
// 热路径 ChannelRandomHeaderKeys 走 O(1) 读锁, 避免每次 json.Unmarshal。
var channelRandomHeaderCache struct {
	sync.RWMutex
	rules map[int][]string
}

// refreshChannelRandomHeaderCache 以给定原始规则流重建解析缓存。
func refreshChannelRandomHeaderCache(raw string) {
	parsed := model.ParseChannelRandomHeaderRules(raw)
	channelRandomHeaderCache.Lock()
	channelRandomHeaderCache.rules = parsed
	channelRandomHeaderCache.Unlock()
}

// ChannelRandomHeaderKeys 返回指定渠道配置的随机请求头名集合(保持声明顺序, 已去重)。
// 无规则返回 nil; 不返回 error, 保持转发热路径零开销语义。
func ChannelRandomHeaderKeys(channelID int) []string {
	channelRandomHeaderCache.RLock()
	keys := channelRandomHeaderCache.rules[channelID]
	channelRandomHeaderCache.RUnlock()
	if len(keys) == 0 {
		return nil
	}
	return keys
}

// RefreshChannelRandomHeaderCacheForTest 以给定原始规则流重建解析缓存, 供测试在不依赖数据库时
// 直接装配全局随机头规则。生产路径应通过 SettingSetString 触发, 此入口仅供测试使用。
func RefreshChannelRandomHeaderCacheForTest(raw string) {
	refreshChannelRandomHeaderCache(raw)
}
