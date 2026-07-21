package tui

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/notify"
	"github.com/voocel/ainovel-cli/internal/utils"
)

type configStep int

const (
	configStepProvider configStep = iota
	configStepAddPicker
	configStepCustomName
	configStepHub // Provider ：Hiện tại，，
	configStepProtocol
	configStepAPI
	configStepModels
	configStepModelDetail // Mô hình：Cửa sổ ngữ cảnh /
	configStepModelName
	configStepModelWindow
	configStepGeneral
	configStepGeneralStyle
	configStepGeneralBudget
	configStepGeneralBudgetInput
	configStepGeneralNotify
	configStepGeneralNotifyCommand
	configStepGeneralNotifyEvents
)

type configProviderChoice struct {
	label    string
	existing *host.ProviderSnapshot
	preset   *bootstrap.ProviderPreset
	custom   bool
	add      bool // “ Provider…”，Trung bình
	general  bool
}

type modelConfigBaseline struct {
	providerType string
	api          string
	baseURL      string
	models       []bootstrap.ModelConfig
}

type modelConfigState struct {
	snapshot   host.ModelConfigurationSnapshot
	step       configStep
	cursor     int
	message    string
	input      textinput.Model
	saving     bool
	testing    bool
	testCancel context.CancelFunc

	providerChoices []configProviderChoice // ： Provider + “ Provider…”
	presetChoices   []configProviderChoice // ：/Tùy chỉnh Provider
	provider        string
	providerType    string
	api             string
	baseURL         string
	models          []bootstrap.ModelConfig
	currentModel    string // Mô hình（Hiện tại provider ），
	existing        bool
	hasAPIKey       bool
	apiKeyHint      string
	apiKeyOptional  bool
	apiKeyAction    host.APIKeyAction
	apiKey          string
	editingField    string
	baseline        *modelConfigBaseline

	modelOrigins []string // 与 models 对齐；已有模型保留原名，新增模型为空，用于生成显式重命名
	modelColumn  int      // 0=模型 ID，1=上下文窗口
	editModelIdx int

	generalStyle  string
	generalBudget bootstrap.BudgetConfig
	generalNotify bootstrap.NotifyConfig
	editSetting   string
}

func newModelConfigState(rt *host.Host) *modelConfigState {
	snapshot := rt.ModelConfiguration()
	style := strings.TrimSpace(snapshot.Style)
	if style == "" {
		style = "default"
	}
	state := &modelConfigState{
		snapshot:      snapshot,
		editModelIdx:  -1,
		generalStyle:  style,
		generalBudget: snapshot.Budget,
		generalNotify: snapshot.Notify,
	}
	state.buildProviderMenus()
	return state
}

// buildProviderMenus ： Provider（）+
// “”， Provider ；（“”
// ） Provider  + Tùy chỉnh。
func (s *modelConfigState) buildProviderMenus() {
	configured := make(map[string]bool, len(s.snapshot.Providers))
	for i := range s.snapshot.Providers {
		provider := s.snapshot.Providers[i]
		configured[provider.Name] = true
		copyProvider := provider
		s.providerChoices = append(s.providerChoices, configProviderChoice{
			label: provider.Name, existing: &copyProvider,
		})
	}
	s.providerChoices = append(s.providerChoices, configProviderChoice{
		label: "+ Thêm Provider…", add: true,
	})
	s.providerChoices = append(s.providerChoices, configProviderChoice{
		label: "Cài đặt chung…", general: true,
	})

	for _, presetValue := range bootstrap.ProviderPresets() {
		if configured[presetValue.Name] && !presetValue.NeedType {
			continue
		}
		preset := presetValue
		choice := configProviderChoice{label: preset.Label, preset: &preset}
		if preset.NeedType {
			choice.custom = true
		}
		s.presetChoices = append(s.presetChoices, choice)
	}
}

// applyProviderChoice Trung bình Provider →  hub；Trung bình → Mặc định hub
// （Tùy chỉnh）。“Giao thức”。
func (s *modelConfigState) applyProviderChoice(choice configProviderChoice) {
	s.cursor = 0
	s.message = ""
	s.editingField = ""
	s.editModelIdx = -1
	s.modelColumn = 0
	s.addingModel = false
	if choice.existing != nil {
		p := choice.existing
		s.provider = p.Name
		s.providerType = p.Type
		s.api = p.API
		s.baseURL = p.BaseURL
		s.models = append([]bootstrap.ModelConfig(nil), p.Models...)
		s.existing = true
		s.hasAPIKey = p.HasAPIKey
		s.apiKeyHint = p.APIKeyHint
		s.apiKeyOptional = !p.RequiresAPIKey
		s.apiKeyAction = host.APIKeyKeep
		s.apiKey = ""
		s.modelOrigins = make([]string, len(s.models))
		for i, model := range s.models {
			s.modelOrigins[i] = model.Name
		}
		s.currentModel = ""
		if s.snapshot.DefaultProvider == s.provider {
			s.currentModel = s.snapshot.DefaultModel
		}
		s.captureBaseline()
		s.step = configStepHub
		return
	}

	//
	s.existing = false
	s.hasAPIKey = false
	s.apiKeyHint = ""
	s.apiKeyAction = host.APIKeyReplace
	s.apiKey = ""
	s.baseline = nil
	s.api = ""
	s.models = nil
	s.currentModel = "" //  provider Trung bình
	if choice.custom {
		s.apiKeyOptional = true
		s.providerType = "openai" // Tùy chỉnhMặc định openai， hub
		s.baseURL = ""
		s.step = configStepCustomName
		s.startTextInput("", "Provider 名称", false)
		return
	}
	s.provider = choice.preset.Name
	s.providerType = "" //  provider Giao thức
	s.baseURL = choice.preset.BaseURL
	s.apiKeyOptional = choice.preset.APIKeyOptional
	s.step = configStepHub
}

// hubField  Provider  hub 。
type hubField struct {
	id    string // protocol / api / key / baseurl / models / save
	label string
	value string
}

// hubFields Hiện tại Provider ：Giao thức，Endpoint  OpenAI Giao thức。
func (s *modelConfigState) hubFields() []hubField {
	var fields []hubField
	if s.providerType != "" {
		fields = append(fields, hubField{"protocol", "Giao thức", s.providerType})
	}
	if s.isOpenAIEndpoint() {
		api := s.api
		if api == "" {
			api = "chat"
		}
		fields = append(fields, hubField{"api", "Endpoint", api})
	}
	fields = append(fields, hubField{"key", "API Key", s.keyStatus()})
	base := s.baseURL
	if base == "" {
		base = "Địa chỉ mặc định"
	}
	fields = append(fields, hubField{"baseurl", "Base URL", base})
	fields = append(fields, hubField{"models", "Mô hình", fmt.Sprintf("%d mục", len(s.models))})
	fields = append(fields, hubField{"save", "Lưu và áp dụng", ""})
	return fields
}

func (s *modelConfigState) testModelName() string {
	for _, model := range s.models {
		if model.Name == s.currentModel {
			return model.Name
		}
	}
	if len(s.models) > 0 {
		return s.models[0].Name
	}
	return ""
}

func (s *modelConfigState) isOpenAIEndpoint() bool {
	return s.providerType == "openai" || (s.providerType == "" && s.provider == "openai")
}

func (s *modelConfigState) keyStatus() string {
	switch s.apiKeyAction {
	case host.APIKeyClear:
		return "Đã xóa"
	case host.APIKeyReplace:
		if s.apiKey != "" {
			return "Đã nhập"
		}
	}
	if s.hasAPIKey {
		return "Đã thiết lập"
	}
	return "Chưa thiết lập"
}

// enterHubField （Giao thức/Endpoint/Key/BaseURL/Mô hình），。
func (s *modelConfigState) enterHubField(id string) (save bool) {
	s.message = ""
	switch id {
	case "protocol":
		s.step = configStepProtocol
		s.cursor = protocolIndex(s.providerType)
	case "api":
		s.step = configStepAPI
		s.cursor = 0
		if s.api == "responses" {
			s.cursor = 1
		}
	case "key":
		return false, s.beginInlineEdit("key")
	case "baseurl":
		return false, s.beginInlineEdit("baseurl")
	case "models":
		s.ensureModelOrigins()
		s.step = configStepModels
		s.cursor = 0
		s.modelColumn = 0
	case "test":
		return false, nil
	case "save":
		return true, nil
	}
	return false, nil
}

func (s *modelConfigState) beginInlineEdit(field string) tea.Cmd {
	s.editingField = field

	switch field {
	case "key":
		placeholder := "输入 API Key"
		if s.hasEffectiveAPIKey() {
			placeholder = "输入新 Key，留空保留"
		}
		return s.startTextInput("", placeholder, true)
	case "baseurl":
		return s.startTextInput(s.baseURL, "留空使用默认地址", false)
	}
	return nil
}

func (s *modelConfigState) startTextInput(value, placeholder string, secret bool) tea.Cmd {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = placeholder
	input.CharLimit = 0
	input.Width = 36
	input.TextStyle = lipgloss.NewStyle().Foreground(bodyTextColor).Underline(true)
	input.PlaceholderStyle = lipgloss.NewStyle().Foreground(colorDim).Underline(true)
	input.Cursor.Style = lipgloss.NewStyle().Foreground(colorAccent)
	if secret {
		input.EchoMode = textinput.EchoPassword
		input.EchoCharacter = '•'
	}
	input.SetValue(value)
	input.CursorEnd()
	s.input = input
	return s.input.Focus()
}

func (s *modelConfigState) hasEffectiveAPIKey() bool {
	switch s.apiKeyAction {
	case host.APIKeyClear:
		return false
	case host.APIKeyReplace:
		return strings.TrimSpace(s.apiKey) != ""
	default:
		return s.hasAPIKey
	}
}

func (s *modelConfigState) finishInlineEdit() bool {
	value := strings.TrimSpace(s.input.Value())
	switch s.editingField {
	case "key":
		if value == "" {
			if !s.apiKeyOptional && !s.hasEffectiveAPIKey() {
				s.message = "该 Provider 必须配置 API Key"
				return false
			}
		} else {
			s.apiKey = value
			s.apiKeyAction = host.APIKeyReplace
		}
	case "baseurl":
		s.baseURL = value
	}
	s.input.Blur()
	s.editingField = ""
	s.message = ""
	return true
}

// escapeBack  Esc ； false Tắt。
// ：Provider  ⊃  hub ⊃ ； ⊃  ⊃ Tùy chỉnh；
// hub Mô hình ⊃ Mô hình/。
func (s *modelConfigState) escapeBack() (configStep, bool) {
	switch s.step {
	case configStepAddPicker, configStepHub, configStepGeneral:
		return configStepProvider, true
	case configStepCustomName:
		return configStepAddPicker, true
	case configStepProtocol, configStepAPI, configStepModels:
		return configStepHub, true
	case configStepGeneralStyle, configStepGeneralBudget, configStepGeneralNotify:
		return configStepGeneral, true
	case configStepGeneralBudgetInput:
		return configStepGeneralBudget, true
	case configStepGeneralNotifyCommand, configStepGeneralNotifyEvents:
		return configStepGeneralNotify, true
	case configStepModelDetail, configStepModelName:
		return configStepModels, true
	case configStepModelWindow:
		if s.editModelIdx >= 0 { // Mô hình → ；Luồng →
			return configStepModelDetail, true
		}
		return configStepModelName, true
	default: // configStepProvider
		return 0, false
	}
}

// modelDetailFields Mô hình hub ：Cửa sổ ngữ cảnh / 。
// “xMặc định”——“Hiện tại” /model，/config 。
func (s *modelConfigState) modelDetailFields() []hubField {
	window := "Tự động"
	if w := s.models[s.editModelIdx].ContextWindow; w > 0 {
		window = formatContextWindow(w)
	}
	return []hubField{
		{"window", "Cửa sổ ngữ cảnh", window},
		{"delete", "Xóa mô hình", ""},
	}
}

// deleteModel  idx Mô hình；Mặc địnhVai trò，。
func (s *modelConfigState) deleteModel(idx int) bool {
	if idx < 0 || idx >= len(s.models) {
		return false
	}
	s.ensureModelOrigins()
	model := s.models[idx]
	if model.Name == s.currentModel {
		s.message = "Mô hình này đang được sử dụng; hãy dùng /model để chuyển trước khi xóa"
		return false
	}
	for _, ref := range s.snapshot.ReferencesFor(s.provider, identity) {
		if ref == "default" {
			continue //  currentModel ，
		}
		s.message = fmt.Sprintf("Mô hình vẫn được %s tham chiếu; hãy chuyển bằng /model trước khi xóa", ref)
		return false
	}
	s.models = append(s.models[:idx], s.models[idx+1:]...)
	s.modelOrigins = append(s.modelOrigins[:idx], s.modelOrigins[idx+1:]...)
	s.cursor = idx
	if s.cursor > len(s.models) {
		s.cursor = len(s.models)
	}
	s.message = ""
	return true
}

func (s *modelConfigState) draft() host.ModelConfigurationDraft {
	s.ensureModelOrigins()
	renames := make([]host.ModelRename, 0)
	for i, model := range s.models {
		if origin := s.modelOrigins[i]; origin != "" && origin != model.Name {
			renames = append(renames, host.ModelRename{From: origin, To: model.Name})
		}
	}
	return host.ModelConfigurationDraft{
		Provider: s.provider, Type: s.providerType, API: s.api, BaseURL: s.baseURL,
		Models:       append([]bootstrap.ModelConfig(nil), s.models...),
		Renames:      renames,
		APIKeyAction: s.apiKeyAction, APIKey: s.apiKey,
	}
}

func (s *modelConfigState) generalDraft() host.GeneralSettingsDraft {
	notifyCfg := s.generalNotify
	notifyCfg.Events = append([]string(nil), s.generalNotify.Events...)
	return host.GeneralSettingsDraft{
		Style:  s.generalStyle,
		Budget: s.generalBudget,
		Notify: notifyCfg,
	}
}

type modelConfigSavedMsg struct{ err error }

type generalConfigSavedMsg struct{ err error }

func saveModelConfiguration(rt *host.Host, draft host.ModelConfigurationDraft) tea.Cmd {
	return func() tea.Msg { return modelConfigSavedMsg{err: rt.ConfigureModels(draft)} }
}

func saveGeneralConfiguration(rt *host.Host, draft host.GeneralSettingsDraft) tea.Cmd {
	return func() tea.Msg { return generalConfigSavedMsg{err: rt.ConfigureGeneralSettings(draft)} }
}

func (m Model) handleModelConfigKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.modelConfig
	if state == nil {
		return m, nil
	}
	if msg.Type == tea.KeyEsc {
		if state.testing {
			if state.testCancel != nil {
				state.testCancel()
			}
			state.message = "正在取消连接测试..."
			return m, nil
		}
		if state.editingField != "" && (state.step == configStepHub || state.step == configStepModels) {
			if state.step == configStepModels {
				state.cancelModelEdit()
			} else {
				state.input.Blur()
				state.editingField = ""
				state.message = ""
			}
			return m, nil
		}
		if target, ok := state.escapeBack(); ok {
			state.input.Blur()
			state.step = target
			state.cursor = 0
			state.message = ""
			return m, nil
		}
		m.modelConfig = nil
		return m, m.textarea.Focus()
	}
	if state.saving || state.testing {
		return m, nil
	}

	switch state.step {
	case configStepProvider:
		moveConfigCursor(state, msg, len(state.providerChoices))
		if msg.Type == tea.KeyEnter && state.cursor >= 0 && state.cursor < len(state.providerChoices) {
			choice := state.providerChoices[state.cursor]
			if choice.add {
				state.step = configStepAddPicker
				state.cursor = 0
				state.message = ""
			} else if choice.general {
				state.step = configStepGeneral
				state.cursor = 0
				state.message = ""
			} else {
				state.applyProviderChoice(choice)
			}
		}
	case configStepGeneral:
		fields := state.generalFields()
		moveConfigCursor(state, msg, len(fields))
		if msg.Type == tea.KeyEnter && state.cursor >= 0 && state.cursor < len(fields) {
			switch fields[state.cursor].id {
			case "style":
				state.step = configStepGeneralStyle
				state.cursor = indexOfString(configStyleOptions, state.generalStyle)
			case "budget":
				state.step = configStepGeneralBudget
				state.cursor = 0
			case "notify":
				state.step = configStepGeneralNotify
				state.cursor = 0
			case "save":
				state.saving = true
				state.message = "Đang kiểm tra và lưu cài đặt chung..."
				return m, saveGeneralConfiguration(m.runtime, state.generalDraft())
			}
		}
	case configStepGeneralStyle:
		moveConfigCursor(state, msg, len(configStyleOptions))
		if msg.Type == tea.KeyEnter {
			state.generalStyle = configStyleOptions[state.cursor]
			state.step = configStepGeneral
			state.cursor = 0
			state.message = ""
		}
	case configStepGeneralBudget:
		fields := state.generalBudgetFields()
		moveConfigCursor(state, msg, len(fields))
		if msg.Type == tea.KeyEnter && state.cursor >= 0 && state.cursor < len(fields) {
			field := fields[state.cursor]
			switch field.id {
			case "book_usd":
				state.editSetting = field.id
				if state.generalBudget.BookUSD > 0 {
					state.input = strconv.FormatFloat(state.generalBudget.BookUSD, 'f', -1, 64)
				} else {
					state.input = ""
				}
				state.step = configStepGeneralBudgetInput
			case "warn_ratio":
				state.editSetting = field.id
				if state.generalBudget.WarnRatio > 0 {
					state.input = strconv.FormatFloat(state.generalBudget.WarnRatio, 'f', -1, 64)
				} else {
					state.input = "0.8"
				}
				state.step = configStepGeneralBudgetInput
			case "hard_stop":
				state.generalBudget.HardStop = !state.generalBudget.HardStop
			}
		}
	case configStepGeneralBudgetInput:
		if handleConfigInput(&state.input, msg) && msg.Type == tea.KeyEnter {
			if err := state.applyBudgetInput(); err != nil {
				state.message = err.Error()
				break
			}
			state.step = configStepGeneralBudget
			state.cursor = 0
			state.message = ""
		}
	case configStepGeneralNotify:
		fields := state.generalNotifyFields()
		moveConfigCursor(state, msg, len(fields))
		if msg.Type == tea.KeyEnter && state.cursor >= 0 && state.cursor < len(fields) {
			switch fields[state.cursor].id {
			case "enabled":
				next := !state.generalNotify.IsEnabled()
				state.generalNotify.Enabled = &next
			case "command":
				state.input = state.generalNotify.Command
				state.step = configStepGeneralNotifyCommand
			case "events":
				state.step = configStepGeneralNotifyEvents
				state.cursor = 0
			}
		}
	case configStepGeneralNotifyCommand:
		if handleConfigInput(&state.input, msg) && msg.Type == tea.KeyEnter {
			state.generalNotify.Command = strings.TrimSpace(state.input)
			state.step = configStepGeneralNotify
			state.cursor = 0
			state.message = ""
		}
	case configStepGeneralNotifyEvents:
		total := len(notify.Kinds()) + 2
		moveConfigCursor(state, msg, total)
		if msg.Type == tea.KeyEnter {
			switch state.cursor {
			case 0:
				state.generalNotify.Events = nil
				enabled := true
				state.generalNotify.Enabled = &enabled
				state.message = "Đã chọn tất cả sự kiện"
			case 1:
				state.generalNotify.Events = nil
				enabled := false
				state.generalNotify.Enabled = &enabled
				state.message = "Đã tắt thông báo; bật lại để gửi sự kiện"
			default:
				state.toggleNotifyEvent(notify.Kinds()[state.cursor-2])
				state.message = ""
			}
		}
	case configStepAddPicker:
		moveConfigCursor(state, msg, len(state.presetChoices))
		if msg.Type == tea.KeyEnter && state.cursor >= 0 && state.cursor < len(state.presetChoices) {
			state.applyProviderChoice(state.presetChoices[state.cursor])
		}
	case configStepCustomName:
		if msg.Type == tea.KeyEnter {
			name := strings.TrimSpace(state.input.Value())
			if name == "" {
				state.message = "Tên nhà cung cấp không được để trống"
				break
			}
			for _, provider := range state.snapshot.Providers {
				if provider.Name == name {
					state.message = "Nhà cung cấp đã tồn tại; hãy quay lại và chọn chỉnh sửa"
					return m, nil
				}
			}
			state.provider = name
			state.step = configStepHub
			state.cursor = 0
			state.message = ""
			return m, nil
		}
		var cmd tea.Cmd
		state.input, cmd = state.input.Update(msg)
		return m, cmd
	case configStepHub:
		fields := state.hubFields()
		if state.editingField != "" {
			if msg.Type == tea.KeyEnter {
				state.finishInlineEdit()
				return m, nil
			}
			var cmd tea.Cmd
			state.input, cmd = state.input.Update(msg)
			return m, cmd
		}
		moveConfigCursor(state, msg, len(fields))
		if msg.Type == tea.KeyDelete && state.cursor >= 0 && state.cursor < len(fields) && fields[state.cursor].id == "key" {
			if !state.apiKeyOptional {
				state.message = "该 Provider 必须配置 API Key，不能清除"
				break
			}
			state.apiKeyAction = host.APIKeyClear
			state.apiKey = ""
			state.message = "API Key 已标记清除，保存配置后生效"
			break
		}
		if msg.Type == tea.KeyEnter && state.cursor >= 0 && state.cursor < len(fields) {
			fieldID := fields[state.cursor].id
			if fieldID == "test" {
				model := state.testModelName()
				if model == "" {
					state.message = "请至少添加一个模型后再测试连接"
					break
				}
				if !state.apiKeyOptional && !state.hasEffectiveAPIKey() {
					state.message = "该 Provider 必须配置 API Key"
					break
				}
				state.testing = true
				state.message = fmt.Sprintf("正在测试连接：%s/%s...", state.provider, model)
				ctx, cancel := context.WithCancel(context.Background())
				state.testCancel = cancel
				return m, testModelConnection(ctx, m.runtime, state.draft(), model)
			}
			save, cmd := state.enterHubField(fieldID)
			if save {
				if len(state.models) == 0 {
					state.message = "Hãy thêm ít nhất một mô hình"
					break
				}
				if !state.apiKeyOptional && !state.hasEffectiveAPIKey() {
					state.message = "该 Provider 必须配置 API Key"
					break
				}
				state.saving = true
				state.message = "Đang kiểm tra và lưu cấu hình..."
				return m, saveModelConfiguration(m.runtime, state.draft())
			}
			return m, cmd
		}
	case configStepProtocol:
		moveConfigCursor(state, msg, len(configProtocols))
		if msg.Type == tea.KeyEnter {
			state.providerType = configProtocols[state.cursor]
			if state.providerType != "openai" {
				state.api = ""
			}
			state.step = configStepHub
			state.cursor = 0
		}
	case configStepAPI:
		moveConfigCursor(state, msg, len(configAPIs))
		if msg.Type == tea.KeyEnter {
			state.api = configAPIs[state.cursor]
			state.step = configStepHub
			state.cursor = 0
		}
	case configStepKeyAction:
		moveConfigCursor(state, msg, len(configKeyActions))
		if msg.Type == tea.KeyEnter {
			state.apiKeyAction = configKeyActions[state.cursor].action
			if state.apiKeyAction == host.APIKeyClear && !state.apiKeyOptional {
				state.message = "Nhà cung cấp này bắt buộc có API Key, không thể xóa"
				return m, nil
			}
			if state.apiKeyAction == host.APIKeyReplace {
				state.step = configStepKeyInput
				state.input = ""
			} else {
				state.step = configStepHub
				state.cursor = 0
			}
		}
	case configStepKeyInput:
		if handleConfigInput(&state.input, msg) && msg.Type == tea.KeyEnter {
			state.apiKey = strings.TrimSpace(state.input)
			if state.apiKey == "" && !state.apiKeyOptional {
				state.message = "Nhà cung cấp này bắt buộc có API Key"
				return m, nil
			}
			// Đầu vào；（）。
			if state.apiKey == "" {
				state.apiKeyAction = host.APIKeyKeep
			} else {
				state.apiKeyAction = host.APIKeyReplace
			}
			state.step = configStepHub
			state.cursor = 0
			state.message = ""
		}
	case configStepBaseURL:
		if handleConfigInput(&state.input, msg) && msg.Type == tea.KeyEnter {
			state.baseURL = strings.TrimSpace(state.input)
			state.step = configStepHub
			state.cursor = 0
			state.message = ""
		}
	case configStepModels:
		// “+ Thêm mô hình…”；Trung bìnhMô hình， ↑↓/Enter。
		moveConfigCursor(state, msg, len(state.models)+1)
		if state.cursor < len(state.models) {
			switch msg.Type {
			case tea.KeyLeft:
				state.modelColumn = 0
			case tea.KeyRight:
				state.modelColumn = 1
			case tea.KeyDelete:
				state.deleteModel(state.cursor)
				return m, nil
			}
		}
		if msg.Type == tea.KeyEnter {
			if state.cursor == len(state.models) {
				state.step = configStepModelName
				state.input = ""
				state.editModelIdx = -1
				state.message = ""
			} else if state.cursor >= 0 && state.cursor < len(state.models) {
				state.editModelIdx = state.cursor
				state.step = configStepModelDetail
				state.cursor = 0
				state.message = ""
			}
		}
	case configStepModelDetail:
		if state.editModelIdx < 0 || state.editModelIdx >= len(state.models) {
			state.step = configStepModels
			state.cursor = 0
			break
		}
		fields := state.modelDetailFields()
		moveConfigCursor(state, msg, len(fields))
		if msg.Type == tea.KeyEnter && state.cursor >= 0 && state.cursor < len(fields) {
			switch fields[state.cursor].id {
			case "window":
				model := state.models[state.editModelIdx]
				state.pendingModel = model.Name
				if model.ContextWindow > 0 {
					state.input = strconv.Itoa(model.ContextWindow)
				} else {
					state.input = ""
				}
				state.step = configStepModelWindow
				state.message = ""
			case "delete":
				if state.deleteModel(state.editModelIdx) {
					state.step = configStepModels
					state.editModelIdx = -1
				}
			}
		}
	case configStepModelName:
		if handleConfigInput(&state.input, msg) && msg.Type == tea.KeyEnter {
			name := strings.TrimSpace(state.input)
			if name == "" {
				state.message = "Tên mô hình không được để trống"
				break
			}
			for _, model := range state.models {
				if model.Name == name {
					state.message = "Mô hình đã tồn tại"
					return m, nil
				}
			}
			state.pendingModel = name
			state.input = ""
			state.step = configStepModelWindow
			state.message = ""
		}
	case configStepModelWindow:
		if handleConfigInput(&state.input, msg) && msg.Type == tea.KeyEnter {
			window, err := parseContextWindowInput(state.input)
			if err != nil {
				state.message = err.Error()
				break
			}
			if state.editModelIdx >= 0 {
				// Mô hình → ，（editModelIdx ）。
				state.models[state.editModelIdx].ContextWindow = window
				state.step = configStepModelDetail
				state.cursor = 1
			} else {
				// Luồng → Trung bình。
				state.models = append(state.models, bootstrap.ModelConfig{Name: state.pendingModel, ContextWindow: window})
				state.cursor = len(state.models) - 1
				state.addingModel = true
				state.message = ""
				return m, state.beginModelEdit(state.cursor, 0)
			} else if state.cursor >= 0 && state.cursor < len(state.models) {
				return m, state.beginModelEdit(state.cursor, state.modelColumn)
			}
		}
	}
	return m, nil
}

var configProtocols = []string{"openai", "anthropic", "gemini"}
var configAPIs = []string{"chat", "responses"}

var configKeyActions = []struct {
	label  string
	action host.APIKeyAction
}{
	{"Giữ API Key hiện có", host.APIKeyKeep},
	{"Nhập API Key mới", host.APIKeyReplace},
	{"Xóa API Key", host.APIKeyClear},
}

func protocolIndex(protocol string) int {
	for i, item := range configProtocols {
		if item == protocol {
			return i
		}
	}
	return 0
}

func moveConfigCursor(state *modelConfigState, msg tea.KeyMsg, total int) {
	if total <= 0 {
		state.cursor = 0
		return
	}
	switch msg.Type {
	case tea.KeyUp:
		state.cursor = (state.cursor - 1 + total) % total
	case tea.KeyDown:
		state.cursor = (state.cursor + 1) % total
	}
}

// handleConfigInput Đầu vào； true Đầu vào。
func handleConfigInput(value *string, msg tea.KeyMsg) bool {
	if msg.String() == "ctrl+u" {
		*value = ""
		return true
	}
	switch msg.Type {
	case tea.KeyEnter:
		return true
	case tea.KeyBackspace, tea.KeyDelete:
		runes := []rune(*value)
		if len(runes) > 0 {
			*value = string(runes[:len(runes)-1])
		}
		return true
	case tea.KeySpace:
		*value += " "
		return true
	case tea.KeyRunes:
		*value += utils.CleanInputRunes(msg.Runes)
		return true
	default:
		return false
	}
}

func parseContextWindowInput(input string) (int, error) {
	value := strings.ToLower(strings.TrimSpace(input))
	if value == "" || value == "0" || value == "auto" {
		return 0, nil
	}
	multiplier := float64(1)
	if strings.HasSuffix(value, "k") {
		multiplier = 1000
		value = strings.TrimSpace(strings.TrimSuffix(value, "k"))
	} else if strings.HasSuffix(value, "m") {
		multiplier = 1_000_000
		value = strings.TrimSpace(strings.TrimSuffix(value, "m"))
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("Cửa sổ ngữ cảnh phải là số nguyên dương, 128K, 1M, hoặc để trống để dùng giá trị tự động")
	}
	result := number * multiplier
	if result > float64(math.MaxInt) || math.Trunc(result) != result {
		return 0, fmt.Errorf("Cửa sổ ngữ cảnh vượt quá phạm vi số nguyên hợp lệ")
	}
	return int(result), nil
}

var configStyleOptions = []string{"default", "romance", "fantasy", "suspense"}

func indexOfString(items []string, value string) int {
	for i, item := range items {
		if item == value {
			return i
		}
	}
	return 0
}

func (s *modelConfigState) generalFields() []hubField {
	return []hubField{
		{"style", "Phong cách", s.generalStyle},
		{"budget", "Ngân sách", s.budgetSummary()},
		{"notify", "Thông báo", s.notifySummary()},
		{"save", "Lưu cài đặt chung", ""},
	}
}

func (s *modelConfigState) generalBudgetFields() []hubField {
	return []hubField{
		{"book_usd", "book_usd", formatFloatSetting(s.generalBudget.BookUSD, "0 = tắt")},
		{"warn_ratio", "warn_ratio", formatFloatSetting(s.effectiveWarnRatio(), "0.8")},
		{"hard_stop", "hard_stop", formatBoolSetting(s.generalBudget.HardStop)},
	}
}

func (s *modelConfigState) generalNotifyFields() []hubField {
	return []hubField{
		{"enabled", "enabled", formatBoolSetting(s.generalNotify.IsEnabled())},
		{"command", "command", emptyAsDefault(s.generalNotify.Command, "mặc định hệ thống")},
		{"events", "events", fmt.Sprintf("%d mục", s.notifyEventCount())},
	}
}

func (s *modelConfigState) budgetSummary() string {
	if s.generalBudget.BookUSD <= 0 {
		return "tắt"
	}
	hard := "tắt"
	if s.generalBudget.HardStop {
		hard = "bật"
	}
	return fmt.Sprintf("%.2f USD, cảnh báo %.0f%%, dừng cứng: %s", s.generalBudget.BookUSD, s.effectiveWarnRatio()*100, hard)
}

func (s *modelConfigState) notifySummary() string {
	if !s.generalNotify.IsEnabled() {
		return "tắt"
	}
	return fmt.Sprintf("bật, %d sự kiện", s.notifyEventCount())
}

func (s *modelConfigState) notifyEventCount() int {
	if len(s.generalNotify.Events) == 0 {
		return len(notify.Kinds())
	}
	return len(s.generalNotify.Events)
}

func (s *modelConfigState) effectiveWarnRatio() float64 {
	if s.generalBudget.WarnRatio > 0 {
		return s.generalBudget.WarnRatio
	}
	return 0.8
}

func formatFloatSetting(value float64, fallback string) string {
	if value <= 0 {
		return fallback
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func formatBoolSetting(value bool) string {
	if value {
		return "bật"
	}
	return "tắt"
}

func emptyAsDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func (s *modelConfigState) applyBudgetInput() error {
	value := strings.TrimSpace(s.input)
	if value == "" {
		value = "0"
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number < 0 {
		return fmt.Errorf("Giá trị ngân sách phải là số không âm")
	}
	switch s.editSetting {
	case "book_usd":
		s.generalBudget.BookUSD = number
		if number > 0 && s.generalBudget.WarnRatio == 0 {
			s.generalBudget.WarnRatio = 0.8
		}
	case "warn_ratio":
		if s.generalBudget.BookUSD > 0 && (number <= 0 || number >= 1) {
			return fmt.Errorf("warn_ratio phải nằm trong khoảng (0, 1) khi budget bật")
		}
		s.generalBudget.WarnRatio = number
	}
	return nil
}

func (s *modelConfigState) toggleNotifyEvent(event string) {
	seen := false
	next := s.generalNotify.Events[:0]
	for _, item := range s.generalNotify.Events {
		if item == event {
			seen = true
			continue
		}
		next = append(next, item)
	}
	if !seen {
		next = append(next, event)
	}
	s.generalNotify.Events = append([]string(nil), next...)
}

func (s *modelConfigState) notifyEventSelected(event string) bool {
	if len(s.generalNotify.Events) == 0 && s.generalNotify.IsEnabled() {
		return true
	}
	for _, item := range s.generalNotify.Events {
		if item == event {
			return true
		}
	}
	return false
}

func renderModelConfigModal(width int, state *modelConfigState) string {
	if state == nil {
		return ""
	}
	//  /model ：rộng、 [60,76] ，Cao=Cao（Đầu vào，）。
	boxW := min(max(60, width*3/5), 76, width-4)
	contentW := paddedModalContentWidth(boxW)
	var lines []string
	title := "/config Cấu hình mô hình"
	hint := "↑↓ Chọn · Enter Xác nhận · Esc Hủy"

	switch state.step {
	case configStepProvider:
		lines = append(lines, configHeading("Chọn nhà cung cấp để chỉnh sửa hoặc thêm mới"))
		lines = append(lines, renderConfigChoices(labelsForProviderChoices(state.providerChoices), state.cursor, contentW, 12)...)
	case configStepAddPicker:
		lines = append(lines, configHeading("Chọn nhà cung cấp muốn thêm"))
		lines = append(lines, renderConfigChoices(labelsForProviderChoices(state.presetChoices), state.cursor, contentW, 12)...)
	case configStepGeneral:
		lines = append(lines, configHeading("Cài đặt chung"))
		lines = append(lines, renderFieldList(state.generalFields(), state.cursor, contentW)...)
		hint = "↑↓ Chọn · Enter Mở/Lưu · Esc Quay lại"
	case configStepGeneralStyle:
		lines = append(lines, configHeading("Phong cách viết"))
		lines = append(lines, renderConfigChoices(configStyleOptions, state.cursor, contentW, 8)...)
	case configStepGeneralBudget:
		lines = append(lines, configHeading("Ngân sách"))
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("Trạng thái: "+state.budgetSummary()))
		lines = append(lines, renderFieldList(state.generalBudgetFields(), state.cursor, contentW)...)
		hint = "↑↓ Chọn · Enter Chỉnh/Bật tắt · Esc Quay lại"
	case configStepGeneralBudgetInput:
		lines = append(lines, configHeading("Ngân sách - "+state.editSetting))
		lines = append(lines, renderConfigInput(state.input, false, contentW))
		hint = configInputHint
	case configStepGeneralNotify:
		lines = append(lines, configHeading("Thông báo"))
		lines = append(lines, renderFieldList(state.generalNotifyFields(), state.cursor, contentW)...)
		hint = "↑↓ Chọn · Enter Chỉnh/Bật tắt · Esc Quay lại"
	case configStepGeneralNotifyCommand:
		lines = append(lines, configHeading("Lệnh thông báo"))
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("Để trống = dùng trình thông báo mặc định của hệ điều hành"))
		lines = append(lines, renderConfigInput(state.input, false, contentW))
		hint = configInputHint
	case configStepGeneralNotifyEvents:
		lines = append(lines, configHeading("Sự kiện thông báo"))
		lines = append(lines, renderNotifyEventChoices(state, contentW)...)
		hint = "↑↓ Chọn · Enter Bật/tắt · Esc Quay lại"
	case configStepCustomName:
		lines = append(lines, configHeading("Tên nhà cung cấp tùy chỉnh"), renderConfigInput(state.input, false, contentW))
		hint = configInputHint
	case configStepHub:
		heading := state.provider
		if !state.existing {
			heading += " (mới)"
		}
		lines = append(lines, configHeading(heading))
		lines = append(lines, renderFieldList(state.hubFields(), state.cursor, contentW)...)
		hint = "↑↓ Chọn · Enter Mở/Lưu · Esc Quay lại"
	case configStepProtocol:
		lines = append(lines, configHeading("Loại giao thức API"))
		lines = append(lines, renderConfigChoices(configProtocols, state.cursor, contentW, 8)...)
	case configStepAPI:
		lines = append(lines, configHeading("OpenAI Endpoint"))
		lines = append(lines, renderConfigChoices([]string{"chat · /v1/chat/completions", "responses · /v1/responses"}, state.cursor, contentW, 8)...)
	case configStepKeyAction:
		lines = append(lines, configHeading("API Key"))
		var labels []string
		for _, item := range configKeyActions {
			labels = append(labels, item.label)
		}
		lines = append(lines, renderConfigChoices(labels, state.cursor, contentW, 8)...)
	case configStepKeyInput:
		label := "Nhập API Key (nội dung được ẩn)"
		if state.apiKeyOptional {
			label += ", có thể để trống"
		}
		lines = append(lines, configHeading(label), renderConfigInput(state.input, true, contentW))
		hint = configInputHint
	case configStepBaseURL:
		lines = append(lines, configHeading("Base URL (để trống để dùng địa chỉ mặc định của nhà cung cấp)"), renderConfigInput(state.input, false, contentW))
		hint = configInputHint
	case configStepModels:
		lines = append(lines, configHeading("Quản lý danh sách mô hình"))
		total := len(state.models) + 1 // “+ Thêm mô hình…”
		start, end := configWindow(total, state.cursor, 10)
		for i := start; i < end; i++ {
			prefix := "  "
			selected := i == state.cursor
			if selected {
				prefix = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("› ")
			}
			if i == len(state.models) {
				style := lipgloss.NewStyle().Foreground(bodyTextColor)
				if selected {
					style = style.Foreground(colorAccent).Bold(true)
				}
				lines = append(lines, prefix+style.Render("+ Thêm mô hình…"))
				continue
			}
			model := state.models[i]
			window := "Tự động"
			if model.ContextWindow > 0 {
				window = formatContextWindow(model.ContextWindow)
			}
			line := fmt.Sprintf("%s%-38s  ngữ cảnh %s", prefix, truncateWidth(model.Name, 36), window)
			lines = append(lines, truncateWidth(line, contentW))
		}
		hint = "↑↓ Chọn · Enter Mở · Esc Quay lại"
	case configStepModelDetail:
		if state.editModelIdx >= 0 && state.editModelIdx < len(state.models) {
			lines = append(lines, configHeading(state.models[state.editModelIdx].Name))
			lines = append(lines, renderFieldList(state.modelDetailFields(), state.cursor, contentW)...)
		}
		hint = "↑↓ Chọn · Enter Xác nhận · Esc Quay lại"
	case configStepModelName:
		lines = append(lines, configHeading("Tên mô hình mới"), renderConfigInput(state.input, false, contentW))
		hint = configInputHint
	case configStepModelWindow:
		lines = append(lines, configHeading("Mô hình "+state.pendingModel+" - cửa sổ ngữ cảnh"))
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("Để trống hoặc 0 = tự động phân tích; hỗ trợ 128K / 1M"))
		lines = append(lines, renderConfigInput(state.input, false, contentW))
		hint = configInputHint
	}

	if state.message != "" {
		color := colorError
		if state.saving || strings.HasPrefix(state.message, "Đã chọn") {
			color = colorAccent
		}
		lines = append(lines, "")
		lines = appendWrappedConfigText(lines, state.message, contentW, lipgloss.NewStyle().Foreground(color))
	}
	return renderPaddedModalFrame(boxW, len(lines)+2, title, hint, lines)
}

const configInputHint = "Nhập · Enter Xác nhận · Ctrl+U Xóa · Esc Hủy"

func configHeading(text string) string {
	return lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(text)
}

// renderFieldList  hub （Provider hub Mô hình）：
//
//	label、Hiện tại；（value ） label。
func renderFieldList(fields []hubField, cursor, contentW int) []string {
	lines := make([]string, 0, len(fields))
	dirty := state.isDirty()
	for i, f := range fields {
		marker := "  "
		labelStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
		primarySave := f.id == "save" && dirty
		if primarySave {
			labelStyle = labelStyle.Foreground(colorSuccess)
		}
		if i == state.cursor {
			selectedColor := colorAccent
			if primarySave {
				selectedColor = colorSuccess
			}
			marker = lipgloss.NewStyle().Foreground(selectedColor).Bold(true).Render("› ")
			labelStyle = labelStyle.Foreground(selectedColor).Bold(true)
		}
		pad := max(1, 10-lipgloss.Width(f.label))
		if state.editingField == f.id {
			state.input.Width = max(8, contentW-2-lipgloss.Width(f.label)-pad)
			line := marker + labelStyle.Render(f.label) + strings.Repeat(" ", pad) + state.input.View()
			lines = append(lines, truncateStyledWidth(line, contentW))
			continue
		}
		var line string
		if f.value == "" {
			line = marker + labelStyle.Render(f.label)
		} else {
			line = marker + labelStyle.Render(f.label) + strings.Repeat(" ", pad) +
				lipgloss.NewStyle().Foreground(colorDim).Render(f.value)
		}
		lines = append(lines, truncateStyledWidth(line, contentW))
	}
	return lines
}

func labelsForProviderChoices(choices []configProviderChoice) []string {
	out := make([]string, 0, len(choices))
	for _, choice := range choices {
		out = append(out, choice.label)
	}
	return out
}

func renderNotifyEventChoices(state *modelConfigState, width int) []string {
	labels := []string{"[+] Chọn tất cả", "[-] Bỏ chọn tất cả"}
	for _, event := range notify.Kinds() {
		mark := "[ ]"
		if state.notifyEventSelected(event) {
			mark = "[x]"
		}
		labels = append(labels, mark+" "+event)
	}
	return renderConfigChoices(labels, state.cursor, width, 12)
}

func renderConfigChoices(labels []string, cursor, width, limit int) []string {
	if len(labels) == 0 {
		return []string{lipgloss.NewStyle().Foreground(colorDim).Render("Không có tùy chọn khả dụng")}
	}
	start, end := configWindow(len(labels), cursor, limit)
	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(bodyTextColor)
		if i == cursor {
			prefix = "› "
			style = style.Foreground(colorAccent).Bold(true)
		}
		lines = append(lines, prefix+style.Render(truncateWidth(labels[i], max(8, width-2))))
	}
	return lines
}

func configWindow(total, cursor, limit int) (int, int) {
	if total <= limit {
		return 0, total
	}
	start := max(0, cursor-limit/2)
	end := min(total, start+limit)
	if end-start < limit {
		start = max(0, end-limit)
	}
	return start, end
}

// renderConfigInput Đầu vào（ /model ，
// ）。
func renderConfigInput(value string, secret bool, width int) string {
	display := value
	if secret {
		display = strings.Repeat("•", utf8.RuneCountInString(value))
	}
	display += "▌"
	shown := truncateWidth(display, max(8, width-4))
	if w := lipgloss.Width(shown); w < 24 { // rộng，Đầu vào
		shown += strings.Repeat(" ", 24-w)
	}
	field := lipgloss.NewStyle().Foreground(bodyTextColor).Underline(true).Render(shown)
	return lipgloss.NewStyle().Foreground(colorAccent).Render("› ") + field
}
