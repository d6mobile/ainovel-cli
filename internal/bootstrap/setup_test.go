package bootstrap

import "testing"

func TestProviderPresetsIncludeDeepSeekAndOllama(t *testing.T) {
	presets := ProviderPresets()
	got := make(map[string]ProviderPreset, len(presets))
	for _, preset := range presets {
		got[preset.Name] = preset
	}

	if p := got["deepseek"]; p.Label != "DeepSeek" {
		t.Fatalf("deepseek preset = %#v, want label DeepSeek", p)
	}
	if p := got["ollama"]; p.Label != "Ollama" || p.BaseURL != "http://localhost:11434/v1" || !p.APIKeyOptional {
		t.Fatalf("ollama preset = %#v, want label Ollama, base URL http://localhost:11434/v1, api key optional", p)
	}
}
