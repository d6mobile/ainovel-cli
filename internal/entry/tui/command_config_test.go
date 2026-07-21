package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
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

func hubFieldIndex(fields []hubField, id string) int {
	for i, f := range fields {
		if f.id == id {
			return i
		}
	}
	return -1
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

// Esc backs out step by step: hub → provider list → close.
func TestEscapeBackHierarchy(t *testing.T) {
	st := &modelConfigState{step: configStepHub}
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
	if st.step != configStepModels || st.editModelIdx != len(st.models)-1 || st.editingField != configModelNameField {
		t.Fatalf("chọn mục thêm mới phải vào sửa tên inline (step=%d, idx=%d field=%q)", st.step, st.editModelIdx, st.editingField)
	}
}

// Selecting an existing model and pressing Enter opens that model's detail view (default/window/delete), not hidden E/D.
func TestSelectingModelOpensDetail(t *testing.T) {
	st := &modelConfigState{step: configStepModels, editModelIdx: -1,
		models: []bootstrap.ModelConfig{{Name: "m1", ContextWindow: 1000}}, modelOrigins: []string{"m1"}}
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyRight})
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.editingField != configModelWindowField || st.step != configStepModels {
		t.Fatalf("右列 Enter 应行内编辑窗口，step=%d field=%q", st.step, st.editingField)
	}
	st.input.SetValue("200K")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	if st.editingField != "" || st.models[0].ContextWindow != 1000 {
		t.Fatalf("Esc 应取消当前单元格且不改值: %#v", st.models[0])
	}
}

func TestModelRenameProducesExplicitDraftAndReferenceNotice(t *testing.T) {
	st := &modelConfigState{
		step: configStepModels, provider: "proxy", models: []bootstrap.ModelConfig{{Name: "old"}},
		modelOrigins: []string{"old"}, snapshot: host.ModelConfigurationSnapshot{
			References: map[string][]string{"proxy\x00old": {"default", "writer fallback[0]"}},
		},
	}
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.step != configStepModels || st.editModelIdx != 0 || st.editingField != configModelNameField {
		t.Fatalf("chọn mô hình phải sửa tên inline (step=%d, idx=%d field=%q)", st.step, st.editModelIdx, st.editingField)
	}
	st.input.SetValue("new")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.models[0].Name != "new" || !strings.Contains(st.message, "tham chiếu") {
		t.Fatalf("đổi tên phải ghi draft và cảnh báo tham chiếu, model=%#v message=%q", st.models[0], st.message)
	}
}

func TestModelWindowEscapeHierarchy(t *testing.T) {
	state := &modelConfigState{step: configStepModels, editModelIdx: 0, editingField: configModelWindowField}
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	if state.step != configStepModels || state.editingField != "" || state.editModelIdx != -1 {
		t.Fatalf("Esc khi sửa cửa sổ mô hình phải hủy edit inline, step=%d field=%q idx=%d", state.step, state.editingField, state.editModelIdx)
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
	state := &modelConfigState{step: configStepHub, provider: "proxy", apiKeyOptional: true}
	state.beginInlineEdit("key")
	state.input.SetValue("sk-super-secret")
	view := renderModelConfigModal(120, state)
	if strings.Contains(view, "sk-super-secret") {
		t.Fatal("API key leaked into rendered modal")
	}
}

func TestProviderHubEditsAPIKeyInlineAndTrims(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", existing: true,
		hasAPIKey: true, apiKeyHint: "sk-o******7890", apiKeyOptional: true, apiKeyAction: host.APIKeyKeep}
	state.cursor = hubFieldIndex(state.hubFields(), "key")
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.step != configStepHub || state.editingField != "key" {
		t.Fatalf("API Key 应在 hub 原行编辑，得到 step=%d field=%q", state.step, state.editingField)
	}
	state.input.SetValue("  sk-new-secret-1234567890  ")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.editingField != "" || state.apiKey != "sk-new-secret-1234567890" || state.apiKeyAction != host.APIKeyReplace {
		t.Fatalf("API Key 行内提交结果错误: field=%q key=%q action=%q", state.editingField, state.apiKey, state.apiKeyAction)
	}
	if got := state.keyStatus(); got != "sk-n******7890" {
		t.Fatalf("新 API Key 应显示脱敏提示，得到 %q", got)
	}
}

func TestProviderHubEditsBaseURLInlineAndKeepsLongTailVisible(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", existing: true,
		apiKeyOptional: true, baseURL: "https://old.example/v1"}
	state.cursor = hubFieldIndex(state.hubFields(), "baseurl")
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.editingField != "baseurl" || state.input.Value() != "https://old.example/v1" {
		t.Fatalf("Base URL 应在原行预填编辑，field=%q value=%q", state.editingField, state.input.Value())
	}
	state.input.SetValue("  https://example.com/a/very/long/provider/path/UNIQUE-END  ")
	state.input.CursorEnd()
	view := renderModelConfigModal(76, state)
	if !strings.Contains(view, "UNIQUE-END") {
		t.Fatalf("长 Base URL 编辑时应显示光标附近尾部:\n%s", view)
	}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.baseURL != "https://example.com/a/very/long/provider/path/UNIQUE-END" {
		t.Fatalf("Base URL 未 TrimSpace，得到 %q", state.baseURL)
	}
}

func TestSaveConfigHighlightsOnlyWhenDirty(t *testing.T) {
	state := &modelConfigState{editModelIdx: -1}
	state.applyProviderChoice(configProviderChoice{existing: &host.ProviderSnapshot{
		Name: "proxy", Type: "openai", BaseURL: "https://old.example/v1", HasAPIKey: true,
		APIKeyHint: "sk-o******7890", Models: []bootstrap.ModelConfig{{Name: "m1"}},
	}})
	if state.isDirty() {
		t.Fatal("刚进入已有 Provider 时不应标记为已修改")
	}
	state.baseURL = "https://new.example/v1"
	if !state.isDirty() {
		t.Fatal("Base URL 变化后应标记为已修改")
	}
	state.baseURL = "https://old.example/v1"
	if state.isDirty() {
		t.Fatal("改回基线值后应自动恢复未修改状态")
	}
	state.beginInlineEdit("baseurl")
	state.input.SetValue("https://editing.example/v1")
	if !state.isDirty() {
		t.Fatal("Base URL 正在输入新值时应实时标记为已修改")
	}
	state.input.SetValue(" https://old.example/v1 ")
	if state.isDirty() {
		t.Fatal("行内输入等价于基线值时不应误报修改")
	}
	state.editingField = ""
	state.apiKeyAction = host.APIKeyReplace
	state.apiKey = "sk-new-secret"
	if !state.isDirty() {
		t.Fatal("替换 API Key 后应标记为已修改")
	}

	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })
	lines := renderProviderHubFields(state, 68)
	want := lipgloss.NewStyle().Foreground(colorSuccess).Render("Lưu cấu hình")
	found := false
	for _, line := range lines {
		if strings.Contains(line, want) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("有变更时保存项应使用成功色，lines=%q", lines)
	}

	newProvider := &modelConfigState{}
	if !newProvider.isDirty() {
		t.Fatal("新增 Provider 应始终视为未保存变更")
	}
}

func TestStyledBaseURLLineKeepsANSIAndFillsModalWidth(t *testing.T) {
	plain := "› Base URL  https://api.deepseek.com"
	styled := "\x1b[38;2;255;200;0m› \x1b[0m" +
		"\x1b[1;38;2;255;200;0mBase URL\x1b[0m  " +
		"\x1b[4;38;2;220;220;220mhttps://api.deepseek.com\x1b[0m"
	if got := ansi.Strip(truncateStyledWidth(styled, 56)); got != plain {
		t.Fatalf("ANSI 感知截断破坏了输入行: %q", got)
	}

	modal := renderPaddedModalFrame(60, 3, "/config", "", []string{styled})
	lines := strings.Split(modal, "\n")
	if len(lines) != 3 || lipgloss.Width(lines[1]) != 60 {
		t.Fatalf("浮层输入行没有填满固定宽度: width=%d\n%s", lipgloss.Width(lines[1]), modal)
	}
	if !strings.Contains(ansi.Strip(lines[1]), "https://api.deepseek.com") {
		t.Fatalf("浮层丢失 Base URL:\n%s", modal)
	}
}

func TestProviderHubDeleteClearsOnlyOptionalAPIKey(t *testing.T) {
	optional := &modelConfigState{step: configStepHub, provider: "proxy", providerType: "openai",
		hasAPIKey: true, apiKeyHint: "sk-o******7890", apiKeyOptional: true, apiKeyAction: host.APIKeyKeep}
	optional.cursor = hubFieldIndex(optional.hubFields(), "key")
	m := Model{modelConfig: optional}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyDelete})
	if optional.apiKeyAction != host.APIKeyClear || optional.keyStatus() != "Đã xóa" {
		t.Fatalf("可选 Key 的 Delete 应标记清除，action=%q status=%q", optional.apiKeyAction, optional.keyStatus())
	}

	required := &modelConfigState{step: configStepHub, provider: "openrouter",
		hasAPIKey: true, apiKeyHint: "sk-o******7890", apiKeyOptional: false, apiKeyAction: host.APIKeyKeep}
	required.cursor = hubFieldIndex(required.hubFields(), "key")
	m = Model{modelConfig: required}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyDelete})
	if required.apiKeyAction != host.APIKeyKeep || !strings.Contains(required.message, "không thể xóa") {
		t.Fatalf("必需 Key 不应被清除，action=%q message=%q", required.apiKeyAction, required.message)
	}
}

func TestConfigTextInputSupportsCursorEditing(t *testing.T) {
	state := &modelConfigState{step: configStepCustomName}
	state.startTextInput("ac", "Provider 名称", false)
	state.input.SetCursor(1)
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if got := state.input.Value(); got != "abc" {
		t.Fatalf("统一输入框应支持在光标处插入，得到 %q", got)
	}
}

func TestProviderHubShowsConfigPathAndConnectionAction(t *testing.T) {
	state := &modelConfigState{
		step: configStepHub, provider: "proxy", apiKeyOptional: true, currentModel: "m2",
		models:   []bootstrap.ModelConfig{{Name: "m1"}, {Name: "m2"}},
		snapshot: host.ModelConfigurationSnapshot{ConfigPath: `C:\work\.ainovel\config.json`},
	}
	fields := state.hubFields()
	idx := hubFieldIndex(fields, "test")
	if idx < 0 || fields[idx].value != "m2" {
		t.Fatalf("测试连接应优先当前模型，fields=%#v", fields)
	}
	view := renderModelConfigModal(120, state)
	for _, want := range []string{"Cấu hình nâng cao", "extra_body"} {
		if !strings.Contains(view, want) {
			t.Fatalf("配置 Hub 缺少 %q:\n%s", want, view)
		}
	}
	compact := strings.NewReplacer("\r", "", "\n", "", " ", "", "│", "").Replace(view)
	if !strings.Contains(compact, `C:\work\.ainovel\config.json`) {
		t.Fatalf("配置 Hub 未完整展示配置路径:\n%s", view)
	}
}

func TestModelConfigMessageWrapKeepsErrorTail(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", apiKeyOptional: true,
		message: "连接失败：" + strings.Repeat("上游返回了很长的错误信息", 8) + " UNIQUE-ERROR-TAIL"}
	view := renderModelConfigModal(64, state)
	compact := strings.NewReplacer("\r", "", "\n", "", " ", "", "│", "").Replace(view)
	if !strings.Contains(compact, "UNIQUE-ERROR-TAIL") {
		t.Fatalf("长错误不应截断尾部:\n%s", view)
	}
}

func TestConnectionActionStartsAsyncTestWithoutLeavingHub(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", providerType: "openai",
		apiKeyOptional: true, models: []bootstrap.ModelConfig{{Name: "m1"}}}
	state.cursor = hubFieldIndex(state.hubFields(), "test")
	m := Model{modelConfig: state}
	_, cmd := m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !state.testing || state.step != configStepHub {
		t.Fatalf("测试连接应异步留在 hub，cmd=%v testing=%v step=%d", cmd != nil, state.testing, state.step)
	}
}

func TestConnectionTestCanBeCancelled(t *testing.T) {
	cancelled := false
	state := &modelConfigState{step: configStepHub, provider: "proxy", testing: true,
		testCancel: func() { cancelled = true }}
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !cancelled || !state.testing || state.message != "Đang hủy kiểm tra kết nối..." {
		t.Fatalf("Esc 应取消在途测试并等待结果，cancelled=%v testing=%v message=%q", cancelled, state.testing, state.message)
	}

	updated, _, handled := m.handleRuntimeMsg(modelConfigConnectionMsg{err: context.Canceled})
	m = updated.(Model)
	if !handled || m.modelConfig.testing || m.modelConfig.message != "Đã hủy kiểm tra kết nối" {
		t.Fatalf("取消结果未正确收敛: handled=%v testing=%v message=%q", handled, m.modelConfig.testing, m.modelConfig.message)
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
	state := &modelConfigState{editSetting: "book_usd"}
	state.startTextInput("25.5", "", false)
	if err := state.applyBudgetInput(); err != nil {
		t.Fatalf("book_usd input failed: %v", err)
	}
	if state.generalBudget.BookUSD != 25.5 || state.generalBudget.WarnRatio != 0.8 {
		t.Fatalf("budget = %#v, want book_usd 25.5 and default warn 0.8", state.generalBudget)
	}
	state.editSetting = "warn_ratio"
	state.input.SetValue("1.2")
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
