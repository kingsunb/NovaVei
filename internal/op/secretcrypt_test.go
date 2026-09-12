package op

import (
	"strings"
	"testing"

	"github.com/kingsunb/NovaVeil/internal/model"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	if err := InitSecretKey(t.TempDir()); err != nil {
		t.Fatalf("InitSecretKey: %v", err)
	}
	t.Cleanup(func() {
		secretsKeyMu.Lock()
		secretsKey = nil
		secretsKeyMu.Unlock()
	})
	plain := "sk-test-secret"
	enc := EncryptSecret(plain)
	if enc == plain || !strings.HasPrefix(enc, secretCipherPrefix) {
		t.Fatalf("expected ciphertext, got %q", enc)
	}
	if EncryptSecret(enc) != enc {
		t.Fatal("encrypting ciphertext must be idempotent")
	}
	if got := DecryptSecret(enc); got != plain {
		t.Fatalf("decrypt = %q, want %q", got, plain)
	}
	if DecryptSecret(plain) != plain {
		t.Fatal("legacy plaintext must pass through")
	}
}

func TestSealRevealChannelSecrets(t *testing.T) {
	if err := InitSecretKey(t.TempDir()); err != nil {
		t.Fatalf("InitSecretKey: %v", err)
	}
	t.Cleanup(func() {
		secretsKeyMu.Lock()
		secretsKey = nil
		secretsKeyMu.Unlock()
	})
	ch := model.Channel{
		Key:  "legacy-key",
		Keys: []model.ChannelKey{{ID: "a", Key: "multi-key"}},
	}
	sealed := sealChannelSecrets(ch)
	if sealed.Key == "legacy-key" || sealed.Keys[0].Key == "multi-key" {
		t.Fatalf("secrets not sealed: %+v", sealed)
	}
	if ch.Key != "legacy-key" {
		t.Fatal("seal must not mutate source")
	}
	revealChannelSecrets(&sealed)
	if sealed.Key != "legacy-key" || sealed.Keys[0].Key != "multi-key" {
		t.Fatalf("reveal mismatch: %+v", sealed)
	}
}

func TestEncryptWithoutKeyIsPlaintext(t *testing.T) {
	secretsKeyMu.Lock()
	secretsKey = nil
	secretsKeyMu.Unlock()
	if got := EncryptSecret("abc"); got != "abc" {
		t.Fatalf("got %q", got)
	}
}
