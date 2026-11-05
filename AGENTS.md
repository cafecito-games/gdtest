# AGENTS.md

## What this is

gdtest is a typed GDScript test framework for Godot 4.7. The product is a Go
executable (`cmd/gdtest`) that owns discovery, incremental analysis, daemon
lifecycle, worker scheduling, caching, reporting, and Vest migration, plus a
deliberately small GDScript runtime (`addons/gdtest/`) that executes tests
inside long-lived headless Godot worker processes.

Two authoritative specifications govern changes and take precedence over
inference from code: `api-spec.md` (the user-facing GDScript contract) and
`spec.md` (the architecture that implements it). Architectural decisions and
justified specification deviations go in `docs/adr/`; milestone evidence goes
in `docs/implementation-status.md`.

## Commands

```sh
scripts/check                        # canonical gate; prek and CI run exactly this
go test ./internal/service           # one package
go test -race -run TestName ./internal/service
scripts/benchmark                    # report all five samples plus the environment header
scripts/profile [output-directory]   # CPU/heap profiles for the synthetic corpora
scripts/package-addon <version>      # stage, inject version, cross-compile, package twice, compare bytes
scripts/test-package-addon <version>
```

`scripts/check` runs gofmt cleanliness, `go mod tidy` no-op verification,
golangci-lint v2, `go test -race ./...`, vet, build, strict
`gdkit lint/format/arch/uid check` over the shipped addon and examples, and
`scripts/test-quality-gates` (which proves each gdkit gate rejects a
deliberately malformed copy of the runtime).

Requires Go 1.26, Godot 4.7, gdkit >= 0.10.0, golangci-lint v2, gpm >= 0.6.0.

Tests that need the engine skip themselves when `godot` is not on `PATH`
(`*_godot_test.go`, `internal/integration`). Performance corpora are gated
behind `GDTEST_RUN_PERFORMANCE=1`. Never run a bare root-level `gpm package` or
publish assets by hand.

Running the tool itself: `go run ./cmd/gdtest run`, or the packaged
`./addons/gdtest/gdtest run`. `--no-daemon` runs orchestration in the
foreground and is the hermetic debugging path.

## Architecture

Request flow: `app` (cobra commands, exit policy) → `client` (one request per
authenticated local Unix-socket connection) → `daemon` (server + namespace) →
`service` (project sessions) → `scheduler` (global bound) → `worker` (one
persistent Godot process) → GDScript `worker.gd`.

Two JSONL protocols, both versioned and size-bounded: client↔daemon
(`daemon.ProtocolVersion`, `daemon.Message`) and daemon↔worker
(`protocol.Version`, `protocol.Envelope`, mirrored in `addons/gdtest/protocol.gd`
and `addons/gdtest/version.gd`). Changing a wire shape means changing both
sides, the version constant, and `fixtures/protocol/*.jsonl`.

### Invariants that are easy to break

- **The plan/event boundary is backend-neutral.** `internal/model` (`TestPlan`,
  `Event`, `DiscoveryManifest`) must contain no Godot objects, no callables, and
  no absolute worktree paths. This exists so a future `gdvm` backend can execute
  eligible tests without Godot. `internal/executor` is that boundary's interface.
- **Two kinds of identity, never mixed.** Reusable caches (`internal/cache`,
  `index`, `dynamic`) key on *content identity*; live sessions and workers key
  on *canonical path identity* (`internal/project`). Putting a worktree path in
  a reusable cache key, or content-keying a live session, is a correctness bug.
- **Prefer a false positive to a false result.** Affected-test selection
  (`internal/selection`), cache refusal, and worker replacement all widen or
  back off rather than risk a false negative. Unknown dynamic loads and
  project-global inputs select everything. An empty affected selection succeeds.
- **A `worker.Process` never changes project or session.** `Reload` is the only
  operation that advances its generation; otherwise the worker is replaced.
  Generations (`internal/generation`, `internal/watch`) are content-proven, and
  obsolete-generation work is rejected by the scheduler.
- **Daemon namespaces isolate by protocol version and exact executable build**
  (`daemon.NewNamespace`), under `os.UserCacheDir()/gdtest/daemons/<protocol>/<build>`,
  mode 0700. Incompatible builds never share a socket.
- **Reconciliation, not notification, is the source of truth** in
  `internal/watch`; platform watcher queues are treated as lossy hints.

### GDScript side

`.gdkit/architecture.json` enforces layering inside `addons/gdtest`:
`api` (`gdtest.gd`, `*_info.gd`, `version.gd`) ← `transport` (`protocol.gd`) ←
`worker` (`worker.gd`, `worker_case_task.gd`); `launcher` may only reach `api`.
Unclassified files are an error, so a new runtime script needs a classification
entry. Do not weaken gdkit by excluding `addons/gdtest`.

`worker.gd` extends `SceneTree` and connects back over TCP with a token;
`worker_case_task.gd` runs one case's hooks and body while the worker enforces
the deadline, emitting `@@GDTEST_CASE_CAPTURE` markers that the Go
process-output classifier parses — that prefix is a shared contract.

## Conventions

- Tests use Testify (`assert`/`require`) and `t.Parallel()` where safe. Every
  bug fix gets focused race/lifecycle/protocol regression coverage.
- No flaky wall-clock thresholds. Performance numbers are reported in
  `docs/performance.md`, not asserted.
- Exit codes are stable CLI behavior: 0 ok, 1 test failures or strict-policy
  violations, 2 configuration/discovery/protocol/worker/reporting error,
  130 cancellation.
- Project configuration is `gdtest.json` (`config.Filename`), loaded strictly
  with safe defaults.
- Do not hand-edit `addons.lock` or `.gpm-state.toml`.
- Package doc comments state each package's single responsibility; keep them
  accurate when responsibilities move.
