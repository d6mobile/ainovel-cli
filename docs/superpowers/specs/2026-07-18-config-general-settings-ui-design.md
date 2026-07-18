# `/config` General Settings UI Design

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the existing `/config` modal so users can edit the app-wide settings that are currently only available in the JSON file: `style`, `budget`, and `notify`.

**Architecture:** Keep `/config` as the single entry point for configuration editing. Add a new top-level choice, `Cài đặt chung…`, that opens a separate nested settings flow with one screen for style selection and two sub-screens for budget and notify. Reuse the existing modal-driven TUI patterns and the current config persistence path; do not introduce a separate `/settings` command.

**Tech Stack:** Go, Bubble Tea TUI, existing `internal/entry/tui`, `internal/host`, and `internal/bootstrap` config/persistence code, markdown docs, table-driven tests.

## Global Constraints

- Do not add new third-party dependencies.
- Keep `/config` as the only configuration entry point.
- Do not change `/model` semantics.
- Preserve all existing provider/model editing behavior.
- Keep config schema keys unchanged.
- Preserve current validation for provider configs and role selection.
- Treat `style`, `budget`, and `notify` as app-wide settings, not provider-specific settings.
- The runtime behavior for `budget` and `notify` must remain unchanged; this work only adds UI and persistence paths.

---

## Context

The repository already has a TUI-based `/config` flow that edits provider definitions, model lists, and model context windows. It also already has runtime config fields for the settings this task targets:

- `Config.Style`
- `Config.Budget`
- `Config.Notify`

Today those fields can be edited only by hand in the JSONC config file. The TUI does not expose them yet.

Important existing facts:

- `/config` is already a modal, keyboard-driven flow with nested screens.
- Provider editing and model editing already use separate subflows.
- `SaveConfig` already persists the full config safely.
- `LoadConfig` and `mergeConfig` already understand `style`, `budget`, and `notify`.

That means the missing piece is UI and a small host-side save path, not a schema change.

---

## Decision

Use a new `Cài đặt chung…` entry inside `/config`, then split the settings into three nested subflows:

1. **Style** — single-choice list.
2. **Budget** — dedicated screen with `book_usd`, `warn_ratio`, and `hard_stop`.
3. **Notify** — dedicated screen with `enabled`, `command`, and multi-select `events`.

This keeps the provider editor focused while still letting users manage all common settings from one place.

---

## Design Options Considered

### Option A: Add a `Cài đặt chung…` branch inside `/config`  
**Chosen.**

**Pros:**
- Preserves one configuration command.
- Fits the existing modal-navigation style.
- Separates provider editing from global settings.
- Avoids a new command users must remember.

**Cons:**
- `/config` gets a little larger.
- Requires a new host save path for non-provider settings.

### Option B: Add a new `/settings` command

**Pros:**
- Splits concerns cleanly.
- Keeps `/config` narrower.

**Cons:**
- Adds another command and mental model.
- Fragments configuration editing across two entry points.

### Option C: Mix the new fields into provider screens

**Pros:**
- Fewer top-level branches.

**Cons:**
- Blends app-wide settings with provider settings.
- Makes the provider flow harder to scan and maintain.

---

## Architecture

### 1. `/config` top-level menu
Add a new top-level item in the existing `/config` provider picker:

- existing providers
- `+ Thêm Provider…`
- `Cài đặt chung…`

Selecting `Cài đặt chung…` opens a new general-settings state.

### 2. General settings state
The general-settings flow owns three concerns:

- `style` selection
- budget editing
- notify editing

It should reuse the same modal frame, navigation keys, and error-message area as the provider flow.

### 3. Host persistence path
Add a host-side save method for app-wide settings that:

- starts from the current in-memory config snapshot,
- replaces only `Style`, `Budget`, and `Notify`,
- validates the result,
- writes the full config back through the existing atomic config save path.

Provider/model editing should continue to use the existing provider-specific save path.

### 4. Clear separation of responsibilities
- TUI: gathers user input and displays validation feedback.
- Host: validates and persists settings.
- Bootstrap/config: keeps schema parsing and merge behavior unchanged.

---

## Component Boundaries

### `internal/entry/tui/command_config.go`
Owns the `/config` modal flow.

Responsibilities:
- add the `Cài đặt chung…` entry,
- render the general-settings menu,
- render the style screen,
- render the budget screen,
- render the notify screen,
- collect user input and surface validation messages.

### `internal/host`
Owns validation and persistence of the resulting settings update.

Responsibilities:
- expose a snapshot of current `Style`, `Budget`, and `Notify`,
- accept a draft for general settings,
- validate the updated config,
- save the full config file.

### `internal/bootstrap/config.go` and `internal/bootstrap/configfile.go`
Remain the source of truth for schema, validation, merge behavior, and safe file writes.

Responsibilities:
- preserve the meaning of `style`, `budget`, and `notify`,
- keep `LoadConfig`, `mergeConfig`, and `SaveConfig` behavior stable,
- continue to validate config semantics.

### Tests
Own the contract for UI presence, persistence, and validation.

Responsibilities:
- verify `/config` exposes the new branch,
- verify each subflow saves expected config values,
- verify invalid budget / notify input is rejected with clear errors.

---

## Data Flow

1. User opens `/config`.
2. The modal lists providers plus `Cài đặt chung…`.
3. User enters `Cài đặt chung…`.
4. The general-settings screen shows summaries for style, budget, and notify.
5. User enters a sub-screen:
   - style selection,
   - budget editing,
   - or notify editing.
6. The TUI collects the changes and hands a draft to the host.
7. The host merges the draft into the current config snapshot.
8. The host validates the full config.
9. The host writes the updated config to the same effective config path used by `/config` and `/model`.
10. The TUI shows success or a validation error inline.

---

## Detailed UI Design

### 1. General settings landing screen
The landing screen should show three rows:

- `Style`
- `Budget`
- `Notify`

Each row should show a short summary value on the right, for example:

- `Style` → `default`, `romance`, `fantasy`, `suspense`
- `Budget` → `tắt` or `50 USD, warn 80%, hard stop: off`
- `Notify` → `off` or `on, 6 events`

Navigation:
- `↑↓` move selection
- `Enter` opens the selected section
- `Esc` returns to the top-level `/config` menu

### 2. Style screen
Style is a single-choice list with these values:

- `default`
- `romance`
- `fantasy`
- `suspense`

Behavior:
- `↑↓` cycles through the list
- `Enter` applies the chosen style and returns to the general-settings landing screen
- `Esc` discards the in-progress choice and returns

### 3. Budget screen
Budget is a dedicated three-field editor.

Fields:
- `book_usd`
- `warn_ratio`
- `hard_stop`

Behavior:
- `book_usd` accepts a non-negative decimal number
- `0` or empty means budget is disabled
- `warn_ratio` accepts a decimal in `(0, 1)` when budget is enabled
- when budget is disabled, `warn_ratio` may still be displayed but should not block saving
- `hard_stop` is a boolean toggle

Recommended UX details:
- show a summary line at the top so the user always sees whether budget is active
- treat `book_usd = 0` as the explicit off state
- preserve existing values if the user re-enters the screen and only changes one field
- if `book_usd > 0` and `warn_ratio` is blank, default it to `0.8`

### 4. Notify screen
Notify is a dedicated three-part editor.

Fields:
- `enabled`
- `command`
- `events`

Behavior:
- `enabled` is a simple on/off toggle
- `command` is a free-text input
- `events` is a multi-select checklist over `notify.Kinds()`
- provide quick actions for `Select all` and `Clear all`
- allow saving with an empty `events` set, but show a warning because it means nothing will fire

Recommended UX details:
- keep `enabled` visible even when it is off
- show the number of selected events in the landing-screen summary
- do not hide `command` when notify is disabled; the user may want to prepare it before enabling

---

## Error Handling

### Style
- Reject values outside the known style list.
- If the style value is missing or invalid, keep the current style and show a clear error.

### Budget
- Reject negative `book_usd`.
- Reject `warn_ratio` when `book_usd > 0` and it is not in `(0, 1)`.
- If `book_usd` is enabled and `warn_ratio` is blank, use `0.8`.
- Use inline validation feedback instead of allowing a broken save.

### Notify
- Reject control characters in `command`.
- Reject any `events` value that is not part of the known event list.
- Allow empty `events`, but warn that no notifications will be emitted.
- Preserve the current meaning of `enabled == nil` as the default-on behavior used by the runtime.

### Persistence and merge failures
- If the config path cannot be resolved or written, surface a clear error message in the modal.
- If validation fails after merging with the current config, do not write partial data.
- Saving a provider config must continue to work even after this feature lands.

---

## Testing Strategy

### UI tests
Add or update TUI tests to verify:

- `/config` includes `Cài đặt chung…` as a top-level choice,
- the general-settings landing screen renders summaries for style, budget, and notify,
- the style screen exposes the four allowed values,
- the budget screen shows and edits the three budget fields,
- the notify screen supports multi-select events.

### Persistence tests
Add or update host/bootstrap tests to verify:

- editing style/budget/notify preserves existing provider and role config,
- only the intended fields are changed when saving general settings,
- config saves still use the atomic write path,
- `LoadConfig` and `mergeConfig` still round-trip the new values.

### Validation tests
Add or update tests to verify:

- invalid budget values are rejected,
- invalid notify events are rejected,
- empty notify events are allowed but reported clearly in the UI,
- config files with existing `style`, `budget`, and `notify` values continue to load correctly.

---

## Non-Goals

- Do not create a separate `/settings` command.
- Do not redesign provider editing.
- Do not change the runtime semantics of budget or notify.
- Do not add new style categories beyond the four existing values.
- Do not change config file format keys.
- Do not introduce new dependencies.

---

## Success Criteria

This work is done when:

1. `/config` includes a `Cài đặt chung…` branch.
2. Users can edit `style`, `budget`, and `notify` without opening the JSON file.
3. Budget supports `book_usd`, `warn_ratio`, and `hard_stop` in a dedicated UI.
4. Notify supports `enabled`, `command`, and multi-select `events` in a dedicated UI.
5. Saving general settings preserves provider/model configuration.
6. Existing config loading and provider editing continue to work unchanged.
