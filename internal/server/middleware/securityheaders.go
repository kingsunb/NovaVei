package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kingsunb/NovaVeil/internal/conf"
)

// SecurityHeaders 为全部响应补上基础浏览器安全响应头:
//   - X-Content-Type-Options: nosniff 禁止浏览器嗅探偏离声明类型的内容,
//     阻断把 JSON/文本响应当脚本或样式执行的变异利用;
//   - X-Frame-Options: DENY 拒绝被任意站点 iframe 嵌入, 消除管理面板被
//     视觉伪装/点击劫持的承载面(写操作另有 SameSite=Lax 与 Origin 校验兜底);
//   - Referrer-Policy: no-referrer 不向下游(含外链跳转目标)泄露面板来源 URL;
//   - HSTS: 仅在 CookieSecure=true 时设置, 强制浏览器后续访问走 HTTPS。
//
// CSP 中 script-src / connect-src 额外放行 Cloudflare 域名:
//   - https://static.cloudflareinsights.com  CF Web Analytics(RUM) 探针脚本, 由 CF 边缘自动注入;
//   - https://cloudflareinsights.com          探针回传 RUM 数据的端点;
//   - https://a.nel.cloudflare.com            CF Network Error Logging(NEL) 上报端点, 同样由边缘注入。
// 未部署在 Cloudflare 后面时这些域名不会被请求, 放行不引入额外攻击面;
// 若未来需要更严格的隔离, 可改为配置化按部署环境切换 CSP。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' https://static.cloudflareinsights.com; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self' https://cloudflareinsights.com https://a.nel.cloudflare.com; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		// API 响应禁止缓存: Cloudflare 和浏览器在无 Cache-Control 时可能对 GET /api/v1/*
		// 做启发式缓存, 导致前端轮询拿到旧数据(渠道列表不更新等)。no-store 阻止存储,
		// private 阻止共享缓存(CDN)保存; SSE 端点在 prepareSSE 中会覆盖为 no-store,no-transform。
		if strings.HasPrefix(c.Request.URL.Path, "/api/") || strings.HasPrefix(c.Request.URL.Path, "/v1/") {
			c.Header("Cache-Control", "no-store, private")
		}
		// 仅在显式配置 HTTPS 部署时启用 HSTS, 避免直连 HTTP 形态下浏览器被永久锁死 HTTPS。
		if conf.AppConfig.Security.CookieSecure {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
