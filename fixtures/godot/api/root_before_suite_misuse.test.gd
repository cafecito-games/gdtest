extends GDTest

## Verifies root `before_suite` framework errors skip all cases while running cleanup hooks.

## Records whether `before_all` ran before root `before_suite`.
var _before_all_ran: bool = false
## Records whether nested `before_suite` or a case body erroneously ran.
var _suite_body_ran: bool = false
## Records whether root `after_suite` ran as cleanup.
var _root_after_suite_ran: bool = false


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Root before_suite misuse"


## Registers a nested suite that must not enter when root `before_suite` fails.
func suite() -> void:
	define(
			"nested child",
			func() -> void:
				test(
						"nested case",
						func() -> void:
							_suite_body_ran = true
							expect_true(true)
				)
	)


## Marks file setup complete.
func before_all() -> void:
	_before_all_ran = true


## Fails root `before_suite` with an out-of-case outcome helper.
func before_suite(suite_info: GDTestSuiteInfo) -> void:
	if suite_info.name == "Root before_suite misuse":
		ok("root before_suite ok")
		return
	_suite_body_ran = true


## Records root `after_suite` cleanup execution.
func after_suite(suite_info: GDTestSuiteInfo) -> void:
	if suite_info.name == "Root before_suite misuse":
		_root_after_suite_ran = true
		return
	_suite_body_ran = true


## Proves `after_all` ran after root `after_suite` without executing suite bodies.
func after_all() -> void:
	if not _before_all_ran or not _root_after_suite_ran or _suite_body_ran:
		fail("root before_suite failure violated cleanup or gating contract")
	todo("root before_suite cleanup verified")


## Must not execute once root `before_suite` records an error.
func test_root_case() -> void:
	_suite_body_ran = true
	expect_true(true)
