# `/config` General Settings UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `Cài đặt chung…` branch inside `/config` so users can edit `style`, `budget`, and `notify` from the TUI instead of hand-editing the config file.

**Architecture:** Extend the existing modal-based `/config` flow with a new general-settings submenu. Keep provider/model editing in the current path, add a new host save path for app-wide settings, and reuse the existing config persistence and validation logic so only the UI surface and the save entry points change.

**Tech Stack:** Go, Bubble Tea TUI, existing `internal/entry/tui`, `internal/host`, and `internal/bootstrap` config/persistence code, table-driven tests, Docker-based verification.

## Global Constraints

- Do not add new third-party dependencies.
- Keep `/config` as the only configuration entry point.
- Do not change `/model` semantics.
- Preserve all existing provider/model editing behavior.
- Keep config schema keys unchanged.
- Preserve current validation for provider configs and role selection.
- Treat `style`, `budget`, and `notify` as app-wide settings, not provider-specific settings.
- The runtime behavior for `budget` and `notify` must remain unchanged; this work only adds UI and persistence paths.

---

## File Structure

- `internal/entry/tui/command_config.go` — extend the `/config` modal flow, add general-settings screens, and wire save actions.
- `internal/entry/tui/model_update.go` — handle the new save result message and refresh the UI after a successful save.
- `internal/entry/tui/command_config_test.go` — cover new menu entries, screen rendering, and local UI state transitions.
- `internal/host/general_config.go` — new host-side save/update path for `style`, `budget`, and `notify`.
- `internal/host/general_config_test.go` — verify general settings save/preserve behavior and validation.
- `internal/host/host.go` — construct or hot-apply general settings if needed.
- `internal/host/events.go` — add a snapshot field if the TUI needs a summary of general settings beyond what is already exposed.
- `internal/bootstrap/config.go` — only if validation or merge behavior needs a narrow update.
- `internal/bootstrap/configfile.go` — only if a new save helper needs to reuse existing atomic write behavior.
- `internal/bootstrap/configfile_test.go` — keep round-trip coverage for the config file.
- `docs/superpowers/plans/2026-07-18-config-general-settings-ui.md` — this plan.

---

## Task 1: Add a host save path for general settings

**Files:**
- Create: `internal/host/general_config.go`
- Create: `internal/host/general_config_test.go`
- Modify: `internal/host/host.go:36-52, 156-186` only if constructor wiring needs a helper

**Interfaces:**
- Consumes: `bootstrap.SaveConfig`, `bootstrap.ValidateBase`, `bootstrap.Config`, `h.cfg`, `h.configPath`
- Produces: `Host.ConfigureGeneralSettings(draft GeneralSettingsDraft) error`

Define a small draft type that carries only the app-wide fields the UI edits:

```go
package host

import "github.com/voocel/ainovel-cli/internal/bootstrap"

type GeneralSettingsDraft struct {
	Style  string
	Budget bootstrap.BudgetConfig
	Notify bootstrap.NotifyConfig
}
```

Add a host method that:
- clones `h.cfg`,
- replaces only `Style`, `Budget`, and `Notify`,
- fills defaults as needed,
- validates the full config,
- saves the full config through the existing atomic config path,
- updates `h.cfg` in memory,
- rebuilds `h.notifier` from the updated notify settings,
- rebuilds or rebinds the budget sentinel if the new budget changed,
- emits a success event for the UI.

Because `style` is read live by `Snapshot()` and by the engine during the next invocation, the save path should also keep the in-memory config consistent immediately after a successful save.

- [ ] **Step 1: Write the failing host test for preserve-and-save behavior**

```go
func TestConfigureGeneralSettingsPersistsAndPreservesProviderConfig(t *testing.T) {
	h, path := newGeneralConfigTestHost(t)

	err := h.ConfigureGeneralSettings(GeneralSettingsDraft{
		Style: "fantasy",
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
```

- [ ] **Step 2: Run the test and confirm it fails**

Run:
```bash
go test ./internal/host -run TestConfigureGeneralSettingsPersistsAndPreservesProviderConfig -v
```
Expected: FAIL because `ConfigureGeneralSettings` does not exist yet.

- [ ] **Step 3: Implement the minimal host save helper**

```go
package host

import (
	"fmt"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/notify"
)

type GeneralSettingsDraft struct {
	Style  string
	Budget bootstrap.BudgetConfig
	Notify bootstrap.NotifyConfig
}

func (h *Host) ConfigureGeneralSettings(draft GeneralSettingsDraft) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	candidate := bootstrap.CloneConfig(h.cfg)
	candidate.Style = strings.TrimSpace(draft.Style)
	candidate.Budget = draft.Budget
	candidate.Notify = draft.Notify
	candidate.FillDefaults()
	if err := candidate.ValidateBase(); err != nil {
		return err
	}
	if h.configPath == "" {
		return fmt.Errorf("无法定位配置文件路径")
	}
	if err := bootstrap.SaveConfig(h.configPath, candidate); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	h.cfg = candidate
	if candidate.Notify.IsEnabled() {
		h.notifier = notify.New(candidate.Notify.Command, candidate.Notify.Events)
	} else {
		h.notifier = nil
	}
	if sentinel := NewBudgetSentinel(candidate.Budget,
		func() float64 { c, _, _, _, _ := h.usage.Totals(); return c },
		func(reason string) { h.abortWithEvent(reason, "error") },
		func(level, summary string) {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
			h.notifier.Send(notify.Notification{Kind: notify.KindBudget, Level: level, Title: "ainovel: 预算", Body: summary})
		},
	); sentinel != nil {
		h.budget = sentinel
		h.usage.SetOnCost(sentinel.OnCost)
		h.usage.SetOnMissingUsage(func() {
			const blind = "预算盲区: 模型未返回 usage 数据，成本统计为 0，预算上限不会触发（自定义模型请确认注册表价格或上游 include_usage）"
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: blind, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: notify.KindBudget, Level: "warn", Title: "ainovel: 预算", Body: blind})
		})
	} else {
		h.budget = nil
		h.usage.SetOnCost(nil)
		h.usage.SetOnMissingUsage(nil)
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: fmt.Sprintf("Cài đặt chung đã được lưu: %s", h.configPath), Level: "info"})
	return nil
}
```

- [ ] **Step 4: Run the host test again until it passes**

Run:
```bash
go test ./internal/host -run TestConfigureGeneralSettingsPersistsAndPreservesProviderConfig -v
```
Expected: PASS.

- [ ] **Step 5: Commit the host save path**

```bash
git add internal/host/general_config.go internal/host/general_config_test.go internal/host/host.go
git commit -m "feat: add general settings save path" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 2: Extend the `/config` modal with a general-settings branch

**Files:**
- Modify: `internal/entry/tui/command_config.go`
- Modify: `internal/entry/tui/command_config_test.go`

**Interfaces:**
- Consumes: `host.ModelConfigurationSnapshot`, `host.GeneralSettingsDraft`, `host.ConfigureGeneralSettings`, `bootstrap.NotifyConfig`, `notify.Kinds()`
- Produces: top-level `Cài đặt chung…` menu entry and nested style/budget/notify screens

Add a new top-level choice alongside provider editing:

- existing providers
- `+ Thêm Provider…`
- `Cài đặt chung…`

Add a new config state with these screens:
- landing screen with three rows: `Style`, `Budget`, `Notify`
- style selection screen
- budget editor screen
- notify editor screen

Use the current modal patterns and input helpers already in this file:
- `configStep`-style state machine,
- `moveConfigCursor`,
- `handleConfigInput`,
- `renderFieldList`,
- `renderConfigChoices`,
- `renderConfigInput`.

Do not redesign provider editing. Keep the provider submenu and model submenu behavior unchanged.

### Style screen
- Fixed list: `default`, `romance`, `fantasy`, `suspense`
- Enter saves the selected style into the general-settings draft and returns to the landing screen
- Esc returns without changing the current selection

### Budget screen
- Fields: `book_usd`, `warn_ratio`, `hard_stop`
- `book_usd` accepts a decimal and treats blank or `0` as disabled
- `warn_ratio` accepts a decimal in `(0, 1)` when budget is enabled
- `hard_stop` toggles between on/off
- show a summary line that tells the user whether budget is enabled

### Notify screen
- Fields: `enabled`, `command`, `events`
- `events` is a multi-select list over `notify.Kinds()`
- include `Select all` and `Clear all` actions
- allow empty events but show a warning in the screen message area

- [ ] **Step 1: Write the failing UI tests for the new top-level entry and screens**

```go
func TestGeneralSettingsEntryAppearsInConfigMenu(t *testing.T) {
	st := &modelConfigState{snapshot: host.ModelConfigurationSnapshot{}}
	st.buildProviderMenus()

	labels := labelsForProviderChoices(st.providerChoices)
	if !slices.Contains(labels, "Cài đặt chung…") {
		t.Fatalf("config menu = %v, want Cài đặt chung…", labels)
	}
}

func TestGeneralSettingsLandingShowsStyleBudgetNotify(t *testing.T) {
	state := &generalConfigState{
		style:  "default",
		budget: bootstrap.BudgetConfig{},
		notify: bootstrap.NotifyConfig{},
	}
	view := renderGeneralConfigModal(120, state)
	for _, want := range []string{"Style", "Budget", "Notify"} {
		if !strings.Contains(view, want) {
			t.Fatalf("general settings modal missing %q: %s", want, view)
		}
	}
}
```

Add focused tests for:
- style selection values,
- budget editor validation and summary,
- notify editor multi-select behavior.

- [ ] **Step 2: Run the UI tests and confirm they fail**

Run:
```bash
go test ./internal/entry/tui -run 'GeneralSettings|ConfigMenu|Budget|Notify' -v
```
Expected: FAIL until the new config state and render helpers exist.

- [ ] **Step 3: Implement the general-settings state machine and renderers**

```go
type generalConfigState struct {
	step      generalConfigStep
	cursor    int
	message   string
	style     string
	budget    bootstrap.BudgetConfig
	notify    bootstrap.NotifyConfig
	input     string
	saving    bool
	selected  map[string]bool
}
```

Add:
- a landing-screen state,
- a style selection state,
- a budget editor state,
- a notify editor state,
- a `draft()` method that returns `host.GeneralSettingsDraft`,
- a `saveGeneralConfiguration` helper that calls `m.runtime.ConfigureGeneralSettings(draft)`.

Keep the modal styling consistent with `/config` and `/model`.

- [ ] **Step 4: Wire the save result into the TUI message loop**

Extend the `modelConfigSavedMsg` equivalent for general settings so successful saves:
- close the modal,
- refresh the UI snapshot,
- restore textarea focus.

On validation failure:
- keep the modal open,
- show the error inline,
- clear the `saving` flag.

- [ ] **Step 5: Run the UI tests again until they pass**

Run:
```bash
go test ./internal/entry/tui -run 'GeneralSettings|ConfigMenu|Budget|Notify' -v
```
Expected: PASS.

- [ ] **Step 6: Commit the TUI expansion**

```bash
git add internal/entry/tui/command_config.go internal/entry/tui/command_config_test.go internal/entry/tui/model_update.go
git commit -m "feat: add general settings ui to config" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 3: Keep runtime state consistent after saving general settings

**Files:**
- Modify: `internal/host/host.go`
- Modify: `internal/host/engine.go` only if style hot-application needs a helper
- Modify: `internal/host/events.go` only if the snapshot needs a new summary field

**Interfaces:**
- Consumes: `Host.ConfigureGeneralSettings`, `Snapshot()`, `engine.style`, `budget`, `notifier`
- Produces: runtime state that matches the saved config without requiring a restart

After general settings are saved, ensure the in-memory host still reflects the new values:
- `Snapshot()` should expose the new style immediately through `h.cfg.Style`
- `h.engine.style` should be updated so the next plan-start decision uses the new style without waiting for a restart
- `h.notifier` should match the new notify settings
- `h.budget` and the `UsageTracker` callbacks should match the new budget settings

If the runtime state update turns out to be too invasive, keep the plan narrowly scoped: persist correctly first, then add the smallest hot-apply helper that updates only the fields the existing runtime already reads.

- [ ] **Step 1: Write the failing hot-apply test**

```go
func TestConfigureGeneralSettingsUpdatesLiveRuntimeState(t *testing.T) {
	h, _ := newGeneralConfigTestHost(t)
	if err := h.ConfigureGeneralSettings(GeneralSettingsDraft{Style: "suspense"}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	if got := h.Snapshot().Style; got != "suspense" {
		t.Fatalf("snapshot style = %q, want suspense", got)
	}
	if h.engine.style != "suspense" {
		t.Fatalf("engine style = %q, want suspense", h.engine.style)
	}
}
```

- [ ] **Step 2: Run the test and confirm it fails if the runtime is not updated**

Run:
```bash
go test ./internal/host -run TestConfigureGeneralSettingsUpdatesLiveRuntimeState -v
```
Expected: FAIL until the runtime fields are updated.

- [ ] **Step 3: Add the smallest runtime update helper**

Update the host save path so it also writes the new style into the live engine and refreshes the notifier/budget bindings.

Prefer a small helper in `internal/host/host.go` rather than adding a new public API if the logic is only used from the general-settings save path.

- [ ] **Step 4: Re-run the runtime test until it passes**

Run:
```bash
go test ./internal/host -run TestConfigureGeneralSettingsUpdatesLiveRuntimeState -v
```
Expected: PASS.

- [ ] **Step 5: Commit the runtime sync update**

```bash
git add internal/host/host.go internal/host/engine.go internal/host/events.go
git commit -m "feat: sync runtime state after config save" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 4: Final verification and docs follow-up

**Files:**
- Modify only if verification exposes a mismatch: `README.md`, `config.example.jsonc`, `internal/bootstrap/config.example.jsonc`

**Interfaces:**
- Consumes: the new `/config` general-settings UI and host save path
- Produces: verified TUI behavior and, only if needed, updated docs/examples for the new UI entry

- [ ] **Step 1: Run the focused package tests first**

Run:
```bash
go test ./internal/bootstrap ./internal/entry/tui ./internal/host -v
```
Expected: PASS.

- [ ] **Step 2: Exercise the app path through Docker**

Run:
```bash
docker run --rm -v /mnt/Data/AI/ainovel-cli:/src -w /src golang:1.25 go test ./internal/entry/tui ./internal/host -run 'GeneralSettings|ConfigMenu|ConfigureGeneralSettings' -v
```
Expected: PASS.

- [ ] **Step 3: Inspect the diff for scope drift**

Run:
```bash
git diff --stat
```
Expected: only the general-settings UI, the host save path, and the new tests are changed.

- [ ] **Step 4: Update README only if user-facing commands changed**

If the UI introduces a new visible label or keybinding that the README should document, add a short note in the `/config` or configuration section. Otherwise keep the docs unchanged.

- [ ] **Step 5: Commit the final verification pass if anything remained uncommitted**

```bash
git add -A
git commit -m "feat: add general settings ui and save path" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage:**
- `/config` general-settings branch: Task 2.
- Budget screen: Task 2.
- Notify screen with multi-select events: Task 2.
- Host-side persistence and validation: Task 1.
- Runtime consistency after save: Task 3.
- Verification: Task 4.

**2. Placeholder scan:**
- No TBD/TODO placeholders.
- The only conditional work is limited to follow-up files if verification exposes a gap.

**3. Type consistency:**
- `host.GeneralSettingsDraft` is the save payload used by the TUI and host.
- `generalConfigState` is the new TUI state for the landing/style/budget/notify screens.
- `modelConfigState` remains the provider/model state machine.
- `ConfigureGeneralSettings` is the new host save entry point.

**4. Scope check:**
- This is one coherent feature: one new config branch, one host save path, one runtime sync pass, one verification pass.
- It does not split into independent sub-projects because the UI and save path are tightly coupled.
