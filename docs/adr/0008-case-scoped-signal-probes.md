# ADR 0008: Case-scoped signal probes with a renamed stop method

- Status: accepted
- Date: 2026-11-05

## Context

Vest's `capture_signal` and `get_signal_emissions` kept a global signal-to-array registry and
required authors to pass an argument count that Godot already exposes. Persistent workers make a
global registry a correctness hazard: a connection made by one case must never survive into the
next case that reuses the same worker and test instance. `api-spec.md` deferred signal capture,
and Uzir's five signal-capture files needed a project-local bridge for that reason.

## Decision

`GDTest.watch_signal(what: Signal) -> GDTestSignalProbe` connects immediately and returns a probe
owned by the executing case:

- Arity is inferred from `Object.get_signal_list()` on the signal's live owner. Zero through eight
  arguments are supported. Missing, ambiguous, or wider metadata is a framework error with no
  connection.
- Each emission is recorded as an immutable `GDTestInvocation` with a case-local sequence number
  and a defensive argument copy. Every returned collection is a copy.
- `wait_until_count` wakes on a private probe notification. A deadline timer is created only when
  the timeout is positive. A timeout of zero defers to the case timeout, matching the other gdtest
  waiting helpers; in that unbounded case the wait also checks each frame whether the owner has been
  freed, because Godot gives plain objects no notification for that.
- `GDTest._gdtest_end_case` disconnects every probe before the case result is finalized. That path
  runs for passing, failing, timed-out, and canceled cases, so no connection outlives its case.
- Migration converts a capture only when every read of that signal in the same `test_` function
  has a proven equivalent, and otherwise reports and preserves the source.

The public method for disconnecting a probe is `stop_recording()`, not the `disconnect()` named in
the original specification. `Object.disconnect(StringName, Callable)` is a native method on every
`Object`, including `RefCounted`. Godot 4.7 rejects a GDScript method with the same name and a
different signature (`The function signature doesn't match the parent`), so a zero-argument
`disconnect()` cannot exist on a probe. `stop_recording()` describes the operation without
colliding with the engine API. Its behavior matches the specified idempotent disconnect.

Because `Object.disconnect` is native, the `GDTest` location walk now skips every frame under
`/addons/gdtest/` rather than only `gdtest.gd`. Otherwise diagnostics from probe methods would be
attributed to the addon file instead of the user's call site.

## Consequences

Authors never state an arity, and the recorder cannot leak across cases. The API is intentionally
small: no callback probes, matchers, or cross-case captures. Captures the migrator cannot prove
equivalent stay in the source with precise findings, which keeps the migration atomic and honest.

Because Godot's global class cache is refreshed only after a failed worker startup, a checkout that
gains a new `class_name` script needs one headless editor import before a run can resolve it. This
is existing behavior and unchanged here.
