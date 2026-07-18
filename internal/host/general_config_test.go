package host

import (
	"path/filepath"
	"reflect"
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

func TestConfigureGeneralSettingsPersistsAndPreservesRuntimeState(t *testing.T) {
	h, path := newGeneralConfigTestHost(t)
	h.budget = h.newBudgetSentinel(bootstrap.BudgetConfig{BookUSD: 10, WarnRatio: 0.8})
	h.engine = &engine{style: "default", budget: h.budget}
	h.budget.OnCost(8.5)
	if h.budget.state.Load() != budgetWarned {
		t.Fatalf("precondition: budget state = %v, want warned", h.budget.state.Load())
	}
	origBudget := h.budget
	origOnCostPtr := reflect.ValueOf(h.usage.onCost).Pointer()
	origMissingPtr := reflect.ValueOf(h.usage.onMissingUsage).Pointer()

	err := h.ConfigureGeneralSettings(GeneralSettingsDraft{
		Style:  "fantasy",
		Budget: bootstrap.BudgetConfig{BookUSD: 50, WarnRatio: 0.8, HardStop: true},
		Notify: bootstrap.NotifyConfig{Enabled: boolPtr(true), Command: "curl -s https://example.test", Events: []string{"run_end", "budget"}},
	})
	if err != nil {
		t.Fatalf("configure general settings: %v", err)
	}

	if h.budget != origBudget {
		t.Fatal("budget sentinel was rebuilt instead of updated in place")
	}
	if got := h.budget.Limit(); got != 50 {
		t.Fatalf("budget limit = %v, want 50", got)
	}
	if got := h.budget.state.Load(); got != budgetWarned {
		t.Fatalf("budget state = %v, want warned", got)
	}
	if h.engine == nil || h.engine.style != "fantasy" {
		t.Fatalf("engine style = %q, want fantasy", h.engine.style)
	}
	if h.engine.budget != h.budget {
		t.Fatal("engine budget pointer was not refreshed")
	}
	if got := reflect.ValueOf(h.usage.onCost).Pointer(); got != origOnCostPtr {
		t.Fatal("usage onCost callback was rebound")
	}
	if got := reflect.ValueOf(h.usage.onMissingUsage).Pointer(); got != origMissingPtr {
		t.Fatal("usage onMissingUsage callback was rebound")
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
