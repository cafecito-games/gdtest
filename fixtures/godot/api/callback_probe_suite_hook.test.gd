extends GDTest

## Requests a callback probe from `before_all`, outside any executing case.

## Records whether the probe returned in `before_all` started closed.
var _hook_probe_closed: bool = false
## Records whether the probe returned in `before_all` exposed a valid callable.
var _hook_callable_valid: bool = true
## Records whether `before_suite` erroneously ran after `before_all` failed.
var _before_suite_ran: bool = false
## Records whether a case body erroneously ran after `before_all` failed.
var _case_ran: bool = false


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Callback probe in suite hook"


## Calls `callback_probe` outside an active case during file setup.
func before_all() -> void:
	var probe: GDTestCallbackProbe = callback_probe(1)
	_hook_probe_closed = probe.is_closed()
	_hook_callable_valid = probe.callback().is_valid()


## Must not execute once `before_all` records an error.
func before_suite(_suite_info: GDTestSuiteInfo) -> void:
	_before_suite_ran = true


## Verifies cleanup still runs and the `before_all` error prevented suite and case execution.
func after_all() -> void:
	if not _hook_probe_closed or _hook_callable_valid or _before_suite_ran or _case_ran:
		fail("before_all error must skip suite and case bodies while keeping probe inert")


## Must not execute once `before_all` records an error.
func test_suite_hook_probe_was_inert() -> void:
	_case_ran = true
	expect_true(true)
