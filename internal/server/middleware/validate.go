// validate.go provides NovaVeil request-validation middleware.
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kingsunb/NovaVeil/internal/server/resp"
)

// RequireJSON rejects non-GET/DELETE/OPTIONS requests whose Content-Type is
// not application/json with 415 Unsupported Media Type.
func RequireJSON() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet ||
			c.Request.Method == http.MethodDelete ||
			c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}

		contentType := c.GetHeader("Content-Type")
		if !strings.Contains(contentType, "application/json") {
			resp.Error(c, http.StatusUnsupportedMediaType, resp.ErrInvalidJSON)
			c.Abort()
			return
		}

		c.Next()
	}
}
