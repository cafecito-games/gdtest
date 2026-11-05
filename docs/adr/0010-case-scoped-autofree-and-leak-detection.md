# ADR 0010: Scoped autofree and whole-tree node leak detection

- Status: accepted
- Date: 2026-11-05

## Context

Persistent headless Godot workers reuse one `SceneTree` and `ObjectDB` across
cases, suites, and files within a run, and across runs within a project session.
When a test instantiates a `Node` without freeing it, or attaches a fixture to
`SceneTree.root` or an autoload singleton and leaves it behind, two hazards
compound:

1. Unparented orphans accumulate in `ObjectDB` for the lifetime of the worker
   process and trigger Godot's `ObjectDB instances were leaked at exit` warning
   only when the worker eventually shuts down, detached from the test that
   allocated them.
2. Stray nodes left inside `SceneTree` keep running `_process`, `_physics_process`,
   timers, and signal handlers during subsequent tests on the same persistent
   worker, causing order-dependent cross-test interference.

Meanwhile, manual `node.free()` boilerplate at the tail of every test is skipped
whenever a mid-case helper returns early or errors, and tests that legitimately
call `queue_free()` need one idle frame for Godot's scene-tree deletion queue to
drain before post-scope inspection.

## Decision

1. **Scoped `GDTest.autofree(node: Node) -> Node`:**
   `GDTest` maintains an execution scope stack (`file` -> `suite` -> `case`)
   storing registered `Node` instance IDs rather than `Object` references (avoiding
   dangling freed-instance `Variant` assignments in typed GDScript). When a scope
   closes normally after its closing hook (`after_case`/`after_each`,
   `after_suite`, or `after_all`), registered nodes still valid are freed in
   reverse registration order via `Object.free()`, skipping descendants whose
   ancestor is freed in the same pass. Registering a node in multiple open scopes
   moves ownership to the narrowest scope so every registered node is freed at
   most once at the earliest scope boundary.

2. **One-frame `queue_free()` drain before inspection:**
   After synchronous `autofree` release, normal scope closure awaits a single
   `SceneTree.process_frame` so deferred `queue_free()` deletions complete before
   baselines are compared.

3. **Whole-tree stray detection alongside orphan baselines:**
   Opening a scope records both `Node.get_orphan_node_ids()` and a full walk from
   `SceneTree.root` using `get_children(true)`. Walking the entire `SceneTree`
   rather than only direct children of `root` catches stray fixtures attached
   under project autoloads or internal containers while naturally preserving
   pre-existing autoload nodes captured in the opening baseline. Survivors from a
   closing inner scope are absorbed into open ancestor baselines so a case leak
   is attributed once to that case and never re-reported on enclosing `suite` or
   `file` scopes.

4. **Skip teardown and leak checks on timeout or cancellation:**
   When a case times out or a run is canceled, the user coroutine may still be
   suspended inside engine callbacks or mutating nodes. Freeing nodes underneath
   an active coroutine or awaiting `process_frame` could hang or corrupt teardown,
   and the worker is already tainted and scheduled for replacement. Aborted
   scopes therefore discard their registrations and baselines synchronously.

5. **Default `fail` policy with structured allowlist and `warn`/`off` escape hatches:**
   `gdtest.json` defaults `leak_check.policy` to `"fail"` because silent stray
   nodes directly undermine persistent-worker determinism. Projects migrating
   large suites or intentional background singletons can opt into `"warn"`
   (`case_warning` events, exit `0`), `"off"`, or subtree exemptions in
   `leak_check.ignore` matching clean `res://...gd` script paths, base scripts,
   native `Node.is_class` identifiers, or global `class_name` identifiers.

## Consequences

- Every normal case, suite, and file scope incurs one `SceneTree.process_frame`
  yield at teardown, adding roughly one frame interval to single-file and
  per-case execution corpora in exchange for deterministic `queue_free()`
  drainage and immediate per-scope leak attribution.
- Existing suites that previously leaked unparented or scene-tree `Node`
  instances now fail by default with source-located `assertion: "leak"`
  diagnostics until converted to `autofree(...)` or configured under
  `leak_check`.
- Both JSONL protocols advance to version 3 to carry `TestPlan.LeakCheck`,
  `case_warning`, and file/suite-scoped `assertion_failed` events without a case
  `test_id`.
