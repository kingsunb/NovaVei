package relay

import (
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/kingsunb/NovaVeil/internal/model"
	"github.com/looplj/axonhub/llm/httpclient"
)

// TestRecordRouteFailureIfReal_InfraRetriesBeforeCooldown 验证:
// 1) 业务错误走 recordRouteFailure 原路径, 用 failures 计数;
// 2) 基础设施错误走 infraFailures 计数, 未达到 MemberInfraMaxRetries 时不写入 Cooldowns;
// 3) 达到 MemberInfraMaxRetries 后调用 recordRouteFailure 走正常冷却通道。
func TestRecordRouteFailureIfReal_InfraRetriesBeforeCooldown(t *testing.T) {
	groupID := 7001
	group := model.Group{
		ID:   groupID,
		Name: "g",
		Mode: model.GroupModeFailover,
		Items: []model.GroupItem{
			{ID: 11, GroupID: groupID, ChannelModelID: 100},
		},
		RelayConfig: model.GroupRelayConfig{
			MemberMaxAttempts:     2,
			MemberInfraMaxRetries: 3, // 3 次网络错误后冷却, 与业务失败分开
			MemberCooldownSeconds: 60,
		},
	}

	// 初始化 RouteState
	routeMu.Lock()
	routes[groupID] = &RouteState{
		GroupID:           groupID,
		Cooldowns:         map[int]int64{},
		Levels:            map[int]int{},
		HalfOpens:         map[int]int64{},
		PostCommitStrikes: map[int]int{},
		emergencyCounts:   map[int]int{},
		emergencyBlocks:   map[int]int64{},
	}
	routeMu.Unlock()
	defer resetRouteCooldownState()

	infraErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("socks connect ... general SOCKS server failure")}
	bizErr := errors.New("Request failed: Bad Request, ...")

	counts := map[int]*memberFailureCounts{}

	// 第 1 次基础设施错误: infra=1, 未达 3, 不进 Cooldowns
	if recordRouteFailureIfReal(group, 11, counts, infraErr) {
		t.Fatalf("第 1 次网络错误不该触发冷却")
	}
	if counts[11].infra != 1 || counts[11].business != 0 {
		t.Fatalf("计数错: business=%d infra=%d, 期望 0/1", counts[11].business, counts[11].infra)
	}

	// 第 2 次: infra=2, 仍未达 3
	if recordRouteFailureIfReal(group, 11, counts, infraErr) {
		t.Fatalf("第 2 次网络错误不该触发冷却")
	}

	// 第 3 次: 达到 3, 走正常冷却路径
	if !recordRouteFailureIfReal(group, 11, counts, infraErr) {
		t.Fatalf("第 3 次网络错误应该触发冷却")
	}

	// 验证 Cooldowns 里有该 itemID
	routeMu.Lock()
	deadline, ok := routes[groupID].Cooldowns[11]
	routeMu.Unlock()
	if !ok {
		t.Fatalf("Cooldowns 应有 itemID=11")
	}
	if deadline == 0 {
		t.Fatalf("冷却截止时间应非零")
	}

	// 业务错误路径使用独立 business 计数，基础设施计数不会串入业务阈值。
	counts = map[int]*memberFailureCounts{}
	// MemberMaxAttempts=2: 第 1 次业务错误不冷却。
	if recordRouteFailureIfReal(group, 11, counts, bizErr) {
		t.Fatalf("第 1 次业务错误不该冷却(MemberMaxAttempts=2)")
	}
	// 第 2 次达到阈值，走正常冷却通道。
	if !recordRouteFailureIfReal(group, 11, counts, bizErr) {
		t.Fatalf("第 2 次业务错误应触发冷却")
	}
	if counts[11].business != 2 || counts[11].infra != 0 {
		t.Fatalf("业务计数错: business=%d infra=%d", counts[11].business, counts[11].infra)
	}
}

// TestRecordRouteFailureIfReal_InfraMaxZeroFollowsMemberMax 验证 MemberInfraMaxRetries=0 时
// 退化为与 MemberMaxAttempts 相同(默认行为)。
func TestRecordRouteFailureIfReal_InfraMaxZeroFollowsMemberMax(t *testing.T) {
	groupID := 7002
	group := model.Group{
		ID:   groupID,
		Mode: model.GroupModeFailover,
		RelayConfig: model.GroupRelayConfig{
			MemberMaxAttempts:     2,
			MemberInfraMaxRetries: 0, // 退化为 MemberMaxAttempts
			MemberCooldownSeconds: 60,
		},
	}
	routeMu.Lock()
	routes[groupID] = &RouteState{
		GroupID:           groupID,
		Cooldowns:         map[int]int64{},
		Levels:            map[int]int{},
		HalfOpens:         map[int]int64{},
		PostCommitStrikes: map[int]int{},
		emergencyCounts:   map[int]int{},
		emergencyBlocks:   map[int]int64{},
	}
	routeMu.Unlock()
	defer resetRouteCooldownState()

	infraErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	counts := map[int]*memberFailureCounts{}

	// MemberMaxAttempts=2: 第 1 次网络错误(infra=1)不冷却; 第 2 次(infra=2)应冷却
	if recordRouteFailureIfReal(group, 11, counts, infraErr) {
		t.Fatalf("第 1 次不该冷却")
	}
	if !recordRouteFailureIfReal(group, 11, counts, infraErr) {
		t.Fatalf("第 2 次该冷却(退化路径)")
	}
}

// TestNormalizeGroupRelayConfigInfraMaxRetries 验证配置归一化:
// 0 是合法"跟随 MemberMaxAttempts"语义, 负数回填默认。
func TestNormalizeGroupRelayConfigInfraMaxRetries(t *testing.T) {
	defaults := model.DefaultGroupRelayConfig()

	// 0 保持 0
	c := model.GroupRelayConfig{MemberMaxAttempts: 5, MemberInfraMaxRetries: 0}
	model.NormalizeGroupRelayConfig(&c)
	if c.MemberInfraMaxRetries != 0 {
		t.Fatalf("0 应被保留, 实际 %d", c.MemberInfraMaxRetries)
	}

	// 正数保持
	c = model.GroupRelayConfig{MemberMaxAttempts: 5, MemberInfraMaxRetries: 7}
	model.NormalizeGroupRelayConfig(&c)
	if c.MemberInfraMaxRetries != 7 {
		t.Fatalf("正数应被保留, 实际 %d", c.MemberInfraMaxRetries)
	}

	// 负数回填默认
	c = model.GroupRelayConfig{MemberMaxAttempts: 5, MemberInfraMaxRetries: -1}
	model.NormalizeGroupRelayConfig(&c)
	if c.MemberInfraMaxRetries != defaults.MemberInfraMaxRetries {
		t.Fatalf("负数应回填默认 %d, 实际 %d", defaults.MemberInfraMaxRetries, c.MemberInfraMaxRetries)
	}
}

// TestRecordRouteFailureIfReal_AlternatingMembersCountIndependently 回归验证:
// 两个成员交替失败时各自独立累计, 先后达到阈值并分别进入冷却。
// 旧实现在成员切换时整体清零计数, 交替失败的两个坏成员永远到不了阈值,
// 请求在两者之间无限轮换不熔断(实测 422/429 交替空转 33 轮的案例)。
func TestRecordRouteFailureIfReal_AlternatingMembersCountIndependently(t *testing.T) {
	groupID := 7003
	group := model.Group{
		ID:   groupID,
		Name: "g",
		Mode: model.GroupModeFailover,
		Items: []model.GroupItem{
			{ID: 11, GroupID: groupID, ChannelModelID: 100},
			{ID: 12, GroupID: groupID, ChannelModelID: 200},
		},
		RelayConfig: model.GroupRelayConfig{
			MemberMaxAttempts:     3,
			MemberInfraMaxRetries: 0,
			MemberCooldownSeconds: 60,
		},
	}
	routeMu.Lock()
	routes[groupID] = &RouteState{
		GroupID:           groupID,
		Cooldowns:         map[int]int64{},
		Levels:            map[int]int{},
		HalfOpens:         map[int]int64{},
		PostCommitStrikes: map[int]int{},
		emergencyCounts:   map[int]int{},
		emergencyBlocks:   map[int]int64{},
	}
	routeMu.Unlock()
	defer resetRouteCooldownState()

	bizErr := errors.New("Request failed: Too Many Requests, usage limit reached")
	counts := map[int]*memberFailureCounts{}

	// A/B 交替业务失败各两次: 任一成员都不该进入冷却。
	for i := 0; i < 2; i++ {
		if recordRouteFailureIfReal(group, 11, counts, bizErr) {
			t.Fatalf("成员 11 第 %d 次失败不该冷却", i+1)
		}
		if recordRouteFailureIfReal(group, 12, counts, bizErr) {
			t.Fatalf("成员 12 第 %d 次失败不该冷却", i+1)
		}
	}
	// 第 3 次失败: 两个成员先后达到各自阈值, 均应进入冷却。
	if !recordRouteFailureIfReal(group, 11, counts, bizErr) {
		t.Fatal("成员 11 第 3 次失败应触发冷却")
	}
	if !recordRouteFailureIfReal(group, 12, counts, bizErr) {
		t.Fatal("成员 12 第 3 次失败应触发冷却")
	}
	routeMu.Lock()
	_, coolingA := routes[groupID].Cooldowns[11]
	_, coolingB := routes[groupID].Cooldowns[12]
	routeMu.Unlock()
	if !coolingA || !coolingB {
		t.Fatalf("交替失败达到阈值后两个成员都应冷却, 实际 11=%v 12=%v", coolingA, coolingB)
	}
}

// TestIsKeyRateLimitError 验证 429 限额错误识别: 结构化状态码与文本短语两种形态都能命中,
// 401/403 等认证拒绝与其他状态码不误报。
func TestIsKeyRateLimitError(t *testing.T) {
	rateErr := errors.New("POST - https://upstream/v1/chat/completions with status 429 Too Many Requests: monthly usage limit reached")
	if !isKeyRateLimitError(rateErr) {
		t.Fatal("429 文本错误应识别为 Key 限速")
	}
	structured := &httpclient.Error{StatusCode: http.StatusTooManyRequests}
	if !isKeyRateLimitError(structured) {
		t.Fatal("429 结构化错误应识别为 Key 限速")
	}
	if isKeyRateLimitError(errors.New("Request failed: Unauthorized")) {
		t.Fatal("401 不应识别为 Key 限速")
	}
	if isKeyRateLimitError(errors.New("Request failed: Forbidden")) {
		t.Fatal("403 不应识别为 Key 限速")
	}
	if isKeyRateLimitError(nil) {
		t.Fatal("nil 不应识别为 Key 限速")
	}
}
