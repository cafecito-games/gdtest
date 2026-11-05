## Abstract base class for project-defined custom matchers.
@abstract
class_name GDTestMatcher
extends RefCounted


## Identifies this matcher in test failure diagnostics.
@abstract
func name() -> String


## Evaluates `actual` against this matcher.
@abstract
func match(actual: Variant) -> GDTestMatchResult


## Validates matcher protocol and executes match evaluation fail-closed.
static func _gdtest_evaluate(actual: Variant, matcher: GDTestMatcher) -> GDTestMatchResult:
	if (
			typeof(matcher) != TYPE_OBJECT
			or matcher == null
			or not is_instance_valid(matcher)
			or not matcher is GDTestMatcher
	):
		return GDTestMatchResult._gdtest_invalid("expect_that requires a valid GDTestMatcher")
	var matcher_name_value: Variant = matcher.name()
	if not matcher_name_value is String or str(matcher_name_value).strip_edges().is_empty():
		return GDTestMatchResult._gdtest_invalid(
				"GDTestMatcher.name() must return a non-empty String"
		)
	var result_value: Variant = matcher.match(actual)
	if (
			typeof(result_value) != TYPE_OBJECT
			or result_value == null
			or not is_instance_valid(result_value)
			or not result_value is GDTestMatchResult
	):
		return GDTestMatchResult._gdtest_invalid(
				"GDTestMatcher.match() must return a GDTestMatchResult"
		)
	var match_result: GDTestMatchResult = result_value
	if not match_result._gdtest_is_valid():
		return match_result
	match_result._gdtest_set_assertion(str(matcher_name_value))
	return match_result
