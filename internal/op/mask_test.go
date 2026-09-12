package op

import (
	"testing"

	"github.com/kingsunb/NovaVeil/internal/model"
)

func TestMaskConfigGetInvalidJSONDoesNotError(t *testing.T) {
	t.Cleanup(func() {
		_ = SettingSetString(model.SettingKeyMaskConfig, "")
	})
	if err := SettingSetString(model.SettingKeyMaskConfig, "{not-json"); err != nil {
		t.Fatalf("seed invalid mask config: %v", err)
	}
	cfg, err := MaskConfigGet()
	if err != nil {
		t.Fatalf("invalid JSON should not fail closed: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("invalid JSON must fall back to disabled default")
	}
}
