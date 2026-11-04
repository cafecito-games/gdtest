extends SceneTree

## Internal protocol codec script.
const Protocol: GDScript = preload("res://addons/gdtest/protocol.gd")
## Internal runtime version declaration.
const Version: GDScript = preload("res://addons/gdtest/version.gd")
## Hard upper bound for one JSONL protocol message.
const MAX_MESSAGE_BYTES: int = 1048576

var _peer: StreamPeerTCP
var _incoming: String = ""
var _host: String = "127.0.0.1"
var _port: int = 0
var _token: String = ""
var _session_id: String = ""
var _generation: int = 0
var _worker_id: String = ""
var _run_id: String = ""
var _request_id: String = ""
var _sequence: int = 0
var _active_case_id: String = ""
var _active_test: GDTest
var _running: bool = false
var _cancel_requested: bool = false
var _shutdown_requested: bool = false


func _initialize() -> void:
	call_deferred("_start")


func _start() -> void:
	var argument_error: String = _parse_arguments(OS.get_cmdline_user_args())
	if not argument_error.is_empty():
		# No protocol connection exists yet, so stderr is the only diagnostic channel.
		# gdkit:ignore = no-engine-logging
		printerr("gdtest worker: " + argument_error)
		quit(2)
		return
	_peer = StreamPeerTCP.new()
	var connect_error: Error = _peer.connect_to_host(_host, _port)
	if connect_error != OK:
		# No protocol connection exists yet, so stderr is the only diagnostic channel.
		# gdkit:ignore = no-engine-logging
		printerr("gdtest worker: connect failed: " + error_string(connect_error))
		quit(2)
		return
	while _peer.get_status() == StreamPeerTCP.STATUS_CONNECTING:
		_peer.poll()
		await process_frame
	if _peer.get_status() != StreamPeerTCP.STATUS_CONNECTED:
		# No protocol connection exists yet, so stderr is the only diagnostic channel.
		# gdkit:ignore = no-engine-logging
		printerr("gdtest worker: daemon listener unavailable")
		quit(2)
		return
	_send(
			{
				"version": Version.WORKER_PROTOCOL_VERSION,
				"type": "hello",
				"token": _token,
				"payload": {
					"runtime_version": Version.PRODUCT_VERSION,
					"worker_id": _worker_id,
					"session_id": _session_id,
					"generation": _generation,
					"project_path": ProjectSettings.globalize_path("res://").trim_suffix("/"),
					"godot_version": Engine.get_version_info(),
					"capabilities": [
						"run",
						"discover",
						"ping",
						"cancel",
						"reload",
						"reset",
						"shutdown",
					],
				},
			}
	)
	_send_event("worker_ready", {})
	while not _shutdown_requested or _running:
		_poll_messages()
		await process_frame
	_peer.disconnect_from_host()
	quit(0)


# Each command-line validation return identifies a distinct actionable startup error.
# gdkit:ignore = max-returns
func _parse_arguments(arguments: PackedStringArray) -> String:
	var index: int = 0
	while index < arguments.size():
		var argument: String = arguments[index]
		if not argument.begins_with("--gdtest-"):
			index += 1
			continue
		if index + 1 >= arguments.size():
			return "missing value for " + argument
		var value: String = arguments[index + 1]
		match argument:
			"--gdtest-worker-host":
				_host = value
			"--gdtest-worker-port":
				_port = value.to_int()
			"--gdtest-worker-token":
				_token = value
			"--gdtest-session", "--gdtest-session-id":
				_session_id = value
			"--gdtest-generation":
				_generation = value.to_int()
			"--gdtest-worker-id":
				_worker_id = value
			_:
				return "unknown argument " + argument
		index += 2
	if _port < 1 or _port > 65535:
		return "worker port is invalid"
	if _token.is_empty():
		return "worker token is missing"
	if _session_id.is_empty():
		return "session identifier is missing"
	if _worker_id.is_empty():
		return "worker identifier is missing"
	# The whole-function diagnostic is located on the final return.
	# gdkit:ignore = max-returns
	return ""


func _poll_messages() -> void:
	_peer.poll()
	if _peer.get_status() != StreamPeerTCP.STATUS_CONNECTED:
		_shutdown_requested = true
		_cancel_requested = true
		return
	var available: int = _peer.get_available_bytes()
	if available <= 0:
		return
	_incoming += _peer.get_utf8_string(available)
	if _incoming.length() > MAX_MESSAGE_BYTES and not _incoming.contains("\n"):
		_send_worker_error("protocol.message_too_large", "incoming message exceeds limit")
		_shutdown_requested = true
		return
	while _incoming.contains("\n"):
		var newline: int = _incoming.find("\n")
		var line: String = _incoming.substr(0, newline).trim_suffix("\r")
		_incoming = _incoming.substr(newline + 1)
		if line.to_utf8_buffer().size() > MAX_MESSAGE_BYTES:
			_send_worker_error("protocol.message_too_large", "incoming message exceeds limit")
			continue
		var message: Dictionary[String, Variant] = Protocol.decode_line(line)
		if message.is_empty():
			_send_worker_error("protocol.invalid_json", "message must be a JSON object")
			continue
		_dispatch(message)


func _dispatch(message: Dictionary[String, Variant]) -> void:
	var message_type: String = str(message.get("type", ""))
	var payload: Dictionary[String, Variant] = _string_dictionary(message.get("payload", {}))
	match message_type:
		"ping":
			_send(
					{
						"version": Version.WORKER_PROTOCOL_VERSION,
						"type": "pong",
						"request_id": str(message.get("request_id", "")),
						"payload": { "worker_id": _worker_id, "generation": _generation },
					}
			)
		"run":
			if _running:
				_send_worker_error("worker.busy", "worker already has an active run")
			else:
				_run_command(message)
		"discover":
			if _running:
				_send_worker_error("worker.busy", "worker already has an active operation")
			else:
				_discover_command(message)
		"cancel":
			if str(payload.get("run_id", "")) == _run_id:
				_cancel_requested = true
		"reload":
			_reload_command(message)
		"reset":
			_reset_command(message)
		"shutdown":
			_shutdown_requested = true
			_cancel_requested = _running
		_:
			_send_worker_error("protocol.unknown_command", "unknown command type: " + message_type)


func _run_command(command: Dictionary[String, Variant]) -> void:
	_running = true
	_cancel_requested = false
	_sequence = 0
	var payload: Dictionary[String, Variant] = _string_dictionary(command.get("payload", {}))
	_run_id = str(payload.get("run_id", ""))
	_request_id = str(command.get("request_id", ""))
	var requested_generation: int = int(payload.get("generation", -1))
	if _run_id.is_empty():
		_send_worker_error("protocol.invalid_run", "run_id is required")
		_finish_run_state()
		return
	if requested_generation != _generation:
		_send_worker_error(
				"worker.stale_generation",
				"worker generation %d cannot execute generation %d" % [
					_generation,
					requested_generation,
				]
		)
		_finish_run_state()
		return
	_send_event("run_started", { "generation": _generation })
	var started_usec: int = Time.get_ticks_usec()
	var totals: Dictionary[String, int] = {
		"pass": 0,
		"fail": 0,
		"error": 0,
		"skip": 0,
		"todo": 0,
		"void": 0,
		"canceled": 0,
	}
	var plan_value: Variant = payload.get("plan", {})
	if not plan_value is Dictionary:
		_send_worker_error("protocol.invalid_plan", "run plan must be an object")
		_finish_run_state()
		return
	var plan: Dictionary[String, Variant] = _string_dictionary(plan_value)
	var files_value: Variant = plan.get("files", [])
	if not files_value is Array:
		_send_worker_error("protocol.invalid_plan", "plan.files must be an array")
		_finish_run_state()
		return
	var prepared_files: Array[Dictionary] = []
	for file_value: Variant in files_value:
		if _cancel_requested:
			break
		if not file_value is Dictionary:
			_send_worker_error("protocol.invalid_plan", "each plan file must be an object")
			continue
		var prepared: Dictionary[String, Variant] = await _prepare_file(
				_string_dictionary(file_value)
		)
		prepared_files.append(prepared)
	_apply_selected_ids(prepared_files, plan)
	var has_focus: bool = false
	for prepared_value: Variant in prepared_files:
		var prepared: Dictionary[String, Variant] = _string_dictionary(prepared_value)
		for case_value: Variant in prepared.get("cases", []):
			if case_value is Dictionary and bool(case_value.get("is_only", false)):
				has_focus = true
	var only_mode: String = str(plan.get("only_mode", "auto"))
	var policy_error: String = Protocol.only_policy_error(only_mode, has_focus)
	if not policy_error.is_empty():
		_send_discovery_error({ "message": policy_error }, "")
		totals["error"] += 1
	for prepared_value: Variant in prepared_files:
		var prepared: Dictionary[String, Variant] = _string_dictionary(prepared_value)
		if _cancel_requested:
			break
		if not policy_error.is_empty():
			break
		var focus_only: bool = has_focus and only_mode != "ignore"
		var file_totals: Dictionary[String, int] = await _run_prepared_file(prepared, focus_only)
		for status: String in totals:
			totals[status] += file_totals.get(status, 0)
	if _cancel_requested:
		totals["canceled"] += 1
	_send_event(
			"run_finished",
			{
				"status": "canceled" if _cancel_requested else _aggregate_status(totals),
				"counts": totals,
				"duration_usec": Time.get_ticks_usec() - started_usec,
			}
	)
	_finish_run_state()


func _discover_command(command: Dictionary[String, Variant]) -> void:
	_running = true
	_cancel_requested = false
	_sequence = 0
	_request_id = str(command.get("request_id", ""))
	var payload: Dictionary[String, Variant] = _string_dictionary(command.get("payload", {}))
	var requested_generation: int = int(payload.get("generation", -1))
	if requested_generation != _generation:
		_send_worker_error(
				"worker.stale_generation",
				"worker generation %d cannot discover generation %d" % [
					_generation,
					requested_generation,
				]
		)
		_finish_run_state()
		return
	var plan_value: Variant = payload.get("plan", {})
	if not plan_value is Dictionary:
		_send_worker_error("protocol.invalid_plan", "discovery plan must be an object")
		_finish_run_state()
		return
	var plan: Dictionary[String, Variant] = _string_dictionary(plan_value)
	var files_value: Variant = plan.get("files", [])
	if not files_value is Array:
		_send_worker_error("protocol.invalid_plan", "plan.files must be an array")
		_finish_run_state()
		return
	var prepared_files: Array[Dictionary] = []
	for file_value: Variant in files_value:
		if not file_value is Dictionary:
			prepared_files.append(
					{
						"path": "",
						"instance": null,
						"cases": [],
						"root_info": null,
						"errors": [{ "message": "each plan file must be an object" }],
						"discovery_error_count": 1,
					}
			)
			continue
		prepared_files.append(await _prepare_file(_string_dictionary(file_value), false))
	var manifest: Dictionary[String, Variant] = Protocol.discovery_manifest(
			prepared_files,
			_generation,
			str(plan.get("content_fingerprint", ""))
	)
	_send(
			{
				"version": Version.WORKER_PROTOCOL_VERSION,
				"type": "discover_finished",
				"request_id": _request_id,
				"payload": manifest,
			}
	)
	_finish_run_state()


func _prepare_file(
		file_plan: Dictionary[String, Variant],
		emit_errors: bool = true
) -> Dictionary[String, Variant]:
	var prepared: Dictionary[String, Variant] = {
		"path": "",
		"instance": null,
		"cases": [],
		"root_info": null,
		"errors": [],
		"discovery_error_count": 0,
	}
	var path: String = str(file_plan.get("file", file_plan.get("path", "")))
	if not path.begins_with("res://"):
		path = "res://" + path.trim_prefix("/")
	prepared["path"] = path
	var resource: Resource = ResourceLoader.load(path, "Script", ResourceLoader.CACHE_MODE_REPLACE)
	if not resource is Script:
		var load_error: Dictionary[String, Variant] = {
			"code": "worker.script_load",
			"message": "unable to load test script " + path,
			"location": { "file": path, "line": -1 },
		}
		prepared["errors"] = [load_error]
		if emit_errors:
			_send_worker_error("worker.script_load", str(load_error["message"]))
		prepared["discovery_error_count"] = 1
		return prepared
	var instance_value: Variant = resource.new()
	if not instance_value is GDTest:
		var type_error: Dictionary[String, Variant] = {
			"code": "worker.invalid_test",
			"message": path + " does not extend GDTest",
			"location": { "file": path, "line": -1 },
		}
		prepared["errors"] = [type_error]
		if emit_errors:
			_send_worker_error("worker.invalid_test", str(type_error["message"]))
		prepared["discovery_error_count"] = 1
		return prepared
	var instance: GDTest = instance_value
	_active_test = instance
	instance._gdtest_configure(self, path, _on_assertion_failed)
	instance._gdtest_begin_registration()
	if instance.has_method("suite"):
		await _call(instance, &"suite", [])
	await instance._gdtest_finish_registration()
	var manifest: Dictionary[String, Variant] = instance._gdtest_manifest()
	var discovery_errors: Array[Variant] = manifest.get("errors", [])
	var cases: Array[Variant] = _planned_cases(file_plan, instance, manifest, discovery_errors)
	if instance.has_method("before_case") and instance.has_method("before_each"):
		discovery_errors.append({ "message": "before_case and before_each cannot both be defined" })
	if instance.has_method("after_case") and instance.has_method("after_each"):
		discovery_errors.append({ "message": "after_case and after_each cannot both be defined" })
	var suite_name: String = await _suite_name(instance, resource, path, discovery_errors)
	for index: int in range(discovery_errors.size()):
		var discovery_error: Variant = discovery_errors[index]
		var normalized: Dictionary[String, Variant] = Protocol.discovery_error(
				discovery_error,
				path
		)
		discovery_errors[index] = normalized
		if emit_errors:
			_send_discovery_error(normalized, path)
	prepared["instance"] = instance
	prepared["cases"] = cases
	prepared["errors"] = discovery_errors
	prepared["root_info"] = GDTestSuiteInfo.new(
			path.trim_prefix("res://"),
			suite_name,
			path,
			int(file_plan.get("line", 1)),
			false
	)
	prepared["discovery_error_count"] = discovery_errors.size()
	_active_test = null
	return prepared


func _apply_selected_ids(
		prepared_files: Array[Dictionary],
		plan: Dictionary[String, Variant]
) -> void:
	if not bool(plan.get("selection_applied", false)):
		return
	var selected: Dictionary[String, bool] = {}
	var selected_value: Variant = plan.get("selected_ids", [])
	if selected_value is Array:
		for id_value: Variant in selected_value:
			selected[str(id_value)] = true
	for prepared_index: int in range(prepared_files.size()):
		var prepared: Dictionary[String, Variant] = prepared_files[prepared_index]
		var filtered: Array[Variant] = []
		for case_value: Variant in prepared.get("cases", []):
			if case_value is Dictionary and selected.has(str(case_value.get("id", ""))):
				filtered.append(case_value)
		prepared["cases"] = filtered
		prepared_files[prepared_index] = prepared


func _run_prepared_file(
		prepared: Dictionary[String, Variant],
		focus_only: bool
) -> Dictionary[String, int]:
	var totals: Dictionary[String, int] = {
		"pass": 0,
		"fail": 0,
		"error": 0,
		"skip": 0,
		"todo": 0,
		"void": 0,
		"canceled": 0,
	}
	var path: String = str(prepared.get("path", ""))
	var instance_value: Variant = prepared.get("instance")
	var discovery_error_count: int = int(prepared.get("discovery_error_count", 0))
	if discovery_error_count > 0:
		totals["error"] += discovery_error_count
	if not instance_value is GDTest:
		_send_event(
				"suite_finished",
				{ "id": path.trim_prefix("res://"), "status": "error", "counts": totals }
		)
		return totals
	var instance: GDTest = instance_value
	_active_test = instance
	var root_info: GDTestSuiteInfo = prepared.get("root_info")
	var cases: Array[Variant] = []
	if discovery_error_count == 0:
		for case_value: Variant in prepared.get("cases", []):
			if (
					case_value is Dictionary
					and (not focus_only or bool(case_value.get("is_only", false)))
			):
				cases.append(case_value)
	_send_event("suite_started", { "id": root_info.id, "name": root_info.name, "file": path })
	if not cases.is_empty():
		await _call_optional(instance, &"before_all", [])
		await _call_optional(instance, &"before_suite", [root_info])
	var open_suites: Array[GDTestSuiteInfo] = []
	for case_value: Variant in cases:
		if _cancel_requested:
			break
		var case_plan: Dictionary[String, Variant] = _string_dictionary(case_value)
		var desired_suites: Array[GDTestSuiteInfo] = _suite_infos(case_plan.get("suite_infos", []))
		var common_count: int = _common_suite_count(open_suites, desired_suites)
		for index: int in range(open_suites.size() - 1, common_count - 1, -1):
			await _call_optional(instance, &"after_suite", [open_suites[index]])
		open_suites.resize(common_count)
		for index: int in range(common_count, desired_suites.size()):
			await _call_optional(instance, &"before_suite", [desired_suites[index]])
			open_suites.append(desired_suites[index])
		var status: String = await _run_case(instance, path, case_plan)
		totals[status] = totals.get(status, 0) + 1
	if not cases.is_empty():
		for index: int in range(open_suites.size() - 1, -1, -1):
			await _call_optional(instance, &"after_suite", [open_suites[index]])
		await _call_optional(instance, &"after_suite", [root_info])
		await _call_optional(instance, &"after_all", [])
	_send_event(
			"suite_finished",
			{ "id": root_info.id, "status": _aggregate_status(totals), "counts": totals }
	)
	_active_test = null
	return totals


func _planned_cases(
		file_plan: Dictionary[String, Variant],
		instance: GDTest,
		manifest: Dictionary[String, Variant],
		discovery_errors: Array[Variant]
) -> Array[Variant]:
	var planned: Array[Variant] = []
	var dynamic_cases: Array[Variant] = manifest.get("cases", [])
	for dynamic_case: Variant in dynamic_cases:
		if dynamic_case is Dictionary:
			planned.append(_string_dictionary(dynamic_case))
	var provided: Variant = file_plan.get("cases", [])
	if provided is Array and not provided.is_empty():
		for value: Variant in provided:
			if value is Dictionary:
				_expand_method_case(planned, _string_dictionary(value), instance, discovery_errors)
		return planned
	var method_list: Array[Dictionary] = instance.get_script().get_script_method_list()
	for method_value: Variant in method_list:
		var method: Dictionary[String, Variant] = _string_dictionary(method_value)
		var method_name: String = str(method.get("name", ""))
		if not method_name.begins_with("test_") or method_name.ends_with("__cases"):
			continue
		var logical_name: String = method_name.trim_suffix("__only")
		_expand_method_case(
				planned,
				{
					"id": file_plan.get("file", file_plan.get("path", "")) + "::" + logical_name,
					"name": logical_name.trim_prefix("test_").capitalize(),
					"method_name": method_name,
					"parameters": [],
					"line": -1,
					"is_only": method_name.ends_with("__only"),
				},
				instance,
				discovery_errors
		)
	return planned


func _expand_method_case(
		planned: Array[Variant],
		base_case: Dictionary[String, Variant],
		instance: GDTest,
		discovery_errors: Array[Variant]
) -> void:
	var method_name: String = str(base_case.get("method_name", base_case.get("method", "")))
	var logical_name: String = method_name.trim_suffix("__only")
	var provider_name: StringName = StringName(logical_name + "__cases")
	var method: Dictionary[String, Variant] = _method_metadata(instance, StringName(method_name))
	if not instance.has_method(provider_name):
		if _required_argument_count(method) > 0:
			discovery_errors.append(
					{
						"message": "%s has required arguments but no %s provider" % [
							method_name,
							provider_name,
						],
					}
			)
			return
		base_case["method_name"] = method_name
		base_case["method"] = method_name
		base_case["parameters"] = []
		planned.append(base_case)
		return
	var provider: Dictionary[String, Variant] = _method_metadata(instance, provider_name)
	if _argument_count(provider) != 0:
		discovery_errors.append({ "message": str(provider_name) + " must take no arguments" })
		return
	var rows_value: Variant = instance.callv(provider_name, [])
	if rows_value is Dictionary:
		var rows: Dictionary[Variant, Variant] = rows_value
		var names: Array[String] = []
		var named_rows: Dictionary[String, Variant] = {}
		for key: Variant in rows:
			var parameter_name: String = str(key)
			if parameter_name.is_empty():
				discovery_errors.append(
						{ "message": str(provider_name) + " has an empty row name" }
				)
				continue
			if named_rows.has(parameter_name):
				discovery_errors.append(
						{ "message": str(provider_name) + " has duplicate row " + parameter_name }
				)
				continue
			named_rows[parameter_name] = rows[key]
			names.append(parameter_name)
		names.sort_custom(_utf8_less)
		for parameter_name: String in names:
			_append_parameter_row(
					planned,
					base_case,
					method,
					named_rows[parameter_name],
					parameter_name,
					_encode_component(parameter_name),
					discovery_errors
			)
		return
	if rows_value is Array:
		var rows: Array[Variant] = rows_value
		for index: int in range(rows.size()):
			_append_parameter_row(
					planned,
					base_case,
					method,
					rows[index],
					str(index),
					str(index),
					discovery_errors,
					index + 1
			)
		return
	discovery_errors.append(
			{ "message": str(provider_name) + " must return a Dictionary or Array" }
	)


func _append_parameter_row(
		planned: Array[Variant],
		base_case: Dictionary[String, Variant],
		method: Dictionary[String, Variant],
		row_value: Variant,
		parameter_name: String,
		id_component: String,
		discovery_errors: Array[Variant],
		ordinal: int = 0
) -> void:
	var method_name: String = str(base_case.get("method_name", base_case.get("method", "")))
	var provider_name: String = method_name.trim_suffix("__only") + "__cases"
	if not row_value is Array:
		discovery_errors.append(
				{ "message": "%s row %s must be an Array" % [provider_name, parameter_name] }
		)
		return
	var row: Array[Variant] = row_value
	var arguments: Array[Variant] = method.get("args", [])
	var required: int = _required_argument_count(method)
	if row.size() < required or row.size() > arguments.size():
		discovery_errors.append(
				{
					"message": "%s row %s has %d arguments; expected %d to %d" % [
						provider_name,
						parameter_name,
						row.size(),
						required,
						arguments.size(),
					],
				}
		)
		return
	for index: int in range(row.size()):
		if not _argument_matches(row[index], _string_dictionary(arguments[index])):
			discovery_errors.append(
					{
						"message": "%s row %s argument %d has incompatible type" % [
							provider_name,
							parameter_name,
							index + 1,
						],
					}
			)
			return
	var row_case: Dictionary[String, Variant] = base_case.duplicate(true)
	row_case["id"] = str(base_case.get("id", "")) + "[" + id_component + "]"
	row_case["method"] = method_name
	row_case["method_name"] = method_name
	row_case["parameters"] = row.duplicate(true)
	row_case["parameter_name"] = parameter_name
	if ordinal > 0:
		row_case["name"] = "%s %d: %s" % [
			str(base_case.get("name", method_name)),
			ordinal,
			str(row),
		]
	else:
		row_case["name"] = "%s [%s]" % [str(base_case.get("name", method_name)), parameter_name]
	planned.append(row_case)


func _suite_name(
		instance: GDTest,
		script: Script,
		path: String,
		discovery_errors: Array[Variant]
) -> String:
	if instance.has_method("get_suite_name"):
		var metadata: Dictionary[String, Variant] = _method_metadata(instance, &"get_suite_name")
		if _argument_count(metadata) != 0:
			discovery_errors.append({ "message": "get_suite_name must take no arguments" })
			return path.trim_prefix("res://")
		var value: Variant = instance.call(&"get_suite_name")
		if not value is String or value.is_empty():
			discovery_errors.append(
					{ "message": "get_suite_name must synchronously return a non-empty String" }
			)
			return path.trim_prefix("res://")
		return value
	var global_name: StringName = script.get_global_name()
	if not global_name.is_empty():
		return str(global_name)
	return path.trim_prefix("res://")


func _method_metadata(instance: Object, method_name: StringName) -> Dictionary[String, Variant]:
	for method_value: Variant in instance.get_method_list():
		if method_value is Dictionary and str(method_value.get("name", "")) == method_name:
			return _string_dictionary(method_value)
	return {}


func _argument_count(method: Dictionary[String, Variant]) -> int:
	var arguments: Variant = method.get("args", [])
	return arguments.size() if arguments is Array else 0


func _required_argument_count(method: Dictionary[String, Variant]) -> int:
	var defaults: Variant = method.get("default_args", [])
	return _argument_count(method) - (defaults.size() if defaults is Array else 0)


func _argument_matches(value: Variant, argument: Dictionary[String, Variant]) -> bool:
	var expected_type: int = int(argument.get("type", TYPE_NIL))
	var usage: int = int(argument.get("usage", 0))
	if expected_type == TYPE_NIL or usage & PROPERTY_USAGE_NIL_IS_VARIANT != 0:
		return true
	if value == null and expected_type == TYPE_OBJECT:
		return true
	return typeof(value) == expected_type


func _utf8_less(left: String, right: String) -> bool:
	var left_bytes: PackedByteArray = left.to_utf8_buffer()
	var right_bytes: PackedByteArray = right.to_utf8_buffer()
	var shared_size: int = mini(left_bytes.size(), right_bytes.size())
	for index: int in range(shared_size):
		if left_bytes[index] != right_bytes[index]:
			return left_bytes[index] < right_bytes[index]
	return left_bytes.size() < right_bytes.size()


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


func _suite_infos(value: Variant) -> Array[GDTestSuiteInfo]:
	var result: Array[GDTestSuiteInfo] = []
	if not value is Array:
		return result
	for suite_value: Variant in value:
		if suite_value is GDTestSuiteInfo:
			result.append(suite_value)
	return result


func _common_suite_count(left: Array[GDTestSuiteInfo], right: Array[GDTestSuiteInfo]) -> int:
	var result: int = 0
	while result < left.size() and result < right.size() and left[result].id == right[result].id:
		result += 1
	return result


func _run_case(instance: GDTest, path: String, case_plan: Dictionary[String, Variant]) -> String:
	var case_id: String = str(case_plan.get("id", ""))
	var case_name: String = str(case_plan.get("name", case_id))
	var method_name: StringName = StringName(
			str(case_plan.get("method", case_plan.get("method_name", "")))
	)
	var parameters: Array[Variant] = []
	var parameter_value: Variant = case_plan.get("parameters", [])
	if parameter_value is Array:
		parameters.assign(parameter_value)
	var info := GDTestCaseInfo.new(
			case_id,
			case_name,
			path,
			int(case_plan.get("line", -1)),
			method_name,
			parameters,
			str(case_plan.get("parameter_name", "")),
			bool(case_plan.get("is_only", false))
	)
	_active_case_id = case_id
	_send_event(
			"case_started",
			{ "test_id": case_id, "name": case_name, "file": path, "line": info.line }
	)
	instance._gdtest_begin_case()
	var started_usec: int = Time.get_ticks_usec()
	if instance.has_method("before_case"):
		await _call(instance, &"before_case", [info])
	else:
		await _call_optional(instance, &"before_each", [info])
	if not _cancel_requested:
		var callback: Variant = case_plan.get("callback")
		if callback is Callable:
			await _call_callable(callback, parameters)
		elif not method_name.is_empty() and instance.has_method(method_name):
			await _call(instance, method_name, parameters)
		else:
			instance._gdtest_record_error("case callback is unavailable")
	if instance.has_method("after_case"):
		await _call(instance, &"after_case", [info])
	else:
		await _call_optional(instance, &"after_each", [info])
	var result: Dictionary[String, Variant] = instance._gdtest_end_case()
	var status: String = "canceled" if _cancel_requested else str(result.get("status", "error"))
	_send_event(
			"case_finished",
			{
				"test_id": case_id,
				"name": case_name,
				"file": path,
				"line": info.line,
				"status": status,
				"assertions": int(result.get("assertions", 0)),
				"messages": result.get("messages", []),
				"outcomes": result.get("outcomes", []),
				"duration_usec": Time.get_ticks_usec() - started_usec,
			}
	)
	_active_case_id = ""
	return status


func _call_optional(instance: Object, method: StringName, arguments: Array[Variant]) -> void:
	if instance.has_method(method):
		await _call(instance, method, arguments)


func _call(instance: Object, method: StringName, arguments: Array[Variant]) -> void:
	await instance.callv(method, arguments)


func _call_callable(callback: Callable, arguments: Array[Variant]) -> void:
	await callback.callv(arguments)


func _on_assertion_failed(diagnostic: Dictionary[String, Variant]) -> void:
	var payload: Dictionary[String, Variant] = {
		"test_id": _active_case_id,
		"assertion": diagnostic.get("assertion", ""),
		"message": diagnostic.get("message", ""),
		"location": diagnostic.get("location", {}),
		"actual": Protocol.wire_value(diagnostic.get("actual")),
	}
	if bool(diagnostic.get("has_expected", false)):
		payload["expected"] = Protocol.wire_value(diagnostic.get("expected"))
	_send_event("assertion_failed", payload)


func _reload_command(command: Dictionary[String, Variant]) -> void:
	if _running:
		_send_worker_error("worker.busy", "cannot reload during an active run")
		return
	var payload: Dictionary[String, Variant] = _string_dictionary(command.get("payload", {}))
	var requested_generation: int = int(payload.get("generation", -1))
	if requested_generation < _generation:
		_send_worker_error("worker.stale_generation", "reload generation moved backwards")
		return
	_generation = requested_generation
	_send(
			{
				"version": Version.WORKER_PROTOCOL_VERSION,
				"type": "reload_finished",
				"request_id": str(command.get("request_id", "")),
				"generation": _generation,
			}
	)


func _reset_command(command: Dictionary[String, Variant]) -> void:
	if _running:
		_send_worker_error("worker.busy", "cannot reset during an active run")
		return
	_active_test = null
	_active_case_id = ""
	_send(
			{
				"version": Version.WORKER_PROTOCOL_VERSION,
				"type": "reset_finished",
				"request_id": str(command.get("request_id", "")),
				"generation": _generation,
			}
	)


func _send_event(event_type: String, data: Dictionary[String, Variant]) -> void:
	_sequence += 1
	var event: Dictionary[String, Variant] = {
		"version": Version.WORKER_PROTOCOL_VERSION,
		"type": "event",
		"request_id": _request_id,
		"payload": {
			"kind": event_type,
			"worker_id": _worker_id,
			"session_id": _session_id,
			"generation": _generation,
			"run_id": _run_id,
			"sequence": _sequence,
			"data": data,
		},
	}
	_send(event)


func _send_worker_error(code: String, text: String) -> void:
	_send_event(
			"worker_error",
			{
				"name": code,
				"message": text,
				"test_id": _active_case_id,
				"metadata": { "code": code },
			}
	)


func _send_discovery_error(value: Variant, default_file: String) -> void:
	var diagnostic: Dictionary[String, Variant] = Protocol.discovery_error(value, default_file)
	var location: Dictionary[String, Variant] = _string_dictionary(diagnostic["location"])
	_send_event(
			"worker_error",
			{
				"name": str(diagnostic["code"]),
				"message": str(diagnostic["message"]),
				"test_id": _active_case_id,
				"location": location,
				"metadata": { "code": diagnostic["code"] },
			}
	)


func _send(message: Dictionary[String, Variant]) -> void:
	if _peer == null or _peer.get_status() != StreamPeerTCP.STATUS_CONNECTED:
		return
	var bytes: PackedByteArray = Protocol.encode_line(message)
	if bytes.size() > MAX_MESSAGE_BYTES:
		# This cannot be sent through the protocol because the protocol limit rejected it.
		# gdkit:ignore = no-engine-logging
		printerr("gdtest worker: outgoing message exceeds limit")
		return
	var send_error: Error = _peer.put_data(bytes)
	if send_error != OK:
		# The protocol connection itself failed, so stderr is the remaining channel.
		# gdkit:ignore = no-engine-logging
		printerr("gdtest worker: send failed: " + error_string(send_error))


func _aggregate_status(totals: Dictionary[String, int]) -> String:
	if totals.get("error", 0) > 0:
		return "error"
	if totals.get("fail", 0) > 0:
		return "fail"
	if totals.get("canceled", 0) > 0:
		return "canceled"
	return "pass"


func _finish_run_state() -> void:
	_running = false
	_cancel_requested = false
	_run_id = ""
	_request_id = ""
	_active_case_id = ""
	_active_test = null


func _string_dictionary(value: Variant) -> Dictionary[String, Variant]:
	var result: Dictionary[String, Variant] = {}
	if not value is Dictionary:
		return result
	for key: Variant in value:
		result[str(key)] = value[key]
	return result
