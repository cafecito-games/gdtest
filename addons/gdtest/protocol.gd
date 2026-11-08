## Internal daemon-to-worker protocol codec shared by the worker and its helpers.
class_name GDTestProtocol
extends RefCounted

## Maximum recursive wire-value depth.
const MAX_NESTING: int = 32
## Maximum items serialized from one collection.
const MAX_COLLECTION_ITEMS: int = 10000


## Encodes one bounded protocol message as JSON Lines bytes.
static func encode_line(message: Dictionary[String, Variant]) -> PackedByteArray:
	return (JSON.stringify(message) + "\n").to_utf8_buffer()


## Builds the initial authenticated `hello` protocol envelope.
static func hello_envelope(
		token: String,
		worker_id: String,
		session_id: String,
		generation: int,
		project_path: String
) -> Dictionary[String, Variant]:
	return {
		"version": GDTestVersion.WORKER_PROTOCOL_VERSION,
		"type": "hello",
		"token": token,
		"payload": {
			"runtime_version": GDTestVersion.PRODUCT_VERSION,
			"worker_id": worker_id,
			"session_id": session_id,
			"generation": generation,
			"project_path": project_path,
			"godot_version": Engine.get_version_info(),
			"capabilities": [
				"run",
				"bench",
				"discover",
				"ping",
				"cancel",
				"reload",
				"reset",
				"shutdown",
			],
		},
	}


## Builds one sequenced worker `event` protocol envelope.
static func event_envelope(
		request_id: String,
		event_type: String,
		worker_id: String,
		session_id: String,
		generation: int,
		run_id: String,
		sequence: int,
		data: Dictionary[String, Variant]
) -> Dictionary[String, Variant]:
	return {
		"version": GDTestVersion.WORKER_PROTOCOL_VERSION,
		"type": "event",
		"request_id": request_id,
		"payload": {
			"kind": event_type,
			"worker_id": worker_id,
			"session_id": session_id,
			"generation": generation,
			"run_id": run_id,
			"sequence": sequence,
			"data": data,
		},
	}


## Returns zeroed status counts for one file or run.
static func empty_totals() -> Dictionary[String, int]:
	return { "pass": 0, "fail": 0, "error": 0, "skip": 0, "todo": 0, "void": 0, "canceled": 0 }


## Builds a versioned response envelope for one worker command reply.
static func response_envelope(
		message_type: String,
		request_id: String,
		payload: Dictionary[String, Variant]
) -> Dictionary[String, Variant]:
	return {
		"version": GDTestVersion.WORKER_PROTOCOL_VERSION,
		"type": message_type,
		"request_id": request_id,
		"payload": payload,
	}


## Builds a versioned pong envelope for a worker liveness check.
static func pong_envelope(
		request_id: String,
		worker_id: String,
		generation: int
) -> Dictionary[String, Variant]:
	return {
		"version": GDTestVersion.WORKER_PROTOCOL_VERSION,
		"type": "pong",
		"request_id": request_id,
		"payload": { "worker_id": worker_id, "generation": generation },
	}


## Builds a generation acknowledgement envelope for reload and reset commands.
static func generation_ack(
		message_type: String,
		request_id: String,
		generation: int
) -> Dictionary[String, Variant]:
	return {
		"version": GDTestVersion.WORKER_PROTOCOL_VERSION,
		"type": message_type,
		"request_id": request_id,
		"generation": generation,
	}


## Builds a worker_error event payload for a named worker or protocol error code.
static func worker_error(
		code: String,
		text: String,
		test_id: String
) -> Dictionary[String, Variant]:
	return { "name": code, "message": text, "test_id": test_id, "metadata": { "code": code } }


## Decodes one JSON Lines object or returns an empty dictionary.
static func decode_line(line: String) -> Dictionary[String, Variant]:
	if not _json_nesting_within_limit(line):
		return {}
	var parsed: Variant = JSON.parse_string(line)
	if not parsed is Dictionary:
		return {}
	var result: Dictionary[String, Variant] = {}
	for key: Variant in parsed:
		result[str(key)] = parsed[key]
	return result


static func _json_nesting_within_limit(line: String) -> bool:
	var depth: int = 0
	var in_string: bool = false
	var escaped: bool = false
	for index: int in range(line.length()):
		var character: String = line.substr(index, 1)
		if in_string:
			if escaped:
				escaped = false
			elif character == "\\":
				escaped = true
			elif character == '"':
				in_string = false
			continue
		if character == '"':
			in_string = true
		elif character == "{" or character == "[":
			depth += 1
			if depth > MAX_NESTING:
				return false
		elif character == "}" or character == "]":
			depth -= 1
			if depth < 0:
				return false
	return depth == 0


## Converts explicit outcome data to bounded wire values.
static func wire_outcomes(value: Variant) -> Array[Dictionary]:
	var encoded: Array[Dictionary] = []
	if not value is Array:
		return encoded
	for item: Variant in value:
		if not item is Dictionary:
			continue
		var outcome: Dictionary[Variant, Variant] = item
		encoded.append(
				{
					"kind": str(outcome.get("kind", "")),
					"message": str(outcome.get("message", "")),
					"data": wire_value(outcome.get("data", {})),
				}
		)
	return encoded


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
				"items": array_values,
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
				"entries": entries,
				"truncated": keys.size() > dictionary_limit,
			}
		TYPE_VECTOR2:
			var vector_2: Vector2 = value
			return { "type": "vector2", "value": { "x": vector_2.x, "y": vector_2.y } }
		TYPE_VECTOR2I:
			var vector_2i: Vector2i = value
			return { "type": "vector2i", "value": { "x": vector_2i.x, "y": vector_2i.y } }
		TYPE_VECTOR3:
			var vector_3: Vector3 = value
			return {
				"type": "vector3",
				"value": { "x": vector_3.x, "y": vector_3.y, "z": vector_3.z },
			}
		TYPE_VECTOR3I:
			var vector_3i: Vector3i = value
			return {
				"type": "vector3i",
				"value": { "x": vector_3i.x, "y": vector_3i.y, "z": vector_3i.z },
			}
		TYPE_VECTOR4:
			var vector_4: Vector4 = value
			return {
				"type": "vector4",
				"value": { "x": vector_4.x, "y": vector_4.y, "z": vector_4.z, "w": vector_4.w },
			}
		TYPE_VECTOR4I:
			var vector_4i: Vector4i = value
			return {
				"type": "vector4i",
				"value": { "x": vector_4i.x, "y": vector_4i.y, "z": vector_4i.z, "w": vector_4i.w },
			}
		TYPE_COLOR:
			var color: Color = value
			return {
				"type": "color",
				"value": { "r": color.r, "g": color.g, "b": color.b, "a": color.a },
			}
		TYPE_RECT2:
			var rectangle: Rect2 = value
			return {
				"type": "rect2",
				"value": {
					"position": wire_value(rectangle.position, depth + 1),
					"size": wire_value(rectangle.size, depth + 1),
				},
			}
		TYPE_RECT2I:
			var rectangle_i: Rect2i = value
			return {
				"type": "rect2i",
				"value": {
					"position": wire_value(rectangle_i.position, depth + 1),
					"size": wire_value(rectangle_i.size, depth + 1),
				},
			}
		TYPE_OBJECT:
			if not is_instance_valid(value):
				return { "type": "object", "display": "<freed>" }
			var object: Object = value
			return { "type": object.get_class().to_snake_case(), "display": str(object) }
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
		var prepared: Dictionary[String, Variant] = string_dictionary(prepared_value)
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
				cases.append(_discovery_case(string_dictionary(case_value), path))
		files.append({ "path": path, "name": name, "line": line, "cases": cases })
		for error_value: Variant in prepared.get("errors", []):
			if error_value is Dictionary:
				errors.append(_serialized_discovery_error(string_dictionary(error_value), path))
	return {
		"protocol_version": GDTestVersion.WORKER_PROTOCOL_VERSION,
		"generation": generation,
		"content_fingerprint": content_fingerprint,
		"files": files,
		"errors": errors,
	}


## Normalizes one runtime discovery diagnostic while retaining its location.
static func discovery_error(value: Variant, default_file: String) -> Dictionary[String, Variant]:
	return _runtime_diagnostic(value, default_file, "worker.discovery")


## Normalizes every runtime discovery diagnostic in `values` against `default_file`.
static func discovery_errors(values: Array[Variant], default_file: String) -> Array[Variant]:
	var normalized: Array[Variant] = []
	for value: Variant in values:
		normalized.append(discovery_error(value, default_file))
	return normalized


## Normalizes one execution-time framework diagnostic while retaining its location.
static func framework_error(value: Variant, default_file: String) -> Dictionary[String, Variant]:
	return _runtime_diagnostic(value, default_file, "worker.framework")


## Normalizes every execution-time framework diagnostic in `values` against `default_file`.
static func framework_errors(values: Array[Variant], default_file: String) -> Array[Variant]:
	var normalized: Array[Variant] = []
	for value: Variant in values:
		normalized.append(framework_error(value, default_file))
	return normalized


## Builds one `worker_error` wire payload from a normalized or raw runtime diagnostic.
static func diagnostic_worker_error(
		value: Variant,
		default_file: String,
		default_code: String,
		assertion: String,
		case_id: String
) -> Dictionary[String, Variant]:
	var diagnostic: Dictionary[String, Variant] = _runtime_diagnostic(
			value,
			default_file,
			default_code
	)
	var payload: Dictionary[String, Variant] = {
		"name": str(diagnostic["code"]),
		"message": str(diagnostic["message"]),
		"test_id": case_id,
		"metadata": { "code": diagnostic["code"] },
	}
	if not assertion.is_empty():
		payload["assertion"] = assertion
	var location: Dictionary[String, Variant] = string_dictionary(diagnostic["location"])
	if not str(location.get("file", "")).is_empty():
		payload["location"] = location
	return payload


## Builds one `assertion_failed` wire payload with normalized project-relative location.
static func assertion_failed(
		case_id: String,
		diagnostic: Dictionary[String, Variant]
) -> Dictionary[String, Variant]:
	return _diagnostic_assertion_event(case_id, diagnostic, false)


## Builds one `case_warning` wire payload with normalized project-relative location.
static func case_warning(
		case_id: String,
		diagnostic: Dictionary[String, Variant]
) -> Dictionary[String, Variant]:
	return _diagnostic_assertion_event(case_id, diagnostic, true)


static func _diagnostic_assertion_event(
		case_id: String,
		diagnostic: Dictionary[String, Variant],
		omit_default_actual: bool
) -> Dictionary[String, Variant]:
	var location: Dictionary[String, Variant] = string_dictionary(diagnostic.get("location", {}))
	if location.has("file"):
		location["file"] = str(location["file"]).trim_prefix("res://")
	var payload: Dictionary[String, Variant] = {
		"assertion": diagnostic.get("assertion", ""),
		"message": diagnostic.get("message", ""),
	}
	if not case_id.is_empty():
		payload["test_id"] = case_id
	var omit_actual: bool = diagnostic.get("omit_actual", false)
	var has_expected: bool = diagnostic.get("has_expected", false)
	if diagnostic.has("actual") or not omit_default_actual and not omit_actual:
		payload["actual"] = wire_value(diagnostic.get("actual"))
	if not str(location.get("file", "")).is_empty():
		payload["location"] = location
	if has_expected:
		payload["expected"] = wire_value(diagnostic.get("expected"))
	var metadata_value: Variant = diagnostic.get("metadata")
	if metadata_value is Dictionary:
		var metadata: Dictionary[String, Variant] = string_dictionary(metadata_value)
		if not metadata.is_empty():
			payload["metadata"] = metadata
	return payload


static func _runtime_diagnostic(
		value: Variant,
		default_file: String,
		default_code: String
) -> Dictionary[String, Variant]:
	var diagnostic: Dictionary[String, Variant] = string_dictionary(value)
	var location: Dictionary[String, Variant] = string_dictionary(diagnostic.get("location", {}))
	var file_path: String = str(location.get("file", ""))
	if file_path.is_empty():
		file_path = default_file
	location["file"] = file_path.trim_prefix("res://")
	if not location.has("line"):
		location["line"] = -1
	return {
		"code": str(diagnostic.get("code", default_code)),
		"message": str(diagnostic.get("message", value)),
		"location": location,
	}


## Orders two strings lexicographically by raw UTF-8 bytes.
static func utf8_less(left: String, right: String) -> bool:
	var left_bytes: PackedByteArray = left.to_utf8_buffer()
	var right_bytes: PackedByteArray = right.to_utf8_buffer()
	var shared_size: int = mini(left_bytes.size(), right_bytes.size())
	for index: int in range(shared_size):
		if left_bytes[index] != right_bytes[index]:
			return left_bytes[index] < right_bytes[index]
	return left_bytes.size() < right_bytes.size()


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


## Produces terminal run data with phase timings in microseconds.
static func run_finished(
		status: String,
		counts: Dictionary[String, int],
		duration_usec: int,
		preparation_usec: int,
		execution_usec: int,
		serialization_usec: int,
		late_callback_invocations: int = 0
) -> Dictionary[String, Variant]:
	var metadata: Dictionary[String, Variant] = {
		"timings_usec": {
			"script_load_dynamic_definition": preparation_usec,
			"hooks_cases": execution_usec,
			"result_serialization": serialization_usec,
		},
	}
	if late_callback_invocations > 0:
		metadata["late_callback_invocations"] = late_callback_invocations
	return {
		"status": status,
		"counts": counts,
		"duration_ns": duration_usec * 1000,
		"metadata": metadata,
	}


## Serializes one case_started event payload.
static func case_started(info: GDTestCaseInfo, timeout_ns: int) -> Dictionary[String, Variant]:
	return {
		"test_id": info.id,
		"name": info.name,
		"location": { "file": info.file.trim_prefix("res://"), "line": info.line },
		"metadata": { "timeout_ns": timeout_ns },
	}


## Serializes one worker_tainted event payload when a case times out or a run is canceled.
static func worker_tainted(case_id: String, timed_out: bool) -> Dictionary[String, Variant]:
	var payload: Dictionary[String, Variant] = {
		"message": "case timeout" if timed_out else "case cancellation",
		"metadata": { "reason": "case_timeout" if timed_out else "canceled" },
	}
	if not case_id.is_empty():
		payload["test_id"] = case_id
	return payload


## Serializes one terminal case event and retains framework-error diagnostics.
static func case_finished(
		case_id: String,
		case_name: String,
		path: String,
		line: int,
		status: String,
		result: Dictionary[String, Variant],
		duration_usec: int
) -> Dictionary[String, Variant]:
	var payload: Dictionary[String, Variant] = {
		"test_id": case_id,
		"name": case_name,
		"location": { "file": path.trim_prefix("res://"), "line": line },
		"status": status,
		"assertions": result.get("assertions", 0),
		"messages": result.get("messages", []),
		"outcomes": wire_outcomes(result.get("outcomes", [])),
		"duration_ns": duration_usec * 1000,
	}
	var failures: Variant = result.get("failures", [])
	if status == "error" and failures is Array:
		var failure_list: Array[Variant] = failures
		if not failure_list.is_empty():
			var diagnostic: Dictionary[String, Variant] = string_dictionary(failure_list[0])
			payload["message"] = str(diagnostic.get("message", "case error"))
			var location: Dictionary[String, Variant] = string_dictionary(
					diagnostic.get("location", {})
			)
			location["file"] = str(location.get("file", path)).trim_prefix("res://")
			payload["location"] = location
	return payload


## Folds case totals into the terminal run status.
static func aggregate_status(totals: Dictionary[String, int]) -> String:
	if totals.get("error", 0) > 0:
		return "error"
	if totals.get("fail", 0) > 0:
		return "fail"
	if totals.get("canceled", 0) > 0:
		return "canceled"
	return "pass"


## Percent-encodes one stable identifier component using RFC 3986 unreserved bytes.
static func encode_component(value: Variant) -> String:
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
						"timeout_ns": int(suite.timeout_seconds * 1000000000.0),
					}
			)
	var case_line: int = case_plan.get("line", -1)
	var case_is_only: bool = case_plan.get("is_only", false)
	return {
		"id": str(case_plan.get("id", "")),
		"name": str(case_plan.get("name", "")),
		"file": str(case_plan.get("file", default_file)).trim_prefix("res://"),
		"line": case_line,
		"method_name": str(case_plan.get("method_name", case_plan.get("method", ""))),
		"parameters": parameters,
		"parameter_name": str(case_plan.get("parameter_name", "")),
		"is_only": case_is_only,
		"timeout_ns": int(float(case_plan.get("timeout_seconds", 0.0)) * 1000000000.0),
		"suites": suites,
	}


static func _serialized_discovery_error(
		diagnostic: Dictionary[String, Variant],
		default_file: String
) -> Dictionary[String, Variant]:
	var normalized: Dictionary[String, Variant] = discovery_error(diagnostic, default_file)
	var location: Dictionary[String, Variant] = string_dictionary(normalized["location"])
	location["file"] = str(location.get("file", default_file)).trim_prefix("res://")
	return { "code": normalized["code"], "message": normalized["message"], "location": location }


## Converts an untyped Dictionary Variant into the protocol's string-keyed form.
static func string_dictionary(value: Variant) -> Dictionary[String, Variant]:
	var result: Dictionary[String, Variant] = {}
	if not value is Dictionary:
		return result
	for key: Variant in value:
		result[str(key)] = value[key]
	return result
