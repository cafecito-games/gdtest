extends GDTest

## Failing and invalid custom matcher conformance fixtures.


## Always-failing matcher without an expected value.
class AlwaysFailsWithoutExpected:
	extends GDTestMatcher

	## Number of times match was called.
	var invocation_count: int = 0


	## Returns matcher name.
	func name() -> String:
		return "always_fails_without_expected"


	## Returns unmatched result without expected.
	func match(_actual: Variant) -> GDTestMatchResult:
		invocation_count += 1
		return GDTestMatchResult.unmatched("explicit mismatch without expected")


## Matcher producing an expected value.
class MatchesExpectedValue:
	extends GDTestMatcher

	var _expected: Variant


	## Stores expected value.
	func _init(expected_value: Variant) -> void:
		_expected = expected_value


	## Returns matcher name.
	func name() -> String:
		return "matches_expected_value"


	## Returns unmatched result with expected value.
	func match(_actual: Variant) -> GDTestMatchResult:
		return GDTestMatchResult.unmatched("values differed", _expected, true)


## Matcher with deep internal helper calls.
class MatcherWithInternalHelpers:
	extends GDTestMatcher


	## Returns matcher name.
	func name() -> String:
		return "matcher_with_internal_helpers"


	## Evaluates via nested internal helpers.
	func match(_actual: Variant) -> GDTestMatchResult:
		return _first_helper()


	func _first_helper() -> GDTestMatchResult:
		return _second_helper()


	func _second_helper() -> GDTestMatchResult:
		return GDTestMatchResult.unmatched("deep internal helper mismatch")


## Matcher returning empty string name.
class EmptyNameMatcher:
	extends GDTestMatcher


	## Returns empty name.
	func name() -> String:
		return ""


	## Returns matched.
	func match(_actual: Variant) -> GDTestMatchResult:
		return GDTestMatchResult.matched()


## Matcher returning whitespace-only name.
class WhitespaceNameMatcher:
	extends GDTestMatcher


	## Returns whitespace name.
	func name() -> String:
		return "   \t\n  "


	## Returns matched.
	func match(_actual: Variant) -> GDTestMatchResult:
		return GDTestMatchResult.matched()


## Matcher returning null result.
class ReturnsNullResultMatcher:
	extends GDTestMatcher


	## Returns name.
	func name() -> String:
		return "returns_null_result"


	## Erroneously returns null.
	func match(_actual: Variant) -> GDTestMatchResult:
		return null


## Matcher returning direct GDTestMatchResult.new().
class ReturnsDirectNewResultMatcher:
	extends GDTestMatcher


	## Returns name.
	func name() -> String:
		return "returns_direct_new_result"


	## Erroneously bypasses factories.
	func match(_actual: Variant) -> GDTestMatchResult:
		return GDTestMatchResult.new()


## Matcher returning unmatched with empty message.
class ReturnsEmptyUnmatchedMessageMatcher:
	extends GDTestMatcher


	## Returns name.
	func name() -> String:
		return "returns_empty_unmatched_message"


	## Erroneously provides empty mismatch message.
	func match(_actual: Variant) -> GDTestMatchResult:
		return GDTestMatchResult.unmatched("")


## Matcher returning unmatched with whitespace-only message.
class ReturnsWhitespaceUnmatchedMessageMatcher:
	extends GDTestMatcher


	## Returns name.
	func name() -> String:
		return "returns_whitespace_unmatched_message"


	## Erroneously provides whitespace mismatch message.
	func match(_actual: Variant) -> GDTestMatchResult:
		return GDTestMatchResult.unmatched("   \t  ")


## Matcher with runtime error inside name().
class ErrorInsideNameMatcher:
	extends GDTestMatcher


	## Raises an assertion error.
	func name() -> String:
		assert(false, "deliberate error in name")
		return "error_inside_name"


	## Returns matched.
	func match(_actual: Variant) -> GDTestMatchResult:
		return GDTestMatchResult.matched()


## Matcher with runtime error inside match().
class ErrorInsideMatchMatcher:
	extends GDTestMatcher


	## Returns name.
	func name() -> String:
		return "error_inside_match"


	## Raises an assertion error.
	func match(_actual: Variant) -> GDTestMatchResult:
		assert(false, "deliberate error in match")
		return GDTestMatchResult.matched()


## Matcher whose match() method is an asynchronous coroutine.
class CoroutineMatchMatcher:
	extends GDTestMatcher


	## Returns name.
	func name() -> String:
		return "coroutine_match"


	## Unawaited coroutine returning a match result.
	func match(_actual: Variant) -> GDTestMatchResult:
		await Engine.get_main_loop().process_frame
		return GDTestMatchResult.matched()


## Matcher holding collections.
class CollectionMatcher:
	extends GDTestMatcher

	## Stored expected dictionary.
	var expected_collection: Dictionary[String, Variant] = {}


	## Stores collection.
	func _init(initial: Dictionary[String, Variant]) -> void:
		expected_collection = initial


	## Returns name.
	func name() -> String:
		return "collection_matcher"


	## Emits unmatched with expected collection.
	func match(_actual: Variant) -> GDTestMatchResult:
		return GDTestMatchResult.unmatched("collection mismatch", expected_collection, true)


## Verifies mismatch without an expected value.
func test_mismatch_without_expected() -> void:
	expect_that("sample", AlwaysFailsWithoutExpected.new())


## Verifies mismatch with an expected value.
func test_mismatch_with_expected() -> void:
	expect_that("received", MatchesExpectedValue.new("wanted"))


## Verifies mismatch with expected null and has_expected true.
func test_mismatch_with_expected_null() -> void:
	expect_that("lingering_value", MatchesExpectedValue.new(null))


## Verifies mismatch composed with caller custom message.
func test_mismatch_with_caller_message() -> void:
	expect_that("sample", AlwaysFailsWithoutExpected.new(), "caller prefix")


## Helper wrapper calling expect_that.
func _wrapper_helper(actual: Variant, matcher: GDTestMatcher) -> void:
	expect_that(actual, matcher)


## Verifies failure source location points at caller line even through a helper wrapper.
func test_wrapped_caller_location_attribution() -> void:
	_wrapper_helper("sample", AlwaysFailsWithoutExpected.new())


## Verifies matcher internal helpers do not obscure test call site attribution.
func test_matcher_internal_helpers_location() -> void:
	expect_that("sample", MatcherWithInternalHelpers.new())


## Proves match() is invoked exactly once during a failing expect_that call.
func test_match_called_only_once_on_failure() -> void:
	var matcher: AlwaysFailsWithoutExpected = AlwaysFailsWithoutExpected.new()
	expect_that("sample", matcher)
	expect_equal(matcher.invocation_count, 1)


## Verifies that mutating collections after unmatched or expect_that does not
## alter recorded diagnostics.
func test_defensive_collection_mutations() -> void:
	var initial_expected: Dictionary[String, Variant] = { "key": "original_expected" }
	var matcher: CollectionMatcher = CollectionMatcher.new(initial_expected)
	var initial_actual: Dictionary[String, Variant] = { "key": "original_actual" }
	expect_that(initial_actual, matcher)
	initial_actual["key"] = "mutated_actual"
	initial_expected["key"] = "mutated_expected"
	matcher.expected_collection["key"] = "mutated_matcher"


## Verifies that getter return mutation does not alter match result expected value.
func test_defensive_getter_cloning() -> void:
	var original: Dictionary[String, Variant] = { "value": 1 }
	var result: GDTestMatchResult = GDTestMatchResult.unmatched("test", original, true)
	var retrieved: Variant = result.expected
	if retrieved is Dictionary:
		retrieved["value"] = 999
	var second_retrieved: Variant = result.expected
	if second_retrieved is Dictionary:
		expect_equal(second_retrieved["value"], 1)


## Verifies that cyclic collections terminate recursion safely without infinite loop.
func test_cyclic_collection_diagnostics() -> void:
	var cyclic_array: Array[Variant] = []
	cyclic_array.append(cyclic_array)
	expect_that(cyclic_array, MatchesExpectedValue.new(cyclic_array))


## Verifies fail-closed rejection of a null matcher.
func test_fail_closed_null_matcher() -> void:
	expect_that("sample", null)


## Verifies fail-closed rejection of an empty matcher name.
func test_fail_closed_empty_matcher_name() -> void:
	expect_that("sample", EmptyNameMatcher.new())


## Verifies fail-closed rejection of a whitespace-only matcher name.
func test_fail_closed_whitespace_matcher_name() -> void:
	expect_that("sample", WhitespaceNameMatcher.new())


## Verifies fail-closed rejection when match() returns null.
func test_fail_closed_null_match_result() -> void:
	expect_that("sample", ReturnsNullResultMatcher.new())


## Verifies fail-closed rejection when match() returns GDTestMatchResult.new().
func test_fail_closed_direct_new_match_result() -> void:
	expect_that("sample", ReturnsDirectNewResultMatcher.new())


## Verifies fail-closed rejection when match() returns unmatched with empty message.
func test_fail_closed_empty_unmatched_message() -> void:
	expect_that("sample", ReturnsEmptyUnmatchedMessageMatcher.new())


## Verifies fail-closed rejection when match() returns unmatched with whitespace message.
func test_fail_closed_whitespace_unmatched_message() -> void:
	expect_that("sample", ReturnsWhitespaceUnmatchedMessageMatcher.new())


## Verifies runtime error when matcher.name() fails with an assertion error.
func test_runtime_error_inside_name() -> void:
	expect_that("sample", ErrorInsideNameMatcher.new())


## Verifies runtime error when matcher.match() fails with an assertion error.
func test_runtime_error_inside_match() -> void:
	expect_that("sample", ErrorInsideMatchMatcher.new())


## Verifies runtime error when matcher.match() is an unawaited coroutine.
func test_runtime_error_coroutine_match() -> void:
	expect_that("sample", CoroutineMatchMatcher.new())
