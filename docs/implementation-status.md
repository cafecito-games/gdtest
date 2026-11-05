# Implementation status

This file maps the delivery milestones and acceptance criteria to implementation and verification
evidence. It deliberately distinguishes locally verified behavior from incomplete or externally
blocked release work.

## Milestones

| Milestone | Status | Implementation and evidence |
| --- | --- | --- |
| 1. Foreground vertical slice | Verified | Go 1.26 module, Cobra CLI, backend-neutral plans/events, gdparser discovery, typed GDScript runtime, authenticated Godot worker, streaming terminal/JSONL reporters, canonical quality script, and real passing/failing Godot fixtures. `scripts/check` and the real-Godot protocol/API tests pass on the current revision. |
| 2. Persistent daemon | Verified | Build-isolated daemon namespace, locking/auto-start, client streaming/cancellation, persistent worker commands, status/sessions/logs/stop/doctor, bounded output capture, and crash replacement. Integration tests reuse one PID and replace only the canceled/crashed lease. Multi-file requests retain a primary worker, use bounded command-scoped extra lanes, and serialize the first project bootstrap. Warm discovery obtains a reconciled generation without pinging or creating a worker, normal identity checks do not launch a second Godot process, and build identity is computed once per CLI process. |
| 3. Worktrees and scheduler | Verified | Canonical Git worktree identity, isolated sessions, fair bounded scheduling, four isolation policies, per-file concurrency, global worker bound, LRU/idle/run/age/memory recycling, and multi-worktree stress coverage. Each process has opaque private `user://` storage removed on exit; memory measurement failure conservatively recycles the worker. See ADR 0006. |
| 4. Discovery and generations | Verified with ADR | Static and dynamic discovery, stable IDs/locations, selectors, dependency analysis, reusable parsed-AST/static/dependency/dynamic artifacts, authoritative reconciliation, stale events, and watch mode are implemented. Any source-generation change replaces the worker instead of attempting unverifiable in-process reload; see ADR 0005. Real Godot generation and dynamic-discovery tests pass. |
| 5. Selection and migration | Verified | Engine-completed selection, dynamic-only files, path/directory/glob/name/focus selectors, conservative affected selection, and gdparser-span Vest migration with atomic writes and precise unsupported-context findings. Nested-class scope, nested SceneTree access, inherited-name warning annotations, migration goldens, a mechanically migrated real-Godot fixture, and the complete 682-file Uzir migration audit pass. |
| 6. Hardening and packaging | In progress | Runtime errors, built-in assertions, cooperative and non-yielding case timeouts, language-neutral protocol fixtures, strict void/todo/skip policies, memory-bound stress, complete assertion-failure conformance, client reconnect, level-filtered bounded lifecycle logs, exact package-tree verification, pinned Linux Godot end-to-end CI, and the specified performance corpora are implemented and verified. The remaining completion work is the final requirement-by-requirement audit and any gaps it exposes; Windows is explicitly deferred in ADR 0004. |

All Go tests use Testify assertions. The CLI is implemented with Cobra v1.10.2.

## Acceptance criteria

| Criterion | Status | Evidence or remaining work |
| --- | --- | --- |
| 1. Versioned gpm release install | Externally pending | Local staged release installation is verified; publishing and consuming a real GitHub release is prohibited in this session. |
| 2. Warm worker reuse | Verified | Separate daemon requests reuse one authenticated worker PID without startup on the second run's critical path. Multi-file requests reuse bounded lanes rather than launching one process per file. |
| 3. Case streaming and linear serialization | Verified | Immutable events stream and are folded once. Size-swept 1,000/5,000/10,000 serialization and rich-failure benchmarks, deterministic encoded-size linearity tests, and real 10,000-assertion/10,000-case corpora pass. |
| 4. Conflicting worktree isolation | Verified | Real dirty linked-worktree integration observes each worktree's own source and stable path-relative IDs. |
| 5. Global worker bound | Verified | Scheduler and service stress tests enforce the configured process bound, including 24 concurrent clients under an eight-worker/96 MiB memory budget and per-file scheduling. Multi-file lane tests cover serialized first bootstrap, lazy expansion, primary reuse, and per-lane maximum-run recycling. Memory measurement failures conservatively recycle workers. |
| 6. Generations and stale results | Verified | Source changes advance generations, active results become stale, obsolete plans are rejected, and replacements load changed bodies. |
| 7. Actionable infrastructure errors | Verified | Crash, cancellation, missing Godot, parse, stale generation, protocol failures, runtime GDScript errors, built-in `assert`, and case timeouts are distinct and source located. Recoverable script errors preserve the worker; timeouts taint and replace only the affected worker. |
| 8. Cheap passing assertions | Verified | Rich values are emitted only for failures. The real-Godot assertion corpus covers every assertion's passing, failing, custom-message, and applicable invalid-input paths without rich payloads on passing assertions. |
| 9. Vest migration safety | Verified | Goldens cover safe rewrites, both Vest provider-marker forms, idempotence, nested-class scope, strict inherited-name warnings, and execution of migrated output. The mechanically supported Uzir slice runs 8,055 tests; all 8,238 tests pass after a project-local native bridge for the deliberately deferred signal-capture API. |
| 10. Required quality gates | Verified for current revision | The canonical full check and real-Godot suite pass. They will be rerun after each remaining hardening change. |
| 11. Consumer worktree cleanliness | Verified | Init/gpm ownership and scratch-install tests pass. Uzir was migrated only in the isolated `local/gdtest-dogfood` worktree; its main worktree remained clean. |
| 12. Backend-neutral executor boundary | Verified | `internal/executor` consumes serializable plans/events independently of Godot and reporters. |
| 13. Reproducible sliced package | Verified | Core plus four macOS/Linux slices stage reproducibly; host-only and all-platform scratch installs pass after removal of unvalidated Windows slices. |
| 14. Atomic host-tool installation | Locally implemented; release pending | Sibling `godot-addon-manager` branch `feat/host-tool-platforms`, commit `80a6244`, implements atomic `platforms = []`; a released gpm containing it does not yet exist. |
| 15. Canonical local/CI policy | Verified for current revision | `scripts/check`, prek, and CI share the required gates, select shipped runtime files, and deliberate malformed fixtures prove the gates fail. |

## Current verification evidence

Completed on 2026-11-05 for the current hardening batch:

- Focused `go test -race` runs cover app policy, reconnect, model serialization, protocol fixtures,
  reporting, result folding, scheduler/service memory stress, runtime classification, assertion
  conformance, and integration lifecycle behavior.
- `gdkit lint check --minimum-version 0.10.0 .` selected 27 files with no diagnostics.
- `gdkit format check --minimum-version 0.10.0 --diff .` selected 27 files.
- `gdkit arch check --minimum-version 0.10.0 .` selected all 7 shipped runtime files and 39
  dependencies.
- `gdkit uid check --minimum-version 0.10.0 .` selected 27 files.
- `scripts/check` passed, including golangci-lint (0 issues), the complete race suite, vet, build,
  all strict gdkit gates, and deliberate gate-failure fixtures.
- `scripts/package-addon 0.0.0-hardening` and `scripts/test-package-addon 0.0.0-hardening` passed
  with reproducible core/macOS/Linux slices, exact selected-tree reassembly, version handshakes,
  clean first Godot import, and host-only/default/all-platform scratch installs.
- `scripts/package-addon 0.0.0-audit` and `scripts/test-package-addon 0.0.0-audit` additionally
  prove that a cross-compiled release binary's standalone `gdtest init` installs its exact embedded
  runtime version and completes a real worker handshake; the development version cannot leak into
  a release installation.
- `GDTEST_PERFORMANCE_SAMPLES=5 GDTEST_LATENCY_SAMPLES=30 scripts/benchmark` passed the size
  sweeps, 64-session scheduler, real-Godot 10,000-assertion/10,000-dynamic-case corpora, and the
  real local-daemon latency corpus. Ready-daemon median/p95 were 0.057/0.224 ms; direct warm
  daemon/worker median/p95 were 13.8/23.6 ms; and complete Cobra CLI median/p95 were 27.9/34.5 ms.
  All are below the tracked 5 ms and 50/100 ms objectives. The full-CLI corpus measures process startup, project and
  configuration identity, both discovery phases, reporting, and the daemon/worker request rather
  than presenting the direct request as end-to-end command latency. A prior run under unrelated
  Swift compiler saturation observed 110.5/168.0 ms full-CLI median/p95 and remains disclosed as a
  contended diagnostic rather than replacing the quiet reference.
- The current migration regression fixture executes three real-Godot cases after rewriting nested
  `Vest.get_tree()` access and strict inherited-name conflicts.
- Parsed GDScript AST artifacts are versioned by tool/parser identity and relative path plus source
  content, reject corrupt payloads, publish atomically through the shared cache, and are reused when
  aggregate discovery configuration changes. Static manifests retain per-script metadata and
  dependency fragments; compatible dynamic manifests remain separately generation-rebound. The
  focused race tests and `scripts/check` pass. Two consecutive foreground discoveries of all 682
  Uzir files emitted identical 8,238-case manifests; the persisted cache-hit run took 4.12 seconds
  and did not invoke Godot.
- User `log_level` now filters the bounded JSONL daemon log at write time. Lifecycle failures and
  janitor/panic diagnostics carry error severity, protocol rejection is warning severity, and
  debug-only worker reuse/discovery records remain available without weakening error retention.
- The real-Godot helper conformance fixture proves `until(..., 0.0)` has no internal deadline and
  that negative or non-finite sleep durations, polling timeouts, and polling intervals produce
  source-located framework-error outcomes without poisoning the reusable worker.
- Build identity is hashed once per command, executable metadata identifies Godot without invoking
  `godot --version` on each run, and a strict `session.generation` RPC reconciles source state
  without starting or pinging a worker. Focused race tests cover source advancement, zero worker
  creation, strict decoding, and concurrent generation lookup during an active run. Doctor still
  executes and reports the human-readable Godot version.
- Generation reconciliation hashes current file contents but reuses parsed structural state only
  for exact path-and-content matches. Index and manifest callers that need only the content
  fingerprint do not parse GDScript. Focused race tests cover reuse, invalidation, and malformed
  content on the hash-only path.
- Multi-file execution now uses a bounded lane pool instead of one Godot process per file. The
  primary worker completes initial bootstrap before extra lanes start and remains reusable across
  commands; extras are command-scoped. Real-Godot reset coverage proves that the startup
  `AudioBusLayout` is restored, and each process uses an opaque private `user://` directory that is
  removed on exit and failed startup. See ADR 0006.
- Pooled multi-file responses and their immutable terminal event now retain phase-separated
  critical-path timings for scheduler wait, worker acquisition/startup, script load and dynamic
  definition, hooks and cases, serialization, reset, and total daemon time. A repeated race test
  covers concurrent aggregation without sharing mutable event metadata.
- `prek run --all-files` passes on `main`; the standard whitespace hooks leave the two authoritative
  specification files byte-identical, and the hook configuration no longer contradicts the
  required cohesive local milestone commits.
- `scripts/package-addon 0.0.0-final-audit` and
  `scripts/test-package-addon 0.0.0-final-audit` pass. The isolated sibling gpm commit `80a6244`
  also passes gofmt, module-tidy diff, vet, build, golangci-lint, the complete race suite, and
  `prek run --all-files`; released gpm 0.5.1 still lacks `--host-tool`.
- `scripts/check`, `prek run --all-files`, `scripts/package-addon
  0.0.0-worker-pool-audit`, and `scripts/test-package-addon
  0.0.0-worker-pool-audit` pass after the bounded worker-pool and isolation changes. The package
  tests include reproducibility, exact slice reassembly, same-version runtime handshakes, and
  host-only, default `platforms = []`, and all-platform installations.
- A pristine Linux arm64 container installed checksum-pinned stock Godot
  `4.7.2.stable.official.ed1daf0bf` and passed the race-enabled integration, shared protocol, and
  worker/API suites. It exposed and fixed a protocol-fixture dependency on a pre-existing global
  class cache. The same container installed the checksum-pinned gpm 0.5.1 release binary and
  passed `scripts/test-package-addon 0.0.0-linux-arm64-audit`, including execution of the packaged
  Linux arm64 host binary. CI and release workflows now use those verified release installers;
  the prior `go install` command produced `gpm version dev` and could not satisfy the packaging
  version gate. The x86-64 installer paths independently verified both release-asset digests and
  exact Godot/gpm version output under Linux emulation. CI installs gdkit from its pinned v0.10.0
  module and rejects any different reported version. `actionlint` also passes for both workflows.
- Packaged-install verification runs `doctor` from the project-local host executable under an
  isolated home and requires its project, engine, runtime, import-cache, daemon namespace/status,
  worker, and static/completed discovery fields before stopping the exact release daemon. This
  exposed the Unix socket limit for long cache paths; bounded hashed fallback sockets now retain
  private permissions and deterministic namespace identity, with real server/race coverage and
  successful macOS and Linux arm64 package runs. See ADR 0007.

## Uzir customer dogfood

The first-customer audit used `/Users/christian/CafecitoGames/uzir-gdtest-dogfood` on the local
`local/gdtest-dogfood` branch. Nothing was pushed and the Uzir main worktree remained clean.

- The source inventory contains 682 Vest files. The current migration check produces 2,580
  source-located findings: 1,607 utility rewrites, 682 base-class rewrites, 138 hook-type
  rewrites, three parameter-provider rewrites, 59 inherited-name warning rewrites, and 91 precise
  signal-capture findings.
- The historical “54 unsupported files” were 54 unique files across overlapping categories. The
  41 files with 93 inherited-name findings included 34 nested-scope false positives that are now
  fixed; the remaining 59 genuine conflicts across 25 files receive mechanical warning
  annotations. Eleven files contained 16 nested `Vest.get_tree()` calls that now rewrite
  mechanically. Five files contained the only deliberately unsupported runtime feature: 45
  non-persistent signal captures and 46 reads.
- Nested classes no longer produce inherited-name false positives. All 16 nested
  `Vest.get_tree()` calls migrate to a typed active `SceneTree`, and the 59 genuine local,
  parameter, loop, and lambda conflicts receive statement-scoped Godot warning annotations.
- Only five files remain intentionally non-mechanical because `api-spec.md` deliberately defers
  Vest signal capture. They contain 45 simple non-persistent captures and 91 total calls/findings.
  The other 677 files are the mechanically supported corpus.
- Foreground discovery emitted 8,055 stable cases from those 677 files. The manifest is larger
  than the old 64 KiB decoder buffer and supplied the regression corpus for bounded fragmented
  JSONL decoding.
- The supported corpus completed with `8055 passed, 0 failed, 0 errors, 0
  skipped, 0 todo, 0 void, 0 canceled` and no stderr output.
- An Uzir-local typed base implemented the five files' existing capture calls with native Godot
  `Signal` connections, without changing gdtest's v1 API. The combined 682-file run completed
  with `8238 passed, 0 failed, 0 errors, 0 skipped, 0 todo, 0 void, 0 canceled`. Its only stderr
  was Uzir's pre-existing unavailable macOS iOSAuthenticationKit extension and disabled Sentry
  startup messages.
- After bounded worker pooling and isolation hardening, a fresh four-lane run completed with
  `8238 passed, 0 failed, 0 errors, 0 skipped, 0 todo, 0 void, 0 canceled` in 96.13 seconds. A
  same-configuration selected follow-up reused PID 46584 and completed in 3.48 seconds. Four
  distinct worker data directories were observed during the corpus and none remained after daemon
  shutdown.
- Strict customer-project checks then passed: gdkit lint, canonical formatting over 2,270 files,
  architecture over 2,274 files and 45,910 dependencies, and UID identity over 2,274 files.
- The audit found and fixed full-import bootstrap termination, large protocol messages,
  non-instantiable-script diagnostics, foreground `list`, provider markers in the first default
  argument, inherited API name conflicts, nested-class utility rewrites, process-per-file startup,
  concurrent cold-import races, shared `user://` corruption, and leaked `AudioServer` state.

The converted customer worktree remains an isolated, uncommitted experiment. The only
non-mechanical source is its documented project-local bridge for the five signal-capture files;
the Uzir main worktree remains clean and nothing was pushed.
