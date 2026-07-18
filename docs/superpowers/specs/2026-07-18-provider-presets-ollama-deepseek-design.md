# Provider Presets for Ollama and DeepSeek Design

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Ollama and DeepSeek first-class provider presets in `/config`, keep README and config examples aligned, and support mixed local/API model usage through role-based model assignment.

**Architecture:** The app already resolves models from a provider/model config tree, so the change is not a new inference subsystem. The design is to centralize provider presets in the setup layer, expose Ollama and DeepSeek as first-class options in the interactive config flow, and keep runtime selection driven by `provider`, `providers`, and `roles`. This lets users mix local Ollama models and API-backed DeepSeek models in one run without special-case runtime code.

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

## Context

The repository already has partial support for the relevant providers:

- `internal/bootstrap/setup.go` already lists `deepseek` and `ollama` in the provider preset table.
- `internal/bootstrap/config.go`, `internal/bootstrap/models.go`, and related tests already treat provider/model selection as config-driven.
- `README.md` already documents Ollama and role-based multi-model usage.

What still needs to be tightened is the user-facing integration path:

1. `/config` should clearly expose Ollama and DeepSeek as first-class presets with the right defaults.
2. README and `config.example.jsonc` should show a local+API multi-model setup that matches the runtime model-selection rules.
3. Tests should lock the provider preset catalog and mixed-provider role configuration so future upstream syncs do not regress the behavior.

---

## Design Options

### Option A: Minimal patch
Keep runtime as-is and only update docs/config samples.

**Pros:** Smallest change.
**Cons:** `/config` still feels fragmented, and the README promise is not strongly enforced by tests.

### Option B: Shared preset catalog in bootstrap layer, with docs/tests aligned
Use one provider preset source of truth for `/config`, config examples, and docs. Keep runtime config-driven and role-based.

**Pros:** Best fit for the current architecture; low risk; makes Ollama and DeepSeek feel native without introducing a new runtime abstraction.
**Cons:** Still leaves provider transport implementation inside existing config/model code, which is fine for this codebase.

### Option C: Full provider registry refactor
Extract all provider metadata into a separate registry used by setup, docs, and runtime.

**Pros:** Strong long-term abstraction.
**Cons:** Too broad for the current need; would touch more files and create unnecessary churn.

**Recommendation:** Option B.

---

## Architecture

### 1. Preset source of truth
The provider preset list in `internal/bootstrap/setup.go` becomes the canonical source for what appears in `/config`. Ollama and DeepSeek are not just “supported in docs”; they are selectable presets with defined labels and default values.

### 2. Role-based local/API mixing
The runtime continues to resolve provider/model per role:

- top-level `provider` + `model` remains the default model,
- `providers` holds all configured provider blocks,
- `roles` chooses a provider/model for each role.

That means one configuration can mix local and API-backed models cleanly. For example:

- `architect` → DeepSeek API for strong planning,
- `writer` → Ollama local for cheaper long-form drafting,
- `editor` → DeepSeek API for review quality.

### 3. Native means first-class, not a new transport layer
“Native provider” in this design means a first-class preset and config key, not a new HTTP stack. The existing provider/model resolution and OpenAI-compatible adapter path remain the execution layer.

### 4. Docs and samples must mirror runtime behavior
README and `config.example.jsonc` should demonstrate the exact patterns the runtime supports:

- Ollama with optional API key and local base URL,
- DeepSeek as a normal provider preset,
- one run using multiple providers via `roles`.

---

## Component Boundaries

### `internal/bootstrap/setup.go`
Owns the preset list shown in `/config`.

Responsibilities:
- expose provider presets for UI selection,
- keep default labels and base URLs,
- keep Ollama API-key optional,
- keep DeepSeek present as a built-in choice.

### `internal/bootstrap/config.example.jsonc`
Owns the canonical sample configuration.

Responsibilities:
- show Ollama and DeepSeek blocks,
- show one local+API role split,
- show the role-based multi-model pattern from the README.

### `README.md`
Owns the user-facing explanation.

Responsibilities:
- explain how to select Ollama and DeepSeek,
- explain how to mix local and API models by role,
- keep examples aligned with the sample config.

### `internal/bootstrap/config*.go`, `internal/bootstrap/model_config*.go`
Own the validation and resolution logic.

Responsibilities:
- preserve provider/model resolution,
- ensure mixed-provider role configs keep working,
- keep the provider key / model name semantics stable.

### Tests
Own the contract that these presets remain selectable and that role-based mixed-provider configs keep resolving.

Responsibilities:
- lock provider preset names, labels, and base URLs,
- validate sample config parsing,
- validate model-set role selection across local and API providers.

---

## Data Flow

1. User opens `/config`.
2. The setup UI presents provider presets, including `DeepSeek` and `Ollama`.
3. User picks a preset.
4. The setup flow seeds provider defaults:
   - Ollama: `base_url=http://localhost:11434/v1`, API key optional,
   - DeepSeek: provider key and API key fields as appropriate for the preset.
5. The generated config is saved.
6. At runtime, `ModelSet` resolves the default model from top-level `provider` + `model`.
7. Role-specific settings override the default per role, so `architect`, `writer`, and `editor` can point at different providers.
8. If a role is omitted, runtime falls back to the top-level default.

---

## Error Handling

- If a preset is selected but its provider block is incomplete, setup must fail with an explicit message rather than writing a broken config.
- If a role references an unknown provider, config loading must continue to surface a clear config error.
- If a model name does not exist for the selected provider, the failure should happen at config/setup time, not later in the middle of a run.
- Ollama should remain usable without an API key; the UI and sample config must not make the key look mandatory.
- Existing fallback behavior must remain intact; this design does not remove or weaken model failover.

---

## Testing Strategy

### Contract tests for setup presets
Add or update tests that assert:

- `DeepSeek` and `Ollama` appear in the preset list returned by `ProviderPresets()`,
- Ollama is marked as API-key optional,
- Ollama’s default base URL is `http://localhost:11434/v1`,
- DeepSeek is exposed as a built-in preset label.

### Config parsing and model resolution tests
Add or update tests that assert:

- `config.example.jsonc` parses successfully with Ollama and DeepSeek entries,
- `roles` can mix local and API providers,
- `ModelSet` resolves per-role provider/model correctly,
- the top-level default still works when roles are omitted.

### Documentation checks
Update README and config examples so that the documented configuration matches the tested runtime behavior.

---

## Non-Goals

- Do not redesign the entire provider abstraction.
- Do not add a new dependency injection layer for providers.
- Do not change the meaning of existing config keys.
- Do not remove OpenRouter, Anthropic, Gemini, OpenAI, Qwen, GLM, Grok, Bedrock, or custom proxy support.
- Do not change the model execution semantics beyond what is needed to expose the presets cleanly.

---

## Success Criteria

This work is done when:

1. `/config` shows Ollama and DeepSeek as first-class choices.
2. README shows a correct example of mixing local Ollama and API DeepSeek models by role.
3. `config.example.jsonc` matches that example.
4. Tests prove the preset list and mixed-provider role resolution remain stable.
5. The runtime still supports the existing provider/model selection flow without breaking current configurations.
