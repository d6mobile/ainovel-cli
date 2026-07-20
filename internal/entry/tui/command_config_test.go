package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
)

func hubFieldIDs(fields []hubField) []string {
	ids := make([]string, len(fields))
	for i, f := range fields {
		ids[i] = f.id
	}
	return ids
}

// Selecting an existing Provider should open the detail hub (review info first, then tweak fields), not jump straight into "edit protocol".
func TestSelectingProviderOpensHub(t *testing.T) {
	st := &modelConfigState{editModelIdx: -1}
	st.applyProviderChoice(configProviderChoice{existing: &host.ProviderSnapshot{
		Name: "openrouter", BaseURL: "u", HasAPIKey: true,
		Models: []bootstrap.ModelConfig{{Name: "m"}},
	}})
	if st.step != configStepHub {
		t.Fatalf("chọn Provider hiện có phải vào hub, got step=%d", st.step)
	}
	ids := hubFieldIDs(st.hubFields())
	// Built-in providers (empty type) do not show protocol/endpoint noise, but still keep key/models/save.
	if slices.Contains(ids, "protocol") || slices.Contains(ids, "api") {
		t.Fatalf("hub provider tích hợp không được hiện Giao thức/Endpoint, got %v", ids)
	}
	for _, want := range []string{"key", "baseurl", "models", "save"} {
		if !slices.Contains(ids, want) {
			t.Fatalf("hub thiếu %q, got %v", want, ids)
		}
	}
}

// Custom providers (explicit openai protocol) show protocol and endpoint in the hub.
func TestCustomProviderHubShowsProtocolAndEndpoint(t *testing.T) {
	st := &modelConfigState{editModelIdx: -1}
	st.applyProviderChoice(configProviderChoice{existing: &host.ProviderSnapshot{
		Name: "proxy", Type: "openai", API: "responses", HasAPIKey: true,
		Models: []bootstrap.ModelConfig{{Name: "m"}},
	}})
	ids := hubFieldIDs(st.hubFields())
	if !slices.Contains(ids, "protocol") || !slices.Contains(ids, "api") {
		t.Fatalf("hub provider openai tùy chỉnh phải có Giao thức/Endpoint, got %v", ids)
	}
}

// Esc backs out step by step: field editor → hub → provider list → close.
func TestEscapeBackHierarchy(t *testing.T) {
	st := &modelConfigState{}
	st.step = configStepBaseURL
	if got, ok := st.escapeBack(); !ok || got != configStepHub {
		t.Fatalf("Esc ở editor trường phải quay về hub, got %d,%v", got, ok)
	}
	st.step = configStepHub
	if got, ok := st.escapeBack(); !ok || got != configStepProvider {
		t.Fatalf("Esc ở hub phải quay về danh sách, got %d,%v", got, ok)
	}
	st.step = configStepProvider
	if _, ok := st.escapeBack(); ok {
		t.Fatal("Esc ở danh sách phải đóng toàn bộ panel")
	}
}

// The last model list item is always the add-model entry; Enter opens naming (no hidden A shortcut).
func TestModelListAddEntryOpensNameInput(t *testing.T) {
	st := &modelConfigState{step: configStepModels, editModelIdx: -1,
		models: []bootstrap.ModelConfig{{Name: "m1"}}}
	st.cursor = len(st.models) // stop on "+ Add model..."
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.step != configStepModelName || st.editModelIdx != -1 {
		t.Fatalf("chọn mục thêm mới phải vào bước đặt tên (step=%d, idx=%d)", st.step, st.editModelIdx)
	}
}

// Selecting an existing model and pressing Enter opens that model's detail view (default/window/delete), not hidden E/D.
func TestSelectingModelOpensDetail(t *testing.T) {
	st := &modelConfigState{step: configStepModels, editModelIdx: -1,
		models: []bootstrap.ModelConfig{{Name: "m1"}, {Name: "m2"}}}
	st.cursor = 1
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.step != configStepModelDetail || st.editModelIdx != 1 {
		t.Fatalf("chọn mô hình phải vào chi tiết (step=%d, idx=%d)", st.step, st.editModelIdx)
	}
	ids := hubFieldIDs(st.modelDetailFields())
	// The detail view only keeps context window / delete; "set default" has been removed (switching is handled by /model).
	if slices.Contains(ids, "default") {
		t.Fatalf("chi tiết mô hình không được còn “đặt làm mặc định”, got %v", ids)
	}
	for _, want := range []string{"window", "delete"} {
		if !slices.Contains(ids, want) {
			t.Fatalf("chi tiết mô hình thiếu %q, got %v", want, ids)
		}
	}
}

// After editing an existing model's window, return to its detail view; Esc also walks detail → list (the add flow returns to naming).
func TestModelWindowEscapeHierarchy(t *testing.T) {
	editing := &modelConfigState{step: configStepModelWindow, editModelIdx: 0}
	if got, ok := editing.escapeBack(); !ok || got != configStepModelDetail {
		t.Fatalf("Esc khi sửa cửa sổ mô hình hiện có phải quay về chi tiết, got %d,%v", got, ok)
	}
	adding := &modelConfigState{step: configStepModelWindow, editModelIdx: -1}
	if got, ok := adding.escapeBack(); !ok || got != configStepModelName {
		t.Fatalf("Esc khi thêm cửa sổ mới phải quay về đặt tên, got %d,%v", got, ok)
	}
	detail := &modelConfigState{step: configStepModelDetail}
	if got, ok := detail.escapeBack(); !ok || got != configStepModels {
		t.Fatalf("Esc ở chi tiết mô hình phải quay về danh sách, got %d,%v", got, ok)
	}
}

func TestParseContextWindowInput(t *testing.T) {
	cases := map[string]int{
		"": 0, "0": 0, "auto": 0, "128K": 128000, "1M": 1000000,
		"1.5m": 1500000, "200000": 200000,
	}
	for input, want := range cases {
		got, err := parseContextWindowInput(input)
		if err != nil || got != want {
			t.Errorf("parseContextWindowInput(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"-1", "abc", "0.5"} {
		if _, err := parseContextWindowInput(input); err == nil {
			t.Errorf("parseContextWindowInput(%q) should fail", input)
		}
	}
}

func TestModelConfigModalDoesNotRenderAPIKey(t *testing.T) {
	state := &modelConfigState{step: configStepKeyInput, input: "sk-super-secret"}
	view := renderModelConfigModal(120, state)
	if strings.Contains(view, "sk-super-secret") {
		t.Fatal("API key leaked into rendered modal")
	}
}

func TestConfigCommandIsRegistered(t *testing.T) {
	spec, ok := commandRegistryInstance().Find("config")
	if !ok {
		t.Fatal("/config is not registered")
	}
	if spec.Usage != "/config" || !spec.AutoExecute {
		t.Fatalf("config spec = %#v", spec)
	}
}

func TestModelSwitchLabelIncludesContextWindow(t *testing.T) {
	state := modelSwitchState{models: []host.ConfiguredModel{{Name: "gpt-test", ContextWindow: 400000}}}
	if got := state.modelLabel(); got != "gpt-test · 400K" {
		t.Fatalf("modelLabel = %q", got)
	}
}

// Like /model: /config renders as a framed overlay sized to content height, not a centered 3/4-screen overlay.
func TestModelConfigModalIsCompactOverlay(t *testing.T) {
	state := &modelConfigState{step: configStepProvider, providerChoices: []configProviderChoice{
		{label: "Chỉnh sửa openrouter", existing: &host.ProviderSnapshot{Name: "openrouter"}},
		{label: "+ Thêm Provider…", add: true},
	}}
	lines := strings.Split(renderModelConfigModal(120, state), "\n")

	// 1 title line + 2 options + top/bottom borders = 5 lines; height follows content, not screen height.
	if len(lines) != 5 {
		t.Fatalf("popup compact phải có 5 dòng (chiều cao nội dung), got %d dòng:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "┌") || !strings.Contains(lines[0], "/config") {
		t.Fatalf("dòng đầu phải là viền trên có tiêu đề /config, got %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "└") {
		t.Fatalf("dòng cuối phải là viền dưới, got %q", lines[len(lines)-1])
	}
}

// The first-level menu only lists "edit existing + add new"; the built-in provider catalog appears only in the second level.
func TestProviderMenuIsTwoLevel(t *testing.T) {
	state := &modelConfigState{snapshot: host.ModelConfigurationSnapshot{
		Providers:       []host.ProviderSnapshot{{Name: "openrouter"}, {Name: "anthropic"}},
		DefaultProvider: "openrouter",
	}}
	state.buildProviderMenus()

	// Level 1 = 2 edit entries + 1 add entry + 1 general-settings entry; provider presets appear only in level 2.
	if len(state.providerChoices) != 4 {
		t.Fatalf("menu cấp một phải có 2 mục sửa + 1 mục thêm + cài đặt chung, got %d mục", len(state.providerChoices))
	}
	if !state.providerChoices[2].add {
		t.Fatal("mục thêm Provider phải là lối vào “Thêm Provider…”")
	}
	if !state.providerChoices[3].general {
		t.Fatal("mục cuối menu cấp một phải là “Cài đặt chung…”")
	}
	for i, c := range state.providerChoices[:2] {
		if c.existing == nil || c.add {
			t.Fatalf("mục menu cấp một thứ %d phải là sửa Provider hiện có, got %#v", i, c)
		}
	}

	// Level 2 = addable catalog: non-empty, and built-ins already configured (openrouter/anthropic) do not repeat.
	if len(state.presetChoices) == 0 {
		t.Fatal("menu cấp hai phải liệt kê catalog Provider có thể thêm")
	}
	if len(state.presetChoices) >= len(bootstrap.ProviderPresets()) {
		t.Fatalf("Provider tích hợp đã cấu hình phải bị loại khỏi catalog thêm mới, presets=%d total=%d",
			len(state.presetChoices), len(bootstrap.ProviderPresets()))
	}
	for _, c := range state.presetChoices {
		if c.preset != nil && (c.preset.Name == "openrouter" || c.preset.Name == "anthropic") {
			t.Fatalf("catalog thêm mới không được chứa %q đã cấu hình", c.preset.Name)
		}
	}
}

func TestProviderMenuShowsDeepSeekAndOllamaPresets(t *testing.T) {
	state := &modelConfigState{snapshot: host.ModelConfigurationSnapshot{}}
	state.buildProviderMenus()

	labels := make([]string, 0, len(state.presetChoices))
	for _, choice := range state.presetChoices {
		if choice.preset != nil {
			labels = append(labels, choice.preset.Label)
		}
	}

	if !slices.Contains(labels, "DeepSeek") || !slices.Contains(labels, "Ollama") {
		t.Fatalf("preset catalog phải có DeepSeek và Ollama, got %v", labels)
	}
}

func TestGeneralSettingsEntryAppearsInConfigMenu(t *testing.T) {
	state := &modelConfigState{snapshot: host.ModelConfigurationSnapshot{}}
	state.buildProviderMenus()

	labels := labelsForProviderChoices(state.providerChoices)
	if !slices.Contains(labels, "Cài đặt chung…") {
		t.Fatalf("config menu = %v, want Cài đặt chung…", labels)
	}
}

func TestGeneralSettingsLandingShowsStyleBudgetNotify(t *testing.T) {
	state := &modelConfigState{
		step:         configStepGeneral,
		generalStyle: "default",
	}
	view := renderModelConfigModal(120, state)
	for _, want := range []string{"Phong cách", "Ngân sách", "Thông báo"} {
		if !strings.Contains(view, want) {
			t.Fatalf("general settings modal missing %q: %s", want, view)
		}
	}
}

func TestGeneralBudgetInputValidation(t *testing.T) {
	state := &modelConfigState{editSetting: "book_usd", input: "25.5"}
	if err := state.applyBudgetInput(); err != nil {
		t.Fatalf("book_usd input failed: %v", err)
	}
	if state.generalBudget.BookUSD != 25.5 || state.generalBudget.WarnRatio != 0.8 {
		t.Fatalf("budget = %#v, want book_usd 25.5 and default warn 0.8", state.generalBudget)
	}
	state.editSetting = "warn_ratio"
	state.input = "1.2"
	if err := state.applyBudgetInput(); err == nil {
		t.Fatal("warn_ratio >= 1 should fail when budget is enabled")
	}
}

func TestGeneralNotifyEventToggle(t *testing.T) {
	enabled := true
	state := &modelConfigState{generalNotify: bootstrap.NotifyConfig{Enabled: &enabled, Events: []string{"budget"}}}
	state.toggleNotifyEvent("run_end")
	if !state.notifyEventSelected("run_end") {
		t.Fatal("run_end should be selected after first toggle")
	}
	state.toggleNotifyEvent("run_end")
	if state.notifyEventSelected("run_end") {
		t.Fatal("run_end should be unselected after second toggle")
	}
}
