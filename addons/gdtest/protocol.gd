extends RefCounted

## Internal runtime version declaration.
const Version: GDScript = preload("res://addons/gdtest/version.gd")
## Maximum recursive wire-value depth.
const MAX_NESTING: int = 32
## Maximum items serialized from one collection.
const MAX_COLLECTION_ITEMS: int = 10000


## Encodes one bounded protocol message as JSON Lines bytes.
static func encode_line(message: Dictionary[String, Variant]) -> PackedByteArray:
	return (JSON.stringify(message) + "\n").to_utf8_buffer()


## Decodes one JSON Lines object or returns an empty dictionary.
static func decode_line(line: String) -> Dictionary[String, Variant]:
	var parsed: Variant = JSON.parse_string(line)
	if not parsed is Dictionary:
		return {}
	var result: Dictionary[String, Variant] = {}
	for key: Variant in parsed:
		result[str(key)] = parsed[key]
	return result


## Converts a Variant to a stable tagged wire representation.
static func wire_value(value: Variant, depth: int = 0) -> Dictionary[String, Variant]:
	if depth >= MAX_NESTING:
		return { "type": "truncated", "display": "maximum nesting exceeded" }
	match typeof(value):
		TYPE_NIL:
			return { "type": "null", "value": null }
		TYPE_BOOL:
			return { "type": "bool", "value": value }
		TYPE_INT:
			return { "type": "int", "value": str(value) }
		TYPE_FLOAT:
			var number: float = value
			if is_nan(number):
				return { "type": "float", "value": "nan" }
			if is_inf(number):
				return { "type": "float", "value": "-inf" if number < 0.0 else "+inf" }
			return { "type": "float", "value": number }
		TYPE_STRING:
			return { "type": "string", "value": value }
		TYPE_STRING_NAME:
			return { "type": "string_name", "value": str(value) }
		TYPE_ARRAY:
			var input_array: Array[Variant] = value
			var array_values: Array[Variant] = []
			var array_limit: int = mini(input_array.size(), MAX_COLLECTION_ITEMS)
			for index: int in range(array_limit):
				array_values.append(wire_value(input_array[index], depth + 1))
			return {
				"type": "array",
				"value": array_values,
				"truncated": input_array.size() > array_limit,
			}
		TYPE_DICTIONARY:
			var input_dictionary: Dictionary[Variant, Variant] = value
			var entries: Array[Variant] = []
			var keys: Array[Variant] = input_dictionary.keys()
			keys.sort_custom(
					func(left: Variant, right: Variant) -> bool: return str(left) < str(right)
			)
			var dictionary_limit: int = mini(keys.size(), MAX_COLLECTION_ITEMS)
			for index: int in range(dictionary_limit):
				var key: Variant = keys[index]
				entries.append(
						{
							"key": wire_value(key, depth + 1),
							"value": wire_value(input_dictionary[key], depth + 1),
						}
				)
			return {
				"type": "dictionary",
				"value": entries,
				"truncated": keys.size() > dictionary_limit,
			}
		_:
			return { "type": type_string(typeof(value)).to_snake_case(), "display": str(value) }


## Converts prepared runtime registration into a JSON-safe discovery manifest.
static func discovery_manifest(
		prepared_files: Array[Dictionary],
		generation: int,
		content_fingerprint: String
) -> Dictionary[String, Variant]:
	var files: Array[Variant] = []
	var errors: Array[Variant] = []
	for prepared_value: Variant in prepared_files:
		var prepared: Dictionary[String, Variant] = _string_dictionary(prepared_value)
		var root_value: Variant = prepared.get("root_info")
		var path: String = str(prepared.get("path", "")).trim_prefix("res://")
		var name: String = path
		var line: int = 1
		if root_value is GDTestSuiteInfo:
			var root_info: GDTestSuiteInfo = root_value
			name = root_info.name
			line = root_info.line
		var cases: Array[Variant] = []
		for case_value: Variant in prepared.get("cases", []):
			if case_value is Dictionary:
				cases.append(_discovery_case(_string_dictionary(case_value), path))
		files.append({ "path": path, "name": name, "line": line, "cases": cases })
		for error_value: Variant in prepared.get("errors", []):
			if error_value is Dictionary:
				errors.append(_serialized_discovery_error(error_value, path))
	return {
		"protocol_version": Version.WORKER_PROTOCOL_VERSION,
		"generation": generation,
		"content_fingerprint": content_fingerprint,
		"files": files,
		"errors": errors,
	}


## Normalizes one runtime discovery diagnostic while retaining its location.
static func discovery_error(value: Variant, default_file: String) -> Dictionary[String, Variant]:
	var diagnostic: Dictionary[String, Variant] = _string_dictionary(value)
	var location: Dictionary[String, Variant] = _string_dictionary(diagnostic.get("location", {}))
	if str(location.get("file", "")).is_empty():
		location["file"] = default_file
	if not location.has("line"):
		location["line"] = -1
	return {
		"code": str(diagnostic.get("code", "worker.discovery")),
		"message": str(diagnostic.get("message", value)),
		"location": location,
	}


## Validates the plan-wide focused-test execution policy.
static func only_policy_error(only_mode: String, has_focus: bool) -> String:
	match only_mode:
		"", "auto", "ignore":
			return ""
		"require":
			return "" if has_focus else "only mode requires at least one focused selected case"
		"forbid":
			return "focused tests are forbidden by only mode" if has_focus else ""
		_:
			return "unsupported only mode: " + only_mode


static func _discovery_case(
		case_plan: Dictionary[String, Variant],
		default_file: String
) -> Dictionary[String, Variant]:
	var parameters: Array[Variant] = []
	var parameter_value: Variant = case_plan.get("parameters", [])
	if parameter_value is Array:
		for value: Variant in parameter_value:
			parameters.append(wire_value(value))
	var suites: Array[Variant] = []
	for suite_value: Variant in case_plan.get("suite_infos", []):
		if suite_value is GDTestSuiteInfo:
			var suite: GDTestSuiteInfo = suite_value
			suites.append(
					{
						"id": suite.id,
						"name": suite.name,
						"file": suite.file.trim_prefix("res://"),
						"line": suite.line,
						"is_only": suite.is_only,
					}
			)
	return {
		"id": str(case_plan.get("id", "")),
		"name": str(case_plan.get("name", "")),
		"file": str(case_plan.get("file", default_file)).trim_prefix("res://"),
		"line": int(case_plan.get("line", -1)),
		"method_name": str(case_plan.get("method_name", case_plan.get("method", ""))),
		"parameters": parameters,
		"parameter_name": str(case_plan.get("parameter_name", "")),
		"is_only": bool(case_plan.get("is_only", false)),
		"suites": suites,
	}


static func _serialized_discovery_error(
		diagnostic: Dictionary[String, Variant],
		default_file: String
) -> Dictionary[String, Variant]:
	var normalized: Dictionary[String, Variant] = discovery_error(diagnostic, default_file)
	var location: Dictionary[String, Variant] = _string_dictionary(normalized["location"])
	location["file"] = str(location.get("file", default_file)).trim_prefix("res://")
	return { "code": normalized["code"], "message": normalized["message"], "location": location }


static func _string_dictionary(value: Variant) -> Dictionary[String, Variant]:
	var result: Dictionary[String, Variant] = {}
	if not value is Dictionary:
		return result
	for key: Variant in value:
		result[str(key)] = value[key]
	return result
