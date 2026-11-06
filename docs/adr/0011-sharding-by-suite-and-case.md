# ADR 0011: Sharding large test files by suite or case

- Status: accepted
- Date: 2026-11-06

## Context

The scheduling granularity has been the file: `service.runFiles` fans out one
lane per `FilePlan` entry, and a single-file project never reaches the fan-out
path at all (`len(plan.Files) > 1`). A file with hundreds of cases occupies one
lane serially while other lanes idle. spec.md section 13 held scheduling to
file-level units "unless a future manifest proves splitting is safe."

The completed discovery manifest is that proof. After
`completeManifestWithTimings`, every case — static, dynamic, and
parameterized rows — is known with a stable ID, and every dynamic case carries
its nested suite chain (`DiscoveryCase.Suites`, produced as `suite_infos` by
the runtime in nesting order). The worker's suite traversal already relies on
that chain being an ordered nesting, and protocol v4 (issue #23) added per-file
`case_ids` subsets and the plan-level `has_focus` stamp that shards need.

## Decision

1. **A pure partitioner owns cut legality.** `internal/shard` is deterministic,
   performs no I/O, and speaks only backend-neutral types, respecting the
   plan/event boundary. A boundary between two consecutive cases is legal in
   `suite` mode iff no non-root suite ID appears in both chains; `case` mode
   makes every boundary legal; `file` mode never cuts. Static method cases
   carry no chain and belong to the root suite only.

2. **`suite` is the safe default when partitioning is enabled; `file` remains
   the project default.** In `suite` mode each suite's `before_suite` and
   `after_suite` run exactly once per run, wrapping exactly its selected cases
   — identical to serial execution on a different wall-clock interleaving,
   because no suite spans a shard boundary. `case` mode is an explicit opt-in
   that relaxes suite hooks to once per shard containing that suite's cases.
   Files at or below `MinCasesPerShard` (16) selected cases are never split,
   and a file with no legal cut stays a single shard without error.

3. **`before_all`/`after_all` run once per shard.** This is inherent to any
   intra-file parallelism: each shard is a fresh `GDTest` instance in its own
   worker process, exactly as a file runs today under a selection. Shards are
   packed greedily in manifest order to `ceil(total/shard_count)` cases, with
   `shard_count = min(jobs, legal cuts + 1)` capped by the worker budget.

4. **The app resolves focus and pre-flights policy before partitioning.** When
   partitioning is enabled, the app mirrors the worker's focus filter
   (dropping non-only cases when focus is present and not ignored), stamps
   `plan.HasFocus` so every shard applies the same decision, and pre-flights
   `Protocol.only_policy_error` so `require`/`forbid` violations are reported
   exactly once instead of once per shard.

5. **Shards are same-path `FilePlan` entries; scheduling is unchanged.**
   Each shard entry carries its `case_ids` subset plus the file's static
   method cases. `runFiles` already tolerates duplicate paths, folds
   per-worker `run_started`/`run_finished` into one pair, and cancels siblings
   on first error, so no scheduler or lane changes were needed.
   `run_finished` metadata now distinguishes `"files"` (distinct paths) from
   `"shards"` (dispatched entries).

6. **Fail closed, always.** The partitioner proves its output is pairwise
   disjoint and order-covering before returning it; on any violation the file
   is returned unsplit, because rerunning beats silently dropping a case.
   Determinism (identical manifest + options produce identical shards) makes
   stale-generation replans and repeated runs dispatch the same units.

7. **Client-side dedupe for per-shard diagnostics.** Worker-side
   re-registration diagnostics are deterministic per file content, so a
   sharded file can emit the same `worker_error` once per shard. The app's
   event sink drops repeats for sharded paths, keyed by (file, event name,
   message).

## Consequences

- Large files and single-file projects now use idle lanes, with report
  ordering preserved: `plan.SelectedIDs` still drives the collector's
  preferred order, and JUnit accumulates by file path so a file's shards merge
  under one suite.
- `suite()` registration re-executes once per shard (each worker builds the
  full suite tree), bounded by the jobs cap and the minimum-cases threshold;
  milestone 3 of the sharding epic (issue #26) measures whether duration
  weighting or adjusted constants are warranted.
- `--no-daemon` executes shards serially in one foreground worker: correct,
  without parallelism.
- CI cross-machine fan-out (`--shard i/n`) remains deferred; it will reuse
  this partitioner unchanged.
