package handlers

import (
	"net/http"

	"github.com/kingsunb/NovaVeil/internal/relay"
	"github.com/kingsunb/NovaVeil/internal/server/middleware"
	"github.com/kingsunb/NovaVeil/internal/server/router"
	"github.com/looplj/axonhub/llm"
)

func init() {
	router.NewGroupRouter("/v1").
		Use(middleware.APIKeyAuth()).
		// 对话类接口
		AddRoute(
			router.NewRoute("/chat/completions", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIChatCompletion)),
		).
		AddRoute(
			router.NewRoute("/responses", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIResponse)),
		).
		AddRoute(
			router.NewRoute("/messages", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatAnthropicMessage)),
		).
		// 图片生成/编辑/变体 (同协议透传)
		AddRoute(
			router.NewRoute("/images/generations", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIImageGeneration)),
		).
		AddRoute(
			router.NewRoute("/images/edits", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIImageEdit)),
		).
		AddRoute(
			router.NewRoute("/images/variations", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIImageVariation)),
		).
		// 视频生成 (同协议透传)
		AddRoute(
			router.NewRoute("/video/generations", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIVideo)),
		).
		// 语音合成/转写/翻译 (同协议透传)
		AddRoute(
			router.NewRoute("/audio/speech", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAISpeech)),
		).
		AddRoute(
			router.NewRoute("/audio/transcriptions", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAITranscription)),
		).
		AddRoute(
			router.NewRoute("/audio/translations", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAITranslation)),
		).
		// 向量嵌入 (同协议透传)
		AddRoute(
			router.NewRoute("/embeddings", http.MethodPost).
				Handle(relay.Forward(llm.APIFormatOpenAIEmbedding)),
		)
}
