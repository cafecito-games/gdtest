## Base class for gdtest test scripts and their assertion API.
class_name GDTest
extends RefCounted

const _STATUS_RANK: Dictionary[String, int] = {
	"void": 0,
	"pass": 1,
	"skip": 2,
	"todo": 3,
	"fail": 4,
	"error": 5,
	"canceled": 6,
}
const _PACKED_TYPES: Array[int] = [
	TYPE_PACKED_BYTE_ARRAY,
	TYPE_PACKED_INT32_ARRAY,
	TYPE_PACKED_INT64_ARRAY,
	TYPE_PACKED_FLOAT32_ARRAY,
	TYPE_PACKED_FLOAT64_ARRAY,
	TYPE_PACKED_STRING_ARRAY,
	TYPE_PACKED_VECTOR2_ARRAY,
	TYPE_PACKED_VECTOR3_ARRAY,
	TYPE_PACKED_COLOR_ARRAY,
	TYPE_PACKED_VECTOR4_ARRAY,
]

var _gdtest_tree: SceneTree
var _gdtest_failure_sink: Callable
var _gdtest_case_active: bool = false
var _gdtest_registration_active: bool = false
var _gdtest_status: String = "void"
var _gdtest_assertions: int = 0
var _gdtest_failures: Array[Variant] = []
var _gdtest_messages: Array[String] = []
var _gdtest_outcome_data: Array[Variant] = []
var _gdtest_dynamic_cases: Array[Variant] = []
var _gdtest_dynamic_suites: Array[Variant] = []
var _gdtest_suite_stack: Array[GDTestSuiteInfo] = []
var _gdtest_definition_roots: Array[Variant] = []
var _gdtest_active_definition: Dictionary[String, Variant] = {}
var _gdtest_suite_ordinals: Dictionary[String, int] = {}
var _gdtest_case_ordinals: Dictionary[int, int] = {}
var _gdtest_discovery_errors: Array[Variant] = []
var _gdtest_file: String = ""


## Registers a nested dynamic suite and immediately invokes its callback.
func define(name: String, callback: Callable, is_only: bool = false) -> GDTestSuiteInfo:
	if not _require_registration("define"):
		return GDTestSuiteInfo.new("", name, _gdtest_file, -1, is_only)
	if name.is_empty():
		_record_discovery_error("define requires a non-empty name")
		return GDTestSuiteInfo.new("", name, _gdtest_file, -1, is_only)
	if not _valid_registration_callback(callback, "define", 0):
		return GDTestSuiteInfo.new("", name, _gdtest_file, -1, is_only)
	var location: Dictionary[String, Variant] = _user_location()
	var parent_id: String = (
			_gdtest_suite_stack.back().id if not _gdtest_suite_stack.is_empty()
			else ""
	)
	var sibling_key: String = str(parent_id.length()) + ":" + parent_id + name
	var sibling_ordinal: int = _gdtest_suite_ordinals.get(sibling_key, 0)
	_gdtest_suite_ordinals[sibling_key] = sibling_ordinal + 1
	var suite_id: String = parent_id + (
			"/" if not parent_id.is_empty()
			else ""
	) + _encode_component(name) + "@" + str(sibling_ordinal)
	var info := GDTestSuiteInfo.new(
			suite_id,
			name,
			str(location.get("file", _gdtest_file)),
			int(location.get("line", -1)),
			is_only
	)
	_gdtest_dynamic_suites.append({ "info": info, "parent_id": parent_id })
	var definition: Dictionary[String, Variant] = {
		"callback": callback,
		"info": info,
		"ancestors": _gdtest_suite_stack.duplicate(),
		"children": [],
	}
	if _gdtest_active_definition.is_empty():
		_gdtest_definition_roots.append(definition)
	else:
		var children: Variant = _gdtest_active_definition["children"]
		if children is Array:
			children.append(definition)
	return info


## Registers a focused nested dynamic suite.
func define_only(name: String, callback: Callable) -> GDTestSuiteInfo:
	return define(name, callback, true)


## Registers a dynamic test case for later execution.
func test(description: String, callback: Callable, is_only: bool = false) -> void:
	if not _require_registration("test"):
		return
	if description.is_empty():
		_record_discovery_error("test requires a non-empty description")
		return
	if not _valid_registration_callback(callback, "test", 0):
		return
	_register_dynamic_case(description, callback, [], "", is_only)


## Registers a focused dynamic test case.
func test_only(description: String, callback: Callable) -> void:
	test(description, callback, true)


## Registers one dynamic case for every named or indexed parameter row.
func test_each(
		description: String,
		cases: Variant,
		callback: Callable,
		is_only: bool = false
) -> void:
	if not _require_registration("test_each"):
		return
	if description.is_empty():
		_record_discovery_error("test_each requires a non-empty description")
		return
	if not callback.is_valid():
		_record_discovery_error("test_each requires a valid callback")
		return
	if cases is Dictionary:
		var dictionary: Dictionary[Variant, Variant] = cases
		var names: Array[String] = []
		var rows: Dictionary[String, Variant] = {}
		for key: Variant in dictionary:
			var parameter_name: String = str(key)
			if parameter_name.is_empty():
				_record_discovery_error("test_each parameter names must not be empty")
				continue
			if rows.has(parameter_name):
				_record_discovery_error("duplicate test_each parameter name: " + parameter_name)
				continue
			rows[parameter_name] = dictionary[key]
			names.append(parameter_name)
		names.sort_custom(_utf8_less)
		for parameter_name: String in names:
			_register_dynamic_row(
					description,
					callback,
					rows[parameter_name],
					parameter_name,
					is_only
			)
		return
	if cases is Array:
		var array: Array[Variant] = cases
		for index: int in range(array.size()):
			_register_dynamic_row(
					description,
					callback,
					array[index],
					str(index),
					is_only,
					index + 1
			)
		return
	_record_discovery_error("test_each cases must be a Dictionary or Array")


## Registers focused dynamic cases for every parameter row.
func test_each_only(description: String, cases: Variant, callback: Callable) -> void:
	test_each(description, cases, callback, true)


## Records a true-condition assertion.
func expect(condition: bool, message: String = "") -> void:
	_record_assertion(condition, "expect", message, condition, true, true)


## Records a false-condition assertion.
func expect_not(condition: bool, message: String = "") -> void:
	_record_assertion(not condition, "expect_not", message, condition, false, true)


## Alias for `expect`.
func expect_true(condition: bool, message: String = "") -> void:
	expect(condition, message)


## Alias for `expect_not`.
func expect_false(condition: bool, message: String = "") -> void:
	expect_not(condition, message)


## Records equality using directional `actual.equals(expected)` when available.
func expect_equal(actual: Variant, expected: Variant, message: String = "") -> void:
	var equal: Variant
	if actual is Object and is_instance_valid(actual) and actual.has_method("equals"):
		equal = actual.call("equals", expected)
		if not equal is bool:
			_record_failure(
					"expect_equal",
					_framework_message(message, "equals() must return bool"),
					actual,
					expected,
					true
			)
			return
	else:
		equal = actual == expected
	_record_assertion(bool(equal), "expect_equal", message, actual, expected, true)


## Records inequality using the inverse of directional equality.
func expect_not_equal(actual: Variant, expected: Variant, message: String = "") -> void:
	var equal: Variant
	if actual is Object and is_instance_valid(actual) and actual.has_method("equals"):
		equal = actual.call("equals", expected)
		if not equal is bool:
			_record_failure(
					"expect_not_equal",
					_framework_message(message, "equals() must return bool"),
					actual,
					expected,
					true
			)
			return
	else:
		equal = actual == expected
	_record_assertion(not bool(equal), "expect_not_equal", message, actual, expected, true)


## Records an identity assertion using Godot `is_same` semantics.
func expect_same(actual: Variant, expected: Variant, message: String = "") -> void:
	_record_assertion(is_same(actual, expected), "expect_same", message, actual, expected, true)


## Records a non-identity assertion using Godot `is_same` semantics.
func expect_not_same(actual: Variant, expected: Variant, message: String = "") -> void:
	_record_assertion(
			not is_same(actual, expected),
			"expect_not_same",
			message,
			actual,
			expected,
			true
	)


## Records an absolute-tolerance numeric assertion.
func expect_approx(
		actual: float,
		expected: float,
		tolerance: float = 0.00001,
		message: String = ""
) -> void:
	if not _valid_tolerance(tolerance, "expect_approx", message):
		return
	var matches: bool = actual == expected if (
			not is_finite(actual)
			or not is_finite(expected)
	) else absf(actual - expected) <= tolerance
	_record_assertion(matches, "expect_approx", message, actual, expected, true)


## Records the inverse of an absolute-tolerance numeric assertion.
func expect_not_approx(
		actual: float,
		expected: float,
		tolerance: float = 0.00001,
		message: String = ""
) -> void:
	if not _valid_tolerance(tolerance, "expect_not_approx", message):
		return
	var matches: bool = actual == expected if (
			not is_finite(actual)
			or not is_finite(expected)
	) else absf(actual - expected) <= tolerance
	_record_assertion(not matches, "expect_not_approx", message, actual, expected, true)


## Records that a value is null.
func expect_null(value: Variant, message: String = "") -> void:
	_record_assertion(value == null, "expect_null", message, value, null, true)


## Records that a value is not null.
func expect_not_null(value: Variant, message: String = "") -> void:
	_record_assertion(value != null, "expect_not_null", message, value, null, true)


## Records that a supported value is empty.
func expect_empty(value: Variant, message: String = "") -> void:
	var result: Dictionary[String, Variant] = _is_empty_value(value)
	if not bool(result["supported"]):
		_record_failure(
				"expect_empty",
				_framework_message(message, str(result["error"])),
				value,
				null,
				false
		)
		return
	_record_assertion(bool(result["value"]), "expect_empty", message, value, null, false)


## Records that a supported value is not empty.
func expect_not_empty(value: Variant, message: String = "") -> void:
	var result: Dictionary[String, Variant] = _is_empty_value(value)
	if not bool(result["supported"]):
		_record_failure(
				"expect_not_empty",
				_framework_message(message, str(result["error"])),
				value,
				null,
				false
		)
		return
	_record_assertion(not bool(result["value"]), "expect_not_empty", message, value, null, false)


## Records that a supported collection or string contains an item.
func expect_contains(value: Variant, item: Variant, message: String = "") -> void:
	var result: Dictionary[String, Variant] = _contains_value(value, item)
	if not bool(result["supported"]):
		_record_failure(
				"expect_contains",
				_framework_message(message, str(result["error"])),
				value,
				item,
				true
		)
		return
	_record_assertion(bool(result["value"]), "expect_contains", message, value, item, true)


## Records that a supported collection or string does not contain an item.
func expect_does_not_contain(value: Variant, item: Variant, message: String = "") -> void:
	var result: Dictionary[String, Variant] = _contains_value(value, item)
	if not bool(result["supported"]):
		_record_failure(
				"expect_does_not_contain",
				_framework_message(message, str(result["error"])),
				value,
				item,
				true
		)
		return
	_record_assertion(
			not bool(result["value"]),
			"expect_does_not_contain",
			message,
			value,
			item,
			true
	)


## Records an explicit passing outcome with optional structured data.
func ok(message: String = "", data: Dictionary[String, Variant] = {}) -> void:
	if not _require_case("ok"):
		return
	_gdtest_assertions += 1
	_promote_status("pass")
	_record_outcome_data("ok", message, data)


## Records an explicit failing outcome with optional structured data.
func fail(message: String = "", data: Dictionary[String, Variant] = {}) -> void:
	if not _require_case("fail"):
		return
	_record_outcome_data("fail", message, data)
	_record_failure(
			"fail",
			message if not message.is_empty() else "explicit failure",
			null,
			null,
			false
	)


## Marks the current case skipped unless it later fails or errors.
func skip(message: String = "", data: Dictionary[String, Variant] = {}) -> void:
	if not _require_case("skip"):
		return
	_promote_status("skip")
	_record_outcome_data("skip", message, data)


## Marks the current case todo unless it later fails or errors.
func todo(message: String = "", data: Dictionary[String, Variant] = {}) -> void:
	if not _require_case("todo"):
		return
	_promote_status("todo")
	_record_outcome_data("todo", message, data)


## Attaches a message to the currently executing case.
func message(text: String) -> void:
	if not _require_case("message"):
		return
	_gdtest_messages.append(text)


## Returns the worker SceneTree used by this test instance.
func get_tree() -> SceneTree:
	return _gdtest_tree


## Waits for a frame or timer and returns a Godot error code.
func sleep(duration_seconds: float = 0.0) -> Error:
	if not is_finite(duration_seconds) or duration_seconds < 0.0:
		_record_framework_error("sleep duration must be finite and non-negative")
		return ERR_INVALID_PARAMETER
	if _gdtest_tree == null:
		return ERR_UNAVAILABLE
	if duration_seconds == 0.0:
		await _gdtest_tree.process_frame
	else:
		await _gdtest_tree.create_timer(duration_seconds).timeout
	return OK


## Polls a condition until success or timeout and returns a Godot error code.
func until(
		condition: Callable,
		timeout_seconds: float = 5.0,
		interval_seconds: float = 0.0
) -> Error:
	if not is_finite(timeout_seconds) or timeout_seconds < 0.0:
		_record_framework_error("until timeout must be finite and non-negative")
		return ERR_INVALID_PARAMETER
	if not is_finite(interval_seconds) or interval_seconds < 0.0:
		_record_framework_error("until interval must be finite and non-negative")
		return ERR_INVALID_PARAMETER
	if _gdtest_tree == null:
		return ERR_UNAVAILABLE
	var started: float = time()
	while true:
		var condition_result: Variant = await condition.call()
		if bool(condition_result):
			return OK
		if timeout_seconds > 0.0 and time() - started >= timeout_seconds:
			return ERR_TIMEOUT
		if interval_seconds == 0.0:
			await _gdtest_tree.process_frame
		else:
			await _gdtest_tree.create_timer(interval_seconds).timeout
	return ERR_BUG


## Returns monotonic elapsed seconds, not wall-clock time.
func time() -> float:
	return Time.get_ticks_usec() / 1000000.0


func _gdtest_configure(tree: SceneTree, file: String, failure_sink: Callable) -> void:
	_gdtest_tree = tree
	_gdtest_file = file
	_gdtest_failure_sink = failure_sink


func _gdtest_begin_registration() -> void:
	_gdtest_registration_active = true
	_gdtest_dynamic_cases.clear()
	_gdtest_dynamic_suites.clear()
	_gdtest_suite_stack.clear()
	_gdtest_definition_roots.clear()
	_gdtest_active_definition.clear()
	_gdtest_suite_ordinals.clear()
	_gdtest_case_ordinals.clear()
	_gdtest_discovery_errors.clear()


func _gdtest_finish_registration() -> void:
	for definition_value: Variant in _gdtest_definition_roots:
		if definition_value is Dictionary:
			await _gdtest_run_definition(_string_dictionary(definition_value))
	_gdtest_suite_stack.clear()
	_gdtest_active_definition.clear()
	_gdtest_registration_active = false


func _gdtest_run_definition(definition: Dictionary[String, Variant]) -> void:
	var previous: Dictionary[String, Variant] = _gdtest_active_definition
	_gdtest_active_definition = definition
	_gdtest_suite_stack.assign(definition.get("ancestors", []))
	var info: GDTestSuiteInfo = definition["info"]
	_gdtest_suite_stack.append(info)
	var callback: Callable = definition["callback"]
	await callback.call()
	var children: Variant = definition["children"]
	if children is Array:
		for child_value: Variant in children:
			if child_value is Dictionary:
				await _gdtest_run_definition(_string_dictionary(child_value))
	_gdtest_active_definition = previous


func _gdtest_manifest() -> Dictionary[String, Variant]:
	return {
		"cases": _gdtest_dynamic_cases.duplicate(true),
		"suites": _gdtest_dynamic_suites.duplicate(true),
		"errors": _gdtest_discovery_errors,
	}


func _gdtest_begin_case() -> void:
	_gdtest_case_active = true
	_gdtest_status = "void"
	_gdtest_assertions = 0
	_gdtest_failures.clear()
	_gdtest_messages.clear()
	_gdtest_outcome_data.clear()


func _gdtest_end_case() -> Dictionary[String, Variant]:
	_gdtest_case_active = false
	return {
		"status": _gdtest_status,
		"assertions": _gdtest_assertions,
		"failures": _gdtest_failures.duplicate(true),
		"messages": _gdtest_messages.duplicate(),
		"outcomes": _gdtest_outcome_data.duplicate(true),
	}


func _gdtest_record_error(text: String) -> void:
	var location: Dictionary[String, Variant] = _user_location()
	_gdtest_record_error_at(
			text,
			str(location.get("file", _gdtest_file)),
			int(location.get("line", -1))
	)


func _gdtest_record_error_at(text: String, file: String, line: int) -> void:
	_promote_status("error")
	var diagnostic: Dictionary[String, Variant] = {
		"assertion": "framework",
		"message": text,
		"location": { "file": file, "line": line },
	}
	_gdtest_failures.append(diagnostic)
	_emit_failure(diagnostic)


func _register_dynamic_row(
		description: String,
		callback: Callable,
		row: Variant,
		parameter_name: String,
		is_only: bool,
		ordinal: int = 0
) -> void:
	if not row is Array:
		_record_discovery_error("test_each row " + parameter_name + " must be an Array")
		return
	var parameters: Array[Variant] = []
	parameters.assign(row)
	if callback.get_argument_count() != parameters.size():
		_record_discovery_error(
				"test_each row %s has %d arguments; callback takes %d" % [
					parameter_name,
					parameters.size(),
					callback.get_argument_count(),
				]
		)
		return
	var display_name: String = "%s %d: %s" % [
		description,
		ordinal,
		str(parameters),
	] if ordinal > 0 else "%s [%s]" % [description, parameter_name]
	_register_dynamic_case(description, callback, parameters, parameter_name, is_only, display_name)


func _register_dynamic_case(
		description: String,
		callback: Callable,
		parameters: Array[Variant],
		parameter_name: String,
		is_only: bool,
		display_name: String = ""
) -> void:
	var location: Dictionary[String, Variant] = _user_location()
	var suite_path: Array[String] = []
	var inherited_only: bool = false
	for suite: GDTestSuiteInfo in _gdtest_suite_stack:
		suite_path.append(suite.name)
		inherited_only = inherited_only or suite.is_only
	var line: int = int(location.get("line", -1))
	var ordinal: int = _gdtest_case_ordinals.get(line, 0)
	_gdtest_case_ordinals[line] = ordinal + 1
	var full_name: Array[String] = suite_path.duplicate()
	full_name.append(description)
	var encoded_name: String = "/".join(full_name.map(_encode_component))
	var case_id: String = _gdtest_file.trim_prefix(
			"res://"
	) + "::dynamic@%d:%d[%s]" % [line, ordinal, encoded_name]
	_gdtest_dynamic_cases.append(
			{
				"id": case_id,
				"name": description if display_name.is_empty() else display_name,
				"file": str(location.get("file", _gdtest_file)),
				"line": line,
				"callback": callback,
				"parameters": parameters.duplicate(true),
				"parameter_name": parameter_name,
				"is_only": is_only or inherited_only,
				"suite_path": suite_path,
				"suite_infos": _gdtest_suite_stack.duplicate(),
			}
	)


func _record_assertion(
		passed: bool,
		assertion: String,
		custom_message: String,
		actual: Variant,
		expected: Variant,
		has_expected: bool
) -> void:
	if not _require_case(assertion):
		return
	_gdtest_assertions += 1
	if passed:
		_promote_status("pass")
		return
	var default_message: String = assertion + " failed"
	_record_failure(
			assertion,
			custom_message if not custom_message.is_empty() else default_message,
			actual,
			expected,
			has_expected
	)


func _record_failure(
		assertion: String,
		text: String,
		actual: Variant,
		expected: Variant,
		has_expected: bool
) -> void:
	if not _require_case(assertion):
		return
	_promote_status("fail")
	var diagnostic: Dictionary[String, Variant] = {
		"assertion": assertion,
		"message": text,
		"actual": actual,
		"has_expected": has_expected,
		"location": _user_location(),
	}
	if has_expected:
		diagnostic["expected"] = expected
	_gdtest_failures.append(diagnostic)
	_emit_failure(diagnostic)


func _emit_failure(diagnostic: Dictionary[String, Variant]) -> void:
	if _gdtest_failure_sink.is_valid():
		_gdtest_failure_sink.call(diagnostic)


func _require_case(operation: String) -> bool:
	if _gdtest_case_active and not _gdtest_registration_active:
		return true
	_record_discovery_error(operation + " is only valid while a case is executing")
	return false


func _require_registration(operation: String) -> bool:
	if _gdtest_registration_active:
		return true
	_record_discovery_error(operation + " is only valid during suite registration")
	return false


func _record_discovery_error(text: String) -> void:
	_gdtest_discovery_errors.append({ "message": text, "location": _user_location() })


func _record_framework_error(text: String) -> void:
	if _gdtest_case_active:
		_gdtest_record_error(text)
	else:
		_record_discovery_error(text)


func _valid_registration_callback(callback: Callable, operation: String, arguments: int) -> bool:
	if not callback.is_valid():
		_record_discovery_error(operation + " requires a valid callback")
		return false
	if callback.get_argument_count() != arguments:
		_record_discovery_error(
				"%s callback takes %d arguments; expected %d" % [
					operation,
					callback.get_argument_count(),
					arguments,
				]
		)
		return false
	return true


func _promote_status(status: String) -> void:
	if _STATUS_RANK.get(status, -1) > _STATUS_RANK.get(_gdtest_status, -1):
		_gdtest_status = status


func _record_outcome_data(kind: String, text: String, data: Dictionary[String, Variant]) -> void:
	_gdtest_outcome_data.append({ "kind": kind, "message": text, "data": data.duplicate(true) })


func _valid_tolerance(tolerance: float, assertion: String, custom_message: String) -> bool:
	if is_finite(tolerance) and tolerance >= 0.0:
		return true
	_record_failure(
			assertion,
			_framework_message(custom_message, "tolerance must be finite and non-negative"),
			tolerance,
			0.0,
			true
	)
	return false


func _is_empty_value(value: Variant) -> Dictionary[String, Variant]:
	var value_type: int = typeof(value)
	if value_type in [TYPE_STRING, TYPE_STRING_NAME, TYPE_NODE_PATH, TYPE_ARRAY, TYPE_DICTIONARY]:
		return { "supported": true, "value": value.is_empty() }
	if value_type in _PACKED_TYPES:
		return { "supported": true, "value": value.is_empty() }
	if value is Object and is_instance_valid(value) and value.has_method("is_empty"):
		var result: Variant = value.call("is_empty")
		if result is bool:
			return { "supported": true, "value": result }
		return { "supported": false, "error": "custom is_empty() must return bool" }
	return { "supported": false, "error": "value does not support emptiness" }


func _contains_value(value: Variant, item: Variant) -> Dictionary[String, Variant]:
	var value_type: int = typeof(value)
	if value_type in [TYPE_ARRAY, TYPE_DICTIONARY] or value_type in _PACKED_TYPES:
		return { "supported": true, "value": value.has(item) }
	if value_type in [TYPE_STRING, TYPE_STRING_NAME, TYPE_NODE_PATH]:
		return { "supported": true, "value": str(value).contains(str(item)) }
	if value is Object and is_instance_valid(value) and value.has_method("has"):
		var result: Variant = value.call("has", item)
		if result is bool:
			return { "supported": true, "value": result }
		return { "supported": false, "error": "custom has() must return bool" }
	return { "supported": false, "error": "value does not support containment" }


func _framework_message(custom_message: String, framework_message: String) -> String:
	if custom_message.is_empty():
		return framework_message
	return custom_message + ": " + framework_message


func _user_location() -> Dictionary[String, Variant]:
	var stack: Array[Variant] = get_stack()
	for frame_value: Variant in stack:
		var frame: Dictionary[String, Variant] = _string_dictionary(frame_value)
		var source: String = str(frame.get("source", ""))
		if not source.ends_with("/addons/gdtest/gdtest.gd"):
			return { "file": source, "line": int(frame.get("line", -1)) }
	return { "file": _gdtest_file, "line": -1 }


func _encode_component(value: Variant) -> String:
	var bytes: PackedByteArray = str(value).to_utf8_buffer()
	var result: String = ""
	for byte: int in bytes:
		var character: String = String.chr(byte)
		if (
				byte >= 65 and byte <= 90
				or byte >= 97 and byte <= 122
				or byte >= 48 and byte <= 57
				or character in ["-", ".", "_", "~"]
		):
			result += character
		else:
			result += "%%%02X" % byte
	return result


func _utf8_less(left: String, right: String) -> bool:
	var left_bytes: PackedByteArray = left.to_utf8_buffer()
	var right_bytes: PackedByteArray = right.to_utf8_buffer()
	var shared_size: int = mini(left_bytes.size(), right_bytes.size())
	for index: int in range(shared_size):
		if left_bytes[index] != right_bytes[index]:
			return left_bytes[index] < right_bytes[index]
	return left_bytes.size() < right_bytes.size()


func _string_dictionary(value: Variant) -> Dictionary[String, Variant]:
	var result: Dictionary[String, Variant] = {}
	if not value is Dictionary:
		return result
	for key: Variant in value:
		result[str(key)] = value[key]
	return result
