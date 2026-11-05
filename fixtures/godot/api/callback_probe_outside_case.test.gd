extends GDTest

## Requests a callback probe from discovery, where no case is executing.


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Callback probe outside a case"


## Requests a probe from a discovery provider and passes its inert state to the case.
# Provider suffix is a reserved gdtest public API spelling.
# gdkit:ignore = function-name
func test_probe_is_inert_outside_case__cases() -> Array[Variant]:
	var probe: GDTestCallbackProbe = callback_probe(1)
	return [[probe.is_closed(), probe.callback().is_valid(), probe.invocation_count()]]


## Verifies the inert probe state observed during discovery.
func test_probe_is_inert_outside_case(
		closed: bool,
		callable_valid: bool,
		recorded_count: int
) -> void:
	expect_true(closed)
	expect_false(callable_valid)
	expect_equal(recorded_count, 0)
