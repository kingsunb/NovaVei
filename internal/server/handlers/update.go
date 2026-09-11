package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-gonic/gin"
	"github.com/kingsunb/NovaVei/internal/conf"
	"github.com/kingsunb/NovaVei/internal/op"
	"github.com/kingsunb/NovaVei/internal/relay"
	"github.com/kingsunb/NovaVei/internal/server/middleware"
	"github.com/kingsunb/NovaVei/internal/server/resp"
	"github.com/kingsunb/NovaVei/internal/server/router"
	"github.com/kingsunb/NovaVei/internal/update"
)

func init() {
	router.NewGroupRouter("/api/v1/update").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).
				Handle(latest),
		).
		AddRoute(
			router.NewRoute("/now-version", http.MethodGet).
				Handle(getNowVersion),
		).
		AddRoute(
			router.NewRoute("/token-trends", http.MethodGet).
				Handle(getTokenTrends),
		).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Handle(updateFunc),
		)
}

func latest(c *gin.Context) {
	latestInfo, err := update.GetLatestInfo()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, *latestInfo)
}

func getNowVersion(c *gin.Context) {
	// 统计查询使用独立超时上下文: 请求上下文随客户端断开取消, 不应中断数据库聚合。
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// range 联动 KPI 口径: 默认 forever(全量), 与趋势图默认档位一致。
	// 选中某档位时, 四个 KPI 分别统计该时间窗口内的请求数 / 活跃客户端 IP / 错误数 / Token 用量。
	rangeKey := c.Query("range")
	if rangeKey == "" {
		rangeKey = "forever"
	}
	if !op.ValidUsageRange(rangeKey) {
		resp.Error(c, http.StatusBadRequest, "range 仅支持 24h / 7d / 30d / 1y / 3y / forever")
		return
	}
	forever := rangeKey == "forever"
	since := op.UsageWindowSince(rangeKey) // forever 时为零值, 表示全量

	// total_requests + Token 用量共用一次 usage_buckets 聚合:
	//   - forever: total_requests 用进程累计全量(relay.TotalRequestCount), token 用 UsageTotals 全表聚合;
	//   - 非 forever: 二者均取自 UsageKPIsByRange 的窗口聚合, total_requests 用 request_count 合计
	//     (仅计有用量上报的请求, 是窗口内请求数的近似)。
	var totalRequests uint64
	var kpiInput, kpiOutput int64
	var kpiErr error
	if forever {
		totalRequests = relay.TotalRequestCount()
		kpiInput, kpiOutput, kpiErr = op.UsageTotals(ctx)
	} else {
		var reqCnt int64
		kpiInput, kpiOutput, reqCnt, kpiErr = op.UsageKPIsByRange(ctx, rangeKey)
		totalRequests = uint64(reqCnt)
	}
	if kpiErr != nil {
		log.Warnf("usage KPI stats unavailable: %v", kpiErr)
	}

	// client_ip_count: forever 用内存缓存去重数(含本进程未刷增量, 与历史行为一致);
	// 非 forever 按 last_seen >= since 查 client_stats 表(跨重启的权威来源)。
	var clientIPCount int64
	if forever {
		clientIPCount = int64(op.ClientStatCount())
	} else {
		cnt, err := op.ClientStatCountSince(ctx, since)
		if err != nil {
			log.Warnf("failed to count client ips by range: %v", err)
		}
		clientIPCount = cnt
	}

	// error_count: forever 用进程级业务错误计数(自启动累计, 不依赖日志留存/去重/丢弃);
	// 非 forever 用 error_logs 表中 created_at >= since 的条数, 受保留上限(默认 50 条)、
	// 按类去重与队满丢弃影响, 仅作面板近期错误趋势的近似指示。
	var errorCount int64
	if forever {
		errorCount = int64(relay.TotalErrorCount())
	} else {
		cnt, err := op.ErrorLogCountSince(ctx, since)
		if err != nil {
			log.Warnf("failed to count error logs: %v", err)
		}
		errorCount = cnt
	}

	// tokens_by_model 始终为全表按模型聚合(模型用量 Top 不随档位联动, 仅四 KPI 卡片联动)。
	tokensByModel, byModelErr := op.UsageTotalsByModel(ctx)
	// stats_available 标识用量统计是否可读: KPI 聚合与按模型聚合任一失败即置 false,
	// 让前端区分"无流量"(true 且全零)与"统计暂时不可用"(false)。
	statsAvailable := kpiErr == nil && byModelErr == nil
	if !statsAvailable {
		log.Warnf("usage stats unavailable: kpi=%v byModel=%v", kpiErr, byModelErr)
	}
	resp.Success(c, gin.H{
		"version":             conf.Version,
		"commit":              conf.Commit,
		"build_time":          conf.BuildTime,
		"client_ip_count":     clientIPCount,
		"total_requests":      totalRequests,
		"error_count":         errorCount,
		"total_tokens_input":  kpiInput,
		"total_tokens_output": kpiOutput,
		"tokens_by_model":     tokensByModel,
		"stats_available":     statsAvailable,
	})
}

// getTokenTrends 返回 Token 用量时间序列: /token-trends?range=24h|7d|30d|1y|3y|forever。
// 档位非法回 400; 返回形状 {points:[{t,in,out}], available:bool}, t 为采样区间起点 Unix 毫秒,
// 点数固定 24/28/30/52/36/120, 窗口内无数据的点输出 0 保证 X 轴连续。
// available 为 false 表示统计暂时不可用(DB 查询失败), 此时 points 仍为零值时间轴;
// 为 true 表示统计可读(可能全零=无流量), 前端据此区分两种状态。
func getTokenTrends(c *gin.Context) {
	rangeKey := c.Query("range")
	if rangeKey == "" {
		rangeKey = "forever"
	}
	if !op.ValidUsageRange(rangeKey) {
		resp.Error(c, http.StatusBadRequest, "range 仅支持 24h / 7d / 30d / 1y / 3y / forever")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	points, err := op.UsageTrendPoints(ctx, rangeKey)
	available := err == nil
	if !available {
		log.Warnf("usage trend stats unavailable: %v", err)
	}
	resp.Success(c, gin.H{"points": points, "available": available})
}

func updateFunc(c *gin.Context) {
	err := update.UpdateCore()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, "update success")
}
