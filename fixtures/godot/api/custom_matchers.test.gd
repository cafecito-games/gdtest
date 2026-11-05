extends GDTest

## Passing custom matcher conformance fixtures.


## Always-passing matcher with invocation counter.
class AlwaysMatches:
	extends GDTestMatcher

	## Number of times match was called.
	var invocation_count: int = 0


	## Returns the stable matcher name.
	func name() -> String:
		return "always_matches"


	## Increments call count and returns matched.
	func match(_actual: Variant) -> GDTestMatchResult:
		invocation_count += 1
		return GDTestMatchResult.matched()


## Matcher checking string equality.
class MatchesPrefix:
	extends GDTestMatcher

	var _prefix: String


	## Stores the expected string prefix.
	func _init(prefix_value: String) -> void:
		_prefix = prefix_value


	## Returns the stable matcher name.
	func name() -> String:
		return "matches_prefix"


	## Evaluates whether actual starts with prefix.
	func match(actual: Variant) -> GDTestMatchResult:
		if not actual is String:
			return GDTestMatchResult.unmatched("expected String")
		var text: String = actual
		if text.begins_with(_prefix):
			return GDTestMatchResult.matched()
		return GDTestMatchResult.unmatched("text does not begin with prefix", _prefix, true)


## Verifies a custom matcher passing check promotes case status and passes.
func test_passing_matcher() -> void:
	expect_that("hello world", MatchesPrefix.new("hello"))


## Verifies a passing matcher succeeds with a caller-provided message.
func test_passing_matcher_with_caller_message() -> void:
	expect_that("hello world", MatchesPrefix.new("hello"), "prefix check")


## Proves that match() is invoked exactly once during a passing expect_that call.
func test_match_called_only_once_on_pass() -> void:
	var matcher: AlwaysMatches = AlwaysMatches.new()
	expect_that("sample", matcher)
	expect_equal(matcher.invocation_count, 1)


## Verifies that multiple successful expect_that assertions increment the assertion count.
func test_multiple_assertions_accumulate_counts() -> void:
	expect_that("first item", MatchesPrefix.new("first"))
	expect_that("second item", MatchesPrefix.new("second"))


## Verifies that GDTestMatchResult properties are getter-only and cannot be altered.
func test_result_properties_are_immutable() -> void:
	var result: GDTestMatchResult = GDTestMatchResult.matched()
	result.set("is_match", false)
	result.set("message", "tampered")
	result.set("expected", "tampered")
	result.set("has_expected", true)
	result.is_match = false
	result.message = "tampered"
	result.expected = "tampered"
	result.has_expected = true
	expect_true(result.is_match)
	expect_equal(result.message, "")
	expect_null(result.expected)
	expect_false(result.has_expected)


## Verifies that GDTestMatchResult preserves container typing on defensive copies.
func test_defensive_typed_array_cloning() -> void:
	var typed_integers: Array[int] = [1, 2, 3]
	var result: GDTestMatchResult = GDTestMatchResult.unmatched("mismatch", typed_integers, true)
	var expected_copy: Variant = result.expected
	expect_true(expected_copy is Array[int])
	expect_true(expected_copy.is_same_typed(typed_integers))
	expect_equal(expected_copy, typed_integers)
	expect_not_same(expected_copy, typed_integers)
