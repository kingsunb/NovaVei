package handlers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kingsunb/NovaVeil/internal/client"
	"github.com/kingsunb/NovaVeil/internal/server/middleware"
	"github.com/kingsunb/NovaVeil/internal/server/resp"
	"github.com/kingsunb/NovaVeil/internal/server/router"
)

func init() {
	router.NewGroupRouter("/api/v1/setting").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/proxy/test", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(testProxy),
		)
}

// proxyTestTimeout 代理测试超时: 涵盖代理握手 + TLS + IP 查询服务响应。
const proxyTestTimeout = 15 * time.Second

// ipEchoURLs 依次尝试的出口 IP 查询服务, 任一成功即返回。
var ipEchoURLs = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
}

type proxyTestRequest struct {
	URL string `json:"url"`
}

type proxyTestResult struct {
	IP      string `json:"ip"`       // 出口 IP 地址; 失败时为空。
	Elapsed int64  `json:"elapsed"`  // 耗时(毫秒)。
}

// testProxy 通过指定代理访问公网 IP 查询服务, 返回出口 IP 地址。
func testProxy(c *gin.Context) {
	var req proxyTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	url := strings.TrimSpace(req.URL)
	if url == "" {
		resp.Error(c, http.StatusBadRequest, "proxy url is required")
		return
	}

	httpClient, err := client.GetHTTPClientCustomProxy(url)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(c.Request.Context(), proxyTestTimeout)
	defer cancel()

	var lastErr error
	for _, echoURL := range ipEchoURLs {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, echoURL, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp2, err := httpClient.Do(httpReq)
		if err != nil {
			lastErr = err
			continue
		}
		// 读取 body 后立即关闭, 避免连接泄漏。
		body, err := io.ReadAll(io.LimitReader(resp2.Body, 64))
		resp2.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp2.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("%s returned status %d", echoURL, resp2.StatusCode)
			continue
		}
		ip := strings.TrimSpace(string(body))
		if ip == "" {
			lastErr = fmt.Errorf("%s returned empty body", echoURL)
			continue
		}
		resp.Success(c, proxyTestResult{
			IP:      ip,
			Elapsed: time.Since(start).Milliseconds(),
		})
		return
	}
	resp.Error(c, http.StatusBadGateway, fmt.Sprintf("代理测试失败: %v", lastErr))
}
