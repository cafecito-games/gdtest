# Getting started

After installing with gpm, the runtime is `res://addons/gdtest/` and the host executable is selected by the project-local `addons/gdtest/gdtest` launcher. The executable and runtime reject version-mismatched handshakes, and workers reject engines outside the supported Godot 4.7 minor.

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

User-wide resource policy lives in the operating system's user configuration directory as
`gdtest/config.json` (for example, `~/Library/Application Support/gdtest/config.json` on macOS):

```json
{
  "max_workers": 4,
  "memory_budget_mb": 2048,
  "worker_max_age": "30m",
  "log_level": "info",
  "godot": "godot"
}
```

`log_level` accepts `debug`, `info`, `warn`, or `error` and filters the bounded structured daemon
log at write time. Error records remain available at every configured level.

CLI flags take precedence over project and user configuration. `--godot` selects an executable,
`--jobs 0` uses concurrency bounded by the daemon's global worker limit, and `--isolation` accepts
`reuse`, `recycle`, `fresh`, or `exclusive`.

Dynamic cases register in `suite()` with `test`, `test_each`, `define`, and their focused variants. Discovery executes registration and parameter providers but never case bodies or lifecycle hooks. Async callbacks and hooks may `await`; workers serialize a file's hooks and cases in source order.

Useful commands:

```sh
./addons/gdtest/gdtest list tests/
./addons/gdtest/gdtest run --jobs 2 --isolation fresh tests/
./addons/gdtest/gdtest watch tests/math.test.gd
./addons/gdtest/gdtest warm
./addons/gdtest/gdtest doctor
./addons/gdtest/gdtest daemon sessions
./addons/gdtest/gdtest daemon logs
```

Exit codes are 0 for success, 1 for test failures, 2 for usage/discovery/infrastructure errors, and 130 for cancellation. `--json` emits immutable events followed by summary and timing records. `--verbose` prints phase timings, including a distinct `project_bootstrap_import` phase when a fresh checkout needs its initial Godot import. Human reports show source-located failures as they arrive.

The full public API, actual-first assertion order, outcomes, hook behavior, stable-ID encoding, focus behavior, and deliberately absent Vest APIs are defined by [api-spec.md](../api-spec.md).
