extends GDTest

## Calls expect_that in before_all outside an active case.


## Matcher tracking whether match() was called.
class HookGuardMatcher:
	extends GDTestMatcher

	## Records whether match was called.
	var match_called: bool = false


	## Returns name.
	func name() -> String:
		return "hook_guard_matcher"


	## Sets flag and returns matched.
	func match(_actual: Variant) -> GDTestMatchResult:
		match_called = true
		return GDTestMatchResult.matched()


## Records whether the matcher was invoked.
var _hook_match_called: bool = true
## Records whether before_suite ran.
var _before_suite_ran: bool = false
## Records whether case body ran.
var _case_ran: bool = false


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Custom matcher in suite hook"


## Calls expect_that outside an active case during suite setup.
func before_all() -> void:
	var matcher: HookGuardMatcher = HookGuardMatcher.new()
	expect_that("sample", matcher)
	_hook_match_called = matcher.match_called


## Must not execute once before_all records an error.
func before_suite(_suite_info: GDTestSuiteInfo) -> void:
	_before_suite_ran = true


## Verifies cleanup still runs and the before_all error prevented suite and case execution.
func after_all() -> void:
	if _hook_match_called or _before_suite_ran or _case_ran:
		fail("before_all error must skip suite and case bodies while keeping matcher uninvoked")


## Must not execute once before_all records an error.
func test_suite_hook_matcher_was_uninvoked() -> void:
	_case_ran = true
	expect_true(true)
