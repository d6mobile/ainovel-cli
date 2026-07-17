package bootstrap

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/models"
	"github.com/voocel/ainovel-cli/internal/notify"
	"github.com/voocel/ainovel-cli/internal/utils"
)

// DefaultContextWindow Mô hình registry 。
const DefaultContextWindow = 200000

// CompactRatio Ngữ cảnh：tokens >= window * CompactRatio 。
// 0.85 ，" prompt + " 15% ，
// Mô hình 85% ， 1M （）。
//
// ；Mô hình context_window。
const CompactRatio = 0.85

// MinCompactReserve  ReserveTokens 。Mô hình（ 32k  qwen3:8b）
//
//	0.15  reserve  4800， commit_chapter  5-8k，
//	8-15k——""。8000 。
const MinCompactReserve = 8000

// CompactReserveTokens  CompactRatio  ReserveTokens  MinCompactReserve floor：
//
//		threshold = window - reserve = window * CompactRatio
//		reserve   = max(MinCompactReserve, window * (1 - CompactRatio))
//
//	 agentcore.context.Engine  EngineConfig.ReserveTokens 。
func CompactReserveTokens(window int) int {
	if window <= 0 {
		return 0
	}
	reserve := window - int(float64(window)*CompactRatio)
	if reserve < MinCompactReserve {
		return MinCompactReserve
	}
	return reserve
}

// ProviderConfig  LLM 。
type ProviderConfig struct {
	Type    string        `json:"type,omitempty"`     // Loại giao thức API（openai/anthropic/gemini），Tùy chỉnh
	API     string        `json:"api,omitempty"`      // OpenAI Giao thức endpoint：chat（Mặc định）/ responses
	APIKey  string        `json:"api_key,omitempty"`  // API Key
	BaseURL string        `json:"base_url,omitempty"` // API Base URL
	Models  []ModelConfig `json:"models,omitempty"`   // Mô hình， TUI
	// ExtraBody  provider （ temperature/top_p/min_p/
	// presence_penalty， nvidia  think  chat_template_kwargs）。
	// Tương thích OpenAI（ extra_body ）；。
	ExtraBody map[string]any `json:"extra_body,omitempty"`
	// Extra  provider （litellm.ProviderConfig.Extra）， HTTP
	// headers、user_agent、anthropic_beta /。
	Extra map[string]any `json:"extra,omitempty"`
	// StreamIdleTimeout Rảnh： chunk
	// （Go duration ， "900s" / "15m"）。Mặc định 5m——；
	// LocalAI/ollama  5 ， provider rộng，
	// （#79）。
	StreamIdleTimeout string `json:"stream_idle_timeout,omitempty"`
}

// ModelConfig  provider Mô hìnhCửa sổ ngữ cảnh。
// ， JSON （"model-name"），；
// 。
type ModelConfig struct {
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window,omitempty"`
}

func (m *ModelConfig) UnmarshalJSON(data []byte) error {
	var legacy string
	if err := json.Unmarshal(data, &legacy); err == nil {
		m.Name = legacy
		m.ContextWindow = 0
		return nil
	}
	type modelConfigAlias ModelConfig
	var decoded modelConfigAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("model config must be a string or object: %w", err)
	}
	*m = ModelConfig(decoded)
	return nil
}

// ModelConfig Mô hình。
func (pc ProviderConfig) ModelConfig(name string) (ModelConfig, bool) {
	name = strings.TrimSpace(name)
	for _, model := range pc.Models {
		if strings.TrimSpace(model.Name) == name {
			return model, true
		}
	}
	return ModelConfig{}, false
}

// defaultStreamIdleTimeout：Đầu ra +  ctx ，reasoning-aware provider
// （mimo / deepseek-r1 ）Giai đoạn server  reasoning delta，
// SSE 。litellm Mặc định watchdog  2 ， 8000 Viết
// ；5 （ tasks/todo.md plan→draft ）。
const defaultStreamIdleTimeout = 5 * time.Minute

// StreamIdleTimeoutValue  provider Rảnh；Mặc định。
func (pc ProviderConfig) StreamIdleTimeoutValue() (time.Duration, error) {
	s := strings.TrimSpace(pc.StreamIdleTimeout)
	if s == "" {
		return defaultStreamIdleTimeout, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (use Go duration like \"900s\" / \"15m\")", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("must be positive, got %q", s)
	}
	return d, nil
}

// RequiresAPIKey  provider  api_key。
// ：
// 1. ollama / bedrock  key；
// 2.  Type Tùy chỉnh， key；
// 3.  provider Mặc định key，。
func (pc ProviderConfig) RequiresAPIKey(name string) bool {
	switch name {
	case "ollama", "bedrock":
		return false
	}
	return pc.Type == ""
}

// ProviderType  Loại giao thức API。
//
//	Type； provider  litellm Trung bình。
func (pc ProviderConfig) ProviderType(name string) (string, error) {
	if pc.Type != "" {
		return pc.Type, nil
	}
	if llm.IsProviderRegistered(name) {
		return name, nil
	}
	return "", fmt.Errorf("provider %q thiếu type và không có trong danh sách provider đã biết của litellm: %w", name, errs.ErrConfig)
}

// ModelRef  provider/model 。
type ModelRef struct {
	Provider string `json:"provider"` // provider （Providers map Trung bình key）
	Model    string `json:"model"`    // Mô hình（，）
}

// RoleConfig Vai tròMô hình。
type RoleConfig struct {
	Provider  string     `json:"provider"`            //  provider （Providers map Trung bình key）
	Model     string     `json:"model"`               // Mô hình（，）
	Fallbacks []ModelRef `json:"fallbacks,omitempty"` //  provider/model
	// ReasoningEffort Vai tròCường độ suy luận（off/low/medium/high/xhigh/max），=Mặc định。
	//  agents.ParseThinkingLevel ，。
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// knownRoles Vai trò。Arbiter Hiện tạiVai trò，
// Mặc địnhMô hình（host.arbiterModel  models.Default）。
// import_* Mô hình（docs/import-pipeline.md §13.1）：
//
//	architect，。
var knownRoles = map[string]bool{
	"architect":         true,
	"writer":            true,
	"editor":            true,
	"import_segment":    true,
	"import_analyze":    true,
	"import_synthesize": true,
}

// Config 。
type Config struct {
	// （ JSON）
	OutputDir string `json:"-"` // Đầu ra

	// Mặc định LLM
	Provider  string `json:"provider"` // Mặc định provider（Providers map Trung bình key）
	ModelName string `json:"model"`    // Mặc địnhMô hình
	// ReasoningEffort Mặc địnhCường độ suy luận（off/low/medium/high/xhigh/max），=（Mô hình/provider Mặc định）。
	// Vai trò reasoning_effort 。
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// Provider
	Providers map[string]ProviderConfig `json:"providers,omitempty"`

	// Vai tròMô hình
	Roles map[string]RoleConfig `json:"roles,omitempty"`

	//
	Style string `json:"style,omitempty"`

	// ContextWindow Cửa sổ ngữ cảnh，Mô hình context_window
	// 。， LLM API 。
	ContextWindow int `json:"context_window,omitempty"`

	// Budget Ngân sách；book_usd > 0 。
	Budget BudgetConfig `json:"budget,omitzero"`

	// Notify ；（system ）。
	Notify NotifyConfig `json:"notify,omitzero"`
}

// BudgetConfig 。
//
//	Abort——Host ，Mô hình（ §10 ）。
type BudgetConfig struct {
	BookUSD   float64 `json:"book_usd,omitempty"`   // ；0/ =
	WarnRatio float64 `json:"warn_ratio,omitempty"` // ，Mặc định 0.8
	HardStop  bool    `json:"hard_stop,omitempty"`  // true=；Mặc địnhHiện tại
}

// Enabled Ngân sách。
func (b BudgetConfig) Enabled() bool { return b.BookUSD > 0 }

// NotifyConfig 。
type NotifyConfig struct {
	Enabled *bool    `json:"enabled,omitempty"` //  true（system ）
	Command string   `json:"command,omitempty"` // ， system （）
	Events  []string `json:"events,omitempty"`  // ， notify.Kinds ；
}

// IsEnabled （ true）。
func (n NotifyConfig) IsEnabled() bool { return n.Enabled == nil || *n.Enabled }

// ValidateBase 。
func (c *Config) ValidateBase() error {
	if err := validateConfigText("provider", c.Provider); err != nil {
		return err
	}
	if err := validateConfigText("model", c.ModelName); err != nil {
		return err
	}

	if c.Provider == "" {
		return fmt.Errorf("provider is required: %w", errs.ErrConfig)
	}
	if c.ModelName == "" {
		return fmt.Errorf("model is required: %w", errs.ErrConfig)
	}

	// Mặc định provider
	pc, ok := c.Providers[c.Provider]
	if !ok {
		return fmt.Errorf("provider %q chưa cấu hình thông tin xác thực trong providers; nếu ghi đè provider trong ./.ainovel/config.json, bạn phải khai báo cả providers.%s (gồm api_key/base_url), không thể chỉ đổi provider cấp cao nhất: %w", c.Provider, c.Provider, errs.ErrConfig)
	}
	if pc.RequiresAPIKey(c.Provider) && pc.APIKey == "" {
		return fmt.Errorf("provider %q has no api_key configured: %w", c.Provider, errs.ErrConfig)
	}
	if err := validateProviderConfigText(c.Provider, pc); err != nil {
		return err
	}
	if err := c.validateProviderAPI("default", c.Provider, pc); err != nil {
		return err
	}
	for name, provider := range c.Providers {
		if err := validateConfigText("provider name", name); err != nil {
			return err
		}
		if err := validateProviderConfigText(name, provider); err != nil {
			return err
		}
		if err := c.validateProviderAPI(fmt.Sprintf("provider %q", name), name, provider); err != nil {
			return err
		}
	}

	// Vai trò
	for role, rc := range c.Roles {
		if err := validateConfigText("role name", role); err != nil {
			return err
		}
		if err := validateConfigText(fmt.Sprintf("role %q provider", role), rc.Provider); err != nil {
			return err
		}
		if err := validateConfigText(fmt.Sprintf("role %q model", role), rc.Model); err != nil {
			return err
		}
		if !knownRoles[role] {
			return fmt.Errorf("unknown role %q in roles config (valid: architect/writer/editor/import_segment/import_analyze/import_synthesize): %w", role, errs.ErrConfig)
		}
		if rc.Provider == "" || rc.Model == "" {
			return fmt.Errorf("role %q must have both provider and model: %w", role, errs.ErrConfig)
		}
		if err := c.validateModelRef(
			fmt.Sprintf("role %q", role),
			ModelRef{Provider: rc.Provider, Model: rc.Model},
		); err != nil {
			return err
		}
		for i, fallback := range rc.Fallbacks {
			if err := validateConfigText(fmt.Sprintf("role %q fallback[%d] provider", role, i), fallback.Provider); err != nil {
				return err
			}
			if err := validateConfigText(fmt.Sprintf("role %q fallback[%d] model", role, i), fallback.Model); err != nil {
				return err
			}
			if err := c.validateModelRef(
				fmt.Sprintf("role %q fallback[%d]", role, i),
				fallback,
			); err != nil {
				return err
			}
		}
	}

	// Ngân sách
	if c.Budget.BookUSD < 0 {
		return fmt.Errorf("budget.book_usd must be >= 0: %w", errs.ErrConfig)
	}
	if c.Budget.Enabled() && (c.Budget.WarnRatio <= 0 || c.Budget.WarnRatio >= 1) {
		return fmt.Errorf("budget.warn_ratio must be in (0, 1): %w", errs.ErrConfig)
	}

	//
	if err := validateConfigText("notify.command", c.Notify.Command); err != nil {
		return err
	}
	for _, ev := range c.Notify.Events {
		if !notify.IsKnownKind(ev) {
			return fmt.Errorf("unknown notify event %q (valid: %s): %w", ev, strings.Join(notify.Kinds(), "/"), errs.ErrConfig)
		}
	}

	return nil
}

func validateProviderConfigText(name string, pc ProviderConfig) error {
	fields := []struct {
		label string
		value string
	}{
		{label: fmt.Sprintf("provider %q type", name), value: pc.Type},
		{label: fmt.Sprintf("provider %q api", name), value: pc.API},
		{label: fmt.Sprintf("provider %q api_key", name), value: pc.APIKey},
		{label: fmt.Sprintf("provider %q base_url", name), value: pc.BaseURL},
	}
	for _, field := range fields {
		if err := validateConfigText(field.label, field.value); err != nil {
			return err
		}
	}
	seenModels := make(map[string]bool, len(pc.Models))
	for i, model := range pc.Models {
		modelName := strings.TrimSpace(model.Name)
		if err := validateConfigText(fmt.Sprintf("provider %q models[%d].name", name, i), model.Name); err != nil {
			return err
		}
		if modelName == "" {
			return fmt.Errorf("provider %q models[%d].name is required: %w", name, i, errs.ErrConfig)
		}
		if seenModels[modelName] {
			return fmt.Errorf("provider %q has duplicate model %q: %w", name, modelName, errs.ErrConfig)
		}
		seenModels[modelName] = true
		if model.ContextWindow < 0 {
			return fmt.Errorf("provider %q model %q context_window must be >= 0: %w", name, modelName, errs.ErrConfig)
		}
	}
	switch pc.API {
	case "", "chat", "responses":
	default:
		return fmt.Errorf("provider %q api must be chat or responses: %w", name, errs.ErrConfig)
	}
	if _, err := pc.StreamIdleTimeoutValue(); err != nil {
		return fmt.Errorf("provider %q stream_idle_timeout: %w: %w", name, err, errs.ErrConfig)
	}
	return nil
}

func validateConfigText(name, value string) error {
	if utils.ContainsControl(value) {
		return fmt.Errorf("%s contains control character: %w", name, errs.ErrConfig)
	}
	return nil
}

// DefaultProviderConfig Mặc định provider 。
func (c *Config) DefaultProviderConfig() ProviderConfig {
	if c.Providers == nil {
		return ProviderConfig{}
	}
	return c.Providers[c.Provider]
}

// FillDefaults Mặc định。
func (c *Config) FillDefaults() {
	if c.OutputDir == "" {
		c.OutputDir = filepath.Join("output", "novel")
	}
	if c.Providers == nil {
		c.Providers = make(map[string]ProviderConfig)
	}
	if c.Roles == nil {
		c.Roles = make(map[string]RoleConfig)
	}
	if c.Style == "" {
		c.Style = "default"
	}
	if c.Budget.Enabled() && c.Budget.WarnRatio == 0 {
		c.Budget.WarnRatio = 0.8
	}
}

// ContextWindowSource ，/。
type ContextWindowSource string

const (
	CtxWindowModelConfig ContextWindowSource = "model_config" // provider Mô hình
	CtxWindowConfig      ContextWindowSource = "config"       //  context_window
	CtxWindowRegistry    ContextWindowSource = "registry"     // OpenRouter Cơ sởTrung bình
	CtxWindowDefault     ContextWindowSource = "default"      // （Tùy chỉnh/Mô hình）
)

// ResolveContextWindow Ngữ cảnh，：
//  1. providers.<provider>.models[].context_window
//  2. ContextWindow（）
//  3. models.DefaultRegistry Mô hình（OpenRouter Cơ sở + 24h ）
//  4. DefaultContextWindow（Tùy chỉnh / Mô hình）
//
// ：， LLM API 。
func (c Config) ResolveContextWindow(provider, modelName string) (int, ContextWindowSource) {
	if pc, ok := c.Providers[strings.TrimSpace(provider)]; ok {
		if model, found := pc.ModelConfig(modelName); found && model.ContextWindow > 0 {
			return model.ContextWindow, CtxWindowModelConfig
		}
	}
	if c.ContextWindow > 0 {
		return c.ContextWindow, CtxWindowConfig
	}
	if rw := models.DefaultRegistry().ResolveContextWindow(modelName); rw > 0 {
		return rw, CtxWindowRegistry
	}
	return DefaultContextWindow, CtxWindowDefault
}

// ResolveReasoningEffort Vai tròCường độ suy luận（off/low/medium/high/xhigh/max ）。
// ：Vai trò Roles[role].ReasoningEffort → Mặc định ReasoningEffort → ""（，Mô hình/provider Mặc định）。
// role  "default" Mặc định。 agents.ParseThinkingLevel 。
func (c Config) ResolveReasoningEffort(role string) string {
	if role != "" && role != "default" {
		if rc, ok := c.Roles[role]; ok && rc.ReasoningEffort != "" {
			return rc.ReasoningEffort
		}
	}
	return c.ReasoningEffort
}

// LogContextWindowChoice Vai trò。source=default  Warn
// Mô hình registry Trung bình（OpenRouter ），Ngữ cảnh
// ——Mô hình， context_window ，、。
func LogContextWindowChoice(role, model string, window int, source ContextWindowSource) {
	attrs := []any{"module", "context", "role", role, "model", model, "window", window, "source", source}
	switch source {
	case CtxWindowModelConfig:
		slog.Info("Cửa sổ ngữ cảnh (từ cấu hình mô hình provider)", attrs...)
	case CtxWindowDefault:
		slog.Warn("Không nhận diện được mô hình, dùng cửa sổ dự phòng (có thể chỉ định rõ tại providers.<name>.models[].context_window)", attrs...)
	case CtxWindowConfig:
		slog.Info("Cửa sổ ngữ cảnh (từ context_window trong cấu hình)", attrs...)
	default:
		slog.Info("Cửa sổ ngữ cảnh", attrs...)
	}
}

// CandidateModels  provider Mô hình。
//
//	provider  models；Hiện tạiTrung bình provider Mô hình。
func (c Config) CandidateModels(provider string) []string {
	if provider == "" {
		return nil
	}

	seen := make(map[string]bool)
	models := make([]string, 0, 4)
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			return
		}
		seen[model] = true
		models = append(models, model)
	}

	if pc, ok := c.Providers[provider]; ok {
		for _, model := range pc.Models {
			add(model.Name)
		}
	}
	if c.Provider == provider {
		add(c.ModelName)
	}
	for _, rc := range c.Roles {
		if rc.Provider == provider {
			add(rc.Model)
		}
		for _, fallback := range rc.Fallbacks {
			if fallback.Provider == provider {
				add(fallback.Model)
			}
		}
	}
	return models
}

func (c Config) validateModelRef(owner string, ref ModelRef) error {
	if ref.Provider == "" || ref.Model == "" {
		return fmt.Errorf("%s must have both provider and model: %w", owner, errs.ErrConfig)
	}

	pc, ok := c.Providers[ref.Provider]
	if !ok {
		return fmt.Errorf("%s references provider %q which is not configured: %w", owner, ref.Provider, errs.ErrConfig)
	}
	if pc.RequiresAPIKey(ref.Provider) && pc.APIKey == "" {
		return fmt.Errorf("%s references provider %q which has no api_key: %w", owner, ref.Provider, errs.ErrConfig)
	}
	if err := c.validateProviderAPI(owner, ref.Provider, pc); err != nil {
		return err
	}
	return nil
}

func (c Config) validateProviderAPI(owner, providerName string, pc ProviderConfig) error {
	if pc.API == "" {
		return nil
	}
	providerType, err := pc.ProviderType(providerName)
	if err != nil {
		return fmt.Errorf("%s provider %q cấu hình api không thể phân tích loại giao thức: %w", owner, providerName, err)
	}
	if strings.ToLower(strings.TrimSpace(providerType)) != "openai" {
		return fmt.Errorf("%s provider %q api chỉ hỗ trợ provider giao thức OpenAI: %w", owner, providerName, errs.ErrConfig)
	}
	return nil
}
