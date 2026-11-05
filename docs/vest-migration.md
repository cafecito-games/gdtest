# Migrating from Vest

Use check mode first. It parses GDScript and reports the exact source-located edits and manual work without changing files:

```sh
gdtest migrate vest --check
```

Pass project-relative files or directories to limit the check. When the report is acceptable, apply every safe edit atomically:

```sh
gdtest migrate vest --write
```

The migration is syntax-aware; it does not use textual regular-expression replacement. Write mode prepares all requested transformations before publishing any file, refuses paths outside the project, and preserves unsupported calls for manual conversion.

## Mechanical conversions

gdtest converts conventional uses of:

- `extends VestTest` to `extends GDTest`;
- `VestDefs.Suite` and `VestDefs.Case` lifecycle arguments to gdtest metadata types;
- unambiguous Vest utilities such as `Vest.message` and `Vest.sleep` to inherited gdtest helpers;
- nested-class `Vest.get_tree()` calls to the typed active engine `SceneTree`;
- local, parameter, loop, and lambda names that collide with the new base API by adding the
  narrow Godot warning annotation to the exact declaration statement;
- a simple `_params: String = "provider"` convention to a `<test>__cases` provider wrapper;
- supported single-matcher fluent assertions to direct gdtest assertions without changing actual-first argument order.

Compatible assertion names and unrelated formatting are left alone. Run the migrated suite before deleting Vest from the project.

## Manual findings

gdtest does not preserve Vest's fluent benchmark builder, generated mixins, event callbacks, or arbitrary fluent matcher chains. Benchmarks move to the dedicated `GDTestBenchmark` workflow by hand. Runtime-generated mocks (`mock`, `when`, `get_calls_of`) are not converted mechanically; a typed double generated ahead of time from `gdtest.json` replaces the common uses, as described under "Runtime mocks and benchmarks" below. These constructs remain in the source and are reported with a file, line, column, stable finding code, and explanation. A finding never silently deletes or weakens a test.

Signal captures are the exception that migrates. A `capture_signal(member_signal, literal_count)`
statement that is a top-level statement of a `test_` function becomes a case-scoped
`watch_signal` probe when every `get_signal_emissions(member_signal)` read in that function is a
`.size()` call, an index into the result, or an emptiness check (`expect_empty`, `expect_not_empty`,
or `expect_equal(..., [])`). Such reads become `emission_count()`, `arguments_at(...)`, and
`emissions()` calls. The probe infers the signal's arity from Godot, so the migrator drops the
count literal. Each signal converts all of its captures and reads together or leaves all of them
untouched.

The migrator preserves a capture and reports a precise finding when it cannot prove the rewrite:
persistent or class-level captures, captures nested in a conditional block, owners reassigned inside
the test, signals also read through an alias, repeated captures of one signal within one test,
non-literal counts, captures inside helper functions that are not `test_` functions, local aliases of
a read result, and whole-list comparisons with non-empty expected values. Because a read that lacks a
matching capture may be an alias the migrator cannot see, one such read anywhere in a file keeps every
signal in that file as written. Files that declare their own `capture_signal`, `get_signal_emissions`,
or `watch_signal` function are left alone for signal migration. Migrated probes are disconnected
automatically when each case ends.

For a preserved capture, signal behavior remains testable with native Godot connections. For example, a one-argument
capture can be migrated explicitly without adding a framework API:

```gdscript
var emissions: Array[Array] = []
var connect_error: Error = subject.changed.connect(
	func(value: Variant) -> void: emissions.append([value])
)
expect_equal(connect_error, OK)

subject.change()

expect_equal(emissions, [[subject.value]])
```

Keep a small project-local recorder when many preserved fixtures need the same zero-, one-, or two-argument
pattern. This is intentionally an application helper rather than a gdtest v1 compatibility API.

Dynamic tests, async tests, and supported focus markers continue to work when they use APIs covered by the gdtest runtime. Project-specific wrappers and configuration still require review; in particular, replace Vest CLI invocations in local scripts and CI with `gdtest run` after the migrated suite passes.

## Converting preserved constructs by hand

Anything the migrator reports and preserves can be converted by hand. Each recipe below names the gdtest API to use. The authoritative contract for every API is in `api-spec.md`.

### Signal captures

`watch_signal` replaces `capture_signal` once the signal is known. The probe infers arity from Godot, so drop the count argument.

| Vest | gdtest |
| --- | --- |
| `capture_signal(owner.signal_name, n)` | `var probe: GDTestSignalProbe = watch_signal(owner.signal_name)` |
| `get_signal_emissions(owner.signal_name).size()` | `probe.emission_count()` |
| `get_signal_emissions(owner.signal_name)[i]` | `probe.arguments_at(i)` |
| `get_signal_emissions(owner.signal_name)[i][j]` | `probe.arguments_at(i)[j]` |
| `expect_empty(get_signal_emissions(...))` | `expect_empty(probe.emissions())` |
| Wait for a later emission | `await probe.wait_until_count(n, timeout_seconds)` |

Keep the probe in the test that creates it. Probes are disconnected when the case ends, so do not store one in a class member or reuse it in another test. If one test needs the whole argument list of a signal in a non-trivial shape, use `probe.emissions()` and compare each invocation's `arguments` explicitly. Do not wrap the list in a helper that outlives the case. Signals with more than eight arguments are not supported by the probe; test them with a native connection in a project-local helper.

### Native connections for anything else

When a capture cannot move to a probe, connect with Godot directly and record into a local array inside the test. This is always valid:

```gdscript
func test_reports_changed_values() -> void:
	var recorded: Array[Variant] = []
	var recorder: Callable = func(value: Variant) -> void:
		recorded.append(value)
	subject.changed.connect(recorder)
	subject.change()
	subject.changed.disconnect(recorder)
	expect_equal(recorded, [subject.value])
```

Disconnect explicitly, because a native connection does not follow the case lifecycle.

### Lifecycle signals

Replace `on_begin`, `on_case_begin`, and similar lifecycle signals with the matching hooks described in `api-spec.md` section 8. Hooks run in a fixed order, so do not depend on signal connection order.

### Fluent assertions

Replace each chained matcher with one direct `expect_*` call with the same actual-first argument order. For multi-matcher chains, write one `expect_*` per matcher.

### Runtime mocks and benchmarks

gdtest does not synthesize mocks at runtime. Instead, declare a typed double of the real class in `gdtest.json` and generate it ahead of time with `gdtest generate doubles`. The generated double is a subclass of the source script, so it is passed wherever the source type is expected. See `api-spec.md` section 14.7 for the full contract.

Convert a Vest mock by hand with these steps:

1. Add an entry for the class that the test mocks. The `source` is the project-relative path of its script; `output` is a path under a directory the project owns, and `class_name` names the generated class:

   ```json
   "test_doubles": [
     { "source": "inventory.gd", "output": "doubles/inventory_double.gd", "class_name": "InventoryDouble" }
   ]
   ```

2. Run `gdtest generate doubles` and commit the generated `.gd` and `.gd.uid` files. Do not edit them; run `gdtest generate doubles --check` in CI.

3. Replace `mock(Class)` with the generated class and pass the constructor arguments the real class requires, plus `double_context()` first. Replace each `when(mock.method).then_return(value)` with `double.gdtest.method.then_return(value)`, and each `get_calls_of(mock.method)` with `double.gdtest.method.calls()` or `call_count()`.

Before, with Vest:

```gdscript
func test_stock_is_added_to_inventory() -> void:
	var inventory = mock(Inventory)
	when(inventory.add).then_return(true)
	var shop := Shop.new(inventory)
	expect_true(shop.stock("apple"))
	expect_equal(get_calls_of(inventory.add).size(), 1)
```

After, with a configured generated double:

```gdscript
func test_stock_is_added_to_inventory() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	var shop: Shop = Shop.new(inventory)
	inventory.gdtest.add.then_return(true)
	expect_true(shop.stock("apple"))
	expect_equal(inventory.gdtest.add.call_count(), 1)
	expect_equal(inventory.gdtest.add.calls()[0].arguments, ["apple", 1])
```

Differences a converted test must account for:

- Argument matchers (`with_args`, and `when` chains with matchers) have no equivalent. Assert on `calls()[i].arguments` instead.
- Queued results are consumed first in, first out, and the last remaining result repeats for every later call. Call `reset()` to clear queued results and recorded calls before configuring a different sequence.
- A call with no configured result is an unexpected call. It fails the case at the test's line and returns the neutral default. Configure every method the code under test calls, or use `then_call_real()` on a method that should run its real body.
- Use `then_answer(callable)` for a computed result. The callable must accept the same number of arguments as the method, and an async callable is awaited only when the method is itself async. A synchronous method needs a synchronous callable.
- Doubles do not replace an instance that already exists. Construct the double and pass it in.

Move each Vest benchmark out of the correctness suite into a `*.bench.gd` file that extends
`GDTestBenchmark`. Replace the builder's measured callback with a typed zero-argument
`benchmark_*() -> void` method. Move unmeasured builder setup into `before_benchmark` or
`before_sample`, and choose `--warmup`, `--samples`, and `--iterations` at the CLI rather than in
source. Do not translate Vest measures, budgets, or fluent configuration into assertions: gdtest
reports the complete distribution and informational baseline deltas without a timing pass/fail.

```gdscript
extends GDTestBenchmark

var _subject: Inventory


func before_sample(_info: GDTestBenchmarkInfo) -> void:
	_subject = Inventory.new()


func benchmark_add_item() -> void:
	_subject.add("apple", 1)
```

### Vest.timeout

Remove `Vest.timeout` and pass explicit `timeout_seconds` to `until` or `wait_until_count`. Otherwise rely on the runner's case timeout, which bounds every case including a wait with no explicit deadline.
