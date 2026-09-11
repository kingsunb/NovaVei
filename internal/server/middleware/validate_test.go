package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequireJSONRejectsBodylessPost(t *testing.T) {
	r := gin.New()
	r.Use(RequireJSON())
	r.POST("/change", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/change", nil))
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("bodyless POST = %d, want 415; body=%s", w.Code, w.Body.String())
	}
}

func TestRequireJSONRejectsNonJSONBody(t *testing.T) {
	r := gin.New()
	r.Use(RequireJSON())
	r.POST("/change", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodPost, "/change", strings.NewReader("value"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text body POST = %d, want 415", w.Code)
	}
}
