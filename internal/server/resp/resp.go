package resp

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type ResponseStruct struct {
	Code    int         `json:"code" example:"200"`
	Message string      `json:"message" example:"success"`
	Data    interface{} `json:"data,omitempty"`
}

func Success(c *gin.Context, data any) {
	c.JSON(http.StatusOK, ResponseStruct{
		Code:    http.StatusOK,
		Message: "success",
		Data:    data,
	})
}

func Error(c *gin.Context, code int, err string) {
	c.AbortWithStatusJSON(code, ResponseStruct{
		Code:    code,
		Message: err,
	})
}

// ErrorMustChangePassword 以 403 返回强制改密错误, 并携带机器可读标记头
// (X-NovaVei-Error: password_change_required): 前端的改密引导按头判定,
// message 文案可自由调整。今后任何新的发射点都必须走本助手而不是直接 Error。
func ErrorMustChangePassword(c *gin.Context) {
	c.Header(ErrMarkerHeader, ErrMarkerPasswordChangeRequired)
	Error(c, http.StatusForbidden, ErrMustChangePassword)
}

// ErrorRateLimited 以 429 返回限流错误并携带 Retry-After 头(秒)。
// retryAfterSeconds 非正数时按 1 秒下发, 避免客户端 0 退避打转。
func ErrorRateLimited(c *gin.Context, message string, retryAfterSeconds int) {
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}
	c.Header("Retry-After", strconv.Itoa(retryAfterSeconds))
	Error(c, http.StatusTooManyRequests, message)
}
