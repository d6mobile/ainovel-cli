# Production Localization Pass Design

## Goal

Remove remaining untranslated Chinese/Cyrillic prose from production user-facing and model-facing string literals while preserving domain data, identifiers, schemas, command names, and story workspace outputs.

## Scope

Translate production strings that users, logs, diagnostics, generated markdown, agents, or LLM prompts can read:

- runtime errors and event summaries
- TUI/CLI/eval/diagnostic output
- agent/arbiter/guard/model prompts
- import pipeline status, validation, and retry messages
- generated markdown headings and labels
- tool/store output labels

Do not translate or modify:

- code identifiers, package names, function names, role names, tool names, command names, JSON keys, schemas, persisted formats
- regex patterns and parser literals for Chinese source novels, such as chapter-title matching
- domain examples and default rule/style-stat data, such as forbidden phrases and Chinese fatigue-word patterns
- generated story workspace data under story directories
- comments unless the comment is embedded in a generated/user-visible string or directly causes scan ambiguity in touched code

## Approach

Use a scan-driven workflow:

1. Extract Chinese/Cyrillic-containing production string literals from Go source and public scripts/docs.
2. Classify each hit as translate / domain-data keep / code false-positive.
3. Translate only the translate group in small package clusters.
4. Add targeted regression coverage where a package already has tests around the affected behavior or where a known prompt/message should stay localized.
5. Verify with targeted package tests, full Docker `go test ./...`, and the same production-string scan.

## Package Clusters

1. `assets`, `internal/agents`, `internal/arbiter`: model-facing prompts, guard messages, retry/error guidance.
2. `internal/host/imp`, `internal/host`, `internal/flow`: import/engine/runtime statuses and prompts.
3. `internal/diag`, `internal/eval`: diagnostic and eval CLI/report output.
4. `internal/store`, `internal/tools`, `internal/rules`, `internal/version`: generated markdown labels, tool errors, remaining user-visible output.

## Testing

- Use Docker-based Go commands only.
- Run targeted tests for modified packages after each cluster when practical.
- Run full verification before completion:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.25 go test ./...
```

- Run a production-string scan and report any remaining Chinese/Cyrillic strings with classification.

## Risks and Constraints

- Some Chinese strings are intentional domain data; translating them would reduce quality for Chinese novel import/style analysis.
- Some generated headings may be asserted in tests; update tests only when the user-visible output language changes, not to mask unrelated failures.
- Keep changes mechanical and scoped; do not refactor control flow or persistence formats.
