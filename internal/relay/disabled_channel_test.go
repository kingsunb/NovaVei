package relay

import (
	"testing"

	"github.com/kingsunb/NovaVeil/internal/model"
)

// TestEffectiveProbeChannelRejectsDisabled 验证停用渠道在探测路径被立即拒绝,
// 避免后台探测 / 半开探测 / TestChannel 持续消耗冷却与并发信号量。
func TestEffectiveProbeChannelRejectsDisabled(t *testing.T) {
	channel := model.Channel{
		ID:      1,
		Name:    "off",
		Type:    model.ChannelProviderOpenAI,
		Enabled: false,
	}
	_, _, _, err := effectiveProbeChannel(channel)
	if err != errChannelDisabled {
		t.Fatalf("停用渠道应返 errChannelDisabled, 实际 %v", err)
	}
}
