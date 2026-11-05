extends GDTest

## Calls expect_that from a discovery provider outside an active case.


## Matcher tracking whether match() was called.
class GuardMatcher:
	extends GDTestMatcher

	## Records whether match() was invoked.
	var match_called: bool = false


	## Returns matcher name.
	func name() -> String:
		return "guard_matcher"


	## Sets flag and returns matched.
	func match(_actual: Variant) -> GDTestMatchResult:
		match_called = true
		return GDTestMatchResult.matched()


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Custom matcher outside a case"


## Parameter provider that calls expect_that during discovery.
# Provider suffix is a reserved gdtest public API spelling.
# gdkit:ignore = function-name
func test_expect_that_outside_case__cases() -> Array[Variant]:
	var matcher: GuardMatcher = GuardMatcher.new()
	expect_that("sample", matcher)
	assert(not matcher.match_called, "outside-case expect_that must not invoke matcher")
	return [[matcher.match_called]]


## Verifies the matcher was not invoked during discovery.
func test_expect_that_outside_case(match_called: bool) -> void:
	expect_false(match_called)
