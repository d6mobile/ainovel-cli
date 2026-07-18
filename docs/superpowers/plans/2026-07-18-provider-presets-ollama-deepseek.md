# Ollama and DeepSeek Provider Presets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Ollama and DeepSeek first-class provider presets in `/config`, keep README and config examples aligned, and support mixed local/API model usage through role-based model assignment.

**Architecture:** The app already resolves models from a provider/model config tree, so the change is not a new inference subsystem. The work is mainly to lock the preset catalog exposed by `/config`, document the supported local+API role split, and add tests that prove the provider presets and mixed-provider role resolution stay stable.

**Tech Stack:** Go, Bubble Tea TUI, existing bootstrap/config/model packages, markdown docs, JSONC config examples, table-driven tests.

## Global Constraints

- Do not add new third-party dependencies.
- Keep the existing config schema keys unchanged: `provider`, `model`, `providers`, `roles`, `base_url`, `api_key`, `models`, `api`, `extra`, `extra_body`.
- `ollama` must remain API-key optional.
- Ollama’s default base URL must stay `http://localhost:11434/v1`.
- DeepSeek must remain available as a first-class provider preset and provider key.
- Existing provider support for OpenRouter, Anthropic, Gemini, OpenAI, Qwen, GLM, Grok, Bedrock, and custom proxies must continue to work.
- Role-based selection must keep working for `architect`, `writer`, `editor`, and the import roles.
- User-facing docs and config samples must match runtime behavior.

---

## File Structure

- `internal/bootstrap/setup.go` — owns the provider preset catalog shown in `/config`.
- `internal/bootstrap/setup_test.go` — new contract tests for preset names, labels, and defaults.
- `internal/entry/tui/command_config_test.go` — existing UI tests that verify the provider setup menu exposes the preset catalog.
- `README.md` — user-facing provider docs and role-based multi-model example.
- `config.example.jsonc` — root config sample shipped in the repo.
- `internal/bootstrap/config.example.jsonc` — embedded config sample written by `saveExampleConfig()`.
- `internal/bootstrap/configfile_test.go` — existing example-config sync and parsing tests.
- `internal/host/model_config_test.go` — runtime resolution tests for mixed-provider role selection.

---

## Task 1: Lock the preset catalog shown in `/config`

**Files:**
- Create: `internal/bootstrap/setup_test.go`
- Modify: `internal/entry/tui/command_config_test.go:186-219`
- Modify only if tests expose drift: `internal/bootstrap/setup.go:53-65, 67-76`

**Interfaces:**
- Consumes: `bootstrap.ProviderPresets()`, `modelConfigState.buildProviderMenus()`, `setupProviders`
- Produces: stable preset labels, defaults, and menu exposure for `DeepSeek` and `Ollama`

- [ ] **Step 1: Write the failing preset-contract test**

```go
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
```

- [ ] **Step 2: Write the failing `/config` menu test**

```go
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
```

- [ ] **Step 3: Run the focused tests and confirm the contract**

Run:
```bash
go test ./internal/bootstrap ./internal/entry/tui -run 'ProviderPresets|ProviderMenu|TwoLevel' -v
```
Expected: PASS after the preset catalog and menu exposure are aligned.

- [ ] **Step 4: Adjust preset metadata only if the contract exposes drift**

If either preset name, label, API-key flag, or base URL differs from the test above, update the existing preset table in `internal/bootstrap/setup.go` so the catalog remains the single source of truth.

- [ ] **Step 5: Commit the preset contract changes**

```bash
git add internal/bootstrap/setup.go internal/bootstrap/setup_test.go internal/entry/tui/command_config_test.go
git commit -m "test: lock ollama and deepseek presets" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 2: Align README and config examples with local+API role usage

**Files:**
- Modify: `README.md`
- Modify: `config.example.jsonc`
- Modify: `internal/bootstrap/config.example.jsonc`

**Interfaces:**
- Consumes: provider preset names `deepseek` and `ollama`, role keys `architect`, `writer`, `editor`
- Produces: documentation and sample config that show a mixed local/API setup that matches runtime behavior

- [ ] **Step 1: Update the README provider section and role example**

Add a concrete example that mixes a DeepSeek API provider with a local Ollama provider through roles. Use a shape like:

```jsonc
{
  "provider": "deepseek",
  "model": "deepseek-chat",
  "providers": {
    "deepseek": {
      "api_key": "sk-...",
      "models": [
        { "name": "deepseek-chat", "context_window": 131072 },
        { "name": "deepseek-reasoner", "context_window": 131072 }
      ]
    },
    "ollama": {
      "base_url": "http://host.docker.internal:11434/v1",
      "models": [
        { "name": "qwen3:14b", "context_window": 32768 }
      ]
    }
  },
  "roles": {
    "architect": { "provider": "deepseek", "model": "deepseek-reasoner" },
    "writer": { "provider": "ollama", "model": "qwen3:14b" },
    "editor": { "provider": "deepseek", "model": "deepseek-chat" }
  }
}
```

The README text should explain that `/config` exposes both presets, that Ollama is API-key optional, and that roles can mix local and API providers in the same run.

- [ ] **Step 2: Mirror the same example in the root config sample**

Update `config.example.jsonc` so the shipped sample matches the README example and keeps DeepSeek and Ollama visible in the provider list.

- [ ] **Step 3: Mirror the same example in the embedded sample**

Update `internal/bootstrap/config.example.jsonc` with the same provider/role example so `saveExampleConfig()` writes the same sample the README describes.

- [ ] **Step 4: Run the example-config sync test**

Run:
```bash
go test ./internal/bootstrap -run TestExampleConfigIsValidAndSelfConsistent -v
```
Expected: PASS and the root `config.example.jsonc` must match the embedded copy exactly.

- [ ] **Step 5: Commit the docs/sample alignment**

```bash
git add README.md config.example.jsonc internal/bootstrap/config.example.jsonc
git commit -m "docs: align provider examples for ollama and deepseek" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 3: Lock mixed-provider runtime resolution

**Files:**
- Modify: `internal/bootstrap/configfile_test.go:272-299`
- Modify: `internal/host/model_config_test.go:11-117`
- Modify only if validation rules expose a gap: `internal/bootstrap/config.go`, `internal/bootstrap/models.go`

**Interfaces:**
- Consumes: `bootstrap.LoadConfigFile`, `bootstrap.NewModelSet`, `bootstrap.Config.ResolveContextWindow`, `bootstrap.ModelSet.CurrentSelection`
- Produces: tests that prove a config can mix DeepSeek and Ollama per role without breaking the default selection path

- [ ] **Step 1: Extend the example-config consistency test**

Update `TestExampleConfigIsValidAndSelfConsistent` so it asserts that the sample config includes both a DeepSeek provider block and an Ollama provider block, and that the top-level default provider still points at one of the configured providers.

- [ ] **Step 2: Add a role-mixing runtime test**

Add a test in `internal/host/model_config_test.go` that uses one DeepSeek provider and one Ollama provider in the same `bootstrap.Config`, then asserts that `bootstrap.NewModelSet(cfg)` resolves the expected role/provider/model pairs:

```go
provider, model, explicit := models.CurrentSelection("architect")
// want: deepseek / deepseek-reasoner / true

provider, model, explicit = models.CurrentSelection("writer")
// want: ollama / qwen3:14b / true

provider, model, explicit = models.CurrentSelection("editor")
// want: deepseek / deepseek-chat / true
```

Also assert that the top-level default still resolves when `role == "default"`.

- [ ] **Step 3: Run the targeted runtime tests**

Run:
```bash
go test ./internal/bootstrap ./internal/host -run 'ExampleConfig|ModelSet|ConfigureModels|ValidateBase' -v
```
Expected: PASS with no network access required during test construction.

- [ ] **Step 4: Adjust validation only if the mixed config is rejected**

If the new sample config is rejected, update `internal/bootstrap/config.go` or `internal/bootstrap/models.go` only as far as needed to keep the existing schema and selection semantics intact.

- [ ] **Step 5: Commit the runtime contract tests**

```bash
git add internal/bootstrap/configfile_test.go internal/host/model_config_test.go
git commit -m "test: lock mixed provider role resolution" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Task 4: Final verification and release check

**Files:**
- None expected beyond the files changed in Tasks 1-3

**Interfaces:**
- Consumes: the updated README, config examples, preset catalog, and tests
- Produces: a verified `main` branch that exposes Ollama and DeepSeek as first-class presets and documents the mixed local/API pattern

- [ ] **Step 1: Run the package-level verification first**

Run:
```bash
go test ./internal/bootstrap ./internal/entry/tui ./internal/host -v
```
Expected: PASS.

- [ ] **Step 2: Run the full suite**

Run:
```bash
go test ./...
```
Expected: PASS.

- [ ] **Step 3: Confirm the docs and samples are aligned**

Run:
```bash
rg -n 'deepseek|ollama|architect|writer|editor' README.md config.example.jsonc internal/bootstrap/config.example.jsonc
```
Expected: the same local/API role split appears in all three places.

- [ ] **Step 4: Review the final diff for scope**

Run:
```bash
git diff --stat
```
Expected: only the provider preset, docs, sample config, and the new/updated tests are changed.

- [ ] **Step 5: Commit the verification pass if anything remained uncommitted**

```bash
git add -A
git commit -m "test: verify ollama and deepseek integration" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage:**
- `/config` first-class presets: Task 1.
- README and config examples: Task 2.
- Mixed local/API role usage: Task 2 and Task 3.
- Contract tests for preset exposure and runtime resolution: Tasks 1 and 3.
- End-to-end verification: Task 4.

**2. Placeholder scan:**
- No TBD/TODO placeholders.
- The only conditional language is limited to the implementation guardrails in Tasks 1 and 3, where code is only touched if a test exposes drift or a validation gap.

**3. Type consistency:**
- `ProviderPresets()` and `setupProviders` are the preset source of truth.
- `bootstrap.NewModelSet`, `ModelSet.CurrentSelection`, and `Config.ResolveContextWindow` stay as the runtime resolution APIs.
- The role names used in tests and docs are consistent: `architect`, `writer`, `editor`.
