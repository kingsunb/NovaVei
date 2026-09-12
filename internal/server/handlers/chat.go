package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kingsunb/NovaVeil/internal/relay"
	"github.com/kingsunb/NovaVeil/internal/server/middleware"
	"github.com/kingsunb/NovaVeil/internal/server/router"
	"github.com/looplj/axonhub/llm"
)

func init() {
	router.NewGroupRouter("/api/v1/chat").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/completions", http.MethodPost).
				Handle(adminChatCompletions),
		)
}

// adminChatCompletions 管理端对话入口: 走管理员 cookie, 不把 API Key 明文拉进浏览器。
// 内部复用 /v1 同一条 Forward 管线(熔断/脱敏/协议转换), 仅跳过密钥级限速。
func adminChatCompletions(c *gin.Context) {
	c.Set("supported_models", "")
	c.Set("api_key_raw", "")
	c.Set("api_key_name", "console")
	c.Set("api_key_id", 0)
	relay.Forward(llm.APIFormatOpenAIChatCompletion)(c)
}
