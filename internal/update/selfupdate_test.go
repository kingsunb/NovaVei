package update

import "testing"

func TestSelfUpdateDisabledByDefault(t *testing.T) {
	t.Setenv("NOVAVEIL_ENABLE_SELF_UPDATE", "")
	t.Setenv("NOVAVEIL_DISABLE_SELF_UPDATE", "")
	if selfUpdateEnabled() {
		t.Fatal("self-update must be opt-in")
	}
}

func TestSelfUpdateEnableFlag(t *testing.T) {
	t.Setenv("NOVAVEIL_DISABLE_SELF_UPDATE", "")
	t.Setenv("NOVAVEIL_ENABLE_SELF_UPDATE", "true")
	if !selfUpdateEnabled() {
		t.Fatal("NOVAVEIL_ENABLE_SELF_UPDATE=true should enable")
	}
}

func TestSelfUpdateDisableWins(t *testing.T) {
	t.Setenv("NOVAVEIL_ENABLE_SELF_UPDATE", "true")
	t.Setenv("NOVAVEIL_DISABLE_SELF_UPDATE", "true")
	if selfUpdateEnabled() {
		t.Fatal("DISABLE must override ENABLE")
	}
}
