package middleware

import (
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
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		// 仅在显式配置 HTTPS 部署时启用 HSTS, 避免直连 HTTP 形态下浏览器被永久锁死 HTTPS。
		if conf.AppConfig.Security.CookieSecure {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}
