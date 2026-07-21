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
	configStepGeneral
	configStepGeneralStyle
	configStepGeneralBudget
	configStepGeneralBudgetInput
	configStepGeneralNotify
	configStepGeneralNotifyCommand
	configStepGeneralNotifyEvents
)

const (
	configModelNameField   = "model_name"
	configModelWindowField = "model_window"
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
	addingModel   bool
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
	s.modelOrigins = nil
	s.currentModel = "" // provider mới chưa được chọn làm mặc định
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

func (s *modelConfigState) captureBaseline() {
	s.baseline = &modelConfigBaseline{
		providerType: s.providerType,
		api:          s.api,
		baseURL:      s.baseURL,
		models:       append([]bootstrap.ModelConfig(nil), s.models...),
	}
}

func (s *modelConfigState) isDirty() bool {
	if !s.existing || s.baseline == nil {
		return true
	}
	if s.apiKeyAction != host.APIKeyKeep {
		return true
	}
	baseURL := s.baseURL
	if s.editingField == "baseurl" {
		baseURL = strings.TrimSpace(s.input.Value())
	}
	if s.editingField == "key" && strings.TrimSpace(s.input.Value()) != "" {
		return true
	}
	return s.providerType != s.baseline.providerType ||
		s.api != s.baseline.api ||
		baseURL != s.baseline.baseURL ||
		!slices.Equal(s.models, s.baseline.models)
}

// hubField là một mục có thể điều chỉnh trong hub chi tiết của Provider.
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
	testModel := s.testModelName()
	if testModel == "" {
		testModel = "Vui lòng thêm mô hình trước"
	}
	fields = append(fields, hubField{"test", "Kiểm tra kết nối", testModel})
	fields = append(fields, hubField{"save", "Lưu cấu hình", ""})
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
			return host.MaskAPIKey(s.apiKey)
		}
	}
	if s.apiKeyHint != "" {
		return s.apiKeyHint
	}
	return "Chưa thiết lập"
}

// enterHubField đi vào mục đã chọn; Key và Base URL chỉnh sửa trực tiếp trên cùng dòng trong hub.
func (s *modelConfigState) enterHubField(id string) (save bool, cmd tea.Cmd) {
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
		placeholder := "Nhập API Key"
		if s.hasEffectiveAPIKey() {
			placeholder = "Nhập Key mới, để trống để giữ nguyên"
		}
		return s.startTextInput("", placeholder, true)
	case "baseurl":
		return s.startTextInput(s.baseURL, "Để trống để dùng địa chỉ mặc định", false)
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
				s.message = "Nhà cung cấp này bắt buộc có API Key"
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

// escapeBack quay lại cấp trước của Esc; giá trị trả về thứ hai là false báo hiệu đóng toàn bộ bảng.
// Hệ thống cấp bậc: Danh sách Provider ⊃ Hub chi tiết ⊃ Trình chỉnh sửa trường; Danh sách ⊃ Thêm mới ⊃ Đặt tên tùy chỉnh.
// Tên mô hình và cửa sổ được chỉnh sửa trực tiếp trong danh sách mô hình, không cần thêm cấp chi tiết.
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
	default: // configStepProvider
		return 0, false
	}
}

func (s *modelConfigState) ensureModelOrigins() {
	if len(s.modelOrigins) == len(s.models) {
		return
	}
	s.modelOrigins = make([]string, len(s.models))
	for i, model := range s.models {
		s.modelOrigins[i] = model.Name
	}
}

func (s *modelConfigState) beginModelEdit(idx, column int) tea.Cmd {
	if idx < 0 || idx >= len(s.models) {
		return nil
	}
	s.editModelIdx = idx
	s.modelColumn = column
	s.message = ""
	if column == 0 {
		s.editingField = configModelNameField
		return s.startTextInput(s.models[idx].Name, "ID Mô hình", false)
	}
	s.editingField = configModelWindowField
	value := ""
	if window := s.models[idx].ContextWindow; window > 0 {
		value = strconv.Itoa(window)
	}
	return s.startTextInput(value, "auto / 128K / 1M", false)
}

// finishModelEdit chỉ áp dụng cho ô hiện tại. Thêm mô hình sau khi nhập tên sẽ chuyển sang cột cửa sổ,
// đổi tên mô hình hiện có sẽ giữ nguyên danh tính gốc, Host sẽ di chuyển tất cả tham chiếu khi lưu.
func (s *modelConfigState) finishModelEdit() (tea.Cmd, bool) {
	idx := s.editModelIdx
	if idx < 0 || idx >= len(s.models) {
		return nil, false
	}
	switch s.editingField {
	case configModelNameField:
		name := strings.TrimSpace(s.input.Value())
		if name == "" {
			s.message = "Tên mô hình không được để trống"
			return nil, false
		}
		for i, model := range s.models {
			if i != idx && model.Name == name {
				s.message = "Mô hình đã tồn tại"
				return nil, false
			}
		}
		s.models[idx].Name = name
		if s.addingModel {
			s.modelColumn = 1
			s.editingField = configModelWindowField
			s.message = ""
			return s.startTextInput("", "auto / 128K / 1M", false), true
		}
		s.input.Blur()
		s.editingField = ""
		s.message = ""
		origin := s.modelOrigins[idx]
		if origin != "" && origin != name {
			if refs := s.snapshot.ReferencesFor(s.provider, origin); len(refs) > 0 {
				s.message = "Lưu sẽ đồng bộ cập nhật tham chiếu: " + strings.Join(refs, "、")
			}
		}
		return nil, true
	case configModelWindowField:
		window, err := parseContextWindowInput(s.input.Value())
		if err != nil {
			s.message = err.Error()
			return nil, false
		}
		s.models[idx].ContextWindow = window
		s.input.Blur()
		s.editingField = ""
		s.addingModel = false
		s.message = ""
		return nil, true
	}
	return nil, false
}

func (s *modelConfigState) cancelModelEdit() {
	// Hàng mới chưa gửi tên không có dữ liệu hợp lệ, Esc sẽ trực tiếp xóa hàng tạm này; tên đã được gửi,
	// khi đang chỉnh sửa cửa sổ thì giữ nguyên cửa sổ "Tự động", người dùng có thể tiếp tục sửa sau.
	if s.addingModel && s.editingField == configModelNameField &&
		s.editModelIdx >= 0 && s.editModelIdx < len(s.models) {
		idx := s.editModelIdx
		s.models = append(s.models[:idx], s.models[idx+1:]...)
		s.modelOrigins = append(s.modelOrigins[:idx], s.modelOrigins[idx+1:]...)
		s.cursor = len(s.models)
	}
	s.input.Blur()
	s.editingField = ""
	s.editModelIdx = -1
	s.addingModel = false
	s.message = ""
}

// deleteModel  idx Mô hình；Mặc địnhVai trò，。
func (s *modelConfigState) deleteModel(idx int) bool {
	if idx < 0 || idx >= len(s.models) {
		return false
	}
	s.ensureModelOrigins()
	model := s.models[idx]
	identity := s.modelOrigins[idx]
	if identity == "" {
		identity = model.Name
	}
	if identity == s.currentModel {
		s.message = "Mô hình này đang được sử dụng, vui lòng dùng /model để chuyển trước khi xóa"
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

type modelConfigConnectionMsg struct {
	model string
	err   error
}

func saveModelConfiguration(rt *host.Host, draft host.ModelConfigurationDraft) tea.Cmd {
	return func() tea.Msg { return modelConfigSavedMsg{err: rt.ConfigureModels(draft)} }
}

func saveGeneralConfiguration(rt *host.Host, draft host.GeneralSettingsDraft) tea.Cmd {
	return func() tea.Msg { return generalConfigSavedMsg{err: rt.ConfigureGeneralSettings(draft)} }
}

func testModelConnection(ctx context.Context, rt *host.Host, draft host.ModelConfigurationDraft, model string) tea.Cmd {
	return func() tea.Msg {
		return modelConfigConnectionMsg{model: model, err: rt.TestModelConnection(ctx, draft, model)}
	}
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
			state.message = "Đang hủy kiểm tra kết nối..."
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
				value := ""
				if state.generalBudget.BookUSD > 0 {
					value = strconv.FormatFloat(state.generalBudget.BookUSD, 'f', -1, 64)
				}
				state.step = configStepGeneralBudgetInput
				return m, state.startTextInput(value, "0 = tắt", false)
			case "warn_ratio":
				state.editSetting = field.id
				value := "0.8"
				if state.generalBudget.WarnRatio > 0 {
					value = strconv.FormatFloat(state.generalBudget.WarnRatio, 'f', -1, 64)
				}
				state.step = configStepGeneralBudgetInput
				return m, state.startTextInput(value, "0.8", false)
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
				state.step = configStepGeneralNotifyCommand
				return m, state.startTextInput(state.generalNotify.Command, "Lệnh shell gửi thông báo", false)
			case "events":
				state.step = configStepGeneralNotifyEvents
				state.cursor = 0
			}
		}
	case configStepGeneralNotifyCommand:
		if handleConfigInput(&state.input, msg) && msg.Type == tea.KeyEnter {
			state.generalNotify.Command = strings.TrimSpace(state.input.Value())
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
				state.message = "Nhà cung cấp này bắt buộc có API Key, không thể xóa"
				break
			}
			state.apiKeyAction = host.APIKeyClear
			state.apiKey = ""
			state.message = "Đã đánh dấu xóa API Key, sẽ áp dụng sau khi lưu cấu hình"
			break
		}
		if msg.Type == tea.KeyEnter && state.cursor >= 0 && state.cursor < len(fields) {
			fieldID := fields[state.cursor].id
			if fieldID == "test" {
				model := state.testModelName()
				if model == "" {
					state.message = "Vui lòng thêm ít nhất một mô hình trước khi kiểm tra kết nối"
					break
				}
				if !state.apiKeyOptional && !state.hasEffectiveAPIKey() {
					state.message = "Nhà cung cấp này bắt buộc có API Key"
					break
				}
				state.testing = true
				state.message = fmt.Sprintf("Đang kiểm tra kết nối: %s/%s...", state.provider, model)
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
					state.message = "Nhà cung cấp này bắt buộc có API Key"
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
	case configStepModels:
		state.ensureModelOrigins()
		if state.editingField != "" {
			if msg.Type == tea.KeyEnter {
				cmd, _ := state.finishModelEdit()
				return m, cmd
			}
			var cmd tea.Cmd
			state.input, cmd = state.input.Update(msg)
			return m, cmd
		}
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
				state.models = append(state.models, bootstrap.ModelConfig{})
				state.modelOrigins = append(state.modelOrigins, "")
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

func handleConfigInput(input *textinput.Model, msg tea.KeyMsg) bool {
	if input == nil {
		return false
	}
	if msg.Type == tea.KeyEnter {
		return true
	}
	var cmd tea.Cmd
	*input, cmd = input.Update(msg)
	_ = cmd
	return false
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
	value := strings.TrimSpace(s.input.Value())
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
		lines = append(lines, renderConfigTextInput(&state.input, contentW))
		hint = configInputHint
	case configStepGeneralNotify:
		lines = append(lines, configHeading("Thông báo"))
		lines = append(lines, renderFieldList(state.generalNotifyFields(), state.cursor, contentW)...)
		hint = "↑↓ Chọn · Enter Chỉnh/Bật tắt · Esc Quay lại"
	case configStepGeneralNotifyCommand:
		lines = append(lines, configHeading("Lệnh thông báo"))
		lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("Để trống = dùng trình thông báo mặc định của hệ điều hành"))
		lines = append(lines, renderConfigTextInput(&state.input, contentW))
		hint = configInputHint
	case configStepGeneralNotifyEvents:
		lines = append(lines, configHeading("Sự kiện thông báo"))
		lines = append(lines, renderNotifyEventChoices(state, contentW)...)
		hint = "↑↓ Chọn · Enter Bật/tắt · Esc Quay lại"
	case configStepCustomName:
		lines = append(lines, configHeading("Tên nhà cung cấp tùy chỉnh"), renderConfigTextInput(&state.input, contentW))
		hint = configInputHint
	case configStepHub:
		heading := state.provider
		if !state.existing {
			heading += " (mới)"
		}
		lines = append(lines, configHeading(heading))
		lines = append(lines, renderProviderHubFields(state, contentW)...)
		if state.snapshot.ConfigPath != "" {
			advanced := "Cấu hình nâng cao (extra / extra_body / stream_idle_timeout): " + state.snapshot.ConfigPath
			lines = append(lines, "")
			lines = appendWrappedConfigText(lines, advanced, contentW, lipgloss.NewStyle().Foreground(colorDim))
		}
		if state.editingField != "" {
			hint = "Nhập · Enter Xác nhận · Esc Hủy"
		} else {
			hint = "↑↓ Chọn · Enter Chỉnh sửa/Mở · Esc Quay lại"
			fields := state.hubFields()
			if state.apiKeyOptional && state.cursor >= 0 && state.cursor < len(fields) && fields[state.cursor].id == "key" {
				hint += " · Delete Xóa"
			}
			if state.cursor >= 0 && state.cursor < len(fields) && fields[state.cursor].id == "test" {
				lines = append(lines, lipgloss.NewStyle().Foreground(colorDim).Render("Kiểm tra sẽ gửi request nhỏ, có thể tốn một ít API"))
			}
		}
	case configStepProtocol:
		lines = append(lines, configHeading("Loại giao thức API"))
		lines = append(lines, renderConfigChoices(configProtocols, state.cursor, contentW, 8)...)
	case configStepAPI:
		lines = append(lines, configHeading("OpenAI Endpoint"))
		lines = append(lines, renderConfigChoices([]string{"chat · /v1/chat/completions", "responses · /v1/responses"}, state.cursor, contentW, 8)...)
	case configStepModels:
		lines = append(lines, configHeading("Quản lý danh sách mô hình"))
		lines = append(lines, renderModelConfigRows(state, contentW)...)
		if state.editingField != "" {
			hint = "Nhập · Enter Xác nhận · Esc Hủy"
		} else {
			hint = "↑↓ Hàng · ←→ Cột · Enter Chỉnh sửa · Delete Xóa · Esc Quay lại"
		}
	}

	if state.message != "" {
		color := colorError
		if strings.HasPrefix(state.message, "Kiểm tra kết nối thành công") {
			color = colorSuccess
		} else if state.saving || state.testing || strings.HasPrefix(state.message, "Đã chọn") ||
			strings.HasPrefix(state.message, "API Key đã") || strings.HasPrefix(state.message, "Đã hủy kiểm tra kết nối") {
			color = colorAccent
		} else if strings.HasPrefix(state.message, "Lưu sẽ đồng bộ cập nhật tham chiếu") {
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

// renderFieldList hiển thị danh sách trường trong hub (ví dụ: hub cài đặt chung):
// Bên trái hiển thị nhãn, bên phải hiển thị giá trị hiện tại; trường chọn nhấn mạnh nhãn.
func renderFieldList(fields []hubField, cursor, contentW int) []string {
	lines := make([]string, 0, len(fields))
	for i, f := range fields {
		marker := "  "
		labelStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
		primarySave := f.id == "save"
		if primarySave {
			labelStyle = labelStyle.Foreground(colorSuccess)
		}
		if i == cursor {
			selectedColor := colorAccent
			if primarySave {
				selectedColor = colorSuccess
			}
			marker = lipgloss.NewStyle().Foreground(selectedColor).Bold(true).Render("› ")
			labelStyle = labelStyle.Foreground(selectedColor).Bold(true)
		}
		pad := max(1, 10-lipgloss.Width(f.label))
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

func appendWrappedConfigText(lines []string, text string, width int, style lipgloss.Style) []string {
	for _, line := range strings.Split(wrapText(text, width), "\n") {
		lines = append(lines, style.Render(line))
	}
	return lines
}

func renderModelConfigRows(state *modelConfigState, contentW int) []string {
	state.ensureModelOrigins()
	contextW := 14
	refsW := 18
	nameW := contentW - 2 - contextW - refsW - 4
	if nameW < 20 {
		refsW = 0
		nameW = max(12, contentW-2-contextW-2)
	}

	header := "  " + padConfigCell("ID Mô hình", nameW) + "  " + padConfigCell("Cửa sổ ngữ cảnh", contextW)
	if refsW > 0 {
		header += "  " + padConfigCell("Tham chiếu", refsW)
	}
	lines := []string{lipgloss.NewStyle().Foreground(colorDim).Render(header)}

	total := len(state.models) + 1
	start, end := configWindow(total, state.cursor, 10)
	for i := start; i < end; i++ {
		selected := i == state.cursor
		marker := "  "
		if selected {
			marker = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("› ")
		}
		if i == len(state.models) {
			style := lipgloss.NewStyle().Foreground(bodyTextColor)
			if selected {
				style = style.Foreground(colorAccent).Bold(true)
			}
			lines = append(lines, marker+style.Render("+ Thêm mô hình..."))
			continue
		}

		model := state.models[i]
		name := padConfigCell(model.Name, nameW)
		window := "Tự động"
		if model.ContextWindow > 0 {
			window = formatContextWindow(model.ContextWindow)
		}
		window = padConfigCell(window, contextW)

		nameCell := lipgloss.NewStyle().Foreground(bodyTextColor).Render(name)
		windowCell := lipgloss.NewStyle().Foreground(colorDim).Render(window)
		if selected && state.modelColumn == 0 {
			nameCell = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(name)
		}
		if selected && state.modelColumn == 1 {
			windowCell = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(window)
		}
		if state.editModelIdx == i && state.editingField == configModelNameField {
			nameCell = renderConfigInputCell(&state.input, nameW)
		}
		if state.editModelIdx == i && state.editingField == configModelWindowField {
			windowCell = renderConfigInputCell(&state.input, contextW)
		}

		line := marker + nameCell + "  " + windowCell
		if refsW > 0 {
			identity := state.modelOrigins[i]
			if identity == "" {
				identity = model.Name
			}
			refs := strings.Join(state.snapshot.ReferencesFor(state.provider, identity), "、")
			line += "  " + lipgloss.NewStyle().Foreground(colorDim).Render(padConfigCell(refs, refsW))
		}
		lines = append(lines, truncateStyledWidth(line, contentW))
	}
	return lines
}

func padConfigCell(value string, width int) string {
	value = truncateWidth(value, width)
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

// renderConfigInputCell hiển thị textinput trong ô bảng có chiều rộng cố định.
// Width của textinput không bao gồm cột con trỏ cuối cùng, nên dự trữ một cột; xử lý trực tiếp đầu ra ANSI,
// không lồng lipgloss.Width nữa để tránh hiểu nhầm kiểu lồng nhau là văn bản có thể ngắt dòng.
func renderConfigInputCell(input *textinput.Model, width int) string {
	input.Width = max(1, width-1)
	view := truncateStyledWidth(input.View(), width)
	return view + strings.Repeat(" ", max(0, width-lipgloss.Width(view)))
}

// renderProviderHubFields hiển thị ô nhập Key/Base URL ở vị trí ban đầu trong chi tiết Provider,
// textinput tích hợp sẵn di chuyển con trỏ và khung nhìn ngang, URL dài sẽ không bị cắt xén phần đuôi đang chỉnh sửa.
func renderProviderHubFields(state *modelConfigState, contentW int) []string {
	fields := state.hubFields()
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

func renderConfigTextInput(input *textinput.Model, width int) string {
	input.Width = max(8, width-4)
	return lipgloss.NewStyle().Foreground(colorAccent).Render("› ") + input.View()
}
