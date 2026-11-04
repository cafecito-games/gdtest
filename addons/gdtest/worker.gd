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
					"capabilities": ["run", "ping", "cancel", "reload", "reset", "shutdown"],
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
	for file_value: Variant in files_value:
		if _cancel_requested:
			break
		if not file_value is Dictionary:
			_send_worker_error("protocol.invalid_plan", "each plan file must be an object")
			continue
		var file_totals: Dictionary[String, int] = await _run_file(_string_dictionary(file_value))
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


func _run_file(file_plan: Dictionary[String, Variant]) -> Dictionary[String, int]:
	var totals: Dictionary[String, int] = {
		"pass": 0,
		"fail": 0,
		"error": 0,
		"skip": 0,
		"todo": 0,
		"void": 0,
		"canceled": 0,
	}
	var path: String = str(file_plan.get("file", file_plan.get("path", "")))
	if not path.begins_with("res://"):
		path = "res://" + path.trim_prefix("/")
	_send_event("suite_started", { "id": path.trim_prefix("res://"), "name": path, "file": path })
	var resource: Resource = ResourceLoader.load(path, "Script", ResourceLoader.CACHE_MODE_REPLACE)
	if not resource is Script:
		_send_worker_error("worker.script_load", "unable to load test script " + path)
		totals["error"] += 1
		_send_event("suite_finished", { "id": path.trim_prefix("res://"), "status": "error" })
		return totals
	var instance_value: Variant = resource.new()
	if not instance_value is GDTest:
		_send_worker_error("worker.invalid_test", path + " does not extend GDTest")
		totals["error"] += 1
		_send_event("suite_finished", { "id": path.trim_prefix("res://"), "status": "error" })
		return totals
	var instance: GDTest = instance_value
	_active_test = instance
	instance._gdtest_configure(self, path, _on_assertion_failed)
	instance._gdtest_begin_registration()
	if instance.has_method("suite"):
		await _call(instance, &"suite", [])
	await instance._gdtest_finish_registration()
	var manifest: Dictionary[String, Variant] = instance._gdtest_manifest()
	var discovery_errors: Array[Variant] = manifest.get("errors", [])
	for discovery_error: Variant in discovery_errors:
		_send_worker_error("worker.discovery", str(discovery_error))
	if not discovery_errors.is_empty():
		totals["error"] += 1
	var cases: Array[Variant] = _planned_cases(file_plan, instance, manifest)
	var root_info := GDTestSuiteInfo.new(
			path.trim_prefix("res://"),
			str(file_plan.get("name", path.trim_prefix("res://"))),
			path,
			int(file_plan.get("line", 1)),
			false
	)
	if not cases.is_empty():
		await _call_optional(instance, &"before_all", [])
		await _call_optional(instance, &"before_suite", [root_info])
	for case_value: Variant in cases:
		if _cancel_requested:
			break
		var case_plan: Dictionary[String, Variant] = _string_dictionary(case_value)
		var status: String = await _run_case(instance, path, case_plan)
		totals[status] = totals.get(status, 0) + 1
	if not cases.is_empty():
		await _call_optional(instance, &"after_suite", [root_info])
		await _call_optional(instance, &"after_all", [])
	_send_event(
			"suite_finished",
			{
				"id": path.trim_prefix("res://"),
				"status": _aggregate_status(totals),
				"counts": totals,
			}
	)
	_active_test = null
	return totals


func _planned_cases(
		file_plan: Dictionary[String, Variant],
		instance: GDTest,
		manifest: Dictionary[String, Variant]
) -> Array[Variant]:
	var planned: Array[Variant] = []
	var provided: Variant = file_plan.get("cases", [])
	if provided is Array and not provided.is_empty():
		for value: Variant in provided:
			if value is Dictionary:
				planned.append(_string_dictionary(value))
		return planned
	var dynamic_cases: Array[Variant] = manifest.get("cases", [])
	for dynamic_case: Variant in dynamic_cases:
		if dynamic_case is Dictionary:
			planned.append(_string_dictionary(dynamic_case))
	var method_list: Array[Variant] = instance.get_method_list()
	method_list.sort_custom(
			func(left: Variant, right: Variant) -> bool:
				return str(left.get("name", "")) < str(right.get("name", ""))
	)
	for method_value: Variant in method_list:
		var method: Dictionary[String, Variant] = _string_dictionary(method_value)
		var method_name: String = str(method.get("name", ""))
		if not method_name.begins_with("test_") or method_name.ends_with("__cases"):
			continue
		var logical_name: String = method_name.trim_suffix("__only")
		planned.append(
				{
					"id": file_plan.get("file", file_plan.get("path", "")) + "::" + logical_name,
					"name": logical_name.trim_prefix("test_").capitalize(),
					"method": method_name,
					"parameters": [],
					"line": -1,
					"is_only": method_name.ends_with("__only"),
				}
		)
	return planned


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
	if instance.has_method("before_case") and instance.has_method("before_each"):
		instance._gdtest_record_error("before_case and before_each cannot both be defined")
	elif instance.has_method("before_case"):
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
	if instance.has_method("after_case") and instance.has_method("after_each"):
		instance._gdtest_record_error("after_case and after_each cannot both be defined")
	elif instance.has_method("after_case"):
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
	_send_event("worker_error", { "code": code, "message": text, "case_id": _active_case_id })


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
