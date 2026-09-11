// logger.go provides the NovaVei HTTP request logger middleware.
package middleware

import "github.com/gin-gonic/gin"

// RequestLogger returns the gin request logger middleware.
func RequestLogger() gin.HandlerFunc {
	return gin.Logger()
}
