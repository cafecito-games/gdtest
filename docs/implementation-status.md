# Implementation status

This file maps the delivery milestones and acceptance criteria to implementation and verification
evidence. It deliberately distinguishes locally verified behavior from incomplete or externally
blocked release work.

## Milestones

| Milestone | Status | Implementation and evidence |
| --- | --- | --- |
| 1. Foreground vertical slice | Verified | Go 1.26 module, Cobra CLI, backend-neutral plans/events, gdparser discovery, typed GDScript runtime, authenticated Godot worker, streaming terminal/JSONL reporters, canonical quality script, and real passing/failing Godot fixtures. `scripts/check` and the real-Godot protocol/API tests pass on the current revision. |
| 2. Persistent daemon | Verified | Build-isolated daemon namespace, locking/auto-start, client streaming/cancellation, persistent worker commands, status/sessions/logs/stop/doctor, bounded output capture, and crash replacement. Integration tests reuse one PID and replace only the canceled/crashed lease. |
| 3. Worktrees and scheduler | Verified | Canonical Git worktree identity, isolated sessions, fair bounded scheduling, four isolation policies, per-file concurrency, global worker bound, LRU/idle/run/age/memory recycling, and multi-worktree stress coverage. Memory measurement failure conservatively recycles the worker. |
| 4. Discovery and generations | Verified with ADR | Static and dynamic discovery, stable IDs/locations, selectors, dependency analysis, atomic content-addressed caches, authoritative reconciliation, stale events, and watch mode are implemented. Any source-generation change replaces the worker instead of attempting unverifiable in-process reload; see ADR 0005. Real Godot generation and dynamic-discovery tests pass. |
| 5. Selection and migration | Verified | Engine-completed selection, dynamic-only files, path/directory/glob/name/focus selectors, conservative affected selection, and gdparser-span Vest migration with atomic writes and precise unsupported-context findings. Migration goldens, a mechanically migrated real-Godot fixture, and the 682-file Uzir migration audit pass. |
| 6. Hardening and packaging | In progress | Runtime errors, built-in assertions, cooperative and non-yielding case timeouts, language-neutral protocol fixtures, strict void/todo/skip policies, memory-bound stress, complete assertion-failure conformance, client reconnect, structured lifecycle logs, exact package-tree verification, and the specified performance corpora are implemented and verified. The remaining completion work is the final requirement-by-requirement audit and any gaps it exposes; Windows is explicitly deferred in ADR 0004. |

All Go tests use Testify assertions. The CLI is implemented with Cobra v1.10.2.

## Acceptance criteria

| Criterion | Status | Evidence or remaining work |
| --- | --- | --- |
| 1. Versioned gpm release install | Externally pending | Local staged release installation is verified; publishing and consuming a real GitHub release is prohibited in this session. |
| 2. Warm worker reuse | Verified | Separate daemon requests reuse one authenticated worker PID without startup on the second run's critical path. |
| 3. Case streaming and linear serialization | Verified | Immutable events stream and are folded once. Size-swept 1,000/5,000/10,000 serialization and rich-failure benchmarks, deterministic encoded-size linearity tests, and real 10,000-assertion/10,000-case corpora pass. |
| 4. Conflicting worktree isolation | Verified | Real dirty linked-worktree integration observes each worktree's own source and stable path-relative IDs. |
| 5. Global worker bound | Verified | Scheduler and service stress tests enforce the configured process bound, including 24 concurrent clients under an eight-worker/96 MiB memory budget and per-file scheduling. Memory measurement failures conservatively recycle workers. |
| 6. Generations and stale results | Verified | Source changes advance generations, active results become stale, obsolete plans are rejected, and replacements load changed bodies. |
| 7. Actionable infrastructure errors | Verified | Crash, cancellation, missing Godot, parse, stale generation, protocol failures, runtime GDScript errors, built-in `assert`, and case timeouts are distinct and source located. Recoverable script errors preserve the worker; timeouts taint and replace only the affected worker. |
| 8. Cheap passing assertions | Verified | Rich values are emitted only for failures. The real-Godot assertion corpus covers every assertion's passing, failing, custom-message, and applicable invalid-input paths without rich payloads on passing assertions. |
| 9. Vest migration safety | Verified | Goldens cover safe rewrites, both Vest provider-marker forms, idempotence, context-sensitive unsupported findings, and execution of migrated output. The supported Uzir slice runs 7,018 migrated tests without failures. |
| 10. Required quality gates | Verified for current revision | The canonical full check and real-Godot suite pass. They will be rerun after each remaining hardening change. |
| 11. Consumer worktree cleanliness | Verified | Init/gpm ownership and scratch-install tests pass. Uzir was migrated only in the isolated `local/gdtest-dogfood` worktree; its main worktree remained clean. |
| 12. Backend-neutral executor boundary | Verified | `internal/executor` consumes serializable plans/events independently of Godot and reporters. |
| 13. Reproducible sliced package | Verified | Core plus four macOS/Linux slices stage reproducibly; host-only and all-platform scratch installs pass after removal of unvalidated Windows slices. |
| 14. Atomic host-tool installation | Locally implemented; release pending | Sibling `godot-addon-manager` branch `feat/host-tool-platforms`, commit `80a6244`, implements atomic `platforms = []`; a released gpm containing it does not yet exist. |
| 15. Canonical local/CI policy | Verified for current revision | `scripts/check`, prek, and CI share the required gates, select shipped runtime files, and deliberate malformed fixtures prove the gates fail. |

## Current verification evidence

Completed on 2026-11-04 for the current hardening batch:

- Focused `go test -race` runs cover app policy, reconnect, model serialization, protocol fixtures,
  reporting, result folding, scheduler/service memory stress, runtime classification, assertion
  conformance, and integration lifecycle behavior.
- `gdkit lint check --minimum-version 0.10.0 .` selected 25 files with no diagnostics.
- `gdkit format check --minimum-version 0.10.0 --diff .` selected 25 files.
- `gdkit arch check --minimum-version 0.10.0 .` selected all 7 shipped runtime files and 39
  dependencies.
- `gdkit uid check --minimum-version 0.10.0 .` selected 25 files.
- `scripts/check` passed, including golangci-lint (0 issues), the complete race suite, vet, build,
  all strict gdkit gates, and deliberate gate-failure fixtures.
- `scripts/package-addon 0.0.0-hardening` and `scripts/test-package-addon 0.0.0-hardening` passed
  with reproducible core/macOS/Linux slices, exact selected-tree reassembly, version handshakes,
  clean first Godot import, and host-only/default/all-platform scratch installs.
- `GDTEST_PERFORMANCE_SAMPLES=5 scripts/benchmark` passed the size sweeps, 64-session scheduler,
  and real-Godot 10,000-assertion/10,000-dynamic-case corpora.

## Uzir customer dogfood

The first-customer audit used `/Users/christian/CafecitoGames/uzir-gdtest-dogfood` on the local
`local/gdtest-dogfood` branch. Nothing was pushed and the Uzir main worktree remained clean.

- The source inventory contained 682 Vest files. Migration check produced 2,614 source-located
  findings: 1,591 utility rewrites, 682 base-class rewrites, 138 hook-type rewrites, three
  parameter-provider rewrites, 93 inherited-name conflicts, 91 signal-capture findings, and 16
  invalid utility-context findings.
- Fifty-four files contain an intentionally unsupported construct and remain manual migration
  work. The other 628 files are the mechanically supported corpus.
- A follow-up scope audit found that 34 of the 93 inherited-name findings are nested-class false
  positives. The 16 utility-context findings are all nested `Vest.get_tree()` calls, and the 91
  signal-capture findings are 45 simple non-persistent captures across only five files. These are
  migrator-precision or explicit-v1-API questions rather than evidence that the tests are
  unexecutable; the next migration pass is reducing the manual set conservatively.
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
