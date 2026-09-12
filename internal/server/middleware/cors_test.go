package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/kingsunb/NovaVeil/internal/op"
)

func originTestEngine() *gin.Engine {
	r := gin.New()
	r.Use(Cors(), OriginProtection())
	r.POST("/api/v1/test", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return r
}

func TestOriginProtectionAcceptsTLSProxySameHost(t *testing.T) {
	r := originTestEngine()
	req := httptest.NewRequest(http.MethodPost, "http://console.example/api/v1/test", strings.NewReader(`{}`))
	req.Host = "console.example"
	req.Header.Set("Origin", "https://console.example")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("TLS-terminated same-host POST = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}

func TestOriginProtectionRejectsCookieWriteWithoutOrigin(t *testing.T) {
	r := originTestEngine()
	req := httptest.NewRequest(http.MethodPost, "http://console.example/api/v1/test", strings.NewReader(`{}`))
	req.Host = "console.example"
	req.AddCookie(&http.Cookie{Name: AuthCookieName, Value: "session"})
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cookie POST without Origin = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestOriginProtectionAllowsCookielessWriteWithoutOrigin(t *testing.T) {
	r := originTestEngine()
	req := httptest.NewRequest(http.MethodPost, "http://console.example/api/v1/test", strings.NewReader(`{}`))
	req.Host = "console.example"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("cookieless POST without Origin = %d, want 204; body=%s", w.Code, w.Body.String())
	}
}

func TestOriginProtectionRejectsDifferentHost(t *testing.T) {
	r := originTestEngine()
	req := httptest.NewRequest(http.MethodPost, "http://console.example/api/v1/test", strings.NewReader(`{}`))
	req.Host = "console.example"
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("cross-host POST = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestCORSPreflightAllowsSupportedSDKHeaders(t *testing.T) {
	previous, err := op.SettingGetString(model.SettingKeyCORSAllowOrigins)
	if err != nil {
		t.Fatalf("read CORS setting: %v", err)
	}
	if err := op.SettingSetString(model.SettingKeyCORSAllowOrigins, "https://sdk.example"); err != nil {
		t.Fatalf("set CORS setting: %v", err)
	}
	t.Cleanup(func() { _ = op.SettingSetString(model.SettingKeyCORSAllowOrigins, previous) })

	r := originTestEngine()
	req := httptest.NewRequest(http.MethodOptions, "http://console.example/api/v1/test", nil)
	req.Host = "console.example"
	req.Header.Set("Origin", "https://sdk.example")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "anthropic-version,anthropic-dangerous-direct-browser-access,openai-beta,openai-project,x-stainless-lang,x-stainless-timeout")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("SDK preflight = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	allowed := strings.ToLower(w.Header().Get("Access-Control-Allow-Headers"))
	for _, header := range []string{"anthropic-version", "anthropic-dangerous-direct-browser-access", "openai-beta", "openai-project", "x-stainless-lang", "x-stainless-timeout"} {
		if !strings.Contains(allowed, header) {
			t.Fatalf("preflight allow headers %q missing %q", allowed, header)
		}
	}
}
