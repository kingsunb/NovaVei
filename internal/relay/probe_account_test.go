package relay

import (
	"testing"

	"github.com/kingsunb/NovaVei/internal/helper"
	"github.com/kingsunb/NovaVei/internal/model"
)

// TestEffectiveProbeChannelResolvesAccountAndIgnoresRemark 验证 {account} 占位符被替换为
// 由「渠道 ID + Key ID」确定性派生的别名: 同一渠道同一 Key 多次解析结果完全一致,
// Remark 无论填什么都不参与替换。
func TestEffectiveProbeChannelResolvesAccountAndIgnoresRemark(t *testing.T) {
	resetChannelKeyHealth()
	t.Cleanup(resetChannelKeyHealth)
	proxy := "socks5h://grok.{account}:pass@resin:2260"
	channel := model.Channel{
		ID:           77,
		Enabled:      true,
		Key:          "legacy",
		Keys:         []model.ChannelKey{{ID: "id-first", Key: "secret-first", Remark: "remark-must-not-be-used"}},
		ChannelProxy: &proxy,
	}
	effective, index, selected, err := effectiveProbeChannel(channel)
	if err != nil {
		t.Fatalf("effectiveProbeChannel: %v", err)
	}
	if index != 0 || selected.ID != "id-first" || effective.Key != "secret-first" {
		t.Fatalf("wrong selected key: index=%d selected=%+v effective.Key=%q", index, selected, effective.Key)
	}
	expected := "socks5h://grok." + helper.AccountAliasFor(77, "id-first") + ":pass@resin:2260"
	if effective.ChannelProxy == nil || *effective.ChannelProxy != expected {
		t.Fatalf("resolved proxy = %v, want %q", effective.ChannelProxy, expected)
	}
	// 同一渠道同一 Key 再次解析, 别名必须与上一次完全一致(确定性派生, 跨请求稳定)。
	again, _, _, err := effectiveProbeChannel(channel)
	if err != nil {
		t.Fatalf("second effectiveProbeChannel: %v", err)
	}
	if again.ChannelProxy == nil || *again.ChannelProxy != expected {
		t.Fatalf("resolved proxy not stable: first=%q second=%v", expected, again.ChannelProxy)
	}
	if len(effective.Keys) != 0 {
		t.Fatalf("effective channel must expose only selected secret, got %d keys", len(effective.Keys))
	}
}
