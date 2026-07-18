# Task 1 Report — Host save path for general settings

## Status
DONE

## Files changed
- `internal/host/general_config.go`
- `internal/host/general_config_test.go`
- `.superpowers/sdd/task-1-report.md`

## Commits
- Pending at report write time; will commit as `feat: add general settings save path`

## Tests run
- `docker run --rm -v /mnt/Data/AI/ainovel-cli:/src -w /src golang:1.25 go test ./internal/host -run TestConfigureGeneralSettingsPersistsAndPreservesProviderConfig -v`
  - Result: failed first as expected with `ConfigureGeneralSettings undefined` and `GeneralSettingsDraft undefined`.
- `docker run --rm -v /mnt/Data/AI/ainovel-cli:/src -w /src golang:1.25 go test ./internal/host -run TestConfigureGeneralSettingsPersistsAndPreservesProviderConfig -v`
  - Result: passed after implementation.
- `docker run --rm -v /mnt/Data/AI/ainovel-cli:/src -w /src golang:1.25 go test ./internal/host -v`
  - Result: passed.

## Self-review
- The new host method clones the current config, replaces only `Style`, `Budget`, and `Notify`, fills defaults, validates the full config, persists through the existing atomic save path, updates the in-memory config, and rebinds notifier/budget state.
- Provider selection and provider/model contents remain unchanged in the save test.
- The implementation keeps `h.cfg` consistent immediately after save, which matches the task requirement that `style` be reflected live for later reads.

## Concerns
- I did not drive the `/config` TUI surface in this task because that UI branch is not part of Task 1; this task only adds the host-side persistence path.
- `docs/superpowers/plans/2026-07-18-config-general-settings-ui.md` exists as an untracked file in the working tree but was not modified for this task.

## Fix report
- Changed `internal/host/general_config.go` to save app-wide settings without rebuilding the budget sentinel, refresh the live engine style, and rebuild notify state in place.
- Changed `internal/host/budget.go` so the sentinel can be updated in place and keep its in-flight state instead of being recreated.
- Added a focused regression test in `internal/host/general_config_test.go` that checks persisted settings, preserved budget state, unchanged usage callbacks, and live engine style refresh.
- Tests run: `docker run --rm -v /mnt/Data/AI/ainovel-cli:/src -w /src golang:1.25 go test ./internal/host -v`
- Commit: `92d497e`
- Remaining concerns: none beyond the unrelated untracked plan file already present in the working tree.

## Second fix report
- Simplified general-settings runtime behavior to avoid unsafe partial live updates: `ConfigureGeneralSettings` now persists and updates `h.cfg`, rebuilds the host-level budget policy from the new config, and refreshes the notifier pointer under `h.mu` without mutating `engine.style` or `engine.budget`.
- Reverted `BudgetSentinel` to the original simple monotonic state machine and removed in-place policy updates that preserved stale warn/stop state.
- Added tests for preserving provider config, rebuilding budget policy state, and disabling budget.
- Replaced direct `h.notifier.Send` call sites in host callbacks with `h.sendNotification`, which snapshots the notifier pointer under `h.mu` before sending.
- Tests run: `docker run --rm -v /mnt/Data/AI/ainovel-cli:/src -w /src golang:1.25 go test ./internal/host -v`
- Commit: pending.
- Remaining concerns: Subagent limit was reached, so this second fix was implemented directly in the main session rather than by another subagent.
