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

gdtest intentionally has no v1 equivalent for runtime-generated mocks, call-history stubbing, benchmarks, generated mixins, event callbacks, or arbitrary fluent matcher chains. These constructs remain in the source and are reported with a file, line, column, stable finding code, and explanation. A finding never silently deletes or weakens a test.

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
