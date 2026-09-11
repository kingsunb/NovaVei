package op

import (
	"context"
	"testing"

	"github.com/kingsunb/NovaVeil/internal/db"
	"github.com/kingsunb/NovaVeil/internal/model"
)

func TestChannelUpdatePreservesMaskedKeyByID(t *testing.T) {
	ctx := context.Background()
	channel := model.Channel{
		Name:    "secret-preserve-test",
		Type:    model.ChannelProviderOpenAI,
		Enabled: true,
		BaseURL: "https://example.invalid",
		Keys:    []model.ChannelKey{{Key: "sk-secret-original", Remark: "old"}},
	}
	if err := ChannelCreate(&channel, ctx); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() {
		_ = db.GetDB().Where("channel_id = ?", channel.ID).Delete(&model.ChannelModel{}).Error
		_ = db.GetDB().Delete(&model.Channel{}, channel.ID).Error
		channelCache.Del(channel.ID)
	})

	keyID := channel.Keys[0].ID
	keys := []model.ChannelKey{{ID: keyID, Key: "", Remark: "new"}}
	updated, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Keys: &keys}, ctx)
	if err != nil {
		t.Fatalf("update channel: %v", err)
	}
	if len(updated.Keys) != 1 || updated.Keys[0].Key != "sk-secret-original" || updated.Keys[0].Remark != "new" {
		t.Fatalf("secret not preserved: %+v", updated.Keys)
	}
}

func TestChannelUpdateRejectsUnknownEmptyKeyID(t *testing.T) {
	ctx := context.Background()
	channel := model.Channel{Name: "secret-reject-test", Type: model.ChannelProviderOpenAI, Enabled: true, BaseURL: "https://example.invalid", Keys: []model.ChannelKey{{Key: "sk-known"}}}
	if err := ChannelCreate(&channel, ctx); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() {
		_ = db.GetDB().Delete(&model.Channel{}, channel.ID).Error
		channelCache.Del(channel.ID)
	})
	keys := []model.ChannelKey{{ID: "unknown", Key: ""}}
	if _, err := ChannelUpdate(&model.ChannelUpdateRequest{ID: channel.ID, Keys: &keys}, ctx); err == nil {
		t.Fatal("unknown id with empty secret must be rejected")
	}
}
