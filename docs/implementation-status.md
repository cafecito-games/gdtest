# Implementation status

This file maps the delivery milestones and acceptance criteria to implementation and verification
evidence. It deliberately distinguishes locally verified behavior from incomplete or externally
blocked release work.

## Milestones

| Milestone | Status | Implementation and evidence |
| --- | --- | --- |
| 1. Foreground vertical slice | Verified | Go 1.26 module, Cobra CLI, backend-neutral plans/events, gdparser discovery, typed GDScript runtime, authenticated Godot worker, streaming terminal/JSONL reporters, canonical quality script, and real passing/failing Godot fixtures. `scripts/check` passed before the current hardening batch; focused real-Godot protocol/API tests and all strict gdkit gates passed again on 2026-11-04. |
| 2. Persistent daemon | Verified | Build-isolated daemon namespace, locking/auto-start, client streaming/cancellation, persistent worker commands, status/sessions/logs/stop/doctor, bounded output capture, and crash replacement. Integration tests reuse one PID and replace only the canceled/crashed lease. |
| 3. Worktrees and scheduler | Verified | Canonical Git worktree identity, isolated sessions, fair bounded scheduling, four isolation policies, per-file concurrency, global worker bound, LRU/idle/run/age/memory recycling, and multi-worktree stress coverage. Memory measurement failure conservatively recycles the worker. |
| 4. Discovery and generations | Verified with ADR | Static and dynamic discovery, stable IDs/locations, selectors, dependency analysis, atomic content-addressed caches, authoritative reconciliation, stale events, and watch mode are implemented. Any source-generation change replaces the worker instead of attempting unverifiable in-process reload; see ADR 0005. Real Godot generation and dynamic-discovery tests pass. |
| 5. Selection and migration | Verified | Engine-completed selection, dynamic-only files, path/directory/glob/name/focus selectors, conservative affected selection, and gdparser-span Vest migration with atomic writes and precise unsupported-context findings. Migration goldens, a mechanically migrated real-Godot fixture, and the 682-file Uzir migration audit pass. |
| 6. Hardening and packaging | In progress | Bootstrap retry, protocol/message bounds, typed wire values, lifecycle policies, documentation, binary-free core, four validated macOS/Linux slices, reproducible staging, and sibling gpm host-tool support are implemented. Remaining local blockers include full runtime-error/built-in-assert/case-timeout classification, language-neutral protocol fixtures, strict CLI policies for void/todo/skip, memory-bound stress, complete API conformance, and the specified performance evidence. Windows is explicitly deferred in ADR 0004. |

All Go tests use Testify assertions. The CLI is implemented with Cobra v1.10.2.

## Acceptance criteria

| Criterion | Status | Evidence or remaining work |
| --- | --- | --- |
| 1. Versioned gpm release install | Externally pending | Local staged release installation is verified; publishing and consuming a real GitHub release is prohibited in this session. |
| 2. Warm worker reuse | Verified | Separate daemon requests reuse one authenticated worker PID without startup on the second run's critical path. |
| 3. Case streaming and linear serialization | Partially verified | Immutable events stream and are folded once; the final specified performance corpus remains. |
| 4. Conflicting worktree isolation | Verified | Real dirty linked-worktree integration observes each worktree's own source and stable path-relative IDs. |
| 5. Global worker bound | Verified | Scheduler and service stress tests enforce the configured process bound, including per-file scheduling. A dedicated memory-bound stress test remains under milestone 6. |
| 6. Generations and stale results | Verified | Source changes advance generations, active results become stale, obsolete plans are rejected, and replacements load changed bodies. |
| 7. Actionable infrastructure errors | Partially verified | Crash, cancellation, missing Godot, parse, stale generation, and protocol failures are distinct. Runtime GDScript errors, built-in `assert`, and case timeouts still need complete case-error classification. |
| 8. Cheap passing assertions | Partially verified | Rich values are emitted only for failures; the final executable assertion corpus remains. |
| 9. Vest migration safety | Verified | Goldens cover safe rewrites, both Vest provider-marker forms, idempotence, context-sensitive unsupported findings, and execution of migrated output. The supported Uzir slice runs 7,018 migrated tests without failures. |
| 10. Required quality gates | Verified for current revision | The canonical full check and real-Godot suite pass. They will be rerun after each remaining hardening change. |
| 11. Consumer worktree cleanliness | Verified | Init/gpm ownership and scratch-install tests pass. Uzir was migrated only in the isolated `local/gdtest-dogfood` worktree; its main worktree remained clean. |
| 12. Backend-neutral executor boundary | Verified | `internal/executor` consumes serializable plans/events independently of Godot and reporters. |
| 13. Reproducible sliced package | Verified | Core plus four macOS/Linux slices stage reproducibly; host-only and all-platform scratch installs pass after removal of unvalidated Windows slices. |
| 14. Atomic host-tool installation | Locally implemented; release pending | Sibling `godot-addon-manager` branch `feat/host-tool-platforms`, commit `80a6244`, implements atomic `platforms = []`; a released gpm containing it does not yet exist. |
| 15. Canonical local/CI policy | Verified for current revision | `scripts/check`, prek, and CI share the required gates, select shipped runtime files, and deliberate malformed fixtures prove the gates fail. |

## Current verification evidence

Completed on 2026-11-04 for the current hardening batch:

- `go test -race ./internal/app ./internal/worker` focused CLI/output tests.
- `go test -race ./internal/worker ./internal/service -count=1`.
- `go test -race ./internal/integration -count=1 -v -timeout=3m`, including fresh checkout,
  migration, generation replacement, cancellation, exact selection, typed values, and worktree tests.
- `gdkit lint check --minimum-version 0.10.0 .` selected 21 files with no diagnostics.
- `gdkit format check --minimum-version 0.10.0 --diff .` selected 21 files.
- `gdkit arch check --minimum-version 0.10.0 .` selected 6 files and 34 dependencies.
- `gdkit uid check --minimum-version 0.10.0 .` selected 21 files.
- `scripts/check` passed, including golangci-lint (0 issues), the complete race suite, vet, build,
  all strict gdkit gates, and deliberate gate-failure fixtures.
- `scripts/package-addon 0.0.0-audit` and `scripts/test-package-addon 0.0.0-audit` passed with
  reproducible core/macOS/Linux slices and host-only/all-platform scratch installs.

## Uzir customer dogfood

The first-customer audit used `/Users/christian/CafecitoGames/uzir-gdtest-dogfood` on the local
`local/gdtest-dogfood` branch. Nothing was pushed and the Uzir main worktree remained clean.

- The source inventory contained 682 Vest files. Migration check produced 2,614 source-located
  findings: 1,591 utility rewrites, 682 base-class rewrites, 138 hook-type rewrites, three
  parameter-provider rewrites, 93 inherited-name conflicts, 91 signal-capture findings, and 16
  invalid utility-context findings.
- Fifty-four files contain an intentionally unsupported construct and remain manual migration
  work. The other 628 files are the mechanically supported corpus.
- Foreground discovery emitted 7,018 stable cases from those 628 files. The manifest is larger
  than the old 64 KiB decoder buffer and supplied the regression corpus for bounded fragmented
  JSONL decoding.
- The supported corpus completed in about 58 seconds with `7018 passed, 0 failed, 0 errors, 0
  skipped, 0 todo, 0 void, 0 canceled` and no stderr output.
- The audit found and fixed full-import bootstrap termination, large protocol messages,
  non-instantiable-script diagnostics, foreground `list`, provider markers in the first default
  argument, inherited API name conflicts, and nested-class utility rewrites.

The converted customer worktree is deliberately not committed: its 54 manual files still contain
unsupported Vest APIs, so committing the full conversion would present an incomplete migration as
usable source.
