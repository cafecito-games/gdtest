## Immutable outcome produced by a custom matcher evaluation.
class_name GDTestMatchResult
extends RefCounted

## Maximum recursion depth for defensive diagnostic cloning.
const MAX_DIAGNOSTIC_DEPTH: int = 16

## Indicates whether the evaluation succeeded.
var is_match: bool:
	get:
		return _is_match

## Diagnostic message explaining a mismatch, or empty when matched.
var message: String:
	get:
		return _message

## Expected value carried by this mismatch, if specified.
var expected: Variant:
	get:
		return _gdtest_clone_value(_expected)

## Indicates whether an expected value was explicitly attached to this mismatch.
var has_expected: bool:
	get:
		return _has_expected

## Records whether this instance was constructed through an approved factory.
var _valid: bool = false
## Explanation recorded when an instance is rejected fail-closed.
var _invalid_reason: String = "GDTestMatchResult must be constructed with matched() or unmatched()"
## Internal match success state.
var _is_match: bool = false
## Internal mismatch explanation.
var _message: String = ""
## Internal expected value representation.
var _expected: Variant = null
## Internal flag recording whether an expected value was provided.
var _has_expected: bool = false
## Internal assertion name recorded for failure diagnostics.
var _assertion: String = "expect_that"


## Returns a successful match result.
static func matched() -> GDTestMatchResult:
	var result: GDTestMatchResult = GDTestMatchResult.new()
	result._valid = true
	result._invalid_reason = ""
	result._is_match = true
	result._message = ""
	result._expected = null
	result._has_expected = false
	return result


## Returns an unsuccessful match result with an explanation and optional expected value.
static func unmatched(
		message_text: String,
		expected_value: Variant = null,
		has_expected_value: bool = false
) -> GDTestMatchResult:
	if message_text.strip_edges().is_empty():
		return _gdtest_invalid("GDTestMatchResult.unmatched() requires a non-empty message")
	var result: GDTestMatchResult = GDTestMatchResult.new()
	result._valid = true
	result._invalid_reason = ""
	result._is_match = false
	result._message = message_text
	result._has_expected = has_expected_value
	result._expected = _gdtest_clone_value(expected_value) if has_expected_value else null
	return result


## Creates an internally invalid match result carrying the failure reason.
static func _gdtest_invalid(reason: String) -> GDTestMatchResult:
	var result: GDTestMatchResult = GDTestMatchResult.new()
	result._valid = false
	result._invalid_reason = reason
	return result


## Reports whether this match result was constructed with valid factory parameters.
func _gdtest_is_valid() -> bool:
	return _valid


## Returns the reason this match result was marked invalid.
func _gdtest_invalid_reason() -> String:
	return _invalid_reason


## Stores the matcher name for failure diagnostics.
func _gdtest_set_assertion(assertion_name: String) -> void:
	_assertion = assertion_name


## Returns the recorded assertion name.
func _gdtest_assertion() -> String:
	return _assertion


## Recursively deep-copies containers while leaving objects and primitives untouched.
static func _gdtest_clone_value(
		value: Variant,
		depth: int = 0,
		ancestors: Array[Variant] = []
) -> Variant:
	match typeof(value):
		TYPE_ARRAY:
			var array: Array[Variant] = value
			if depth >= MAX_DIAGNOSTIC_DEPTH or _gdtest_contains_same(ancestors, array):
				var truncated_array: Array[Variant] = array.duplicate(false)
				truncated_array.clear()
				return truncated_array
			var next_ancestors: Array[Variant] = ancestors.duplicate()
			next_ancestors.append(array)
			var cloned_array: Array[Variant] = array.duplicate(false)
			for index: int in range(cloned_array.size()):
				cloned_array[index] = _gdtest_clone_value(
						cloned_array[index],
						depth + 1,
						next_ancestors
				)
			return cloned_array
		TYPE_DICTIONARY:
			var dictionary: Dictionary[Variant, Variant] = value
			if depth >= MAX_DIAGNOSTIC_DEPTH or _gdtest_contains_same(ancestors, dictionary):
				var truncated_dictionary: Dictionary[Variant, Variant] = dictionary.duplicate(false)
				truncated_dictionary.clear()
				return truncated_dictionary
			var next_ancestors: Array[Variant] = ancestors.duplicate()
			next_ancestors.append(dictionary)
			var cloned_dictionary: Dictionary[Variant, Variant] = dictionary.duplicate(false)
			cloned_dictionary.clear()
			for key: Variant in dictionary:
				cloned_dictionary[_gdtest_clone_value(
						key,
						depth + 1,
						next_ancestors
				)] = _gdtest_clone_value(dictionary[key], depth + 1, next_ancestors)
			return cloned_dictionary
		TYPE_PACKED_BYTE_ARRAY, \
		TYPE_PACKED_INT32_ARRAY, \
		TYPE_PACKED_INT64_ARRAY, \
		TYPE_PACKED_FLOAT32_ARRAY, \
		TYPE_PACKED_FLOAT64_ARRAY, \
		TYPE_PACKED_STRING_ARRAY, \
		TYPE_PACKED_VECTOR2_ARRAY, \
		TYPE_PACKED_VECTOR3_ARRAY, \
		TYPE_PACKED_COLOR_ARRAY, \
		TYPE_PACKED_VECTOR4_ARRAY:
			return value
		_:
			return value


## Returns whether candidate is present in list by exact identity.
static func _gdtest_contains_same(list: Array[Variant], candidate: Variant) -> bool:
	for item: Variant in list:
		if is_same(item, candidate):
			return true
	return false
