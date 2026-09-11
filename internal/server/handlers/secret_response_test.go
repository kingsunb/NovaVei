package handlers

import (
	"testing"

	"github.com/kingsunb/NovaVeil/internal/model"
)

func TestAPIKeySummaryDoesNotExposeSecret(t *testing.T) {
	summary := apiKeyToSummary(model.APIKey{ID: 1, Name: "prod", APIKey: "sk-secret-ABCD", Enabled: true})
	if summary.APIKeyMasked != "****ABCD" {
		t.Fatalf("mask = %q, want ****ABCD", summary.APIKeyMasked)
	}
}

func TestChannelAdminSummaryDoesNotExposeSecrets(t *testing.T) {
	channel := model.Channel{
		Key:  "legacy-secret-WXYZ",
		Keys: []model.ChannelKey{{ID: "id1", Key: "channel-secret-1234", Remark: "primary"}},
	}
	summary := channelAdminSummary(channel)
	if summary.Key != "" || summary.Keys[0].Key != "" {
		t.Fatalf("summary leaked secrets: %+v", summary)
	}
	if summary.KeyMasked != "****WXYZ" || summary.Keys[0].KeyMasked != "****1234" {
		t.Fatalf("unexpected masks: legacy=%q key=%q", summary.KeyMasked, summary.Keys[0].KeyMasked)
	}
	if summary.Keys[0].ID != "id1" || summary.Keys[0].Remark != "primary" {
		t.Fatalf("editable metadata lost: %+v", summary.Keys[0])
	}
}

func TestChannelKeySecretsRevealPlaintextOnly(t *testing.T) {
	channel := model.Channel{
		Key:  "legacy-secret-WXYZ",
		Keys: []model.ChannelKey{{ID: "id1", Key: "channel-secret-1234", Remark: "primary"}},
	}
	views := channelKeySecrets(channel)
	if len(views) != 1 || views[0].ID != "id1" || views[0].Key != "channel-secret-1234" || views[0].Remark != "primary" {
		t.Fatalf("unexpected views: %+v", views)
	}

	// 旧式单 Key 渠道: 无稳定 ID, 以空 id 唯一行返回明文。
	legacy := channelKeySecrets(model.Channel{Key: "only-key"})
	if len(legacy) != 1 || legacy[0].ID != "" || legacy[0].Key != "only-key" {
		t.Fatalf("unexpected legacy views: %+v", legacy)
	}

	// 无密钥渠道返回空切片。
	if got := channelKeySecrets(model.Channel{}); len(got) != 0 {
		t.Fatalf("expected empty views, got %+v", got)
	}
}
