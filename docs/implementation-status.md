# Implementation status

This file maps the delivery milestones and acceptance criteria to implementation and verification
evidence. It deliberately distinguishes implementation claims from the concrete local, CI, and
published-release evidence that verifies them.

## Milestones

| Milestone | Status | Implementation and evidence |
| --- | --- | --- |
| 1. Foreground vertical slice | Verified | Go 1.26 module, Cobra CLI, backend-neutral plans/events, gdparser discovery, typed GDScript runtime, authenticated Godot worker, streaming terminal/JSONL reporters, canonical quality script, and real passing/failing Godot fixtures. `scripts/check` and the real-Godot protocol/API tests pass on the current revision. |
| 2. Persistent daemon | Verified | Build-isolated daemon namespace, locking/auto-start, client streaming/cancellation, persistent worker commands, status/sessions/logs/stop/doctor, bounded output capture, and crash replacement. Integration tests reuse one PID and replace only the canceled/crashed lease. Multi-file requests retain a primary worker, use bounded command-scoped extra lanes, and serialize the first project bootstrap. Warm discovery obtains a reconciled generation without pinging or creating a worker, normal identity checks do not launch a second Godot process, and build identity is computed once per CLI process. |
| 3. Worktrees and scheduler | Verified | Canonical Git worktree identity, isolated sessions, fair bounded scheduling, four isolation policies, per-file concurrency, global worker bound, LRU/idle/run/age/memory recycling, and multi-worktree stress coverage. Each process has opaque private `user://` storage removed on exit; memory measurement failure conservatively recycles the worker. See ADR 0006. |
| 4. Discovery and generations | Verified with ADR | Static and dynamic discovery, stable IDs/locations, selectors, dependency analysis, reusable parsed-AST/static/dependency/dynamic artifacts, authoritative reconciliation, stale events, and watch mode are implemented. Any source-generation change replaces the worker instead of attempting unverifiable in-process reload; see ADR 0005. Real Godot generation and dynamic-discovery tests pass. |
| 5. Selection and migration | Verified | Engine-completed selection, dynamic-only files, path/directory/glob/name/focus selectors, conservative affected selection, and gdparser-span Vest migration with atomic writes and precise unsupported-context findings. Nested-class scope, nested SceneTree access, inherited-name warning annotations, migration goldens, a mechanically migrated real-Godot fixture, and the complete 682-file Uzir migration audit pass. |
| 6. Hardening and packaging | Verified and released | Runtime errors, built-in assertions, cooperative and non-yielding case timeouts, language-neutral protocol fixtures, strict void/todo/skip policies, memory-bound stress, complete assertion-failure conformance, client reconnect, level-filtered bounded lifecycle logs, exact package-tree verification, pinned Linux Godot end-to-end CI, profiling, supported-engine validation, and the specified performance corpora are implemented and verified. gpm 0.6.0 and gdtest v0.1.1 are published, and a clean project installed and exercised the public release through the project-local host executable. Windows is explicitly deferred in ADR 0004. |

All Go tests use Testify assertions. The CLI is implemented with Cobra v1.10.2.

## Dedicated benchmark runner

Issue #6 adds the public `GDTestBenchmark` API and `gdtest bench` command without mixing
benchmark measurements into ordinary correctness tests. Static `*.bench.gd` discovery produces
backend-neutral benchmark plans; the existing daemon scheduler runs them on authenticated,
persistent workers with generation checks, cancellation, reset, and conservative worker
replacement. Warmups are discarded, hooks execute outside the measured region, and each sample
records normalized nanoseconds per iteration. Schema-versioned results retain raw samples,
deterministic min/median/p95/max statistics, source identity, generation/content identity, and
runtime/host metadata. Strict baseline decoding, stable-ID comparison, and atomic writes keep
historical measurements reviewable without introducing flaky performance thresholds.

Focused Go tests cover discovery and hook validation, statistics and event folding, protocol
fixtures, worker streaming/cancellation, session reuse/reset, CLI validation, and baseline
behavior. The basic example executes the command through real Godot, and packaged-addon
verification executes a fresh benchmark through the project-local launcher. A deterministic
fake-clock Godot test proves hook time is excluded, real-Godot tests cover lifecycle ordering,
runtime and hook errors, and cancellation, and service stress covers global bounds across distinct
worktree identities plus crash replacement and recycling. On 2026-11-05, `scripts/check`,
`scripts/package-addon 0.0.0-issue-6`, and `scripts/test-package-addon 0.0.0-issue-6` all passed;
the package checks used the repository's checksum-pinned gpm 0.6.0 installer.

## Acceptance criteria

| Criterion | Status | Evidence or remaining work |
| --- | --- | --- |
| 1. Versioned gpm release install | Verified | Published gpm 0.6.0 installed published gdtest v0.1.1 into a fresh project with the documented one-command `--host-tool` flow. The project-local binary reported 0.1.1, passed the three-case example with clean startup output, returned 1 with a source-located intentional failure, and left the committed consumer tree clean after reinstall. |
| 2. Warm worker reuse | Verified | Separate daemon requests reuse one authenticated worker PID without startup on the second run's critical path. Multi-file requests reuse bounded lanes rather than launching one process per file. |
| 3. Case streaming and linear serialization | Verified | Immutable events stream and are folded once. Size-swept 1,000/5,000/10,000 serialization and rich-failure benchmarks, deterministic encoded-size linearity tests, and real 10,000-assertion/10,000-case corpora pass. |
| 4. Conflicting worktree isolation | Verified | Real dirty linked-worktree integration observes each worktree's own source and stable path-relative IDs. |
| 5. Global worker bound | Verified | Scheduler and service stress tests enforce the configured process bound, including 24 concurrent clients under an eight-worker/96 MiB memory budget and per-file scheduling. Multi-file lane tests cover serialized first bootstrap, lazy expansion, primary reuse, and per-lane maximum-run recycling. Memory measurement failures conservatively recycle workers. |
| 6. Generations and stale results | Verified | Source changes advance generations, active results become stale, obsolete plans are rejected, and replacements load changed bodies. |
| 7. Actionable infrastructure errors | Verified | Crash, cancellation, missing Godot, parse, stale generation, protocol failures, runtime GDScript errors, built-in `assert`, and case timeouts are distinct and source located. Recoverable script errors preserve the worker; timeouts taint and replace only the affected worker. |
| 8. Cheap passing assertions | Verified | Rich values are emitted only for failures. The real-Godot assertion corpus covers every assertion's passing, failing, custom-message, and applicable invalid-input paths without rich payloads on passing assertions. |
| 9. Vest migration safety | Verified | Goldens cover safe rewrites, both Vest provider-marker forms, idempotence, nested-class scope, strict inherited-name warnings, and execution of migrated output. The mechanically supported Uzir slice runs 8,055 tests; all 8,238 tests pass after a project-local native bridge for the deliberately deferred signal-capture API. |
| 10. Required quality gates | Verified for released revision | Release run `42019524497` passed the canonical full check, real-Godot suite, and package verification at commit `ceffcf0`. Main branch protection strictly requires the GitHub Actions `check` and `package` contexts before merge. |
| 11. Consumer worktree cleanliness | Verified | Init/gpm ownership and scratch-install tests pass. Uzir was migrated only in the isolated `local/gdtest-dogfood` worktree; its main worktree remained clean. |
| 12. Backend-neutral executor boundary | Verified | `internal/executor` consumes serializable plans/events independently of Godot and reporters. |
| 13. Reproducible sliced package | Verified | Core plus four macOS/Linux slices stage reproducibly; host-only and all-platform scratch installs pass after removal of unvalidated Windows slices. |
| 14. Atomic host-tool installation | Released and verified end to end | gpm PR #58 merged implementation commit `80a6244` and transaction-test commit `67e8265`; gpm 0.6.0 atomically writes `platforms = []` and installs real packaged core plus only the current-host slice despite mobile/web project targets. Its release workflow, checksums, version output, and `--host-tool` help were verified against the published macOS arm64 artifact. The public gdtest v0.1.1 acceptance install contained only core plus `bin/macos-arm64/gdtest`. |
| 15. Canonical local/CI policy | Verified for released revision | `scripts/check`, prek, and CI share the required gates, select shipped runtime files, and deliberate malformed fixtures prove the gates fail. Main protection requires strict, up-to-date `check` and `package` results from GitHub Actions. |

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
- Worker handshakes now require numeric Godot 4.7 major/minor identity after the authenticated
  runtime/protocol handshake. Focused race tests reject Godot 4.6, 5.0, and malformed version
  fields, while the complete real-Godot suite passes on 4.7.2.
- Fresh-checkout bootstrap/import has its own one-shot `project_bootstrap_import` timing. Daemon
  and foreground discovery propagate it without replaying stale startup time from a reused worker
  or dynamic-discovery cache hit; the real fresh-checkout integration test requires the phase.
- `GDTEST_PROFILE_BENCHTIME=1x scripts/profile <temporary-directory>` produced valid CPU and heap
  profiles, the retained symbolizing test binary, environment and benchmark records, and top-30
  summaries for every specified synthetic performance corpus.
- The release workflow passes `actionlint`, runs the canonical source/race/real-engine/gdkit gate
  before packaging, and grants release-write permission only to the dependent packaging job.
- `scripts/package-addon 0.0.0-completion-audit` and
  `scripts/test-package-addon 0.0.0-completion-audit` passed after the final hardening changes,
  including byte-for-byte reproducibility, exact slice contents, repeat-install cleanliness,
  host-only/default/all-platform scratch installs, and an installed 4.7.2 worker handshake.
- gpm PR #58 passed its build/test and lint jobs, merged at commit `7be106f`, and released as
  v0.6.0 by successful workflow run `42015313749`. The published macOS arm64 archive matched its
  checksum, reported `gpm version 0.6.0`, and exposed the exact `--host-tool` contract; the pinned
  Linux installer downloaded and executed both published x86-64 and arm64 archives in clean
  architecture-matched containers. `scripts/package-addon 0.0.0-gpm-0.6-audit` and
  `scripts/test-package-addon 0.0.0-gpm-0.6-audit` then passed with the released macOS binary.
- Release workflow run `42017841320` passed its quality and package jobs and published the initial
  gdtest v0.1.0 release. A subsequent clean public install exposed recovered Godot bootstrap parse
  diagnostics on the first run. PR #1 added a regression test and buffered pre-handshake process
  output so a successful bootstrap retry is quiet while genuine startup failures retain their
  bounded diagnostic tails. Both required PR checks passed, and squash commit `ceffcf0` contains
  the fix.
- Patch release run `42019524497`, invoked with only the `patch` choice, passed the canonical
  quality and package jobs and atomically published gdtest v0.1.1 from `ceffcf0`. The release has
  core plus macOS/Linux arm64/x86-64 slices and a gpm index. A fresh consumer downloaded the
  published gpm 0.6.0 macOS arm64 archive, used `gpm add ... --version v0.1.1 --host-tool`, and
  obtained `platforms = []`, the pure core, and only `bin/macos-arm64/gdtest`. The project-local
  executable reported 0.1.1; its first run printed only the successful three-case summary with no
  Godot banner or parse diagnostics. An intentional fixture returned exit 1 with
  `failing.test.gd:7`, and repeated installation left the committed consumer worktree clean.
- Classic main branch protection was read back through the GitHub API with `strict: true` and the
  GitHub Actions contexts `check` and `package` (app id 15368). Pull requests remain required, and
  force pushes and branch deletion remain disabled.
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
  `scripts/test-package-addon 0.0.0-final-audit` pass. The isolated sibling gpm implementation
  commit `80a6244` and test commit `67e8265` also pass gofmt, module-tidy diff, vet, build,
  golangci-lint, the complete race suite, and `prek run --all-files`. The command-level test uses
  a real reproducible slice package and verifies the documented one-command transaction, explicit
  empty addon platforms, host-only materialization, machine-independent complete lock pins, and
  rollback coverage; the commits are now available in released gpm 0.6.0.
- `scripts/check`, `prek run --all-files`, `scripts/package-addon
  0.0.0-worker-pool-audit`, and `scripts/test-package-addon
  0.0.0-worker-pool-audit` pass after the bounded worker-pool and isolation changes. The package
  tests include reproducibility, exact slice reassembly, same-version runtime handshakes, and
  host-only, default `platforms = []`, and all-platform installations.
- A pristine Linux arm64 container installed checksum-pinned stock Godot
  `4.7.2.stable.official.ed1daf0bf` and passed the race-enabled integration, shared protocol, and
  worker/API suites. It exposed and fixed a protocol-fixture dependency on a pre-existing global
  class cache. The same container installed the then-current checksum-pinned gpm 0.5.1 release
  binary and passed `scripts/test-package-addon 0.0.0-linux-arm64-audit`, including execution of
  the packaged Linux arm64 host binary. CI and release workflows now use verified release installers;
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

## Case-scoped signal probes

Implements `watch_signal` and `GDTestSignalProbe` / `GDTestInvocation` from issue #3, the Vest
signal-capture migration subset, and the ADR for the renamed probe disconnect method (ADR 0008).

- Real-Godot conformance (`fixtures/godot/api/signal_probes.test.gd`): 14 passing cases cover
  zero- through eight-argument user signals, built-in signal arity, ordered and defensive records,
  sequence numbers, independent probes beside pre-existing connections, manual stop, awaited success
  and timeout, freed owners, and cross-case cleanup on one persistent worker
  (`internal/worker/signal_probe_godot_test.go`). Nested emitted collections are snapshotted at emission.
- Invalid inputs (`fixtures/godot/api/signal_probe_failures.test.gd`): null signal, freed owner,
  arity above eight, negative count, negative timeout, and out-of-range index each end `error` with a
  source-located framework diagnostic.
- Migration goldens (`fixtures/migration/signal_capture.*`) prove conversion of counts, indexes,
  emptiness checks, bare captures, and collision-free names, and preservation with precise findings
  for every blocked form. The migration is idempotent and passes gdtest's static discovery contract.
- Uzir dry run (`migrate vest --check` over the five signal-capture files) finds all 45 captures and
  46 reads. It converts 19 captures and 20 reads. Of the 26 preserved captures, 11 are helper-function
  captures and 15 read through unsupported forms (local aliases and whole-list comparisons). Their
  findings are source-located. The five files still contain other unsupported Vest constructs and
  retain the project-local bridge for the preserved captures, so they are not yet run as converted.
- `scripts/check` passes (gofmt, tidy, golangci-lint, `go test -race ./...`, vet, build, strict
  gdkit lint/format/arch/uid, and quality gates).
- Not verified here: `scripts/package-addon` and `scripts/test-package-addon` require gpm 0.6.0 or
  newer. The installed `gpm` reports `dev`, and the released module is not reachable from this
  machine, so the packaging gate remains to be run in an environment with gpm 0.6.0.

## Generated test doubles

Implements `gdtest generate doubles`, the `test_doubles` configuration, the `GDTest.double_context()`
runtime (`GDTestDoubleContext`, `GDTestDoubleMethod`), and the ahead-of-time generator in
`internal/doubles`. The contract is `api-spec.md` section 14.7, and the decision record is
ADR 0009.

- Example (`examples/basic`): `inventory.gd` and `shop.gd` are hand-written. The generated
  `doubles/inventory_double.gd` and its `.uid` are committed and not hand-edited. The injection
  test `inventory_double.test.gd` covers sticky FIFO results, sync and async `then_answer`,
  `then_call_real`, void `then_return()`, `calls()` arguments with a defaulted parameter, and
  `reset()`. Alongside `callback_probe.test.gd`, `gdtest run --no-daemon` on the example reports
  `12 passed, 0 failed, 0 errors` across the example's files, of which the inventory test
  contributes six. The integration test `TestDocumentedBasicExampleExecutes` asserts the `12 passed`
  count and passes under `-race`.
- Gate: `scripts/check` runs `generate doubles --check --project examples/basic` after the build
  step. A one-byte change to the generated file exits `1` and reports `stale:`. Removing the marker
  line exits `2` with a refusal. Restoring the file exits `0`.
- Class names: the example's `Inventory` and `InventoryDouble` duplicate the names of the
  `fixtures/godot/api` classes. `examples/basic` has its own `project.godot`, so the repository-root
  project skips it during import, and no single Godot project contains both declarations. A
  headless import of a copy of the root project reported `Detected another project.godot at
  res://examples/basic. The folder will be ignored.` and registered `Inventory` only once.
- Migration: the runtime-mock finding (`unsupported.runtime-mock`) names `gdtest generate doubles`
  and `docs/vest-migration.md`. The migrator's conversions are unchanged.
- Packaging: `scripts/package-addon 0.0.0-test` includes the four `gdtest_double_*` and
  `gdtest_invocation` runtime files in the core slice. `scripts/test-package-addon 0.0.0-test` passes
  with the release gpm 0.6.0, pinned by `scripts/install-gpm-linux-ci`, including the packaged Godot
  handshake (no skip).
- Not covered by the example: the failure fixtures for unexpected calls, wrong-typed answers, and
  wrong-arity answers live in `fixtures/godot/api/double_failures.test.gd` and are asserted by
  `internal/worker/double_godot_test.go`. Those fixtures are not part of the example project.
- Known pre-existing condition: Godot prints `ObjectDB instances were leaked at exit` for the
  example project run. The unmodified baseline examples (`math.test.gd`, `signal_probe.test.gd`)
  produce the same warning, so it is not introduced by the generated double.
- Review hardening (each item names the test that proves it):
  - Configured class_name collisions with real scripts exit `2`:
    `TestAnalyzeRefusesConfiguredClassNameDeclaredByAnotherScript`,
    `TestBuildIndexDiagnosesDuplicateClassName`, and
    `TestGenerateDoublesRenamedOutputCollidingWithRealScriptFailsClosed`.
  - Ownership under excluded paths is found without parsing the excluded file:
    `TestBuildIndexFindsOwnershipInExcludedScriptsWithoutParsingThem` and
    `TestGenerateDoublesExcludedOutputIsOwnedAndRemovedWhenUnconfigured`.
  - Marker paths with spaces round-trip: `TestBuildIndexRecordsOwnershipForSourcePathsWithSpaces`
    and `TestGenerateDoublesPathsWithSpacesRoundTrip`.
  - `.godot` and `.git` segments are rejected in source and output paths, and indexing skips
    those directories: `TestValidateTestDoublesRejectsInvalidEntries` and
    `TestBuildIndexSkipsGodotAndGitDirectories`.
  - Global enum returns are checked as enums: `TestAnalyzeReturnCheckTable`.
  - Output rename convergence: generated doubles never enter the class map, so a renamed output
    keeps its class_name and an interrupted rename converges:
    `TestBuildIndexGeneratedDoublesNeverDeclareClasses`,
    `TestGenerateDoublesRenamedOutputKeepsClassNameAndConverges`, and
    `TestGenerateDoublesInterruptedRenameConvergesByRemovingOldOutput`.
  - Removal preflight: every removal is proven before the first rename, so a failed probe or a
    vanished owned file changes no target: `TestWriteRemovalProbeFailureChangesNoTarget`,
    `TestWriteRefusesUnexpectedFileRemovedSincePrepare`, and
    `TestGenerateDoublesUnremovableOwnedFileChangesNothing`, which uses real permissions and is
    skipped under root.
  - Marker-owned scripts are recognized before parsing, so a generated file with a corrupted body
    is still regenerated or removed: `TestGenerateDoublesCorruptedOwnedOutputIsRegeneratedAndRemovable`.
  - Outputs that differ only in case are rejected as duplicates, because they name one file on
    case-insensitive filesystems: `TestValidateTestDoublesRejectsInvalidEntries`.
  - An owned output that is the same file as a case-variant configured output is never removed,
    so a case-only rename on a case-insensitive filesystem keeps the new output:
    `TestPrepareKeepsOwnedOutputThatAliasesCaseVariantDesiredOutput` and
    `TestPrepareRemovesDistinctOwnedOutputDifferingOnlyInCase`.
  - An unimplemented abstract method starting with `_` is refused instead of producing an
    uninstantiable double: `TestAnalyzeRefusesUnimplementedAbstractPrivateMethod`.
  - Caller locations skip only `res://addons/gdtest/`, so a user test under a nested
    `addons/gdtest` directory keeps its line:
    `TestGodotDoubleCallerUnderNestedAddonsDirectoryKeepsItsLocation`.
  - A project `class_name` equal to a generated inner class (`GDTestControls` or a
    `<Method>Method` controller) is refused, because Godot 4.7 rejects an inner class that hides a
    global script class: `TestAnalyzeRefusesGeneratedInnerClassHidingGlobalClass`.
    The configured `class_name` of the double itself is held to the same rule:
    `TestAnalyzeRefusesConfiguredClassNameEqualToGeneratedInnerClass`.
  - The `addons/gdtest/`, `.godot`, and `.git` path rules ignore letter case, so a case variant
    cannot reach the runtime directory on a case-insensitive filesystem:
    `TestValidateTestDoublesRejectsInvalidEntries`.
  - An owned output is never removed while it is the same file as a configured output spelled
    differently, including through a directory symbolic link inside the project:
    `TestGenerateDoublesOutputThroughDirectorySymlinkStaysOwned`.

## Case-scoped callback probes

Implements `GDTest.callback_probe(argument_count: int = 0) -> GDTestCallbackProbe` from issue #5
and `api-spec.md` section 14.9, unifying probe ledger, defensive argument snapshotting, and
asynchronous `wait_until_count` handling across `GDTestSignalProbe` and `GDTestCallbackProbe` in
`addons/gdtest/probe_ledger.gd`.

- Shared ledger and cross-source ordering: `GDTestSignalProbe`, `GDTestCallbackProbe`, and
  `GDTestDoubleContext` allocate from one case-local monotonic sequence counter on `GDTest`, so
  `GDTestInvocation.sequence` orders signal emissions, callback invocations, and generated double
  calls within a case and resets on the next case.
- Closure retention and persistent-worker isolation: `GDTestCallbackProbe.callback()` builds an
  exact-arity (`0..8`) closure in a static helper that captures only an internal `_CallbackGate`.
  Closing the probe at case end severs `_CallbackGate` and `CaseContext` references to the ledger,
  `GDTest` instance, `SceneTree`, and failure sink. Late calls on a retained `Callable` mutate no
  probe state, emit no cross-case diagnostic or stderr output, and increment a bounded (1,024-cap)
  counter surfaced in `run_finished` metadata and logged by `SessionService` at `debug` severity
  (`worker_late_callbacks`).
- Real-Godot conformance (`internal/worker/callback_probe_godot_test.go`):
  - `fixtures/godot/api/callback_probes.test.gd` (11 passing cases): default zero arity, synchronous
    recording before returning to caller, arities `0..8`, defensive `GDTestInvocation` and nested
    collection snapshots, async completion/error waits, immediate return when satisfied, positive
    deadline expiry returning `false` without assertion failure, pending wait wakeup on `close()`,
    idempotent `close()`, cross-source sequence ordering across `watch_signal`, `callback_probe`,
    and `InventoryDouble`, sequence reset across cases, and deterministic `WeakRef` release of both
    `GDTest` and `GDTestCallbackProbe` while `probe.callback()` stays retained.
  - `fixtures/godot/api/callback_probe_failures.test.gd` (9 error cases): negative arity, arity
    above eight, negative count, negative/infinite/NaN timeouts, negative and out-of-range
    `arguments_at` indices, and invalid index after mid-case `close()` each report source-located
    framework errors at the exact test call site.
  - `fixtures/godot/api/callback_probe_outside_case.test.gd`,
    `fixtures/godot/api/callback_probe_class_initializer.test.gd`, and
    `fixtures/godot/api/callback_probe_suite_hook.test.gd`: outside-case creation in providers,
    member initializers, and suite hooks returns a closed inert probe with an invalid `Callable()`,
    reporting source-located `worker.discovery` diagnostics during `Discover` and `worker.framework`
    diagnostics during suite-hook `Run` while skipping the affected suite body and attempting
    cleanup hooks.
  - `fixtures/godot/api/callback_probe_leak.test.gd` and
    `fixtures/godot/api/callback_probe_timeout.test.gd`: retained callbacks invoked >1,024 times in
    later cases and subsequent runs on the same persistent worker PID pass with bounded
    `late_callback_invocations: 1024` metadata (omitted on clean runs) while `WeakRef` proves the
    prior run's suite was freed; unbounded `wait_until_count(1)` yields cleanly to external worker
    `CaseTimeout`.
- Migration, examples, and packaging: `internal/migrate` registers `callback_probe` and
  `double_context` in `gdtestInheritedNames` (`TestMigrationReportsCallbackProbeAndDoubleContextNameConflicts`)
  without adding new Vest API conversions, `docs/vest-migration.md` documents the manual callback
  probe recipe, `examples/basic/callback_probe.test.gd` executes under
  `TestDocumentedBasicExampleExecutes`, and `scripts/package-addon 0.0.0-issue-5` plus
  `scripts/test-package-addon 0.0.0-issue-5` passed alongside `scripts/check` on Godot 4.7.2 and
  gpm 0.6.0, verifying the shipped core files and packaged `callback_probe` runtime handshake.

## Post-Discovery Framework Error Reporting

- Runtime drainage and lifecycle cleanup (`addons/gdtest/gdtest.gd`, `addons/gdtest/protocol.gd`,
  `addons/gdtest/worker.gd`, `addons/gdtest/worker_case_task.gd`):
  - `_require_registration` routes through `_record_framework_error` so calling `define`, `test`,
    or `test_each` inside an active case marks that case `error` with a source-located `framework`
    diagnostic instead of appending silently to `_gdtest_discovery_errors`.
  - `worker.gd` drains `_gdtest_discovery_errors` immediately after every execution-time hook
    (`before_all`, root and nested `before_suite`, nested and root `after_suite`, `after_all`),
    after each case, and before `suite_finished`, emitting source-located `worker.framework`
    `worker_error` events with normalized project-relative paths (no `res://` prefix) and counting
    each error in the file and run totals.
  - Framework errors in `before_all`, root or nested `before_suite`, and `before_case` /
    `before_each` prevent the affected file, suite, or case body from running while still
    attempting applicable cleanup hooks (`after_case`, `after_suite`, `after_all`) per `api-spec.md`
    section 8.
  - `GDTest._gdtest_dispose()` clears dynamic registration closures and probe contexts after
    discovery, normal execution, and skipped prepared files so `RefCounted` suites leave zero
    leaked `ObjectDB` instances on worker exit.
- Go orchestration, folding, and reporting (`internal/worker`, `internal/service`,
  `internal/results`, `internal/reporter`, `internal/app`):
  - `worker.Process` treats `worker.framework` as a recoverable diagnostic so persistent workers
    reset and continue serving subsequent runs, and `GodotExecutor.Run` applies the same
    project-relative diagnostic path validation as `worker.Process`.
  - `SessionService.runFiles` promotes aggregate `run_finished` status on `worker_error`,
    `suite_finished`, and per-file `run_finished` events across multi-file runs.
  - `results.Collector` folds `worker.framework` events into `Summary.Errors` and `Summary.Errored`
    without marking `Summary.Infrastructure`, so execution-time framework misuse exits with
    `ExitFailures` (`1`) while real discovery/protocol/worker failures retain `ExitError` (`2`).
  - `reporter.Terminal` renders `ERROR <name> (<file>:<line>): <message>` when a `worker_error`
    carries source location metadata.
- Verification (`fixtures/godot/api/hook_misuse.test.gd`,
  `internal/worker/hook_misuse_godot_test.go`, `internal/integration/godot_test.go`,
  `internal/results/results_test.go`, `internal/reporter/reporter_test.go`,
  `internal/service/session_test.go`, `internal/app/app_test.go`): unit and real-Godot tests prove
  ordered, source-located framework error reporting across `before_all`, `before_suite`,
  `after_suite`, `after_all`, and in-case registration misuse in foreground, JSONL, and multi-file
  session runs.
