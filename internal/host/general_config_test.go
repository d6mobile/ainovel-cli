package host

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

func newGeneralConfigTestHost(t *testing.T) (*Host, string) {
	t.Helper()
	pc := bootstrap.ProviderConfig{
		Type: "openai", APIKey: "old-secret", BaseURL: "https://example.com/v1",
		Models: []bootstrap.ModelConfig{{Name: "old", ContextWindow: 128000}, {Name: "writer-model"}},
	}
	cfg := bootstrap.Config{
		Provider: "proxy", ModelName: "old", Providers: map[string]bootstrap.ProviderConfig{"proxy": pc},
		Roles: map[string]bootstrap.RoleConfig{"writer": {Provider: "proxy", Model: "writer-model"}},
	}
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		t.Fatalf("new model set: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := bootstrap.SaveConfig(path, cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return &Host{
		cfg: cfg, models: models, usage: NewUsageTracker(models, nil), events: make(chan Event, 4),
		configPath: path,
	}, path
}

func boolPtr(v bool) *bool { return &v }

func TestConfigureGeneralSettingsPersistsAndPreservesProviderConfig(t *testing.T) {
	h, path := newGeneralConfigTestHost(t)

	err := h.ConfigureGeneralSettings(GeneralSettingsDraft{
		Style:  "fantasy",
		Budget: bootstrap.BudgetConfig{BookUSD: 50, WarnRatio: 0.8, HardStop: true},
		Notify: bootstrap.NotifyConfig{Enabled: boolPtr(true), Command: "curl -s https://example.test", Events: []string{"run_end", "budget"}},
	})
	if err != nil {
		t.Fatalf("configure general settings: %v", err)
	}

	saved, err := bootstrap.LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load saved: %v", err)
	}
	if saved.Style != "fantasy" {
		t.Fatalf("style = %q, want fantasy", saved.Style)
	}
	if saved.Budget.BookUSD != 50 || saved.Budget.WarnRatio != 0.8 || !saved.Budget.HardStop {
		t.Fatalf("budget = %#v, want 50 / 0.8 / true", saved.Budget)
	}
	if saved.Notify.Enabled == nil || !*saved.Notify.Enabled {
		t.Fatalf("notify.enabled = %#v, want true", saved.Notify.Enabled)
	}
	if saved.Notify.Command != "curl -s https://example.test" {
		t.Fatalf("notify.command = %q", saved.Notify.Command)
	}
	if got := strings.Join(saved.Notify.Events, ","); got != "run_end,budget" {
		t.Fatalf("notify.events = %v", saved.Notify.Events)
	}
	if saved.Provider != "proxy" || saved.ModelName != "old" {
		t.Fatalf("provider selection changed unexpectedly: %#v", saved)
	}
}
