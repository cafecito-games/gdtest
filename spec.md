# gdtest specification

Status: proposed implementation specification  
Primary implementation language: Go 1.26  
Initial execution backend: Godot 4.x headless workers  
Future execution backend: `gdvm`

The authoritative user-facing GDScript contract is
[`api-spec.md`](./api-spec.md). This document defines the architecture that
implements it.

## 1. Purpose

`gdtest` is a fast, worktree-aware GDScript unit-test runner. It is a net-new
project, not a rewrite inside Vest. It should preserve the useful parts of
Vest's public test-writing experience where doing so does not compromise the
new architecture. Migration from Vest should be largely mechanical.

The main product is a Go executable named `gdtest`. It owns discovery,
incremental project analysis, daemon lifecycle, worker scheduling, caching,
reporting, migration, and editor/CLI integration. A deliberately small
GDScript runtime executes tests inside long-lived headless Godot worker
processes.

The architecture must support many Git worktrees and many concurrent clients.
Godot state is isolated by worktree and worker. Content-addressed analysis is
shared across worktrees. A single user-level daemon bounds total resource use.

The design must leave a clean execution boundary so that a future sibling
project, `gdvm`, can execute eligible GDScript tests without launching Godot.
Implementing `gdvm` is not part of this specification.

## 2. Product principles

In priority order:

1. Correct results and strict worktree isolation.
2. A very short save-to-result loop for warm workers.
3. Predictable bounded resource use with many parallel agents.
4. Automatic behavior with useful diagnostics and few required decisions.
5. Mechanical migration for conventional Vest suites.
6. Portable, inspectable protocols and state.
7. An execution model that can later host `gdvm`.

The implementation must not preserve a Vest behavior merely because Vest has
it. Compatibility is valuable at the source API boundary; Vest's internal
architecture is not a constraint.

## 3. Goals

The first production release must:

- Run GDScript test files in headless Godot.
- Amortize Godot startup through persistent workers.
- Support concurrent clients and concurrent Git worktrees safely.
- Enforce a global worker limit rather than creating an unbounded pool per
  worktree.
- Stream case-level results instead of repeatedly serializing a complete
  result tree.
- Discover conventional `test_*` methods and dynamically registered tests.
- Support synchronous and asynchronous tests and hooks.
- Provide a small assertion API familiar to Vest users.
- Be installable as a complete, version-matched addon and host tool with
  `gpm`; this is the primary consumer installation path.
- Provide stable test identifiers and source locations.
- Detect stale workers and stale results after source changes.
- Recover cleanly from worker crashes and abandoned worktrees.
- Provide human-readable terminal output and machine-readable JSON Lines.
- Include a migration command for supported Vest syntax.
- Run correctly from clean CI checkouts and from dirty developer worktrees.
- Keep mutable daemon state outside the repository and worktree.

The architecture must also make these later features possible without
redesigning the public result model:

- Affected-test selection based on the GDScript dependency graph.
- Deterministic result caching.
- Editor integration.
- A `gdvm` execution backend.
- Additional reporters such as JUnit and TAP.

## 4. Non-goals for the first production release

- Reimplementing GDScript outside Godot.
- Full behavioral compatibility with every Vest feature.
- A graphical test-results dock.
- Distributed execution across machines.
- Sandboxing untrusted tests. Tests execute arbitrary project code.
- Perfectly resetting every kind of Godot global state in place.
- Sharing a single Godot process between different worktrees.
- Sharing a writable `.godot` directory between worktrees.
- Runtime source generation for mocks. Test doubles are generated ahead of time
  by `gdtest generate doubles` from `gdtest.json` (section 9); the runtime never
  synthesizes script source.
- Mixing performance measurements into correctness tests or enforcing wall-clock budgets.
  Dedicated informational benchmarks are specified below as a separate concern.

## 5. High-level architecture

```text
CLI, editor, and agent clients
             |
             v
  user-level gdtest daemon (Go)
    - project sessions
    - discovery and dependency indexes
    - bounded fair scheduler
    - worker lifecycle
    - result event routing
    - shared content-addressed caches
       |              |
       v              v
 worktree A       worktree B
 Godot worker     Godot worker
       |
       v
 small GDScript runtime

 Future:
 daemon scheduler --> gdvm executor
```

There are three protocol boundaries:

1. Client to daemon.
2. Daemon to Godot worker.
3. Executor to the backend-neutral test event model.

The protocols must be explicitly versioned.

## 6. Suggested repository layout

The autonomous implementation agent may refine package boundaries, but the
separation of responsibilities must remain recognizable.

```text
gdtest/
  addons/
    gdtest/
      gdtest.gd
      worker.gd
      protocol.gd
      plugin.cfg
      launcher.gd
  cmd/gdtest/
  internal/
    app/
    daemon/
    client/
    config/
    doubles/
    pathglob/
    project/
    discovery/
    dependency/
    scheduler/
    worker/
    protocol/
    results/
    reporter/
    cache/
    migrate/
    testutil/
  packaging/
    gpm-package.toml
  scripts/
    stage-addon-tree
    package-addon
    test-package-addon
  fixtures/
  benchmarks/
  docs/
  go.mod
```

The Go module must declare Go 1.26. The implementation should use the standard
library when it is a good fit, but mature small dependencies are allowed when
they materially improve cross-platform correctness. GDScript parsing and AST
analysis should use the sibling `gdparser` Go module directly rather than
shelling out to another command.

The addon must be developed at `addons/gdtest`, exactly where it will be
installed in a consumer project. Release packaging may stage a copy of this
tree and add cross-compiled host binaries, but source development must not use
a different addon path.

`internal/doubles` is the single owner of ahead-of-time test double generation: it
analyzes project script inheritance and signatures, renders deterministic
GDScript and `.gd.uid` sidecars, decides ownership from the first-line marker,
and plans, checks, and writes outputs transactionally. `internal/pathglob` is the
single glob-matching policy shared by project configuration and discovery.
`internal/app` only maps flags and results to exit codes for these packages.

## 7. Executable and daemon model

One `gdtest` binary provides both client and daemon behavior. The daemon is
started through an internal or documented subcommand of the same executable.

Normal commands connect to a compatible daemon. If none exists, they start
one automatically and wait for it to become ready. Users should not normally
need to manage the daemon.

The daemon is user-scoped, not project-scoped. One compatible daemon may host
sessions for many repositories and worktrees. It applies global scheduling
and memory/worker limits across all of them.

The initial command surface is:

```text
gdtest init
gdtest run [selectors...]
gdtest list [selectors...]
gdtest watch [selectors...]
gdtest warm
gdtest doctor
gdtest migrate vest [--check | --write]
gdtest daemon status
gdtest daemon sessions
gdtest daemon stop
gdtest daemon logs
```

Commands may gain flags as needed, but defaults must work without a config
file in a conventional Godot project.

`gdtest run --no-daemon` must be available for hermetic integration tests,
debugging, and constrained CI. It may still launch a Godot worker; it simply
keeps orchestration in the foreground process.

### 7.1 Daemon namespaces

The daemon namespace must include:

- Client/daemon protocol version.
- The gdtest build identity.

Normal installed clients with the same build share one daemon. Different
development builds must not accidentally connect to each other. This is
essential when parallel agents develop gdtest in separate worktrees.

Build identity may be derived from Go build information plus an executable
content hash. It must be stable for the same binary and different for locally
rebuilt incompatible binaries.

Daemon sockets, metadata, logs, and locks live below `os.UserCacheDir()` in a
directory structured approximately as:

```text
<user-cache>/gdtest/daemons/<protocol>/<build-id>/
```

An isolated foreground mode must use a unique namespace and never interfere
with the user's normal daemon.

### 7.2 Client transport

On Unix platforms, prefer a Unix domain socket. On Windows, use an appropriate
local transport; authenticated loopback TCP is acceptable if a named-pipe
dependency is not justified. TCP listeners must bind only to loopback and use
a random authentication token stored in daemon metadata readable only by the
current user.

The client protocol should initially use versioned JSON Lines. Messages must
have bounded size, explicit request IDs, and a top-level message type. Long
running commands stream events followed by exactly one terminal response.

## 8. Project and worktree identity

The daemon hosts one project session per Godot project in a worktree.

When Git is available:

- Repository identity is based on the canonical absolute Git common
  directory, obtained through Git rather than inferred from `.git` layout.
- Worktree identity is based on the canonical absolute worktree root and its
  Git worktree metadata directory.

For a non-Git project, canonical project-root path is the identity.

Branch names and commit IDs must not be used as session identity. Branches can
move, names can collide, and dirty worktrees are expected.

A session key includes at least:

- Repository identity.
- Canonical worktree/project path.
- Godot executable identity.
- Relevant gdtest configuration identity.

Each session has monotonically increasing source generations. A generation
records the content hashes needed to prove which source state a worker loaded.
Workers are leased only for a compatible session and generation.

If a worktree disappears, the daemon cancels its queued work, shuts down or
kills its workers, and removes the session after a short grace period. Idle
sessions and workers are evicted by LRU/TTL policy.

## 9. Configuration

Project configuration is optional and stored as strict JSON in
`gdtest.json` at the project root. Unknown keys are errors rather than silently
ignored. Reasonable defaults must support a conventional project.

The initial schema should cover:

```json
{
  "tests": ["**/*.test.gd"],
  "exclude": [".godot/**"],
  "godot": "godot",
  "jobs": 0,
  "warm_workers": 1,
  "worker_idle_timeout": "2m",
  "worker_max_runs": 100,
  "only_mode": "auto",
  "partition": "file",
  "leak_check": {
    "policy": "fail",
    "ignore": []
  }
}
```

`jobs: 0` means automatic, subject to the daemon's global limit. Project
configuration may request less concurrency but may not bypass the global
limit.

`partition` selects how large test files are scheduled across workers:
`"file"` (default) never splits a file; `"suite"` splits only between cases
that share no nested suite, so suite hooks keep their serial semantics;
`"case"` splits anywhere, relaxing suite hooks to once per shard containing
that suite's cases. Files with at most 16 selected cases are never split, and
`before_all`/`after_all` run once per shard in every partitioned mode. The
`--partition` flag overrides the configured value. Section 8.2 of
`api-spec.md` defines the author-facing guarantees; ADR 0011 records the
decision.

`test_doubles` lists the generated typed test doubles that `gdtest generate
doubles` owns. It is an array of strict entries:

```json
"test_doubles": [
  { "source": "inventory.gd", "output": "doubles/inventory_double.gd", "class_name": "InventoryDouble" }
]
```

- `source` (required) is the project-relative path of the script to double. It
  must end in `.gd`.
- `output` (required) is the project-relative path of the generated script. It
  must end in `.gd`, must not be under `addons/gdtest/`, and must not equal any
  `source`.
- `class_name` (optional) names the generated class. When present it must be a
  GDScript identifier. Each `class_name` and each `source` and `output` must be
  unique across entries.

Paths must be clean, forward-slashed, project-relative paths without a `..`
segment. Unknown fields are rejected, and every validation error names the entry
index. Section 14.7 of `api-spec.md` defines the generated contract.

`leak_check` configures post-scope `Node` leak detection on workers:

```json
"leak_check": {
  "policy": "fail",
  "ignore": ["res://ui/toast_layer.gd", "Timer"]
}
```

- `policy` (optional; defaults to `"fail"` when omitted) must be `"fail"`,
  `"warn"`, or `"off"`.
- `ignore` (optional) is an array of unique clean `res://...gd` script paths or
  valid GDScript identifiers (`^[A-Za-z_][A-Za-z0-9_]*$`). Absolute paths,
  parent segments, whitespace-padded entries, duplicates, unknown fields, and
  `null` or non-object values are rejected at load time.

The backend-neutral `TestPlan` carries `leak_check` into worker execution and
plan-affecting cache identities. Section 14.10 of `api-spec.md` defines the
runtime semantics.

User-level configuration may set global maximum workers, memory budget,
logging, and default Godot resolution. Project configuration must not be able
to force an unsafe global resource allocation.

Configuration precedence, highest first:

1. Explicit CLI flags.
2. Project `gdtest.json`.
3. User-level configuration.
4. Built-in defaults.

Environment variables should be reserved for operational overrides and CI,
not required for normal use.

## 10. Distribution and installation

`gpm` is the primary consumer installation path. A gdtest release must be a
sliced gpm package that installs both the GDScript addon and the appropriate
host `gdtest` executable. Installing the addon must not require a separate Go,
Homebrew, or system package-manager step.

A normal consumer workflow should be one `gpm add` command:

```text
gpm init                                      # only when the project has no manifest
gpm add --name gdtest --source github-release \
  --repo cafecito-games/gdtest --version <tag> <host-tool-platform-override>
```

gdtest is a host development tool, not an export-platform dependency. Its
`addons.toml` entry must therefore have `platforms = []`, which overrides
rather than inherits `[project].platforms`; gpm still installs the current host
slice implicitly. Without this override, a mobile or web game would ask gdtest
for meaningless iOS, Android, or web executable slices and installation would
fail.

At the time of this specification, `gpm add` may not expose an option that
writes an empty addon-level platform override before it installs. Providing a
small gpm enhancement for an atomic host-tool add is explicitly in scope and
preferred over publishing dummy target slices. The exact flag spelling should
follow gpm conventions. Until that capability is available, the documented
fallback is this manifest entry followed by `gpm install`:

```toml
[addons.gdtest]
source = "github-release"
repo = "cafecito-games/gdtest"
version = "<tag>"
platforms = []
```

The final documented command must resolve a versioned GitHub release carrying
`gpm-index.toml` and all referenced archives while writing the empty platform
override atomically.

Consumers commit `addons.toml` and `addons.lock`; they never commit
`.gpm-state.toml`. CI uses `gpm install`, never `gpm update`. Disposable
worktrees may use `gpm install --host-only` to materialize only the mandatory
core and current host executable without changing the byte-identical lockfile.

### 10.1 Package contents

Every published release contains:

- A mandatory `core` slice with the pure-GDScript runtime, optional lightweight
  editor/bootstrap integration, launcher scripts, license, and version
  metadata.
- One architecture-specific slice for each supported host executable.
- A `gpm-index.toml` beside every archive in the GitHub release.

The initial desired executable slices are:

- `macos.arm64`
- `macos.x86_64`
- `linux.arm64`
- `linux.x86_64`
- `windows.arm64` when supported by the Go toolchain and CI
- `windows.x86_64`

The agent may defer a slice only when the build or test environment cannot
reasonably validate it; the published index and support documentation must
state the actual set exactly. Platform tags must use gpm's canonical lowercase
spelling.

Cross-compiled binaries use unique paths below `addons/gdtest/bin/`, for
example:

```text
addons/gdtest/bin/macos-arm64/gdtest
addons/gdtest/bin/linux-x86_64/gdtest
addons/gdtest/bin/windows-x86_64/gdtest.exe
```

Small POSIX and Windows project-local launchers in `core` locate the installed
host binary and preserve arguments and exit status. Consequently, a user can
run the installed tool without placing it on the global `PATH`, using a stable
documented project-local command. An optional editor bootstrap may locate the
same binary. Homebrew and `go install` distribution may be offered for global
CLI convenience, but they are not prerequisites for a gpm-installed project.

The GDScript runtime and Go executable must carry the same release version and
reject incompatible handshakes. The host slice is installed implicitly by gpm
even when the project's export platforms differ from the development host.

### 10.2 Author packaging layout

The committed authoring manifest lives at `packaging/gpm-package.toml`, not at
the repository root. Packaging must use a staged addon tree:

1. Cross-compile release binaries into a build-output directory.
2. Copy the committed `addons/gdtest` source into a temporary staging tree.
3. Copy only shippable binaries into their platform directories.
4. Copy `gpm-package.toml` beside the staged `addons/` directory.
5. Run `gpm package --dir <staging-tree>`.

This is required because gpm has no producer-side exclusion mechanism: every
unclaimed file below `addon_path` would otherwise enter the mandatory core
slice. Go source, fixtures, build intermediates, and developer tooling must
never be staged into the addon.

The manifest has `addon_path = "addons/gdtest"`. Executables are assigned to
architecture slices with `[package.slices]`; everything else intended for all
hosts belongs to core. Core must contain no compiled executable. Different
slices must not publish the same logical path.

An illustrative manifest is:

```toml
[package]
name = "gdtest"
addon_path = "addons/gdtest"
version = "0.0.0"

[package.slices]
"macos.arm64" = ["bin/macos-arm64/gdtest"]
"macos.x86_64" = ["bin/macos-x86_64/gdtest"]
"linux.arm64" = ["bin/linux-arm64/gdtest"]
"linux.x86_64" = ["bin/linux-x86_64/gdtest"]
"windows.arm64" = ["bin/windows-arm64/gdtest.exe"]
"windows.x86_64" = ["bin/windows-x86_64/gdtest.exe"]
```

The release version is supplied by the packaging script. Unsupported slices
are omitted rather than declared empty.

The release scripts must:

- Require a known minimum gpm version.
- Accept an explicit release version rather than editing the manifest in
  place.
- Package twice and verify byte-identical outputs.
- Verify core has no Go executable or build-time source.
- Verify every promised slice exists and contains exactly its host binary.
- Test a scratch-project install with `--host-only`.
- Test `--all-platforms` and verify the reassembled addon tree.
- Verify a repeated `gpm install` is idempotent and does not dirty the scratch
  project.
- Publish the index and every referenced archive together.

### 10.3 Runtime ownership and `gdtest init`

The installed runtime lives at:

```text
res://addons/gdtest/
```

When the addon is managed by gpm, gdtest must never self-update or overwrite
its files. Version movement is performed with `gpm update gdtest` and reviewed
through `addons.toml`/`addons.lock`. gdtest must not edit `addons.lock` or
`.gpm-state.toml`.

`gdtest init` remains available for repository development and users who
installed a standalone global binary. It may install an embedded runtime only
when gdtest can establish that the target is not gpm-owned. On an existing or
apparently gpm-managed addon it reports the correct gpm command instead of
overwriting files. Initialization is idempotent and never overwrites local
modifications without an explicit, safe action.

The runtime must not load icons, scenes, docks, or unrelated editor-only code
in a headless worker. A lightweight editor plugin or launcher is allowed for
daemon discovery and test commands, but a graphical results UI is not required
for the first release.

Test scripts conventionally begin with:

```gdscript
extends GDTest
```

The runtime may use `class_name GDTest` for ergonomics. The tool must handle a
fresh checkout whose Godot global-class/import cache has not yet been built.
`warm`, `doctor`, or worker startup should perform a headless import/bootstrap
when required and retry once. This work is session setup, not repeated per
test run.

## 11. Godot worker model

A Godot worker belongs to exactly one project session and one loaded source
generation. It never serves another worktree.

The daemon launches it approximately as:

```text
godot --headless --path <project> --script res://addons/gdtest/worker.gd \
  --gdtest-worker-port <port> --gdtest-worker-token <token>
```

Exact flags may vary by supported Godot version. The daemon must capture
stdout and stderr without allowing a worker to block on full pipes. Output is
associated with the worker and active run.

The daemon opens a loopback listener on an ephemeral port. Each worker gets a
cryptographically random one-time authentication token. The first worker
message must authenticate and complete a versioned handshake containing:

- Worker protocol version (version 4, synchronized across `model.ProtocolVersion`,
  `protocol.Version`, `daemon.ProtocolVersion`, and `addons/gdtest/version.gd`).
- gdtest runtime version.
- Godot version and build information.
- Canonical project path.
- Session and generation identifiers.
- Worker capabilities.

Mismatches fail clearly; they must never silently run tests with the wrong
runtime or source generation.

### 11.1 Worker commands

The initial worker protocol supports:

- `ping`
- `run`
- `cancel`
- `reload`
- `reset`
- `shutdown`

Each `run` contains a unique run ID and a test plan containing files and
optional case selectors. A worker executes at most one run at a time.

### 11.2 Worker events

Workers stream immutable events:

- `worker_ready`
- `run_started`
- `suite_started`
- `case_started`
- `assertion_failed`
- `case_warning`
- `output`
- `case_finished`
- `suite_finished`
- `run_finished`
- `worker_tainted`
- `worker_error`

The worker must never send the complete accumulated result tree after every
case. The Go side folds events into reports once.

At the end of every case, suite, and file scope that completes without timeout
or cancellation, the worker runs the closing hook, frees `autofree`
registrations in reverse order, awaits one `SceneTree.process_frame` so pending
`queue_free()` deletions drain, and compares `Node.get_orphan_node_ids()` plus a
full `SceneTree.root` walk against the scope's opening baseline. Under
`policy: "fail"`, un-ignored leaks emit `assertion_failed` (`assertion: "leak"`,
omitting `test_id` for suite and file scopes) and fail the case or file; under
`policy: "warn"`, they emit `case_warning` without changing pass/fail status.
Timed-out and canceled scopes skip `autofree` release, the frame drain, and the
leak check before tainting and retiring the worker.

### 11.3 Wire values

JSON cannot represent every Godot Variant. Failure data therefore uses an
explicit tagged wire value. It must preserve normal scalar and collection
values recursively and provide stable type/display information for engine and
user objects. Initial support must include:

- null
- bool
- signed integer
- float, including non-finite values through explicit tags
- String and StringName
- Array and Dictionary
- common value types when straightforward
- fallback object `{type, display}` information

Only failures and explicit diagnostic output need rich value serialization.
Passing assertions must not serialize their operands.

## 12. Worker lifecycle and isolation

Persistent workers provide speed but may retain static variables, autoload
state, resources, ProjectSettings changes, signal connections, and other
global state.

The scheduler supports four policies:

- `reuse`: reset and reuse the worker when safe.
- `recycle`: finish the run, then replace the worker asynchronously.
- `fresh`: start a dedicated worker before the run.
- `exclusive`: do not colocate other work with the worker/session during the
  run.

The default is bounded reuse with periodic recycling. A worker is recycled
when any of these occur:

- It reports itself tainted.
- Reload or reset fails.
- A structural source change invalidates it.
- It reaches `worker_max_runs`.
- It exceeds a configurable age or memory threshold.
- It produces a protocol error.

The daemon should maintain a warm replacement when allowed by the global
budget. Replacement startup happens off the requesting client's critical path
whenever possible.

Do not claim perfect in-process isolation. Tests needing guaranteed clean
process state must be schedulable with `fresh` or `exclusive` policy.

## 13. Global scheduling

The daemon enforces one global worker budget across all sessions. The built-in
default should be conservative, such as four workers, and configurable by the
user. Automatic project concurrency is bounded by this global budget.

Scheduling requirements:

- Never run two requests concurrently in one worker.
- Never use a worker for another worktree.
- Fairly serve independent sessions so one agent cannot starve others.
- Prefer interactive `run` requests over background warming and watching.
- Cap background warmups.
- Cancel queued warmups when their generation becomes obsolete.
- Use file-level work units by default; a project may enable intra-file
  partitioning (`partition: "suite"` or `"case"`), which splits one file's
  selected cases into same-path units proven safe by the completed discovery
  manifest's stable IDs and suite chains. See ADR 0011.
- Preserve deterministic report ordering independently of execution order.

A weighted fair queue or similarly simple policy is sufficient. The behavior
must be covered by deterministic scheduler tests.

## 14. Source generations, watching, and reload

The daemon watches relevant files and maintains an incremental project index.
Filesystem notifications may be coalesced or lost, so the implementation must
support reconciliation scans. Watcher overflow triggers a rescan, not silent
staleness.

Every run records the requested generation. Relevant file changes during a
run mark its result stale. A stale result remains visible but must be labeled
and must not be reported as authoritative for the new generation. `watch`
mode automatically schedules a replacement run.

There are two update paths:

1. In-place reload for changes proven safe to reload.
2. New worker generation for structural or uncertain changes.

At minimum, these changes are structural and require replacement unless
experiments prove a reliable reload path:

- `class_name` changes.
- Base-class changes.
- Signal declaration changes.
- Autoload or `project.godot` changes.
- GDExtension configuration changes.
- Runtime addon changes.
- Parse failures during reload.

Function-body-only changes may use Godot script reload APIs. If reload cannot
be proven correct, replacement is the required fallback. Correctness is more
important than avoiding a background startup.

`gdtest warm` creates or updates the current session and prepares at least one
worker for the latest generation. Worktree creation tools and autonomous
agents should be able to call it asynchronously.

## 15. Discovery and test manifest

Discovery has static and dynamic components.

Static discovery is performed in Go with `gdparser`. It identifies:

- Test files matching configured patterns.
- Classes extending `GDTest` directly or transitively when resolvable.
- Conventional `test_*` methods.
- Hook methods.
- Static source locations.
- Static `__only` markers.
- Script and resource dependencies that can be resolved conservatively.

Dynamic registration through `suite()`, `define()`, and `test()` is completed
inside a Godot worker because arbitrary GDScript may generate cases. Dynamic
discovery results are cached by the relevant content fingerprint.

The test manifest assigns stable identifiers:

- Method test: normalized relative file path plus method name.
- Dynamic test: normalized relative file path, definition location,
  description, and a deterministic ordinal for collisions.

Paths use forward slashes and are relative to the project root in user-visible
IDs. Absolute worktree paths must not make IDs differ across worktrees.

Test selection accepts:

- No selector: all configured tests.
- File or directory paths.
- Glob patterns.
- Exact stable test IDs.
- Substring/name filters through an explicit flag.

Unknown selectors are usage errors rather than silently empty successful runs,
unless an explicit `--allow-empty` flag is supplied.

Benchmark discovery is a parallel static-only manifest over configured `*.bench.gd` patterns.
It resolves `GDTestBenchmark` inheritance, validates typed `benchmark_*` methods and hooks, and
uses the same path/directory/glob/exact-ID selector semantics. Benchmark stable IDs are the
normalized project-relative file plus the complete method name. Benchmark plans never contain
absolute worktree paths or Godot objects.

## 16. GDScript public API

[`api-spec.md`](./api-spec.md) is the implementation and conformance contract
for the GDScript API. In summary, v1 includes:

- Source-ordered `test_*` methods and `<test>__cases` parameter providers.
- Dynamic `suite`, `define`, `test`, and `test_each` registration.
- Async lifecycle hooks and immutable suite/case metadata.
- Direct boolean, equality, identity, approximate, null, empty, and containment
  assertions with actual-first argument order.
- Explicit pass, fail, skip, todo, void, error, and canceled outcomes.
- Ahead-of-time generated typed test doubles configured in `gdtest.json`, with
  call history and per-method results (section 14.7 of `api-spec.md`).
- Plan-wide focused tests and CI enforcement.
- Inherited SceneTree, sleep, polling, timing, and message helpers.
- A source-aware Vest migration map and executable API conformance suite.

The runtime must not add undocumented public methods merely because Vest had
them. Passing assertions update only compact state: they do not capture a
stack, allocate rich diagnostic dictionaries, or serialize operands. APIs
listed as deferred in `api-spec.md` are reported by migration rather than
silently emulated or removed.

## 17. Backend-neutral execution model

Go orchestration must not depend on Godot-specific result objects. Define a
backend-neutral model conceptually equivalent to:

```go
type Executor interface {
    Run(context.Context, TestPlan, EventSink) error
}

type EventSink interface {
    Emit(Event) error
}

type BenchmarkExecutor interface {
    Bench(context.Context, BenchmarkPlan, BenchmarkEventSink) error
}
```

The concrete interface may differ, but:

- Test plans are serializable and independent of absolute worktree paths.
- Events use stable test IDs.
- Godot-specific metadata is optional structured data.
- Reporters consume the same events regardless of executor.
- The scheduler can later route eligible tests to a `gdvm` executor.

Do not build a speculative plugin framework. Establish one clean internal
boundary and implement the Godot executor first.

Benchmark planning and event folding remain parallel to correctness-test folding. One leased
persistent worker runs a benchmark's warmups and samples serially. Hooks, scheduling, worker
startup, protocol serialization, and reporting are outside the timed interval. The worker uses a
monotonic engine clock around each method invocation and streams immutable start/sample/finish
events. A stale generation, cancellation, timeout, runtime error, crash, or incomplete sample set
invalidates the result and cannot create a baseline entry.

## 18. Reporting and exit behavior

The default terminal reporter is concise, deterministic, and optimized for
agent consumption as well as humans. It prints failures with file, line,
message, expected/actual values, and captured output. Passing cases need not be
printed unless verbose mode is selected.

Machine output uses JSON Lines with one event per line and a final summary.
JSON mode writes no decorative text to stdout. Diagnostics may go to stderr.

Exit codes:

- `0`: selected tests completed without failures.
- `1`: one or more tests failed.
- `2`: configuration, discovery, protocol, startup, or other infrastructure
  error.
- `130`: canceled by the user.

`gdtest generate doubles [--check] [--project DIR]` uses a separate exit policy.
`0` means the outputs were written, or `--check` found every generated double
up to date. `1` means `--check` found a missing, stale, or unexpected generated
file, which it lists on stdout. `2` means configuration, parsing, signature,
ownership, or filesystem refusal; nothing is written in that case.

Skipped, todo, and void cases do not fail by default. Strict flags may change
that policy and must be reflected in the final summary.

Parallel execution must not make report ordering nondeterministic. Sort final
results by normalized file path and manifest order while still streaming
immediate failure events when useful.

`gdtest bench` uses the same project sessions, scheduler, worker bounds, worktree isolation, and
exit codes. Its versioned result schema is shared by JSONL, terminal output, baseline persistence,
and comparison. It contains the complete normalized sample array; min, median, p95, and max;
iteration/sample counts; source generation and content fingerprint; product/runtime/protocol and
Godot identities; OS, architecture, CPU identity when available, logical CPU count, and dirty
worktree state. It never contains an absolute project path. Baselines decode strictly, sort by
stable ID, and publish by same-directory atomic replacement. Comparison reports matching, added,
and removed IDs and never changes exit status because of a timing delta.

## 19. Cancellation and failure recovery

On client cancellation:

1. The daemon removes queued work.
2. It sends `cancel` to active workers.
3. After a short configurable grace period, it terminates only the exact
   unresponsive worker process.
4. It returns exit code 130.

Worker startup and handshake may be retried once after an import/bootstrap or
fresh-worker replacement. A case must not be silently retried after
`case_started`; tests may have external side effects. A worker crash during an
active case produces an infrastructure result naming the last known case.

The same cancellation and exact-process recovery rules apply to benchmarks. Retrying after stale
generation or worker replacement discards all samples from the obsolete attempt rather than
merging partial distributions.

The daemon must survive malformed worker messages, worker crashes, client
disconnects, and deleted worktrees. Panics in one session must not crash the
whole daemon; unexpected panics should still be logged with enough context to
debug.

## 20. Caching

All reusable caches are content-addressed and live outside worktrees.

The first release should cache:

- Parsed GDScript ASTs.
- Static discovery metadata.
- Dependency/index fragments.
- Dynamic discovery manifests when their inputs are unchanged.

Cache keys include tool/parser version and all configuration that affects the
result. Absolute worktree paths are metadata, not semantic cache keys.

Passing test-result caching is deferred until deterministic eligibility can be
proven or explicitly opted into. Do not cache arbitrary test results merely
because source files appear unchanged; time, randomness, environment,
filesystem, autoload, and engine state may affect them.

The cache must tolerate corruption by rejecting invalid entries and
recomputing them. Concurrent processes must not expose partially written
entries; use atomic publication.

## 21. Affected-test selection

After basic discovery is reliable, build a conservative dependency graph using
`gdparser` and project metadata. `gdtest run --affected <paths...>` selects
tests whose transitive dependency closure includes any changed path.

Rules:

- False positives are acceptable; false negatives are not.
- Unresolved dynamic loads widen selection conservatively.
- Changes to project configuration, autoloads, runtime code, or unknown global
  inputs may select all tests.
- The command explains why a test was selected in verbose/debug output.
- Empty affected selections succeed and state that no tests were affected.

This graph and its cache are shared across worktrees by content identity while
session-specific path resolution remains isolated.

## 22. Vest migration

`gdtest migrate vest` uses `gdparser` and syntax-aware edits. It must not use
regular expressions to rewrite GDScript.

The migration workflow has check and write modes:

- `--check` reports planned rewrites and unsupported constructs without
  changing files.
- `--write` applies safe rewrites atomically and reports remaining manual work.

Expected mechanical rewrites include:

- `extends VestTest` to `extends GDTest`.
- Supported utility/global names to their gdtest equivalents.
- Configuration and CLI invocation guidance.
- Hook aliases where semantics are unambiguous.

Assertion names that remain compatible should not be rewritten unnecessarily.
Dynamic tests, async tests, and supported focus markers should continue to
work.

Unsupported features such as runtime-generated mocks and benchmarks produce
precise findings with file and source location. Migration must never silently
delete or weaken a test.

The repository must contain representative Vest fixtures and golden migration
tests proving that common suites migrate mechanically.

## 23. Worktree-heavy workflow requirements

The following are release-blocking behaviors:

- Two worktrees with different dirty implementations can run concurrently and
  each observes only its own files.
- No Godot worker crosses a worktree boundary.
- Shared parse/index caches do not leak path-specific results.
- Global worker count remains bounded when many agents invoke gdtest at once.
- Interactive work from one session is not indefinitely starved by another.
- Deleting a worktree cleans up its workers without harming other sessions.
- Different gdtest development builds use separate daemon namespaces.
- Reports use project-relative paths so equivalent tests have stable IDs
  across worktrees.
- A result generated from an obsolete source generation is visibly stale and
  is not cached as current.
- Daemon and cache files never dirty a worktree.

An integration test must create at least two temporary Git worktrees from one
repository, make conflicting changes, run tests concurrently, and assert both
isolation and the global worker bound.

## 24. Security and trust boundary

gdtest runs trusted local project code with the user's permissions. It is not a
sandbox.

Even so:

- Local listeners bind only to local transports/loopback.
- Tokens are cryptographically random and permission-protected.
- Protocol message size and nesting are bounded.
- Paths received over protocols are validated against their session project
  root where appropriate.
- Process termination targets exact recorded child PIDs, never patterns or
  broad process groups without validation.
- Logs must not include authentication tokens or entire environment dumps.

## 25. Observability and diagnostics

The daemon writes structured logs with timestamps, build ID, session ID,
worker ID, generation, run ID, and event type. Logs rotate or are bounded.

`gdtest doctor` checks and reports:

- Project root and configuration.
- gdtest runtime installation/version.
- Godot executable and version.
- Whether a headless worker can start and handshake.
- Import/global-class cache readiness.
- Daemon connection and namespace.
- Worker budget and active sessions.
- Test discovery count.

Errors must be actionable. A startup failure should include the command,
captured Godot diagnostics, project path, and suggested next action without
requiring the user to find daemon logs first.

Timing instrumentation must distinguish at least:

- Client-to-daemon connection.
- Queue wait.
- Worker acquisition/startup.
- Project bootstrap/import.
- Discovery.
- Script load and dynamic definition.
- Hook and case execution.
- Result serialization.
- Reporting.

Expose detailed timings through verbose or JSON output.

## 26. Performance objectives

Performance objectives are measured after the worker is warm and exclude
deliberate sleeps, frame waits, and user test computation.

Initial objectives on a modern development machine:

- Client-to-ready-daemon overhead: median below 5 ms.
- Warm single-file run with a handful of trivial tests: median below 50 ms and
  p95 below 100 ms.
- Passing assertion overhead does not grow diagnostic data or capture stacks.
- Result streaming remains linear in event count.
- Creating many sessions never exceeds the configured global worker bound.
- Idle workers are reclaimed according to configured TTL.

Benchmarks must report distributions and environment information. Until a
stable CI baseline exists, performance objectives should be tracked and
reported rather than enforced with flaky wall-clock unit-test thresholds.

The repository needs synthetic benchmark corpora for:

- 10,000 passing assertions.
- 10,000 cases.
- 1,000 test files.
- Deeply nested dynamic suites.
- Many failures with rich values.
- Concurrent runs across many worktree sessions.

## 27. Testing and quality gates

The repository must have one canonical check script used identically by local
development, pre-commit hooks, and CI. CI must not carry a weaker duplicate of
the local policy.

Commit a `.pre-commit-config.yaml` compatible with `prek run --all-files` (and
standard pre-commit implementations). Hooks may split the canonical checks for
useful failure output, but their commands and configuration must match CI.

### 27.1 Go quality gates

gdtest follows the Go 1.26 linting conventions used by gpm. Commit a
golangci-lint v2 `.golangci.yml` whose baseline is:

```yaml
version: "2"

linters:
  default: standard

formatters:
  enable:
    - gofmt
```

Narrow exclusions are allowed only with a comment explaining the concrete
false positive or test pattern. Do not globally disable a standard linter to
make one finding disappear.

Required Go checks are:

```text
gofmt -l .                    # must print nothing
go mod tidy                   # must leave go.mod/go.sum unchanged
golangci-lint run ./...
go test -race ./...
go vet ./...
go build ./...
```

The race-enabled test suite is mandatory in CI. A non-race test run may be
used for quick iteration but does not replace it.

### 27.2 GDScript quality gates

All committed runtime and valid-fixture GDScript must be formatted and linted
with gdkit. Commit strict `.gdkit/lint.json` and `.gdkit/format.json`
configuration. Because gdkit's defaults exclude `addons/**`, the gdtest config
must explicitly include `addons/gdtest` as a source root **and** replace the
default exclude list with one that does not exclude the shipped addon. A clean
run that selected zero runtime files is a broken quality gate.

The lint configuration must:

- Pin the supported Godot minor used for semantic typing checks; the initial
  configuration targets Godot 4.7.
- Enable every static typing rule: `require-return-type`,
  `require-argument-type`, `require-variable-type`,
  `require-typed-collection`, `require-signal-argument-type`, and
  `require-typed-loop-variable`.
- Enable `inconsistent-return-statements`, `unused-private-member`, and other
  new strict rules through `enable_new_rules: true`.
- Set `missing-docstring` to `class`, `signal`, `func`, `var`, `const`, and
  `enum`, requiring `##` documentation for the public API.
- Keep every enabled rule at error severity unless a temporary adoption ADR
  gives an expiry and removal plan.
- Exclude intentionally invalid parser/protocol fixtures narrowly; never
  exclude the shipped addon tree.

All public runtime signatures, locals, loop variables, signals, and
collections must be statically typed to the extent supported by the configured
Godot version. Suppressions must name one rule at the smallest possible source
location and include an adjacent explanation when the reason is not obvious.

Commit `.gdkit/architecture.json` with classifications that cover the shipped
addon rather than relying on a permissive/default addon classification. At a
minimum it must enforce that:

- The public test API/core does not depend on worker transport or editor code.
- The headless worker does not depend on editor integration.
- Editor/bootstrap code may depend on the public API and client launcher, but
  those layers never depend back on it.
- Dependency cycles between runtime components fail CI.

Commit UID sidecars for shipped GDScript. Fixture projects may have their own
explicit UID policy, but excluding the runtime from `gdkit uid check` is not
allowed.

Required GDScript checks are:

```text
gdkit lint check --minimum-version 0.10.0 .
gdkit format check --minimum-version 0.10.0 --diff .
gdkit arch check --minimum-version 0.10.0 .
gdkit uid check --minimum-version 0.10.0 .
```

The minimum version may be raised deliberately but not removed. Exit `1`
means source findings; exit `2` means the tool/configuration/I/O setup is
broken and must not be described as a lint failure.

Developers may use `gdkit format write .` to apply canonical formatting, but
must review the diff. CI always uses the non-writing check. GDScript emitted or
copied by packaging and generation tests must be checked against the same lint
and format configuration as handwritten runtime code.

### 27.3 Release-package quality gates

Required release-package checks:

```text
scripts/package-addon <version>
scripts/test-package-addon <version>
```

These scripts invoke `gpm package` through the staged tree described in
section 10. They are part of the release contract, not optional examples.

Tests should use temporary project/worktree directories and fake worker
transports where possible. End-to-end tests requiring Godot must be clearly
identified and skip with an explicit reason when no compatible Godot binary is
configured.

Required coverage areas include:

- Strict config parsing and precedence.
- Daemon auto-start, locking, and build namespaces.
- Client reconnect and cancellation.
- Fair bounded scheduling.
- Session and generation transitions.
- Worker handshake and protocol rejection.
- Worker crash and timeout behavior.
- Event folding and deterministic reporting.
- Static and dynamic discovery.
- Async tests and hooks.
- Failure-only diagnostics.
- Fresh-checkout bootstrap.
- Cache atomicity and corruption recovery.
- Vest migration goldens.
- Multi-worktree isolation.

Protocol fixtures should be language-neutral JSONL files so the Go and
GDScript implementations can each be tested against the same examples.

## 28. Delivery milestones

The implementation should proceed in vertical slices. Each milestone must be
usable and tested before expanding scope.

### Milestone 1: foreground vertical slice

- Initialize the Go module and command.
- Add the canonical check script, golangci-lint v2 configuration, strict gdkit
  lint/format/architecture configuration, UID policy, and CI quality gates
  before the runtime grows.
- Author the minimal GDScript runtime at `addons/gdtest` and support standalone
  development installation.
- Implement `GDTest`, method discovery, core assertions, and hooks.
- Launch one foreground Godot worker.
- Complete a versioned handshake.
- Run one test file and stream events.
- Provide terminal and JSONL reporters.
- Add end-to-end fixtures and timing output.

### Milestone 2: persistent daemon

- Add daemon namespace, locking, auto-start, and local client protocol.
- Keep one session worker warm.
- Implement `warm`, `status`, `stop`, and `doctor` basics.
- Add cancellation, crash handling, and logs.
- Demonstrate repeated warm runs meeting the intended latency class.

### Milestone 3: worktrees and global scheduler

- Add repository/worktree identity.
- Host multiple isolated sessions.
- Add global worker limits, fair queuing, idle eviction, and recycling.
- Add multi-worktree concurrency integration tests.
- Add development-build daemon isolation.

### Milestone 4: incremental discovery and generations

- Integrate `gdparser` static discovery.
- Add stable test manifests and source locations.
- Add file watching, reconciliation, generations, stale-result handling, and
  safe reload versus replacement.
- Cache parse and discovery artifacts by content.

### Milestone 5: selection and migration

- Add complete selector behavior.
- Add conservative dependency graph and `--affected`.
- Add `migrate vest --check/--write` with golden fixtures.
- Document unsupported Vest features clearly.

### Milestone 6: hardening

- Add bootstrap behavior for clean CI checkouts.
- Add cross-compilation, staged gpm packaging, reproducibility verification,
  scratch-project installation tests, and GitHub release automation.
- Ensure gpm can atomically add gdtest with `platforms = []`; contribute the
  minimal gpm host-tool/platform-override enhancement if the installed gpm
  release still lacks it.
- Verify a gpm-installed host binary and runtime complete a version-matched
  worker handshake without any separately installed gdtest executable.
- Expand value serialization and diagnostics.
- Add synthetic performance corpora and profiling.
- Harden cache concurrency/corruption handling.
- Validate supported platforms and Godot versions.
- Write user and contributor documentation.

## 29. Acceptance criteria

The specification is implemented when all of the following are true:

1. A fresh conventional Godot project can add a versioned gdtest GitHub release
   with gpm and then execute a passing and failing `.test.gd` file using the
   project-local installed host executable, with no separate gdtest install.
2. A second run reuses a warm worker and avoids Godot process startup on the
   critical path.
3. Results stream case by case and total serialization work is linear.
4. Concurrent runs in two conflicting worktrees observe the correct source in
   each worktree.
5. The configured global worker bound is never exceeded during a many-client
   stress test.
6. Source changes create a new generation; obsolete results are marked stale.
7. Worker crash, cancellation, missing Godot, parse failure, and protocol
   mismatch all produce distinct actionable errors.
8. Passing assertions do not capture stacks or construct rich diagnostic
   payloads.
9. Common Vest fixtures migrate mechanically and unsupported constructs are
   reported without destructive edits.
10. All required Go quality gates and the configured Godot end-to-end suite
    pass.
11. No normal command creates tracked or unignored mutable state in a consumer
    worktree beyond files intentionally managed by gpm or explicit standalone
    `gdtest init` installation/configuration. gdtest never hand-edits
    `addons.lock` or `.gpm-state.toml`.
12. The backend-neutral plan/event boundary is covered by tests and can accept
    a future non-Godot executor without changing reporters or test IDs.
13. `gpm package` produces a mandatory binary-free core and the documented host
    slices reproducibly; host-only and all-platform scratch installs succeed,
    and a repeated install leaves the scratch worktree clean.
14. A project whose `[project].platforms` includes mobile or web targets can add
    gdtest through the documented single gpm command; the resulting gdtest
    entry has `platforms = []` and the current host executable is installed.
15. The canonical local and CI check runs golangci-lint, race tests, vet, build,
    strict gdkit lint, gdkit format check, gdkit architecture checks, and UID
    checks over the actual shipped source; a deliberately malformed runtime
    fixture proves each relevant gate is capable of failing.

## 30. Autonomous implementation decision policy

This document intentionally leaves ordinary engineering choices to the
implementing agent. The agent should proceed without asking for direction when
a decision can be made within these rules:

1. Prefer correctness and isolation over an optimization that cannot be
   verified.
2. Prefer the simplest design that satisfies the current milestone and keeps
   the specified boundaries intact.
3. Do not build speculative abstractions beyond the Godot executor, but do not
   couple reporters, test IDs, or scheduling to Godot objects.
4. Use content identity for reusable analysis and canonical path identity for
   live worktree state.
5. Treat unknown dynamic behavior conservatively: run more tests, replace a
   worker, or decline to cache rather than risk a false result.
6. Add a focused test for every concurrency, lifecycle, or protocol bug.
7. Record material deviations or irreversible architectural decisions in a
   short ADR under `docs/adr/` and continue unless the deviation contradicts a
   product goal.
8. Keep protocols versioned from their first implementation.
9. Preserve user source on migration or initialization conflicts; report the
   conflict instead of overwriting it.
10. Measure before moving hot paths into native code or adding protocol
    complexity.

The agent should request human input only when the choice would change the
public test API, weaken worktree isolation, introduce a new trust/security
boundary, or materially contradict a goal or non-goal in this specification.

