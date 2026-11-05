extends GDTest

## Exercises invalid callback probe inputs; every case must report a source-located framework error.


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Callback probe failures"


## Rejects a negative argument count and returns an invalid callable on a closed probe.
func test_negative_arity_reports_framework_error() -> void:
	var probe: GDTestCallbackProbe = callback_probe(-1)
	expect_true(probe.is_closed())
	expect_false(probe.callback().is_valid())
	expect_equal(probe.invocation_count(), 0)


## Rejects an argument count above eight and returns an invalid callable on a closed probe.
func test_arity_above_eight_reports_framework_error() -> void:
	var probe: GDTestCallbackProbe = callback_probe(9)
	expect_true(probe.is_closed())
	expect_false(probe.callback().is_valid())
	expect_equal(probe.invocation_count(), 0)


## Rejects a negative wait count without waiting or mutating recorded invocations.
func test_negative_count_reports_framework_error() -> void:
	var probe: GDTestCallbackProbe = callback_probe(1)
	probe.callback().call("kept")
	var reached: bool = await probe.wait_until_count(-1)
	expect_false(reached)
	expect_equal(probe.invocation_count(), 1)


## Rejects a negative wait timeout without waiting or mutating recorded invocations.
func test_negative_timeout_reports_framework_error() -> void:
	var probe: GDTestCallbackProbe = callback_probe(1)
	probe.callback().call("kept")
	var reached: bool = await probe.wait_until_count(2, -0.5)
	expect_false(reached)
	expect_equal(probe.invocation_count(), 1)


## Rejects an infinite wait timeout without waiting or mutating recorded invocations.
func test_infinite_timeout_reports_framework_error() -> void:
	var probe: GDTestCallbackProbe = callback_probe(0)
	var reached: bool = await probe.wait_until_count(1, INF)
	expect_false(reached)
	expect_equal(probe.invocation_count(), 0)


## Rejects a NaN wait timeout without waiting or mutating recorded invocations.
func test_nan_timeout_reports_framework_error() -> void:
	var probe: GDTestCallbackProbe = callback_probe(0)
	var reached: bool = await probe.wait_until_count(1, NAN)
	expect_false(reached)
	expect_equal(probe.invocation_count(), 0)


## Rejects a negative invocation index and returns a defensive empty array.
func test_negative_index_reports_framework_error() -> void:
	var probe: GDTestCallbackProbe = callback_probe(1)
	probe.callback().call("kept")
	var fallback: Array[Variant] = probe.arguments_at(-1)
	expect_equal(fallback, [])
	fallback.append("mutated")
	expect_equal(probe.arguments_at(0), ["kept"])


## Rejects an invocation index at or beyond the recorded count and returns an empty array.
func test_out_of_range_index_reports_framework_error() -> void:
	var probe: GDTestCallbackProbe = callback_probe(0)
	expect_equal(probe.arguments_at(0), [])


## Still reports a call-site framework error when `arguments_at` is misused after `close()`.
func test_closed_probe_invalid_index_still_reports_framework_error() -> void:
	var probe: GDTestCallbackProbe = callback_probe(0)
	probe.close()
	expect_equal(probe.arguments_at(2), [])
