package relay

import (
	"testing"
	"time"
)

func TestPruneExpiredKeyCooldowns(t *testing.T) {
	resetChannelKeyHealth()
	t.Cleanup(resetChannelKeyHealth)
	keyCooldownsMu.Lock()
	keyCooldowns[keyRef{ChannelID: 1, KeyID: "old"}] = time.Now().Add(-time.Second)
	keyCooldowns[keyRef{ChannelID: 1, KeyID: "live"}] = time.Now().Add(time.Hour)
	keyCooldownsMu.Unlock()
	removed := PruneExpiredKeyCooldowns()
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if channelKeyCooling(1, "live") != true {
		t.Fatal("live cooldown should remain")
	}
	if channelKeyCooling(1, "old") {
		t.Fatal("expired cooldown should be gone")
	}
}
