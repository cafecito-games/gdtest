extends GDTest

## Requests a callback probe from `before_all`, outside any executing case.

var _hook_probe_closed: bool = false
var _hook_callable_valid: bool = true


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Callback probe in suite hook"


## Calls `callback_probe` outside an active case during suite setup.
func before_all() -> void:
	var probe: GDTestCallbackProbe = callback_probe(1)
	_hook_probe_closed = probe.is_closed()
	_hook_callable_valid = probe.callback().is_valid()


## Executes only if suite setup ran; the hook error must still fail the run.
func test_suite_hook_probe_was_inert() -> void:
	expect_true(_hook_probe_closed)
	expect_false(_hook_callable_valid)
