# gdtest public API specification

Status: proposed public contract  
Audience: test authors, migration tooling, and runtime implementers  
Companion architecture: [`spec.md`](./spec.md)

## 1. Scope and authority

This document defines the target user-facing GDScript API for gdtest. An
implementation agent should be able to implement the test runtime from this
document without reading Vest's internals. Vest may be consulted for migration
fixtures and edge cases, but it is not the behavioral authority for gdtest.

This specification covers:

- Test-file shape and discovery.
- Method and dynamic test definitions.
- Parameterized tests.
- Lifecycle hooks.
- Assertions and case outcomes.
- Focused tests.
- Async and SceneTree helpers.
- Public metadata exposed to hooks.
- Compatibility and migration from Vest.

`spec.md` defines daemon, worker, protocol, packaging, CLI, scheduling, and
worktree architecture. If the documents conflict about user-visible GDScript
behavior, this API specification takes precedence. If they conflict about
runtime architecture, `spec.md` takes precedence.

## 2. Design principles

The public API should be:

1. Familiar to existing Vest users.
2. Cheap on passing tests.
3. Statically discoverable where practical.
4. Explicit about dynamic or engine-dependent behavior.
5. Suitable for a future non-Godot `gdvm` backend.
6. Small enough to document and test exhaustively.
7. Strictly typed and clean under the repository's gdkit lint and format
   configuration.

Compatibility does not require preserving Vest's internal result objects,
signals, mixin system, runtime mock generation, or benchmark implementation.

### 2.1 Typing and formatting contract

Every public method, hook, callback, signal, property, and return value in the
shipped API has an explicit GDScript type. Runtime implementation code also
types locals, loop variables, and collection element/key/value types whenever
Godot's type system can express them. `Variant` is used deliberately at true
dynamic boundaries, not as a shortcut around a known type.

All shipped GDScript and executable examples must pass the strict `gdkit lint`
configuration and canonical `gdkit format check` defined by `spec.md`. The API
spelling in this document is semantic; if a snippet's whitespace differs from
canonical formatter output, the formatter's output is authoritative.

## 3. Minimal test file

A test file conventionally ends in `.test.gd` and extends `GDTest`:

```gdscript
extends GDTest

func test_damage_reduces_health() -> void:
	var health := Health.new(10)
	health.damage(3)
	expect_equal(health.value, 7)
```

`GDTest` is a globally named class installed at `res://addons/gdtest/`.

A test script is valid when it:

- Matches the configured test-file patterns.
- Is concrete and instantiable.
- Extends `GDTest` directly or transitively.

The runner creates one test-script instance per file per run. Cases in that
file execute serially on the same instance. Instance fields therefore persist
between cases; authors use case hooks to reset them. Different files may run
concurrently in different workers.

Test authors must not depend on the order in which different files execute.

## 4. Suite identity and naming

Each test file has an implicit root suite.

The suite name is selected in this order:

1. The value returned by `get_suite_name()` when implemented.
2. The script's `class_name` when present.
3. The project-relative test-file path.

The optional method is:

```gdscript
func get_suite_name() -> String:
	return "Health"
```

It must be synchronous, take no arguments, and return a non-empty string. An
invalid return is a discovery error.

Display names are not stable IDs. Renaming a suite may change report text but
method test IDs remain based on project-relative file and method name.

## 5. Method tests

### 5.1 Discovery

A method is a test when its name begins with `test_` and it is declared by the
concrete test script.

Examples:

```gdscript
func test_addition() -> void:
	expect_equal(2 + 2, 4)

func test_empty_inventory_has_no_weight() -> void:
	expect_equal(Inventory.new().weight(), 0)
```

The following are not test methods:

- `test` without the underscore.
- Private methods beginning with `_`.
- Reserved provider methods ending in `__cases`.
- Methods inherited from another script. A concrete base file that is itself
  discovered runs its methods in its own suite; descendants do not repeat
  them.

Tests execute in source order. Static discovery uses source locations, not the
incidental order returned by Godot reflection.

When a file contains both `suite()` registrations and method tests, dynamic
children registered by `suite()` appear first, followed by method tests in
source order. This ordering is deterministic and part of the manifest.

### 5.2 Method requirements

A non-parameterized test method:

- Takes no required arguments.
- May have an explicit `void` return annotation or no return annotation.
- May suspend with `await`.
- Must not return a value for the runner to interpret.

A required argument without a matching parameter provider is a discovery
error. Default arguments are allowed, but are not a parameterization mechanism.

### 5.3 Method display names

The default display name is the method name with `test_` removed and remaining
underscores converted to spaces and humanized for display. For example,
`test_empty_inventory` displays as `Empty inventory`.

The method name, not the display name, is part of the stable ID.

## 6. Parameterized method tests

Parameterized methods use a provider named exactly `<logical test
method>__cases`:

```gdscript
func test_adds_numbers(a: int, b: int, expected: int) -> void:
	expect_equal(a + b, expected)

func test_adds_numbers__cases() -> Dictionary[String, Variant]:
	return {
		"positive": [2, 3, 5],
		"negative": [-2, -3, -5],
		"identity": [7, 0, 7],
	}
```

The provider must:

- Be synchronous.
- Take no arguments.
- Return either a `Dictionary` or an `Array`.

For a `Dictionary` provider:

- Every key is converted to a non-empty display name with `str(key)`.
- Every value must be an `Array` of positional arguments.
- The key is part of the parameterized case's stable ID.
- Duplicate stringified keys are a discovery error.
- Rows are ordered by the UTF-8 byte ordering of their stringified names, so
  order does not depend on Dictionary implementation details.

For an `Array` provider:

- Every element must be an `Array` of positional arguments.
- Cases are named with a one-based ordinal followed by a readable argument
  representation.
- The zero-based row index is part of the stable ID.

Example compatible with mechanically migrated Vest providers:

```gdscript
func test_capitalize(input: String, expected: String) -> void:
	expect_equal(input.capitalize(), expected)

func test_capitalize__cases() -> Array[Variant]:
	return [
		["hello", "Hello"],
		["hello world", "Hello World"],
	]
```

Each row is a distinct case. `before_case` and `after_case` run for every row.
Argument-count or argument-type failures are discovery errors and identify the
provider row.

Providers execute during discovery. They should be deterministic and must not
perform assertions, mutate engine-global state, or depend on case hooks.

For a focused method such as `test_adds_numbers__only`, the logical method name
is `test_adds_numbers`, so its provider remains
`test_adds_numbers__cases`. Toggling focus therefore does not rename the
provider or change case IDs.

## 7. Dynamic suites and cases

Dynamic registration is supported for nested suites, generated case names,
and migration from Vest.

The optional `suite()` method executes during discovery:

```gdscript
func suite() -> void:
	define("arithmetic", func() -> void:
		test("adds", func() -> void:
			expect_equal(2 + 2, 4)
		)
	)
```

`suite()` takes no arguments. It may be asynchronous. Registration completes
before `before_all` and before any case executes.

### 7.1 `define`

```gdscript
func define(
	name: String,
	callback: Callable,
	is_only: bool = false,
) -> GDTestSuiteInfo
```

`define` creates a nested suite and immediately calls its registration
callback. Nested calls to `define` and `test` attach to the current suite.
The callback may be asynchronous and is awaited by discovery.

The name must be non-empty. Calling `define` outside `suite()` or another
registration callback is a framework error.

Convenience form:

```gdscript
func define_only(name: String, callback: Callable) -> GDTestSuiteInfo
```

### 7.2 `test`

```gdscript
func test(
	description: String,
	callback: Callable,
	is_only: bool = false,
) -> void
```

`test` registers a case but does not execute the case callback during
discovery. The callback takes no arguments unless registered through
`test_each`. It may be asynchronous.

The description must be non-empty. Dynamic cases execute in registration
order.

Convenience form:

```gdscript
func test_only(description: String, callback: Callable) -> void
```

### 7.3 `test_each`

`test_each` is the preferred dynamic parameterization API:

```gdscript
func suite() -> void:
	test_each("adds numbers", {
		"positive": [2, 3, 5],
		"negative": [-2, -3, -5],
	}, func(a: int, b: int, expected: int) -> void:
		expect_equal(a + b, expected)
	)
```

Signature:

```gdscript
func test_each(
	description: String,
	cases: Variant,
	callback: Callable,
	is_only: bool = false,
) -> void
```

`cases` follows the same `Dictionary` or `Array` row shapes as method
providers. Each row becomes a separate case and invokes the callback with the
row's positional arguments.

Convenience form:

```gdscript
func test_each_only(
	description: String,
	cases: Variant,
	callback: Callable,
) -> void
```

### 7.4 Discovery restrictions

Assertions and outcomes are invalid while `suite`, `define`, or a provider is
registering tests. Registration-time failures are discovery errors, not test
failures.

Dynamic registration may read project data when necessary, but doing so can
make discovery engine-dependent and prevent future `gdvm` execution. Method
tests and static providers are preferred when they express the same intent.

## 8. Lifecycle hooks

All hooks are optional and may be synchronous or asynchronous.

```gdscript
func before_all() -> void:
	pass

func after_all() -> void:
	pass

func before_suite(suite: GDTestSuiteInfo) -> void:
	pass

func after_suite(suite: GDTestSuiteInfo) -> void:
	pass

func before_case(case: GDTestCaseInfo) -> void:
	pass

func after_case(case: GDTestCaseInfo) -> void:
	pass
```

Hook order for a normal case is:

1. `before_all`, once for the test-file instance.
2. `before_suite`, outermost to innermost for suites entered.
3. `before_case`.
4. Case callback.
5. `after_case`.
6. `after_suite`, innermost to outermost for suites exited.
7. `after_all`, once after the file's selected cases finish.

Hooks run only when the file/suite has at least one selected case. A file
excluded by selectors or plan-wide focus performs no discovery-time case
execution and no lifecycle hooks.

The implicit root suite participates in suite hooks. A nested suite's suite
hooks wrap all selected descendants, not each case independently.

`after_case`, applicable `after_suite` hooks, and `after_all` are best-effort
cleanup hooks. The runner attempts them after assertion failures and case
errors. A worker crash or process termination may prevent cleanup.

An error in a before hook prevents the affected case or suite body from
running and records an error. Cleanup hooks that can still run are attempted.
An error in an after hook records an error against the relevant case or suite.

### 8.1 Hook aliases

For familiarity with other frameworks, these aliases are accepted:

```gdscript
func before_each(case: GDTestCaseInfo) -> void
func after_each(case: GDTestCaseInfo) -> void
```

They are exact aliases for `before_case` and `after_case`. Defining both names
for the same phase is a discovery error; the runner never calls both.

Vest lifecycle signals such as `on_begin` and `on_case_finish` are not public
gdtest APIs. Hooks are the supported lifecycle extension point.

### 8.2 Sharded file execution

Test files are unaware of sharding. Projects opt in through the `partition`
key in `gdtest.json` or the `gdtest run --partition` flag (the flag wins):

| Mode | Cut rule | `before_suite`/`after_suite` | `before_all`/`after_all` |
| --- | --- | --- | --- |
| `file` (default; absent) | never split | once per suite | once per file |
| `suite` | only between cases where no nested suite spans the boundary | root suite once per shard; every nested suite once, wrapping exactly its selected cases | once per shard |
| `case` | anywhere | root suite once per shard; every nested suite once per shard containing its cases | once per shard |

`before_all`/`after_all` running once per shard is inherent to intra-file
parallelism: each shard is a fresh test-file instance in its own worker
process, exactly as a file runs today under a selection. The root suite is the
file's suite scope, so its `before_suite`/`after_suite` hooks also run once per
shard. In `suite` mode, every nested suite's hooks run exactly once per run —
the same guarantee as serial execution, on a different wall-clock
interleaving. `case` mode is the documented relaxation: a nested suite's hooks
run once per shard that contains any of its cases.

Files with at most 16 selected cases are never split, a file with no legal cut
stays a single shard without error, and sharding never changes case identity,
per-case statuses, or report ordering. Shard sizing considers the whole
selection: existing file-level work fills lanes first, and only a file whose
case count exceeds the run-wide per-lane target is split. This prevents a
many-file run from duplicating registration and file hooks merely because
individual files also exceed the minimum.

```gdscript
# suites.test.gd — 30 cases in "Alpha", 6 in "Beta"
extends GDTest

var _configured: int = 0

func before_all() -> void:
	# Runs once per shard: twice under `gdtest run --partition suite --jobs 2`
	# for this file, because Alpha's cases and Beta's cases form two shards.
	_configured += 1

func suite() -> void:
	define(
			"Alpha",
			func() -> void:
				for index: int in range(30):
					test(
							"alpha case %02d" % index,
							func() -> void:
								expect_equal(1, _configured)
					)
	)
	define(
			"Beta",
			func() -> void:
				for index: int in range(6):
					test(
							"beta case %02d" % index,
							func() -> void:
								expect_equal(1, _configured)
					)
	)
```

Running `gdtest run --partition suite` on the file above with two available
workers executes Alpha's cases and Beta's cases concurrently in separate
worker processes; every case observes `before_all` once, each suite's
`before_suite`/`after_suite` wraps exactly its own cases, the root suite hooks
run once in each worker, and the report lists all 36 cases in the same order as
an unpartitioned run.

## 9. Public metadata

Hook metadata is immutable from the test author's perspective. The concrete
implementation may use getter-only properties or defensive copies.

### 9.1 `GDTestSuiteInfo`

Public properties:

```gdscript
id: String
name: String
file: String
line: int
is_only: bool
```

`file` is a project-relative `res://` path. `line` is one-based or `-1` when
unknown.

### 9.2 `GDTestCaseInfo`

Public properties:

```gdscript
id: String
name: String
file: String
line: int
method_name: StringName
parameters: Array[Variant]
parameter_name: String
is_only: bool
```

`method_name` is empty for a purely dynamic callback. `parameters` is empty for
a non-parameterized case. `parameter_name` is empty when not applicable.

Metadata classes are informational. Test authors do not instantiate them and
must not use object identity as a stable value.

## 10. Assertion model

Assertions are inherited instance methods on `GDTest`. They record results in
the currently executing case and return `void` unless stated otherwise.

Passing assertions must be cheap: they do not capture a stack, construct rich
diagnostic dictionaries, or serialize operands. Failure diagnostics include
the assertion's user-code file and line whenever Godot makes them available.

Argument order is consistently **actual first, expected second**:

```gdscript
expect_equal(actual, expected)
```

This order is part of the public contract.

### 10.1 Boolean assertions

```gdscript
func expect(condition: bool, message: String = "") -> void
func expect_not(condition: bool, message: String = "") -> void
func expect_true(condition: bool, message: String = "") -> void
func expect_false(condition: bool, message: String = "") -> void
```

`expect_true` is an alias for `expect`; `expect_false` is an alias for
`expect_not`.

### 10.2 Equality assertions

```gdscript
func expect_equal(
	actual: Variant,
	expected: Variant,
	message: String = "",
) -> void

func expect_not_equal(
	actual: Variant,
	expected: Variant,
	message: String = "",
) -> void
```

Equality follows these rules:

1. If `actual` is an Object with an `equals(other)` method, call it with
   `expected`. The method must return `bool`; another result is a framework
   assertion failure.
2. Otherwise, use normal Godot `==` semantics.

This is intentionally directional. Value objects generated by gdkit can
provide `equals`; an object wanting deeper semantics can implement `equals`
in terms of its generated `deep_equals` capability.

### 10.3 Identity assertions

```gdscript
func expect_same(
	actual: Variant,
	expected: Variant,
	message: String = "",
) -> void

func expect_not_same(
	actual: Variant,
	expected: Variant,
	message: String = "",
) -> void
```

These use Godot identity semantics equivalent to `is_same(actual, expected)`.
They are useful when normal equality is structural for a built-in collection.

### 10.4 Approximate numeric assertions

```gdscript
func expect_approx(
	actual: float,
	expected: float,
	tolerance: float = 0.00001,
	message: String = "",
) -> void

func expect_not_approx(
	actual: float,
	expected: float,
	tolerance: float = 0.00001,
	message: String = "",
) -> void
```

Tolerance is absolute and must be finite and non-negative. Approximate
assertions pass when `abs(actual - expected) <= tolerance`. Non-finite values
compare equal only when normal Godot float equality says they are equal; NaN
does not equal NaN.

### 10.5 Null assertions

```gdscript
func expect_null(value: Variant, message: String = "") -> void
func expect_not_null(value: Variant, message: String = "") -> void
```

These test `value == null`. A freed Object reference follows Godot's normal
validity/null behavior; use engine validity APIs when that distinction matters.

### 10.6 Empty assertions

```gdscript
func expect_empty(value: Variant, message: String = "") -> void
func expect_not_empty(value: Variant, message: String = "") -> void
```

Supported values are:

- String, StringName, and NodePath.
- Array and Dictionary.
- Packed arrays.
- Objects exposing `is_empty() -> bool`.

An unsupported value or a custom `is_empty` method returning a non-bool is an
assertion failure with a framework-generated diagnostic.

### 10.7 Containment assertions

```gdscript
func expect_contains(
	value: Variant,
	item: Variant,
	message: String = "",
) -> void

func expect_does_not_contain(
	value: Variant,
	item: Variant,
	message: String = "",
) -> void
```

Containment uses:

- `has(item)` for arrays, dictionaries, and packed arrays.
- `contains(str(item))` for String, StringName, and NodePath.
- `has(item) -> bool` for custom objects.

An unsupported value or invalid custom return type is an assertion failure.

### 10.8 Failure messages

When `message` is empty, gdtest supplies a concise default. User messages are
reported without being discarded or replaced. Equality-style failures attach
structured actual and expected values when they can be serialized.

The exact prose and terminal coloring of default messages are not stable API.
Structured status, source location, and actual/expected roles are stable.

### 10.9 Fluent assertions

Vest's `assert_that(value)` fluent assertion object is not part of gdtest v1.
It allocates an object for a convenience that direct assertions already cover.
Reusable domain assertions that emit structured diagnostics are written using
custom matchers via `expect_that` (section 10.10). The migration tool may
rewrite simple fluent chains into direct assertions and must report chains it
cannot translate safely.

### 10.10 Custom matchers

Projects may define domain-specific assertions by subclassing `GDTestMatcher`
and evaluating candidates with `expect_that`:

```gdscript
func expect_that(actual: Variant, matcher: GDTestMatcher, message: String = "") -> void
```

#### Matcher protocol

A custom matcher extends the `@abstract` base class `GDTestMatcher` and
implements both abstract methods:

```gdscript
@abstract
class_name GDTestMatcher
extends RefCounted

@abstract
func name() -> String

@abstract
func match(actual: Variant) -> GDTestMatchResult
```

- `name() -> String`: returns a non-empty, non-whitespace string identifying the
  assertion in test reports and failure diagnostics.
- `match(actual: Variant) -> GDTestMatchResult`: synchronously evaluates `actual`
  and returns a `GDTestMatchResult` constructed via `GDTestMatchResult.matched()`
  or `GDTestMatchResult.unmatched(...)`.

#### Match result

Evaluation outcomes are represented by immutable `GDTestMatchResult` instances:

```gdscript
class_name GDTestMatchResult
extends RefCounted

static func matched() -> GDTestMatchResult
static func unmatched(message: String, expected: Variant = null, has_expected: bool = false) -> GDTestMatchResult

var is_match: bool: get
var message: String: get
var expected: Variant: get
var has_expected: bool: get
```

Properties are getter-only. Container values (`Array`, `Dictionary`, and all
`Packed*Array` variants) are cloned defensively with recursion bounded to depth
16 and ancestor cycle detection to preserve container typing and prevent
post-evaluation caller mutations or cyclic structures from altering diagnostics.

#### Execution and failure reporting

1. Passing `expect_that` calls execute without capturing a call stack or allocating
   failure diagnostic payloads. On assertion failures or invalid outside-case
   calls, `expect_that` records the caller's source location, ensuring diagnostics
   point directly at the `expect_that` call site.
2. If called outside an active test case, `expect_that` records a source-located
   framework error without invoking `matcher`.
3. `GDTestMatcher` and its `name()` / `match(actual)` methods are `@abstract`, so
   Godot 4.7 rejects direct instantiation or unimplemented subclasses at script
   compile time. If at runtime `matcher` is null, returns an empty or
   whitespace-only name, returns null, or returns an invalid result (such as via
   direct `GDTestMatchResult.new()` or an empty mismatch message), `expect_that`
   records a source-located assertion failure with assertion `"expect_that"`
   without incrementing the valid assertion count.
4. On a passing match, `_gdtest_assertions` increments by 1 and case status is
   promoted to `"pass"`.
5. On a mismatch, `_gdtest_assertions` increments by 1, and an `assertion_failed`
   event is recorded with `assertion = matcher.name()`, actual value, optional
   expected value, and composed message (`"<caller>: <explanation>"` when
   `message` is non-empty).

#### Runnable example

```gdscript
extends GDTest

class HasEvenLength:
	extends GDTestMatcher

	func name() -> String:
		return "has_even_length"

	func match(actual: Variant) -> GDTestMatchResult:
		if not actual is Array and not actual is String:
			return GDTestMatchResult.unmatched("expected Array or String")
		var length: int = actual.size() if actual is Array else (actual as String).length()
		if length % 2 == 0:
			return GDTestMatchResult.matched()
		return GDTestMatchResult.unmatched("length is odd", length, true)


func test_custom_matcher() -> void:
	expect_that([1, 2, 3, 4], HasEvenLength.new())
	expect_that("sample", HasEvenLength.new(), "length check")
```

When custom matchers declare typed method signatures or return types in GDScript,
Godot 4.7's virtual machine validates type boundaries directly: passing an
incompatible type to a typed `match(actual: ...)` parameter or returning a
wrong type from a typed `match(...) -> GDTestMatchResult` triggers an engine
runtime script error (`status = "error"`). Untyped or dynamic matchers that
violate the matcher protocol fail closed through runtime framework validation,
producing source-located assertion failures (`status = "fail"` on assertion
`"expect_that"`).


## 11. Explicit outcomes

The following methods affect the current case:

```gdscript
func ok(message: String = "", data: Dictionary[String, Variant] = {}) -> void
func fail(message: String = "", data: Dictionary[String, Variant] = {}) -> void
func skip(message: String = "", data: Dictionary[String, Variant] = {}) -> void
func todo(message: String = "", data: Dictionary[String, Variant] = {}) -> void
```

Behavior:

- `ok` records a passing outcome, equivalent to a passing assertion.
- `fail` records a failure. Failure is sticky and cannot be overwritten by a
  later passing assertion or `ok`.
- `skip` marks the case skipped when it has not failed.
- `todo` marks the case todo when it has not failed.

GDScript has no general exception mechanism gdtest can use to unwind arbitrary
user code. `skip` and `todo` therefore do not return from the user's method.
Authors must return explicitly when code should not continue:

```gdscript
func test_network_feature() -> void:
	if not network_available():
		skip("Network unavailable")
		return

	expect_true(await use_network())
```

A failure or framework error after `skip`/`todo` changes the final status to
failed/error rather than hiding the problem.

A case with no assertion and no explicit outcome has status `void`. Void,
skip, and todo do not fail the run by default; strict CLI policies may do so.

## 12. Messages and output

```gdscript
func message(text: String) -> void
```

`message` attaches text to the currently executing case. Calling it during
discovery or outside a case is a framework error.

Messages preserve call order and appear in failure/verbose reports. Empty
messages are allowed but may be omitted from concise output.

Normal `print`, `push_warning`, and engine output are captured by the worker
when possible and attributed to the active run or case. Output capture is
separate from `message` and is not guaranteed to intercept native code writing
directly to process file descriptors.

## 13. Focused tests

Focused tests exist for short local feedback loops.

Supported forms:

- Method suffix: `func test_name__only()`.
- `test_only(description, callback)`.
- `define_only(name, callback)`.
- `test_each_only(description, cases, callback)`.
- The optional `is_only` argument on dynamic registration methods.

With the default `only` policy, if any focused case or suite exists in the
selected plan, only focused cases and descendants of focused suites execute.
Discovery still reports all tests.

CLI policy can:

- Honor focus automatically.
- Ignore focus and run all selected tests.
- Require focus and run nothing else.
- Fail before execution when focused tests are present, for CI enforcement.

`__only` is removed from the method's display name but remains recognizable in
source diagnostics. The logical method name used in the stable test ID also
removes `__only`; focus is never part of identity.

## 14. Async behavior and engine helpers

Test methods, dynamic case callbacks, dynamic suite callbacks, and lifecycle
hooks may suspend with `await`. The runner awaits completion before advancing
that serial execution scope.

The following inherited helpers are available:

```gdscript
func get_tree() -> SceneTree
func sleep(duration_seconds: float = 0.0) -> Error
func until(
	condition: Callable,
	timeout_seconds: float = 5.0,
	interval_seconds: float = 0.0,
) -> Error
func time() -> float
```

### 14.1 `get_tree`

Returns the worker's SceneTree. Tests may add nodes to its root. Tests are
responsible for freeing nodes and undoing connections they create. A clean
worker process is not guaranteed for every case unless fresh isolation is
requested through runner configuration.

Using `get_tree` marks a case as requiring the Godot backend for future backend
classification.

### 14.2 `sleep`

`await sleep(0.0)` waits for the next process frame. A positive duration waits
for a SceneTree timer. A negative or non-finite duration is a framework error.
It returns `OK` after the wait and `ERR_UNAVAILABLE` if no SceneTree exists.

### 14.3 `until`

Calls `condition` until it returns true or the timeout expires. It waits one
process frame when the interval is zero and uses a timer for a positive
interval. It returns `OK`, `ERR_TIMEOUT`, or `ERR_UNAVAILABLE`.

Timeout and interval must be finite and non-negative. A timeout of zero means
no deadline, matching Vest migration behavior. The active case's external
runner timeout still applies.

### 14.4 `time`

Returns monotonic seconds suitable for elapsed-time measurement. It must not be
documented as Unix wall-clock time.

### 14.5 Errors and timeouts

An uncaught GDScript runtime error, failed built-in `assert`, or case timeout is
an error outcome distinct from an assertion failure when the worker can
attribute it to the active case. If attribution is impossible or the worker
dies, the daemon reports an infrastructure error naming the last active case.

### 14.6 Signal probes

`watch_signal(what: Signal) -> GDTestSignalProbe` connects a case-scoped recorder
to `what` immediately and returns its probe. The probe records every emission in
order until the case finishes. It never asserts on its own; tests compare the
recorded values with the ordinary `expect_*` methods.

```gdscript
func test_reports_changed_values() -> void:
	var probe: GDTestSignalProbe = watch_signal(source.changed)
	source.set_value(3)
	expect_equal(probe.emission_count(), 1)
	expect_equal(probe.arguments_at(0), [3])
```

The probe's arity comes from the live owner's signal metadata
(`Object.get_signal_list()`), so authors never pass an argument count. Signals
with zero through eight arguments are supported. A signal whose metadata is
missing, ambiguous, or wider than eight arguments is reported as a framework
error and records nothing. Built-in and script-declared signals use the same
path.

Public members:

- `emission_count() -> int` returns the number of recorded emissions.
- `emissions() -> Array[GDTestInvocation]` returns a defensive copy of every
  recorded invocation in emission order.
- `arguments_at(index: int) -> Array[Variant]` returns a defensive copy of the
  arguments of one emission. An index outside the recorded range is a framework
  error that returns an empty array.
- `wait_until_count(count: int, timeout_seconds: float = 0.0) -> bool` suspends
  until at least `count` emissions are recorded and returns `true`. It returns
  `false` when `timeout_seconds` expires or the probe stops recording first. A
  timeout of `0.0` has no internal deadline; the case timeout remains
  authoritative. A negative count, or a timeout that is negative or not finite,
  is a framework error that returns `false` without waiting.
- `stop_recording() -> void` disconnects the recorder. Repeated calls have no
  further effect, and recorded emissions remain readable.

`GDTestSignalProbe` is named for what it does, and `stop_recording` is the
disconnect method. Godot's `Object.disconnect(signal, callable)` is a native
method that GDScript cannot override with a zero-argument signature; see
[ADR 0008](./docs/adr/0008-case-scoped-signal-probes.md).

Each invocation is a `GDTestInvocation` with two read-only members:

- `sequence: int` is the one-based position of the invocation within its case.
  Sequence numbers increase monotonically across every signal probe, callback
  probe, and generated test double in one case.
- `arguments: Array[Variant]` is a defensive copy of the recorded arguments in
  parameter order.

Probes belong to the case that created them. gdtest disconnects every probe
after the case's `after_case` or `after_each` hook and before the case result
is finalized, including timed-out and canceled cases. A connection made in one
case is therefore absent in the next case that reuses the same worker and test
instance. Calling `watch_signal` outside an executing case is a discovery or
framework error and returns an inert probe. A null signal, or one whose owner
has been freed, is a framework error and returns an inert probe.

### 14.7 Generated test doubles

A generated test double is a subclass of a project script. A test passes the
double wherever the source type is expected, so the code under test receives it
through its ordinary constructor, parameters, or properties. The double records
every call, returns results the test configured, and can run the real source
method on demand. gdtest generates doubles ahead of time from explicit
configuration. Nothing is synthesized at runtime, and no argument matchers are
provided: tests assert on `calls()`.

Doubles do not monkey-patch existing instances. They do not intercept native
or static methods, replace properties, or inject dependencies into production
objects. An instance that was not constructed as a double is never changed.

#### Configuration

Doubles are declared in `test_doubles` in `gdtest.json`:

```json
{
  "test_doubles": [
    {
      "source": "inventory.gd",
      "output": "doubles/inventory_double.gd",
      "class_name": "InventoryDouble"
    }
  ]
}
```

- `source` is the project-relative path of the script to double. It must end in
  `.gd`.
- `output` is the project-relative path of the generated script. It must end in
  `.gd`, must not be under `addons/gdtest/` in any letter case, and must not equal any
  `source`.
- `class_name` is optional. When it is present it must be a GDScript identifier
  and unique across entries. When it is empty, the generated script declares no
  `class_name`.

Paths use forward slashes, must be clean relative paths, and must not contain a
`..` segment. A path may contain spaces, but it must not begin or end with
whitespace, must not contain a double quote or a control character (a code
below `0x20` or `0x7f`), and must not contain a `.godot` or `.git` segment, in any letter case, at
any depth, because those directories are never indexed and an output placed
there could not be found again. Every `source` and every `output` must be
unique across entries; outputs are compared ignoring case, because paths that
differ only in case name one file on case-insensitive filesystems. Unknown
fields are rejected. Each error names the entry
index, for example `test_doubles[1].output`.

#### Command

```sh
gdtest generate doubles [--check] [--project DIR]
```

Without `--check`, the command writes every desired file that differs from disk
and removes owned files that are no longer configured. It prints
`N file(s) written; M file(s) removed` and exits `0`. With `--check`, it writes
nothing. It prints one line per difference, `missing: <path>`, `stale: <path>`,
or `unexpected: <path>`, sorted by path. It exits `0` and prints
`0 double(s) out of date` when nothing differs.

| Exit code | Meaning |
|---|---|
| `0` | Written, or `--check` found every generated file up to date. |
| `1` | `--check` found a missing, stale, or unexpected generated file. |
| `2` | Configuration, parse, signature, ownership, or filesystem error. Nothing is written. |

Invalid inputs always win over out-of-date outputs. An invalid input is reported
with a source location when one exists, and no entry is written unless every
entry is valid.

A write that fails before its first rename changes no file: every changed file
is staged as a temporary beside its target, and every owned file to remove is
confirmed to be a regular file in a directory that accepts a temporary which
can then be removed. If any of these checks fails, the command exits `2`, removes
its temporaries, and leaves the project as it was. A filesystem error during the
rename or removal phase can leave some outputs installed. The next run converges
that state from the files on disk.

#### Output and ownership

The first line of every generated script is the ownership marker:

```gdscript
# gdtest:generated-double v1 source=res://inventory.gd
```

The command rewrites or removes only files that begin with this exact marker.
A configured `output` that exists without the marker is refused with exit `2`,
and so is a `.gd.uid` beside a configured output whose `.gd` is missing. An
output that is a symbolic link, or that resolves outside the project through a
symbolic link, is refused. Ownership is established only by the marker; no
state file records what was generated.

Each output has a `.gd.uid` sidecar whose value is derived from the output's
`res://` path. The same configuration, sources, gdtest version, and gdparser
version produce byte-identical files regardless of project location, operating
system, or directory walk order. Regenerating unchanged inputs changes no file.

#### Which methods are doubled

- Every non-static function whose name does not begin with `_` is doubled. The
  function may be declared at the top level of the source script or of any
  ancestor that the source extends through a project script. The nearest
  declaration wins, and doubled methods are emitted in byte order of their names.
- The generated constructor mirrors the nearest `_init` in the project-script
  chain and forwards its arguments to the real constructor. Without one, the
  constructor takes only the context.
- An asynchronous source method is awaited by the double.

Generation is refused, with a source-located diagnostic and exit `2`, when a
doubled method has an untyped, inferred (`:=`), or variadic parameter, has no
return type, or cannot be given a return type the generator supports. It is also
refused when a name collides with a reserved name or a generated name, or when a
doubled method has the name of a method that `Object` or `RefCounted` already
defines. Reserved names are listed in section 17.

#### Context lifecycle

`double_context() -> GDTestDoubleContext` returns the executing case's context.
The first call in a case creates it; later calls in the same case return the
same context. Doubles and signal probes created in one case share one call
sequence, so `calls()` and `watch_signal` records can be ordered against each
other by `sequence`.

Calling `double_context()` outside an executing case is a discovery or framework
error and returns a closed context. Gdtest closes the case's context when the
case finishes, including when it fails, times out, or is canceled. A double built
with a closed context records nothing into any case. Each call to it reports
through `push_error` with the form
`gdtest: <Name>: construct doubles with double_context() inside an executing case`,
and returns the neutral default.

#### Controller API

A double exposes its controllers through the `gdtest` member. Each doubled method
has a typed controller, reached as `double.gdtest.<method_name>`:

- `then_return(value: T) -> void` queues a value of the method's return type. A
  void method has `then_return() -> void`, which marks the next call as expected
  and returns nothing. Configuration methods return `void` and do not chain.
- `then_answer(answer: Callable) -> void` queues a callable that receives the
  forwarded arguments and returns the result. The callable's argument count must
  equal the method's parameter count, including defaulted parameters. An answer
  is awaited only when the doubled method is asynchronous, meaning the source
  declaration awaits. An answer for a synchronous method must itself be
  synchronous. A coroutine answer for a synchronous method raises Godot's script
  error `Trying to call an async function without "await".` in the case, and
  execution stops before any value returns. A generated method for a synchronous
  source stays synchronous, so the source method's calling contract does not
  change.
- `then_call_real() -> void` queues a call to the source method through `super`.
  It is rejected for an abstract method.
- `calls() -> Array[GDTestInvocation]` returns a defensive copy of every recorded
  call in call order. Each invocation has a `sequence` and a defensive copy of
  its `arguments`. Defaulted parameters appear in `arguments` with their values.
- `call_count() -> int` returns the number of recorded calls.
- `reset() -> void` clears queued behaviours and recorded calls.

#### Queued behaviour order

Each call consumes the front queued behaviour (first in, first out). The last
remaining behaviour is sticky: it repeats for every later call. With
`then_return(true)` followed by `then_return(false)`, the first call returns
`true` and every later call returns `false`.

A call with no queued behaviour is unexpected. It is recorded, reported as
described below, and returns the neutral default of the return type.

#### Typed collection answers

An answer for a method whose return type is an enum must be an integer. The
global enums of Godot 4.7 (`Error`, `Key`, `MouseButton`, and the rest of
`@GlobalScope`) and the `Variant.Type` and `Variant.Operator` enums are checked
this way, so a null answer for `-> Error` fails with
`answer returned Nil, expected Error`.

An answer for a method that returns a typed collection must return an identically
typed collection. An untyped literal is rejected even when its elements are
correct: `Array[int]().is_same_typed([7])` is `false` in Godot 4.7, so an answer
that returns `[7]` for an `Array[int]` return fails. Return
`Array[int]` values constructed with that type.

#### Failures

The following conditions are framework errors, recorded as failures of the
executing case at the caller's line rather than in the generated file. The
double then returns the neutral default of its return type.

| Condition | Message |
|---|---|
| Call with no queued behaviour | `unexpected call to Inventory.add` |
| Answer returns the wrong type | `Inventory.add answer returned String, expected bool` |
| `then_answer` callable has the wrong arity | `then_answer callable takes 1 arguments; expected 2`, with nothing queued |
| `then_answer` callable is invalid | `... then_answer requires a valid callable`, with nothing queued |
| `then_call_real` on an abstract method | `... cannot call an abstract method through then_call_real`, with nothing queued |

The location is the first stack frame outside gdtest and the generated double,
so a failure points at the line in the test that made the call or configured the
double.

#### Example

The example project in `examples/basic` contains a source script, `inventory.gd`,
and a configured double. `shop.gd` receives an `Inventory` through its
constructor, and `inventory_double.test.gd` injects an `InventoryDouble`:

```gdscript
func test_queued_results_are_consumed_in_order_then_last_is_sticky() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	var shop: Shop = Shop.new(inventory)
	inventory.gdtest.add.then_return(true)
	inventory.gdtest.add.then_return(false)
	expect_true(shop.stock("apple"))
	expect_false(shop.stock("pear"))
	expect_false(shop.stock("plum"))
	expect_equal(inventory.gdtest.add.call_count(), 3)


func test_then_answer_async_result_is_awaited() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	var shop: Shop = Shop.new(inventory)
	inventory.gdtest.fetch_counts.then_answer(_snapshot_after_frame)
	var report: Dictionary[String, int] = await shop.stock_report()
	expect_equal(report, { "apple": 2 })
```

The file `examples/basic/inventory_double.test.gd` also covers `then_call_real`,
`then_return()` on a void method, `calls()` arguments including a defaulted
parameter, and `reset()`. The generated file `examples/basic/doubles/inventory_double.gd`
is committed and checked by `scripts/check`.

### 14.8 Dedicated benchmarks

Benchmarks are separate from correctness tests. A benchmark file conventionally ends in
`.bench.gd`, extends `GDTestBenchmark`, and declares one or more typed methods:

```gdscript
extends GDTestBenchmark

var _values: Array[int] = []


func before_sample(_info: GDTestBenchmarkInfo) -> void:
	_values = [5, 3, 1, 4, 2]


func benchmark_sort_integers() -> void:
	_values.sort()
```

A benchmark method is named `benchmark_*`, takes no arguments, and explicitly returns `void`.
It may suspend. Its stable ID is `<project-relative-file>::<method-name>`.

The optional typed hooks are:

```gdscript
func before_all() -> void
func after_all() -> void
func before_benchmark(info: GDTestBenchmarkInfo) -> void
func after_benchmark(info: GDTestBenchmarkInfo) -> void
func before_sample(info: GDTestBenchmarkInfo) -> void
func after_sample(info: GDTestBenchmarkInfo) -> void
```

Every hook is awaited and excluded from the timed interval. `before_all` and `after_all` run once
per non-empty benchmark file. The benchmark hooks run once per selected method. Sample hooks run
for both discarded warmups and measured samples. `GDTestBenchmarkInfo` exposes immutable `id`,
`name`, `file`, `line`, `method_name`, `warmup`, `sample_index`, and `iterations` fields.

`gdtest bench` defaults to three warmups, thirty samples, and one method invocation per sample.
Each sample invokes the method `iterations` times and records the monotonic engine duration
normalized per invocation. Reports retain every normalized sample and deterministically derive
min, median, p95, and max. Timing deltas are informational and never become correctness failures.

`GDTestBenchmark.get_tree()` and `sleep()` support asynchronous setup and benchmark bodies. The
benchmark base has no assertion API: correctness belongs in `GDTest` tests.

### 14.9 Callback probes

`callback_probe(argument_count: int = 0) -> GDTestCallbackProbe` creates a
case-scoped recorder for an injected `Callable` parameter, such as an
`on_complete`, `on_error`, retry, or progress handler. Callbacks with zero
through eight arguments are supported, and calls are recorded synchronously
before returning to the caller. Like `watch_signal`, the probe never invents an
assertion on its own; tests assert on its recorded invocations with the ordinary
`expect_*` methods.

```gdscript
func test_awaits_injected_completion_callback() -> void:
	var inventory: Inventory = Inventory.new(3)
	var checkout: CheckoutFlow = CheckoutFlow.new(inventory, get_tree())
	var on_complete: GDTestCallbackProbe = callback_probe(2)
	var on_error: GDTestCallbackProbe = callback_probe(1)
	checkout.purchase_async("apple", 2, on_complete.callback(), on_error.callback())
	expect_true(await on_complete.wait_until_count(1, 1.0))
	expect_equal(on_complete.arguments_at(0), ["apple", 2])
	expect_equal(on_error.invocation_count(), 0)
```

Public members on `GDTestCallbackProbe`:

- `callback() -> Callable` returns an exact-arity `Callable` accepting
  `argument_count` `Variant` arguments, or an invalid `Callable()` when the
  probe was requested outside an active case or with an unsupported arity.
- `invocation_count() -> int` returns the number of invocations recorded while
  the probe was open.
- `invocations() -> Array[GDTestInvocation]` returns a defensive copy of every
  recorded invocation in call order. Each `GDTestInvocation` carries its
  one-based case `sequence` and a defensive snapshot of `arguments`.
- `arguments_at(index: int) -> Array[Variant]` returns a defensive copy of the
  arguments recorded at `index`. An index outside the recorded range reports a
  source-located framework error and returns an empty array.
- `wait_until_count(count: int, timeout_seconds: float = 0.0) -> bool` suspends
  until at least `count` invocations are recorded and returns `true`. It returns
  `false` when `timeout_seconds` expires or the probe closes first. A timeout of
  `0.0` has no internal deadline; the active case's external runner timeout
  remains authoritative. A negative `count`, or a `timeout_seconds` that is
  negative or not finite, reports a source-located framework error and returns
  `false` without waiting or mutating recorded invocations.
- `close() -> void` closes the probe. Repeated calls have no further effect, and
  invocations recorded before closing remain readable.
- `is_closed() -> bool` returns whether the probe is closed or was created in an
  inert state.

Signal probes (`watch_signal`), callback probes (`callback_probe`), and
generated test doubles (`double_context`) created in the same case share one
monotonic one-based sequence allocator, so tests can assert relative ordering
across emitted signals, injected callbacks, and dependency calls through
`GDTestInvocation.sequence`.

gdtest closes every callback probe automatically after the case's `after_case`
or `after_each` hook and before finalizing the case result, including timed-out
and canceled cases. Closing detaches the returned `Callable` from the `GDTest`
instance, `SceneTree`, and probe ledger so retained project references cannot
keep completed test suites alive on persistent workers. Invoking a closed
callback mutates no probe state, emits no cross-case failure or output, and
increments only a bounded worker-level diagnostic counter reported at debug log
severity. Requesting `callback_probe` outside an executing case reports a
source-located discovery or framework error and returns a closed inert probe; an
`argument_count` outside `0..8` reports a source-located framework error and
returns a closed probe with an invalid `Callable()`.

The runnable example in `examples/basic/callback_probe.test.gd` exercises both
asynchronous completion/error callbacks and cross-source sequence ordering with
`watch_signal` and `InventoryDouble`.

### 14.10 Scoped autofree and leak detection

`autofree(node: Node) -> Node` registers a `Node` with the active execution
scope so gdtest frees it automatically when that scope finishes, and returns
`node` unchanged so construction and registration compose inline:

```gdscript
func test_autofrees_case_node_attached_to_tree() -> void:
	var timer: Timer = autofree(Timer.new())
	timer.name = "CaseTimer"
	timer.one_shot = true
	_shared_container.add_child(timer)
	expect_true(timer.is_inside_tree())
	expect_equal(_shared_container.get_child_count(), 1)
```

#### Scope mapping

| Where `autofree` is called | Owning scope | Freed |
|---|---|---|
| Case body, `before_case`, `after_case`, `before_each`, `after_each` | `case` | After the case's closing hook |
| `before_suite` (root or nested) | `suite` | After the matching `after_suite` |
| `before_all` | `file` | After `after_all` |

Registering the same node more than once in the same scope has no additional
effect. If a node is registered in more than one open scope, the narrowest
scope wins, so each registered node is freed at most once at the earliest
closing scope.

Calling `autofree(null)` or passing a freed instance reports a source-located
error and registers nothing. Calling `autofree` during discovery or
registration (`script.new()` member initializers, `suite()`, `define()`
callbacks, `*__cases()` providers, or `get_suite_name()`), during suite or
file closing hooks (`after_suite` or `after_all`), during end-of-scope
release or frame drain (such as `_exit_tree()` or `NOTIFICATION_PREDELETE`
callbacks triggered while gdtest closes a scope), or outside an active file,
suite, or case scope reports a source-located discovery or framework error and
registers nothing.

#### End-of-scope teardown and leak detection

For every case, suite, and file scope that completes without timeout or
cancellation, gdtest executes in order:

1. Run the scope's closing hook (`after_case` or `after_each`, `after_suite`,
   or `after_all`).
2. Release any case-scoped probes (for `case` scopes), then free the scope's
   `autofree` registrations in reverse registration order with `Object.free()`,
   skipping already-freed instances and descendants whose ancestor is freed in
   the same pass.
3. Await one `SceneTree.process_frame` so pending `queue_free()` deletions
   drain before inspection. Ordinary `queue_free()` cleanup is never reported as
   a leak.
4. Run the post-scope leak check unless `leak_check.policy` is `"off"`.
5. Finalize the scope result.

Timed-out and canceled scopes skip steps 2 through 4 because arbitrary user
coroutines may still be mutating nodes while the tainted worker shuts down.

At scope start, gdtest snapshots both `Node.get_orphan_node_ids()` and the
instance IDs of every node reachable from `SceneTree.root` (including internal
children and autoload subtrees). At step 4, gdtest compares current state
against that baseline and reports:

- **Stray leaks** (`kind: "stray"`): nodes still inside `SceneTree` whose
  instance ID was absent from the tree baseline.
- **Orphan leaks** (`kind: "orphan"`): unparented live `Node` instances in
  `Node.get_orphan_node_ids()` whose instance ID was absent from the orphan
  baseline.

Survivors from a completed inner scope are absorbed into open enclosing suite
and file baselines so a node leaked inside a case is reported once on that case
rather than re-reported when enclosing `suite` and `file` scopes close.

Each leak set is collapsed to un-ignored subtree roots: a leaked node is
reported as a root when its parent is null, invalid, present in the baseline,
or outside the un-ignored leaked set, with `descendants` counting collapsed
leaked descendants under that root. Records are plain JSON-safe dictionaries
(`kind`, `class`, `name`, `script`, `path`, `descendants`) ordered with strays
first sorted by UTF-8 `path`, then orphans sorted by UTF-8 `class` and `name`,
with instance ID as final tiebreaker. At most 20 root records are serialized in
`metadata.leaks`; additional roots set `metadata.omitted` and append
`, and N more` to the diagnostic message.

#### Policy and allowlist configuration

`gdtest.json` configures leak checking through the optional `leak_check` object:

```json
{
  "leak_check": {
    "policy": "fail",
    "ignore": ["res://ui/toast_layer.gd", "Timer"]
  }
}
```

- `policy`: `"fail"` (the default when `leak_check` or `policy` is omitted),
  `"warn"`, or `"off"`. Under `"fail"`, leaks emit `assertion_failed` with
  `assertion: "leak"` and promote case status to `fail` (`error` retains higher
  precedence) or fail the file for suite/file hook leaks. Under `"warn"`, leaks
  emit `case_warning` with the same payload without changing pass/fail status.
  Under `"off"`, step 4 is skipped while `autofree` and the one-frame
  `queue_free()` drain remain active.
- `ignore`: optional array of unique clean `res://...gd` script paths or valid
  GDScript identifiers. A leaked node and its entire subtree are exempt when the
  node or any ancestor matches an entry via attached or base-script
  `resource_path`, `Node.is_class(entry)`, or global `class_name` in its script
  inheritance chain.

The runnable example in `examples/basic/autofree.test.gd` exercises both
suite-scoped and case-scoped `autofree` registration alongside `queue_free()`
drainage.

## 15. Case status and precedence

Public case statuses are:

- `pass`
- `fail`
- `error`
- `skip`
- `todo`
- `void`
- `canceled`

For a single case, stronger outcomes take precedence:

```text
error > fail > todo > skip > pass > void
```

`canceled` describes incomplete execution rather than an aggregate assertion
outcome.

Multiple passing assertions still produce one passing case. Every failed
assertion may be retained for diagnostics; implementations may cap retained
failure detail with a clearly reported limit to prevent unbounded memory use.

## 16. Stable IDs and source locations

User-visible IDs are independent of absolute worktree paths.

Method test:

```text
<project-relative-file>::<logical-method-name>
```

Named parameter row:

```text
<project-relative-file>::<logical-method-name>[<escaped-name>]
```

Indexed parameter row:

```text
<project-relative-file>::<logical-method-name>[<zero-based-index>]
```

Dynamic IDs additionally include suite path and definition location or a
deterministic registration ordinal:

```text
<project-relative-file>::dynamic@<line>:<ordinal>[<escaped-full-name>]
```

`ordinal` is zero-based among dynamic cases registered from the same source
line. `full-name` percent-encodes each nested suite name and the case
description independently, then joins those components with a literal `/`.

Name components and named parameter keys use RFC 3986 percent-encoding over
UTF-8 bytes: ASCII letters, digits, `-`, `.`, `_`, and `~` remain literal; all
other bytes are `%HH` with uppercase hex. The file portion is the normalized
project-relative path with forward slashes and no `res://` prefix. GDScript
identifiers need no additional escaping.

A benchmark method ID is:

```text
<project-relative-file>::<benchmark-method-name>
```

File locations use `res://` paths and one-based lines. Unknown lines are `-1`.
Reports and selectors must not expose the absolute worktree path as identity.

## 17. Reserved names

The following names/patterns are reserved by gdtest:

- `suite`
- `get_suite_name`
- `callback_probe`, the test method that creates a case-scoped callback recorder.
- `double_context`, the test method that returns a case's double context.
- `expect_that`, the test method that evaluates a custom matcher.
- `autofree`, the test method that registers a `Node` for automatic scope-end teardown.
- `gdtest` and `GDTestControls`, the controller member and class that generated
  test doubles expose.
- Lifecycle hook names in section 8.
- `test_*__cases` parameter providers.
- `test_*__only` focus suffix.
- Methods beginning with `__gdtest_`.
- Benchmark methods beginning with `benchmark_` and the benchmark hooks in section 14.8.

Test authors should not override inherited methods from `GDTest` except the
documented hooks and naming methods.

## 18. Deferred and intentionally absent APIs

The following Vest-facing concepts are not gdtest v1 APIs:

- Lifecycle signals (`on_begin`, `on_case_begin`, and similar).
- Runtime-generated mocks (`mock`, `when`, `get_calls_of`). Ahead-of-time generated
  doubles (section 14.7) cover the typed subset of this use case.
- Vest's fluent benchmark builders and measure objects. Use the dedicated API in section 14.8.
- Fluent `assert_that` objects. Reusable domain assertions are implemented as custom matchers via `GDTestMatcher` and `expect_that` (section 10.10).
- Test mixin generation or user-configurable inheritance chains.
- Public mutable suite/result definition objects.
- Directory traversal, globbing, project settings, and runner-control helpers
  exposed through a test singleton.
- Reusable timeout-budget objects equivalent to `Vest.timeout`.
- Vest's global signal-to-array registry (`capture_signal`,
  `get_signal_emissions`). Case-scoped `watch_signal` probes (section 14.6)
  replace the non-persistent subset that migration can prove equivalent.

Mocks are provided by ahead-of-time generated doubles (section 14.7), not by
runtime synthesis. Signal capture may later return as a small engine-only helper. Their absence in v1 is
deliberate and migration tooling must report uses precisely.

## 19. Vest migration map

The migration tool should implement or report these mappings without needing
Vest runtime internals.

| Vest source API | gdtest target | Migration |
|---|---|---|
| `extends VestTest` | `extends GDTest` | Mechanical |
| `get_suite_name` | Same | No change |
| `suite`, `define`, `test` | Same public shape | Usually no change |
| `define_only`, `test_only`, `__only` | Same | No change |
| `before_all`, `after_all` | Same | No change |
| `before_suite`, `after_suite` | Same names, gdtest info types | Type rewrite if annotated |
| `before_case`, `after_case` | Same names, gdtest info types | Type rewrite if annotated |
| Direct `expect_*` assertions | Same actual/expected order | Usually no change |
| `ok`, `fail`, `skip`, `todo` | Same | Usually no change |
| `Vest.message(x)` | `message(x)` | Mechanical |
| `Vest.get_tree()` | `get_tree()` | Mechanical |
| `await Vest.sleep(x)` | `await sleep(x)` | Mechanical |
| `await Vest.until(...)` | `await until(...)` | Mechanical |
| Vest default-argument parameter provider | `<test>__cases` provider | Syntax-aware rewrite |
| `assert_that` | Direct assertions or custom matcher (`expect_that`) | Rewrite simple chains; report others with a pointer to [docs/vest-migration.md](./docs/vest-migration.md) |
| Lifecycle signals | Hooks | Report or rewrite only when unambiguous |
| `Vest.timeout` | No v1 equivalent | Report |
| Runtime mocks (`mock`, `when`, `get_calls_of`) | Configured generated double (section 14.7) for the typed subset; otherwise hand-written | Report with a pointer to [docs/vest-migration.md](./docs/vest-migration.md); not converted mechanically |
| Benchmarks | `GDTestBenchmark` plus `benchmark_*` methods | Report with the manual recipe in [docs/vest-migration.md](./docs/vest-migration.md); do not translate the builder DSL mechanically |
| `capture_signal(member_signal, literal_count)` in a `test_` function, with reads only as `get_signal_emissions(...).size()`, an index into the result, or an emptiness check | `var probe := watch_signal(member_signal)` with reads rewritten to `probe.emission_count()`, `probe.arguments_at(...)`, or `probe.emissions()` | Syntax-aware rewrite when unambiguous |
| Persistent, aliased, nested, repeated-in-one-test, reassigned-owner, non-literal-count, or otherwise ambiguous signal capture | No v1 equivalent | Report and preserve |

Vest's parameterized method form resembles:

```gdscript
func test_capitalize(input: String, expected: String, __ = "capitalize_cases"):
	pass
```

Migration removes the provider-name default argument and renames or wraps the
provider as `test_capitalize__cases`. If a provider is shared by several tests,
the migration tool may create small forwarding provider methods rather than
duplicating data.

The migration tool must not silently change assertion argument order, delete
unsupported code, or replace a test with a weaker assertion.

## 20. API versioning

The addon and executable share a semantic product version.

Before 1.0, this specification is the target and breaking changes require a
documented migration note. At and after 1.0:

- Adding an assertion or optional method is a minor change.
- Adding a required method argument is a major change.
- Changing discovery, hook ordering, assertion roles, equality rules, status
  precedence, or stable ID construction is a major change.
- Default diagnostic prose and terminal styling may change in a patch release.
- Worker/client protocol versions are independent implementation concerns and
  do not define the public API version.

Deprecated APIs must emit actionable diagnostics for at least one minor
release before removal unless retaining them would produce incorrect results.

## 21. API conformance requirements

The runtime is conformant when tests prove all of the following:

1. Method tests are discovered in source order with stable IDs.
2. Named and indexed parameter providers create distinct cases and hook runs.
3. Dynamic nested suites preserve registration and hook order.
4. Async registration, hooks, and cases are awaited.
5. Every assertion has passing, failing, custom-message, and invalid-input
   coverage where applicable.
6. Passing assertions do not collect stack traces or rich values.
7. Failure remains sticky after later passing assertions.
8. Skip/todo followed by failure cannot hide the failure.
9. Void behavior and strict policies are represented correctly.
10. Focus behavior is plan-wide and can be rejected in CI mode.
11. Metadata is project-relative and cannot be mutated to change execution.
12. SceneTree helpers obey timing, timeout, and error contracts.
13. Common Vest fixtures migrate and execute without semantic weakening.
14. Unsupported Vest APIs produce source-located migration findings.
15. Equivalent tests in two worktrees receive identical stable IDs.
16. The runtime and every executable example pass strict gdkit typing/lint and
    canonical gdkit formatting checks over the actual shipped addon path.
17. Benchmark discovery rejects invalid signatures with source locations, hooks are untimed,
    warmups are discarded, and normalized raw samples reproduce every reported statistic.
18. Benchmark IDs, results, baselines, and comparisons remain project-relative and stable across
    equivalent worktrees.
19. Custom matchers (`GDTestMatcher`, `GDTestMatchResult`, `expect_that`) execute synchronous domain checks,
    enforce fail-closed contracts, attribute failure locations to the caller, and defend against post-evaluation
    collection mutation.
20. Scoped `autofree` frees registered `Node` instances in reverse order at case, suite, and file
    scope boundaries without double-freeing narrowed or parent-covered nodes, drains `queue_free()`
    deletions before post-scope orphan and `SceneTree` leak checks, honors `fail`, `warn`, `off`,
    and `ignore` subtree exemptions, and skips teardown and leak checks on timed-out or canceled
    scopes.

Examples and API documentation in the repository must be executable
conformance fixtures rather than untested prose snippets wherever practical.

