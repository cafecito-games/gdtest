extends GDTest

## Exercises unbounded `wait_until_count` under an external worker case timeout.


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Callback probe timeout"


## Waits without an internal probe deadline so the external case timeout must terminate it.
func test_unbounded_wait_yields_to_external_case_timeout() -> void:
	var probe: GDTestCallbackProbe = callback_probe(0)
	var reached: bool = await probe.wait_until_count(1)
	expect_true(reached)
