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
- a simple `_params: String = "provider"` convention to a `<test>__cases` provider wrapper;
- supported single-matcher fluent assertions to direct gdtest assertions without changing actual-first argument order.

Compatible assertion names and unrelated formatting are left alone. Run the migrated suite before deleting Vest from the project.

## Manual findings

gdtest intentionally has no v1 equivalent for runtime-generated mocks, call-history stubbing, benchmarks, Vest signal-capture helpers, generated mixins, event callbacks, or arbitrary fluent matcher chains. These constructs remain in the source and are reported with a file, line, column, stable finding code, and explanation. A finding never silently deletes or weakens a test.

Dynamic tests, async tests, and supported focus markers continue to work when they use APIs covered by the gdtest runtime. Project-specific wrappers and configuration still require review; in particular, replace Vest CLI invocations in local scripts and CI with `gdtest run` after the migrated suite passes.
