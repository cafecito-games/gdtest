# Implementation status

This file maps each delivery milestone and acceptance criterion to concrete implementation and
verification evidence. Commands are recorded only after they have completed successfully.

## Milestones

| Milestone | Status | Implementation | Verification evidence |
| --- | --- | --- | --- |
| 1. Foreground vertical slice | Complete | Go CLI and backend-neutral model; gdparser source-ordered discovery; safe embedded `init`; authenticated versioned Godot worker; typed runtime assertions/hooks/helpers; streamed terminal and JSONL reporting; passing/failing fixtures. | `scripts/check` passed 2026-11-04: golangci-lint 0 issues; `go test -race ./...` including real Godot E2E; vet/build; gdkit lint clean; format selected 8 files; architecture selected 6 files/15 dependencies; UID selected 8 files. Manual failing fixture emitted `res://fixtures/godot/basic/failing.test.gd:7` and exit 1. |
| 2. Persistent daemon | Complete | Build/protocol-isolated user daemon; secure Unix socket, metadata and locking; auto-start client; streamed terminal responses; persistent authenticated Godot process with ping/reset/reload/cancel/shutdown; session warm/status/sessions/logs/stop and doctor handshake; crash replacement. | `scripts/check` passed 2026-11-04 with race tests and 9 selected GDScript files. Real Godot persistent-worker integration reused one PID across two runs; cancellation killed its exact lease and a replacement authenticated. Separate CLI invocations reported the same PID with `runs: 2`. Timed cold run 0.63 s and warm run 0.03 s. |
| 3. Worktrees and scheduler | Complete | Canonical Git common-dir/worktree identity; build/config/Godot-aware session keys; deterministic fair scheduler with global capacity, bounded interactive priority, background cap, cancellation, generation invalidation and exclusive/fresh policies; globally bounded retained workers with LRU eviction, run recycling and disappearance/idle reconciliation. | `scripts/check` passed 2026-11-04. Scheduler race tests stress 64 sessions at capacity 4. Real Godot/Git integration ran conflicting dirty linked worktrees concurrently with stable IDs, then six sessions under a two-worker bound, and removed one worktree without harming the other. |
| 4. Discovery and generations | Pending | — | — |
| 5. Selection and migration | Pending | — | — |
| 6. Hardening and packaging | Pending | — | — |

## Acceptance criteria

A green unit test alone is not used as evidence for an end-to-end criterion.

| Criterion | Status | Evidence |
| --- | --- | --- |
| 3. Case-streaming and linear serialization | Partially verified | Worker emits immutable events and the Go collector folds each event once; protocol fixtures and Godot E2E cover the stream. Stress corpus remains milestone 6. |
| 2. Warm worker reuse | Verified | Two separate daemon clients ran the fixture through the same worker PID; session run count advanced to 2 and the measured warm invocation was 0.03 s. |
| 4. Conflicting worktree isolation | Verified | A real linked-worktree integration test observed pass in dirty worktree A and the source-specific `from-worktree-b` failure in dirty worktree B; both used the same project-relative stable ID. |
| 5. Global worker bound | Verified | Deterministic 64-session scheduler stress and real six-worktree Godot stress never exceeded their configured bounds; retained idle workers are independently capped. |
| 7. Distinct actionable infrastructure errors | Partially verified | Protocol mismatch, authentication, project mismatch, missing Godot, source parse errors, cancellation, and crash-with-last-case paths have focused tests. Broader generation and bootstrap diagnostics remain later milestones. |
| 8. Cheap passing assertions | Implemented | Passing assertion path increments compact counters/status only; rich wire values are created solely in the failure callback. Performance corpus remains milestone 6. |
| 10. Required quality gates | Partially verified | All milestone-1 applicable gates are green locally, including race-enabled Godot E2E. Packaging gates arrive in milestone 6. |
| 12. Backend-neutral execution boundary | Implemented | `internal/executor` accepts serializable `model.TestPlan` and `EventSink`; worker and reporters depend on that boundary, with unit coverage. |
| 15. Canonical local/CI policy | Partially verified | `scripts/check`, prek, and CI share the same command; it selects actual shipped sources. Deliberate gate-failure fixtures remain milestone 6. |
