extends GDTest


## Equality helper whose result deliberately violates the public contract.
class InvalidEquals extends RefCounted:
	## Deliberately violates the directional equality contract.
	func equals(_other: Variant) -> String:
		return "not a bool"


## Exercises the custom-message failure path for `expect`.
func test_expect_failure() -> void:
	expect(false, "custom expect")


## Exercises the custom-message failure path for `expect_not`.
func test_expect_not_failure() -> void:
	expect_not(true, "custom expect_not")


## Exercises the custom-message failure path for `expect_true`.
func test_expect_true_failure() -> void:
	expect_true(false, "custom expect_true")


## Exercises the custom-message failure path for `expect_false`.
func test_expect_false_failure() -> void:
	expect_false(true, "custom expect_false")


## Exercises the custom-message failure path for equality.
func test_expect_equal_failure() -> void:
	expect_equal(1, 2, "custom expect_equal")


## Exercises the custom-message failure path for inequality.
func test_expect_not_equal_failure() -> void:
	expect_not_equal(1, 1, "custom expect_not_equal")


## Exercises the custom-message failure path for identity.
func test_expect_same_failure() -> void:
	var first: Array[int] = [1]
	var second: Array[int] = [1]
	expect_same(first, second, "custom expect_same")


## Exercises the custom-message failure path for inverse identity.
func test_expect_not_same_failure() -> void:
	var shared: Array[int] = [1]
	expect_not_same(shared, shared, "custom expect_not_same")


## Exercises the custom-message failure path for approximation.
func test_expect_approx_failure() -> void:
	expect_approx(1.0, 2.0, 0.01, "custom expect_approx")


## Exercises the custom-message failure path for inverse approximation.
func test_expect_not_approx_failure() -> void:
	expect_not_approx(1.0, 1.0, 0.01, "custom expect_not_approx")


## Exercises the custom-message failure path for null checks.
func test_expect_null_failure() -> void:
	expect_null(1, "custom expect_null")


## Exercises the custom-message failure path for non-null checks.
func test_expect_not_null_failure() -> void:
	expect_not_null(null, "custom expect_not_null")


## Exercises the custom-message failure path for empty checks.
func test_expect_empty_failure() -> void:
	expect_empty([1], "custom expect_empty")


## Exercises the custom-message failure path for non-empty checks.
func test_expect_not_empty_failure() -> void:
	expect_not_empty([], "custom expect_not_empty")


## Exercises the custom-message failure path for containment.
func test_expect_contains_failure() -> void:
	expect_contains("gdtest", "absent", "custom expect_contains")


## Exercises the custom-message failure path for inverse containment.
func test_expect_does_not_contain_failure() -> void:
	expect_does_not_contain([1], 1, "custom expect_does_not_contain")


## Exercises invalid directional equality results.
func test_expect_equal_invalid_result() -> void:
	expect_equal(InvalidEquals.new(), 1, "custom expect_equal invalid")


## Exercises invalid inverse directional equality results.
func test_expect_not_equal_invalid_result() -> void:
	expect_not_equal(InvalidEquals.new(), 1, "custom expect_not_equal invalid")


## Exercises invalid approximation tolerance.
func test_expect_approx_invalid_tolerance() -> void:
	expect_approx(1.0, 1.0, -1.0, "custom expect_approx invalid")


## Exercises invalid inverse-approximation tolerance.
func test_expect_not_approx_invalid_tolerance() -> void:
	expect_not_approx(1.0, 1.0, -1.0, "custom expect_not_approx invalid")


## Exercises unsupported empty input.
func test_expect_empty_invalid_input() -> void:
	expect_empty(42, "custom expect_empty invalid")


## Exercises unsupported non-empty input.
func test_expect_not_empty_invalid_input() -> void:
	expect_not_empty(42, "custom expect_not_empty invalid")


## Exercises unsupported containment input.
func test_expect_contains_invalid_input() -> void:
	expect_contains(42, 4, "custom expect_contains invalid")


## Exercises unsupported inverse-containment input.
func test_expect_does_not_contain_invalid_input() -> void:
	expect_does_not_contain(42, 4, "custom expect_does_not_contain invalid")
