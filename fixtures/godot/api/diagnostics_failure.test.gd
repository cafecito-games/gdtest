extends GDTest


## Produces source-located custom and default failure diagnostics.
func test_failure_diagnostics_are_failure_only() -> void:
	expect_equal("actual", "expected", "custom equality message")
	expect_empty(42, "custom invalid-input message")
	expect_approx(1.0, 1.0, -1.0)
	ok("later passing work cannot erase failure")


## Proves skip cannot hide a later failure.
func test_skip_then_failure() -> void:
	skip("initial skip")
	fail("failure is sticky")


## Proves todo cannot hide a later failure.
func test_todo_then_failure() -> void:
	todo("initial todo")
	expect_false(true)


## Exercises the explicit void outcome.
func test_void_case() -> void:
	pass
