# Upstream Main Vietnamese Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge the latest `upstream/main` into this fork and localize only newly introduced user-facing strings while preserving the existing Vietnamese translation layer.

**Architecture:** Treat upstream sync and localization as two passes over the same diff. First, bring the codebase to the latest upstream state and resolve structural conflicts without altering existing Vietnamese copy. Then localize only new or changed user-visible text in prompts, docs, CLI/TUI strings, and fixtures, while leaving technical identifiers and earlier translations intact.

**Tech Stack:** Git merge workflow, Go 1.21+, embedded markdown assets, Bubble Tea/Lip Gloss TUI, `go test ./...`

## Global Constraints

- Keep existing Vietnamese translations unless the corresponding upstream text changed.
- Translate only new user-facing strings introduced by `upstream/main`.
- Preserve function names, variable names, package names, exported APIs, config keys, data formats, and schemas.
- Do not add new third-party dependencies.
- Prefer small, reviewable changes over large rewrites.
- Preserve existing runtime behavior unless the upstream merge requires a conflict resolution.
- Validate with targeted Go tests before any full-repo test pass.

## File Structure

### Merge surface
- Repo-wide sync surface: `README.md`, `assets/**`, `docs/**`, `cmd/ainovel-cli/main.go`, `config.example.jsonc`, `rules.md.example`, `evals/**`, `go.mod`, `go.sum`, `.gitattributes`, `.gitignore`, `.goreleaser.yml`, `internal/**`

### User-facing copy hotspots
- Prompt and reference assets: `assets/prompts/**`, `assets/references/**`, `assets/styles/**`, `assets/voice.md`
- CLI/TUI strings: `internal/entry/tui/**`, `internal/entry/headless/**`, `internal/entry/startup/**`, `internal/logger/logger.go`, `internal/notify/**`
- Runtime messages and user guidance: `internal/bootstrap/**`, `internal/host/**`, `internal/tools/**`
- Fixtures and examples: `config.example.jsonc`, `rules.md.example`, `assets/testdata/**`, `evals/cases/**`

---

### Task 1: Merge `upstream/main` into the fork without losing the Vietnamese baseline

**Files:**
- Modify: `README.md`, `assets/**`, `docs/**`, `cmd/ainovel-cli/main.go`, `config.example.jsonc`, `rules.md.example`, `evals/**`, `go.mod`, `go.sum`, `.gitattributes`, `.gitignore`, `.goreleaser.yml`, `internal/**`
- Test: all package tests touched by the merge, especially `internal/bootstrap/*_test.go`, `internal/entry/tui/*_test.go`, `internal/host/*_test.go`, `internal/rules/*_test.go`, `internal/store/*_test.go`, `internal/tools/*_test.go`

**Interfaces:**
- Consumes: current branch state plus `upstream/main`
- Produces: a merged working tree that already includes the latest upstream code, with existing Vietnamese text preserved wherever upstream did not change the meaning

- [ ] **Step 1: Bring upstream into the working tree**

```bash
git fetch upstream
git merge --no-commit upstream/main
```

Expected: merge stops in the working tree with conflicts only in files where upstream touched code or copy that already existed in this fork.

- [ ] **Step 2: Resolve structural conflicts first**

Use `git diff --name-only --diff-filter=U` to list conflict files, then resolve them file-by-file. Keep the fork’s existing Vietnamese wording in already localized surfaces unless the upstream edit changes behavior or invalidates the old copy.

- [ ] **Step 3: Verify the merge shape before translating anything**

```bash
git diff --check
git status --short
```

Expected: no whitespace errors, no unresolved conflict markers, and only the intended merge changes remain.

- [ ] **Step 4: Commit the merge baseline**

```bash
git add -A
git commit -m "merge: sync upstream/main into Vietnamese fork"
```

Expected: one merge commit that represents the upstream codebase refresh before any localization-only edits.

---

### Task 2: Localize only the new or changed user-facing strings from upstream

**Files:**
- Modify: `README.md`, `assets/prompts/**`, `assets/references/**`, `assets/styles/**`, `assets/voice.md`, `cmd/ainovel-cli/main.go`, `internal/entry/headless/**`, `internal/entry/startup/**`, `internal/entry/tui/**`, `internal/logger/logger.go`, `internal/notify/**`, `internal/bootstrap/**`, `internal/host/**`, `internal/tools/**`, `config.example.jsonc`, `rules.md.example`, `assets/testdata/**`, `evals/cases/**`
- Test: any package tests or golden tests whose expected output changes because a new string is now Vietnamese

**Interfaces:**
- Consumes: the merged upstream baseline from Task 1
- Produces: updated Vietnamese copy for only the new upstream-facing text, with technical identifiers and earlier translations unchanged

- [ ] **Step 1: Isolate just the upstream-added text**

```bash
git diff --word-diff=plain HEAD^..HEAD -- README.md assets docs cmd internal config.example.jsonc rules.md.example
```

Expected: the diff highlights the newly introduced or modified copy that still reads as source-language text or needs a Vietnamese refresh.

- [ ] **Step 2: Translate only new visible strings**

Edit the touched files so that:
- new prompts, help text, errors, and docs read naturally in Vietnamese;
- identifiers such as function names, package names, config keys, struct fields, and file paths stay unchanged;
- existing Vietnamese paragraphs stay as-is unless upstream changed the underlying meaning.

- [ ] **Step 3: Keep fixture text aligned with the localized copy**

Update any golden files or example outputs only when they assert user-visible strings that changed in the source files.

- [ ] **Step 4: Commit the localization pass**

```bash
git add -A
git commit -m "feat: Việt hoá phần mới từ upstream"
```

Expected: a clean commit containing only the new localization work and no technical renames.

---

### Task 3: Run regression checks and confirm the fork still behaves like the upstream codebase plus Vietnamese copy

**Files:**
- Test: `./...`

**Interfaces:**
- Consumes: the merged and localized tree from Tasks 1-2
- Produces: a verified build/test result that confirms the sync did not break the CLI, TUI, or core runtime packages

- [ ] **Step 1: Run focused package tests first**

```bash
go test ./internal/bootstrap ./internal/entry/tui ./internal/entry/headless ./internal/host ./internal/rules ./internal/store ./internal/tools ./internal/logger ./internal/notify
```

Expected: `ok` for each package, or a short list of failures that point directly to the changed area.

- [ ] **Step 2: Run the full test suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 3: Spot-check the most visible surfaces**

Open or inspect `README.md`, `assets/prompts/*.md`, and `internal/entry/tui/command_help.go`-driven copy to confirm the new upstream text is Vietnamese while older translations still match the fork’s style.

- [ ] **Step 4: Summarize the sync scope for handoff**

Record which files were taken verbatim from upstream, which were translated, and which Vietnamese strings were intentionally left unchanged because they were already correct.
