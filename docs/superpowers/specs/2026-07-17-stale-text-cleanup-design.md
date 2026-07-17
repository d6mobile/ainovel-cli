# Stale text cleanup pass

## Goal
Do one focused cleanup pass to remove remaining mixed-language or stale text from the codebase after the prior commit, while keeping behavior unchanged.

## Scope
Only touch:
- Go comments
- test names
- test assertion messages
- helper text inside tests when it is obviously stale or mixed-language

Do not touch:
- runtime logic
- public APIs
- persisted formats
- prompt/fixture/story text in assets
- unrelated refactors

## Approach
Recommended approach: conservative comments-and-tests-only pass.

### Why this approach
- Lowest risk of changing runtime behavior.
- Keeps the pass narrowly aligned with the user’s request.
- Avoids expanding into asset/prompt/story text, which the user explicitly excluded.

## Implementation plan
1. Scan changed Go files for remaining mixed-language or stale text.
2. Classify hits into comments, tests, and runtime/user-visible strings.
3. Fix only comments and tests.
4. Re-run `gofmt` on edited Go files.
5. Validate with focused Docker tests for the touched packages, then a full `go test ./...` if the focused tests pass.

## Validation
Success means:
- No remaining stale or mixed-language comments/tests in the targeted pass.
- Focused Docker tests pass for touched packages.
- `go test ./...` still passes.

## Non-goals
- Full-language normalization of the repository.
- Editing prompts, fixtures, or story data.
- Adjusting runtime log strings unless they are part of a test helper or assertion.
