package model

// ProxyEntry 代理池中的单个代理条目。
type ProxyEntry struct {
	ID      string `json:"id"`      // 唯一标识, 前端生成(UUID), 用于 React key 与删除定位。
	Name    string `json:"name"`    // 人类可读名称, 如 "香港-01"。
	URL     string `json:"url"`     // 代理地址, 支持 http/https/socks/socks5/socks5h。
	Enabled bool   `json:"enabled"` // 是否启用。
}

// 代理池条目数量上限, 防止误配置撑大存储与前端渲染。
const MaxProxyPoolCount = 64

// 代理条目名称与 URL 的长度上限。
const (
	MaxProxyNameLen = 100
	MaxProxyURLLen  = 2048
)
