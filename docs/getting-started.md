# Getting started

After installing with gpm, the runtime is `res://addons/gdtest/` and the host executable is selected by the project-local `addons/gdtest/gdtest` launcher. The executable and runtime reject version-mismatched handshakes.

Create `tests/math.test.gd`:

```gdscript
extends GDTest


## Supplies named addition rows.
func test_adds__cases() -> Dictionary[String, Array]:
	return { "small": [1, 2, 3], "zero": [0, 0, 0] }


## Verifies one provider row.
func test_adds(left: int, right: int, expected: int) -> void:
	expect_equal(left + right, expected)
```

Run everything or select one stable provider row:

```sh
./addons/gdtest/gdtest run
./addons/gdtest/gdtest run 'tests/math.test.gd::test_adds[small]'
```

The default configuration discovers `**/*.test.gd`. Optional strict `gdtest.json` keys are `tests`, `exclude`, `godot`, `jobs`, `warm_workers`, `worker_idle_timeout`, `worker_max_runs`, and `only_mode`. Unknown keys fail. Focus policies are `auto`, `ignore`, `require`, and `forbid`, configurable in the file or per invocation with `--only`.

Dynamic cases register in `suite()` with `test`, `test_each`, `define`, and their focused variants. Discovery executes registration and parameter providers but never case bodies or lifecycle hooks. Async callbacks and hooks may `await`; workers serialize a file's hooks and cases in source order.

Useful commands:

```sh
./addons/gdtest/gdtest list tests/
./addons/gdtest/gdtest watch tests/math.test.gd
./addons/gdtest/gdtest warm
./addons/gdtest/gdtest doctor
./addons/gdtest/gdtest daemon sessions
./addons/gdtest/gdtest daemon logs
```

Exit codes are 0 for success, 1 for test failures, 2 for usage/discovery/infrastructure errors, and 130 for cancellation. `--json` emits immutable events followed by summary and timing records. Human reports show source-located failures as they arrive.

The full public API, actual-first assertion order, outcomes, hook behavior, stable-ID encoding, focus behavior, and deliberately absent Vest APIs are defined by [api-spec.md](../api-spec.md).
