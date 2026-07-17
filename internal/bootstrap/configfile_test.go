package bootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/errs"
)

const validGlobal = `{
  "provider": "openrouter",
  "model": "google/gemini-2.5-flash",
  "providers": { "openrouter": { "api_key": "sk-test-123456" } }
}`

// writeGlobal  HOME ， HOME。
func writeGlobal(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Windows  os.UserHomeDir  USERPROFILE；x ~/.ainovel。
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ainovel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
			t.Fatalf("write global: %v", err)
		}
	}
	return home
}

// writeProjectConfig Hiện tại ./.ainovel/ 。
//
//	t.Chdir 。
func writeProjectConfig(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(".ainovel", 0o755); err != nil {
		t.Fatalf("mkdir .ainovel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".ainovel", "config.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write project: %v", err)
	}
}

// 3： ./.ainovel/config.json  JSON，，。
func TestLoadConfig_CorruptProjectFailsLoud(t *testing.T) {
	writeGlobal(t, validGlobal)
	proj := t.TempDir()
	t.Chdir(proj)
	// —— JSON。
	writeProjectConfig(t, `{ "model": "x", }`)

	if _, err := LoadConfig(); err == nil {
		t.Fatal("./.ainovel/config.json hỏng phải báo lỗi, nhưng đã bị bỏ qua im lặng")
	}
}

// Thấp：Cao（——
//
//	fail-loud，" + "）。
func TestLoadConfig_CorruptGlobalDoesNotBlockProjectOverride(t *testing.T) {
	writeGlobal(t, `{ not json`)
	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, validGlobal)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("global config hỏng không được chặn config cấp dự án hợp lệ, got: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("phải dùng giá trị cấu hình cấp dự án, got provider=%q", cfg.Provider)
	}
}

// ： ./.ainovel/config.json  EffectiveConfigPath （），
// ——/config  /model 。
func TestEffectiveConfigPathPrefersProject(t *testing.T) {
	writeGlobal(t, validGlobal)

	t.Chdir(t.TempDir()) //
	if got := EffectiveConfigPath(); got != DefaultConfigPath() {
		t.Fatalf("không có config dự án thì phải fallback global, got %q want %q", got, DefaultConfigPath())
	}

	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, validGlobal)
	wantAbs, err := filepath.Abs(filepath.Join(".ainovel", "config.json"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if got := EffectiveConfigPath(); got != wantAbs {
		t.Fatalf("có config dự án thì phải ghi vào dự án, got %q want %q", got, wantAbs)
	}
}

// （/），。
func TestLoadConfig_MissingFilesNoError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // ~/.ainovel/config.json
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir()) //  ./.ainovel/config.json

	if _, err := LoadConfig(); err != nil {
		t.Fatalf("thiếu file cấu hình không được báo lỗi, got: %v", err)
	}
}

// ： + 。
func TestLoadConfig_ValidMergeWorks(t *testing.T) {
	writeGlobal(t, validGlobal)
	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, `{
  "model": "google/gemini-2.5-pro",
  "reasoning_effort": "high",
  "roles": {
    "writer": {
      "provider": "openrouter",
      "model": "google/gemini-2.5-flash",
      "reasoning_effort": "low"
    }
  }
}`)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("cấu hình hợp lệ không được báo lỗi: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("provider phải giữ giá trị global openrouter, got %q", cfg.Provider)
	}
	if cfg.ModelName != "google/gemini-2.5-pro" {
		t.Errorf("model phải bị ghi đè bởi cấp dự án, got %q", cfg.ModelName)
	}
	if cfg.ReasoningEffort != "high" {
		t.Errorf("reasoning_effort phải bị ghi đè bởi cấp dự án, got %q", cfg.ReasoningEffort)
	}
	if got := cfg.Roles["writer"].ReasoningEffort; got != "low" {
		t.Errorf("roles.writer.reasoning_effort phải bị ghi đè bởi cấp dự án, got %q", got)
	}
}

func TestMergeConfig_ProviderExtraFields(t *testing.T) {
	base := Config{
		Provider:  "openrouter",
		ModelName: "google/gemini-2.5-flash",
		Providers: map[string]ProviderConfig{
			"openrouter": {
				API:    "chat",
				APIKey: "sk-test-123456",
				ExtraBody: map[string]any{
					"temperature": 0.8,
				},
				Extra: map[string]any{
					"user_agent": "base-client/1.0",
				},
			},
		},
	}
	overlay := Config{
		Providers: map[string]ProviderConfig{
			"openrouter": {
				API:     "responses",
				BaseURL: "https://proxy.example.com/v1",
				ExtraBody: map[string]any{
					"min_p": 0.05,
				},
				Extra: map[string]any{
					"user_agent": "override-client/1.0",
					"headers": map[string]any{
						"X-Custom-Client": "ainovel",
					},
				},
			},
		},
	}

	cfg := mergeConfig(base, overlay)
	pc := cfg.Providers["openrouter"]
	if pc.APIKey != "sk-test-123456" {
		t.Fatalf("APIKey = %q, want inherited key", pc.APIKey)
	}
	if pc.API != "responses" {
		t.Fatalf("API = %q, want responses", pc.API)
	}
	if pc.BaseURL != "https://proxy.example.com/v1" {
		t.Fatalf("BaseURL = %q, want overlay URL", pc.BaseURL)
	}
	if _, ok := pc.ExtraBody["temperature"]; ok {
		t.Fatalf("ExtraBody should be replaced by overlay, got %#v", pc.ExtraBody)
	}
	if got := pc.ExtraBody["min_p"]; got != 0.05 {
		t.Fatalf("ExtraBody[min_p] = %#v, want 0.05", got)
	}
	if got := pc.Extra["user_agent"]; got != "override-client/1.0" {
		t.Fatalf("Extra[user_agent] = %#v, want override-client/1.0", got)
	}
	headers, ok := pc.Extra["headers"].(map[string]any)
	if !ok {
		t.Fatalf("Extra[headers] missing or invalid: %#v", pc.Extra["headers"])
	}
	if got := headers["X-Custom-Client"]; got != "ainovel" {
		t.Fatalf("Extra.headers[X-Custom-Client] = %#v, want ainovel", got)
	}
}

//	2（issue #37 ）： provider  providers ，
//
// ValidateBase  config （）。
func TestValidateBase_ProviderOverrideWithoutCredentials(t *testing.T) {
	cfg := Config{
		Provider:  "mimo",
		ModelName: "mimo-v2.5-pro",
		Providers: map[string]ProviderConfig{
			"openrouter": {APIKey: "sk-test-123456"},
		},
	}
	cfg.FillDefaults()
	err := cfg.ValidateBase()
	if err == nil {
		t.Fatal("provider thiếu credential phải báo lỗi")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("phải wrap errs.ErrConfig, got: %v", err)
	}
}

func TestValidateBaseRejectsInvalidProviderAPI(t *testing.T) {
	cfg := Config{
		Provider:  "openai",
		ModelName: "gpt-5.1",
		Providers: map[string]ProviderConfig{
			"openai": {APIKey: "sk-test-123456", API: "legacy"},
		},
	}
	cfg.FillDefaults()
	err := cfg.ValidateBase()
	if err == nil {
		t.Fatal("provider api không hợp lệ phải báo lỗi")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("phải wrap errs.ErrConfig, got: %v", err)
	}
}

func TestValidateBaseRejectsProviderAPIOnNonOpenAIProvider(t *testing.T) {
	cfg := Config{
		Provider:  "anthropic",
		ModelName: "claude-sonnet-4",
		Providers: map[string]ProviderConfig{
			"anthropic": {APIKey: "sk-test-123456", API: "responses"},
		},
	}
	cfg.FillDefaults()
	err := cfg.ValidateBase()
	if err == nil {
		t.Fatal("provider không phải OpenAI cấu hình api phải báo lỗi")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("phải wrap errs.ErrConfig, got: %v", err)
	}
}

// ： JSON、
//
//	provider 、“”——，。
func TestExampleConfigIsValidAndSelfConsistent(t *testing.T) {
	if exampleConfig == "" {
		t.Fatal("go:embed không có hiệu lực, exampleConfig rỗng")
	}
	rootExample, err := os.ReadFile(filepath.Join("..", "..", "config.example.jsonc"))
	if err != nil {
		t.Fatalf("đọc config.example.jsonc ở root: %v", err)
	}
	if string(rootExample) != exampleConfig {
		t.Fatal("config.example.jsonc ở root không khớp internal/bootstrap/config.example.jsonc")
	}
	var cfg Config
	if err := json.Unmarshal(stripJSONComments([]byte(exampleConfig)), &cfg); err != nil {
		t.Fatalf("ví dụ tích hợp sau khi bỏ comment không phải JSON hợp lệ (người dùng copy sẽ lỗi): %v", err)
	}
	if cfg.Provider == "" || cfg.ModelName == "" {
		t.Fatal("ví dụ phải cung cấp provider/model mặc định")
	}
	if _, ok := cfg.Providers[cfg.Provider]; !ok {
		t.Errorf("provider cấp cao nhất trong ví dụ %q không trỏ tới mục trong providers — ví dụ con trỏ đang bị treo", cfg.Provider)
	}
	if !contains(exampleConfig, "con trỏ") {
		t.Error("ví dụ phải nói rõ provider là một con trỏ — đừng để bẫy nhận thức #37 quay lại")
	}
}

func TestWriteStartupError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path := WriteStartupError("boom: provider not configured")
	if path == "" {
		t.Fatal("phải trả về đường dẫn ghi ra đĩa")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("đọc last-error.log: %v", err)
	}
	if want := "boom: provider not configured"; !contains(string(data), want) {
		t.Errorf("log phải chứa %q, thực tế: %s", want, data)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
