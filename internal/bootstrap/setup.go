package bootstrap

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/utils"
)

// exampleConfig  ~/.ainovel/config.example.jsonc 。
//
//	config.example.jsonc ，。
//
//go:embed config.example.jsonc
var exampleConfig string

// NeedsSetup （）。
func NeedsSetup() bool {
	if p := DefaultConfigPath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			return false
		}
	}
	if _, err := os.Stat(projectConfigPath()); err == nil {
		return false
	}
	return true
}

type setupProvider struct {
	name           string
	label          string
	baseURL        string //  base_url
	needType       bool   // Tùy chỉnh type  base_url
	apiKeyOptional bool   // true  API Key
}

// ProviderPreset  /config  provider 。
type ProviderPreset struct {
	Name           string
	Label          string
	BaseURL        string
	NeedType       bool
	APIKeyOptional bool
}

var setupProviders = []setupProvider{
	{name: "openrouter", label: "OpenRouter", baseURL: "https://openrouter.ai/api/v1"},
	{name: "anthropic", label: "Anthropic"},
	{name: "gemini", label: "Gemini"},
	{name: "openai", label: "OpenAI"},
	{name: "deepseek", label: "DeepSeek"},
	{name: "qwen", label: "Qwen"},
	{name: "glm", label: "GLM"},
	{name: "grok", label: "Grok"},
	{name: "ollama", label: "Ollama", baseURL: "http://localhost:11434/v1", apiKeyOptional: true},
	{name: "bedrock", label: "Bedrock", apiKeyOptional: true},
	{name: "custom", label: "Custom Proxy", needType: true, apiKeyOptional: true},
}

// ProviderPresets x。
func ProviderPresets() []ProviderPreset {
	out := make([]ProviderPreset, 0, len(setupProviders))
	for _, preset := range setupProviders {
		out = append(out, ProviderPreset{
			Name: preset.name, Label: preset.label, BaseURL: preset.baseURL,
			NeedType: preset.needType, APIKeyOptional: preset.apiKeyOptional,
		})
	}
	return out
}

// RunSetup ，。
func RunSetup() (Config, error) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99")).
		Render("Không tìm thấy tệp cấu hình, bắt đầu thiết lập ban đầu..."))
	fmt.Fprintf(os.Stderr, "  Đường dẫn tệp cấu hình: %s\n", lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(DefaultConfigPath()))
	fmt.Fprintf(os.Stderr, "  Sau khi hoàn tất, bạn có thể chỉnh tệp này bất cứ lúc nào để điều chỉnh thiết lập nâng cao.\n")
	fmt.Fprintln(os.Stderr)

	// Step 1:  Provider
	sp, err := runProviderSelect()
	if err != nil {
		return Config{}, err
	}

	providerName := sp.name
	var pc ProviderConfig
	printStepDone("Provider", sp.label)

	// Tùy chỉnh： Loại giao thức API
	if sp.needType {
		providerName, err = runTextInput("Tên Provider", "my-proxy")
		if err != nil {
			return Config{}, err
		}
		providerType, err := runTypeSelect()
		if err != nil {
			return Config{}, err
		}
		pc.Type = providerType
	}

	// Step 2: Đầu vào API Key
	var apiKey string
	if sp.apiKeyOptional {
		apiKey, err = runOptionalTextInput("[2/4] API Key (có thể để trống)", "Để trống nghĩa là không dùng API Key")
	} else {
		apiKey, err = runTextInput("[2/4] API Key", "sk-xxx")
	}
	if err != nil {
		return Config{}, err
	}
	pc.APIKey = apiKey
	if apiKey == "" {
		printStepDone("API Key", "Chưa thiết lập")
	} else {
		printStepDone("API Key", maskKey(apiKey))
	}

	// Step 3: Base URL（Địa chỉ mặc định）
	baseDefault := sp.baseURL
	baseHint := "Để trống để dùng địa chỉ chính thức"
	if baseDefault != "" {
		baseHint = baseDefault
	}
	baseURL, err := runTextInputWithDefault("[3/4] Base URL (nhấn Enter để dùng mặc định; người dùng proxy hãy nhập địa chỉ proxy)", baseHint, baseDefault)
	if err != nil {
		return Config{}, err
	}
	pc.BaseURL = baseURL
	if baseURL != "" {
		printStepDone("Base URL", baseURL)
	} else {
		printStepDone("Base URL", "Mặc định")
	}

	// Step 4: Mô hình（）
	modelName, err := runTextInput("[4/4] Tên mô hình", "Ví dụ: gpt-4o / claude-sonnet-4 / gemini-2.5-pro")
	if err != nil {
		return Config{}, err
	}
	printStepDone("Model", modelName)
	pc.Models = []ModelConfig{{Name: modelName}}

	cfg := Config{
		Provider:  providerName,
		ModelName: modelName,
		Providers: map[string]ProviderConfig{providerName: pc},
		Roles:     map[string]RoleConfig{},
		Style:     "default",
	}

	//
	path := DefaultConfigPath()
	if err := SaveConfig(path, cfg); err != nil {
		return cfg, fmt.Errorf("save config: %w", err)
	}

	//
	saveExampleConfig()

	// Luồng（runWithConfig），
	rulesDir := rules.DefaultHomeRulesDir()

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "%s Cấu hình đã lưu vào %s\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("✓"), path)
	fmt.Fprintf(os.Stderr, "  Mặc địnhMô hình：%s\n", modelName)
	fmt.Fprintln(os.Stderr, "  Để cấu hình mô hình khác nhau theo vai trò, hãy chỉnh tệp cấu hình.")
	if rulesDir != "" {
		fmt.Fprintf(os.Stderr, "  Có thể đặt tùy chọn viết toàn cục trong các tệp .md dưới %s (xem README.txt trong đó)\n", rulesDir)
	}
	fmt.Fprintln(os.Stderr)

	return cfg, nil
}

func saveExampleConfig() {
	dir, err := configDir()
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "config.example.jsonc"), []byte(exampleConfig), 0o644)
}

// printStepDone Hoàn tất。
func printStepDone(label, value string) {
	fmt.Fprintf(os.Stderr, "  %s %s: %s\n",
		lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("✓"),
		label,
		lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(value))
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// ---------- TUI  ----------

func runProviderSelect() (setupProvider, error) {
	m := setupSelectModel{
		title: "[1/4] Chọn Provider",
		items: setupProviders,
	}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return setupProvider{}, err
	}
	result := final.(setupSelectModel)
	if result.cancelled {
		return setupProvider{}, fmt.Errorf("setup cancelled")
	}
	return result.items[result.cursor], nil
}

var apiTypeOptions = []setupProvider{
	{name: "openai", label: "Tương thích OpenAI"},
	{name: "anthropic", label: "Tương thích Anthropic"},
	{name: "gemini", label: "Tương thích Gemini"},
}

func runTypeSelect() (string, error) {
	m := setupSelectModel{
		title: "Loại giao thức API",
		items: apiTypeOptions,
	}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	result := final.(setupSelectModel)
	if result.cancelled {
		return "", fmt.Errorf("setup cancelled")
	}
	return result.items[result.cursor].name, nil
}

func runTextInput(label, placeholder string) (string, error) {
	return runTextInputWithDefault(label, placeholder, "")
}

func runOptionalTextInput(label, placeholder string) (string, error) {
	m := setupInputModel{label: label, placeholder: placeholder, allowEmpty: true}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	result := final.(setupInputModel)
	if result.cancelled {
		return "", fmt.Errorf("setup cancelled")
	}
	return utils.CleanInputLine(result.value), nil
}

func runTextInputWithDefault(label, placeholder, defaultValue string) (string, error) {
	m := setupInputModel{label: label, placeholder: placeholder, defaultValue: defaultValue}
	p := tea.NewProgram(m, tea.WithOutput(os.Stderr))
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	result := final.(setupInputModel)
	if result.cancelled {
		return "", fmt.Errorf("setup cancelled")
	}
	if result.value == "" && result.defaultValue != "" {
		return result.defaultValue, nil
	}
	return utils.CleanInputLine(result.value), nil
}

// ----------  ----------

var (
	setupCursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	setupDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	setupHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("99"))
	setupInputStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
)

type setupSelectModel struct {
	title     string
	items     []setupProvider
	cursor    int
	cancelled bool
}

func (m setupSelectModel) Init() tea.Cmd { return nil }

func (m setupSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter":
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m setupSelectModel) View() string {
	var b strings.Builder
	b.WriteString(setupHeaderStyle.Render(m.title))
	b.WriteString("\n\n")
	for i, item := range m.items {
		cursor := "  "
		label := item.label
		if i == m.cursor {
			cursor = setupCursorStyle.Render("❯ ")
			label = setupCursorStyle.Render(label)
		}
		b.WriteString(cursor + label + "\n")
	}
	b.WriteString(setupDimStyle.Render("\n  ↑↓ Chọn  Enter Xác nhận  Esc Hủy"))
	return b.String()
}

// ---------- Đầu vào ----------

type setupInputModel struct {
	label        string
	placeholder  string
	defaultValue string // Mặc định
	allowEmpty   bool   // Đầu vào
	value        string
	cancelled    bool
}

func (m setupInputModel) Init() tea.Cmd { return nil }

func (m setupInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "enter":
			if utils.CleanInputLine(m.value) != "" || m.defaultValue != "" || m.allowEmpty {
				return m, tea.Quit
			}
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "backspace":
			if len(m.value) > 0 {
				runes := []rune(m.value)
				m.value = string(runes[:len(runes)-1])
			}
		default:
			if msg.Type == tea.KeyRunes {
				m.value += utils.CleanInputRunes(msg.Runes)
			} else if msg.Type == tea.KeySpace {
				m.value += " "
			}
		}
	}
	return m, nil
}

func (m setupInputModel) View() string {
	var b strings.Builder
	b.WriteString(setupHeaderStyle.Render(m.label))
	b.WriteString("\n\n")
	b.WriteString(setupInputStyle.Render("❯ "))
	if m.value == "" {
		b.WriteString(setupCursorStyle.Render("▌"))
		b.WriteString(setupDimStyle.Render(m.placeholder))
	} else {
		b.WriteString(m.value)
		b.WriteString(setupCursorStyle.Render("▌"))
	}
	b.WriteString(setupDimStyle.Render("  (Enter Xác nhận, Esc Hủy)"))
	b.WriteString("\n")
	return b.String()
}
