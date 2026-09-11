package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/kingsunb/NovaVeil/internal/op"
	"github.com/kingsunb/NovaVeil/internal/server/resp"
)

func Cors() gin.HandlerFunc {
	config := cors.DefaultConfig()
	config.AllowCredentials = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	// Explicitly cover supported browser SDKs without allowing arbitrary request headers.
	config.AllowHeaders = []string{
		"Authorization", "Content-Type", "X-Api-Key", "X-Session-Id",
		"Anthropic-Version", "Anthropic-Beta", "Anthropic-Dangerous-Direct-Browser-Access",
		"OpenAI-Organization", "OpenAI-Project", "OpenAI-Beta",
		"X-Stainless-Arch", "X-Stainless-Async", "X-Stainless-Lang", "X-Stainless-OS",
		"X-Stainless-Package-Version", "X-Stainless-Retry-Count", "X-Stainless-Runtime",
		"X-Stainless-Runtime-Version", "X-Stainless-Timeout",
		"HTTP-Referer", "X-Title",
	}
	config.ExposeHeaders = []string{"Content-Disposition"}
	config.AllowOriginWithContextFunc = func(c *gin.Context, origin string) bool {
		return isSameRequestOrigin(origin, c.Request) || isAllowedCrossOrigin(origin)
	}
	return cors.New(config)
}

// OriginProtection provides a baseline CSRF check for cookie-authenticated management writes.
// Requests without Origin/Referer remain compatible with non-browser CLI clients; browser requests
// must be same-origin or explicitly listed in the validated CORS setting.
func OriginProtection() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isUnsafeMethod(c.Request.Method) || !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}

		source := strings.TrimSpace(c.GetHeader("Origin"))
		if source == "" {
			source = strings.TrimSpace(c.GetHeader("Referer"))
			if source == "" {
				c.Next()
				return
			}
		}
		origin, ok := canonicalOrigin(source)
		if !ok || !isSameRequestOrigin(origin, c.Request) && !isAllowedCrossOrigin(origin) {
			resp.Error(c, http.StatusForbidden, "cross-origin management request denied")
			c.Abort()
			return
		}
		c.Next()
	}
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions && method != http.MethodTrace
}

func isAllowedCrossOrigin(origin string) bool {
	canonical, ok := canonicalOrigin(origin)
	if !ok {
		return false
	}
	allowed, err := op.SettingGetString(model.SettingKeyCORSAllowOrigins)
	if err != nil {
		return false
	}
	for _, item := range strings.Split(allowed, ",") {
		candidate, valid := canonicalOrigin(item)
		if valid && candidate == canonical {
			return true
		}
	}
	return false
}

func canonicalOrigin(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "*" {
		return "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), true
}

func requestOrigin(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(req.Host)
}

// isSameRequestOrigin accepts exact same-origin requests and the common TLS-termination case
// where the reverse proxy preserves Host but forwards the request to this backend over HTTP.
// No X-Forwarded-* header is trusted, so a remote client cannot forge the external host.
func isSameRequestOrigin(origin string, req *http.Request) bool {
	canonical, ok := canonicalOrigin(origin)
	if !ok {
		return false
	}
	if canonical == requestOrigin(req) {
		return true
	}
	parsed, err := url.Parse(canonical)
	if err != nil || req.TLS != nil || parsed.Scheme != "https" {
		return false
	}
	return strings.EqualFold(parsed.Host, req.Host)
}
