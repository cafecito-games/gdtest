extends SceneTree
## Internal protocol codec script.
const Protocol: GDScript = preload("res://addons/gdtest/protocol.gd")
## Internal runtime version declaration.
const Version: GDScript = preload("res://addons/gdtest/version.gd")
## Internal asynchronous case task.
const CaseTask: GDScript = preload("res://addons/gdtest/worker_case_task.gd")
## Internal benchmark command runner.
const BenchmarkRunner: GDScript = preload("res://addons/gdtest/worker_benchmark_runner.gd")
## Internal scoped node leak tracker.
const LeakTracker: GDScript = preload("res://addons/gdtest/worker_leak_tracker.gd")
## Internal shared probe ledger tracking bounded late callback diagnostics.
const ProbeLedger: GDScript = preload("res://addons/gdtest/probe_ledger.gd")
## Hard upper bound for one JSONL protocol message.
const MAX_MESSAGE_BYTES: int = 16777216
## Default external deadline for one case including its hooks.
const DEFAULT_CASE_TIMEOUT_USEC: int = 30000000
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
var _serialization_usec: int = 0
var _case_timeout_usec: int = DEFAULT_CASE_TIMEOUT_USEC
var _abort_run_requested: bool = false
var _leak_tracker: RefCounted
var _initial_audio_bus_layout: AudioBusLayout = AudioServer.generate_bus_layout()


func _initialize() -> void:
	call_deferred("_start")


func _start() -> void:
	var argument_error: String = _parse_arguments(OS.get_cmdline_user_args())
	if not argument_error.is_empty():
		# gdkit:ignore = no-engine-logging
		printerr("gdtest worker: " + argument_error)
		quit(2)
		return
	_peer = StreamPeerTCP.new()
	var connect_error: Error = _peer.connect_to_host(_host, _port)
	if connect_error != OK:
		# gdkit:ignore = no-engine-logging
		printerr("gdtest worker: connect failed: " + error_string(connect_error))
		quit(2)
		return
	while _peer.get_status() == StreamPeerTCP.STATUS_CONNECTING:
		_peer.poll()
		await process_frame
	if _peer.get_status() != StreamPeerTCP.STATUS_CONNECTED:
		# gdkit:ignore = no-engine-logging
		printerr("gdtest worker: daemon listener unavailable")
		quit(2)
		return
	_send(
			Protocol.hello_envelope(
					_token,
					_worker_id,
					_session_id,
					_generation,
					ProjectSettings.globalize_path("res://").trim_suffix("/")
			)
	)
	_send_event("worker_ready", {})
	while not _shutdown_requested or _running:
		_poll_messages()
		await process_frame
	_peer.disconnect_from_host()
	quit(0)


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
			"--gdtest-user-dir-name":
				ProjectSettings.set_setting("application/config/use_custom_user_dir", true)
				ProjectSettings.set_setting("application/config/custom_user_dir_name", value)
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
	var payload: Dictionary[String, Variant] = _dict(message.get("payload", {}))
	match message_type:
		"ping":
			_send(
					Protocol.pong_envelope(
							str(message.get("request_id", "")),
							_worker_id,
							_generation
					)
			)
		"run", "bench":
			if _running:
				_send_worker_error("worker.busy", "worker already has an active operation")
			elif message_type == "run":
				_run_command(message)
			else:
				BenchmarkRunner.new().start(self, message)
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
	_abort_run_requested = false
	_sequence = 0
	var payload: Dictionary[String, Variant] = _dict(command.get("payload", {}))
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
	_serialization_usec = 0
	var totals: Dictionary[String, int] = Protocol.empty_totals()
	var plan_value: Variant = payload.get("plan", {})
	if not plan_value is Dictionary:
		_send_worker_error("protocol.invalid_plan", "run plan must be an object")
		_finish_run_state()
		return
	var plan: Dictionary[String, Variant] = _dict(plan_value)
	_leak_tracker = LeakTracker.create(
			self,
			plan,
			_send_event,
			_is_run_aborted,
			_call_hook,
			_drain_framework_errors
	)
	var case_timeout_ns: int = int(plan.get("case_timeout_ns", 0))
	if case_timeout_ns <= 0:
		_case_timeout_usec = DEFAULT_CASE_TIMEOUT_USEC
	else:
		_case_timeout_usec = maxi(1, case_timeout_ns / 1000)
	var files_value: Variant = plan.get("files", [])
	if not files_value is Array:
		_send_worker_error("protocol.invalid_plan", "plan.files must be an array")
		_finish_run_state()
		return
	var prepared_files: Array[Dictionary] = []
	var preparation_started_usec: int = Time.get_ticks_usec()
	for file_value: Variant in files_value:
		if _cancel_requested:
			break
		if not file_value is Dictionary:
			_send_worker_error("protocol.invalid_plan", "each plan file must be an object")
			continue
		var prepared: Dictionary[String, Variant] = await _prepare_file(_dict(file_value))
		prepared_files.append(prepared)
	var preparation_usec: int = Time.get_ticks_usec() - preparation_started_usec
	_apply_case_filters(prepared_files, plan)
	var has_focus: bool = _plan_has_focus(prepared_files, plan)
	var execution_started_usec: int = Time.get_ticks_usec()
	var only_mode: String = str(plan.get("only_mode", "auto"))
	var policy_error: String = Protocol.only_policy_error(only_mode, has_focus)
	if not policy_error.is_empty():
		_send_discovery_error({ "message": policy_error }, "")
		totals["error"] += 1
	var focus_only: bool = has_focus and only_mode != "ignore"
	for prepared_index: int in range(prepared_files.size()):
		var prepared: Dictionary[String, Variant] = _dict(prepared_files[prepared_index])
		if _cancel_requested or not policy_error.is_empty():
			_release_skipped_prepared_files(prepared_files, prepared_index, totals)
			break
		var file_totals: Dictionary[String, int] = await _run_prepared_file(prepared, focus_only)
		for status: String in totals:
			totals[status] += file_totals.get(status, 0)
		if _abort_run_requested:
			_release_skipped_prepared_files(prepared_files, prepared_index + 1, totals)
			break
	if _cancel_requested:
		totals["canceled"] += 1
		_taint_canceled_run()
	var execution_usec: int = Time.get_ticks_usec() - execution_started_usec
	var late_callback_invocations: int = ProbeLedger.take_late_call_count()
	_send_event(
			"run_finished",
			Protocol.run_finished(
					"canceled" if _cancel_requested else Protocol.aggregate_status(totals),
					totals,
					Time.get_ticks_usec() - started_usec,
					preparation_usec,
					execution_usec,
					_serialization_usec,
					late_callback_invocations
			)
	)
	_finish_run_state()


func _discover_command(command: Dictionary[String, Variant]) -> void:
	_running = true
	_cancel_requested = false
	_sequence = 0
	_request_id = str(command.get("request_id", ""))
	var payload: Dictionary[String, Variant] = _dict(command.get("payload", {}))
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
	var plan: Dictionary[String, Variant] = _dict(plan_value)
	var files_value: Variant = plan.get("files", [])
	if not files_value is Array:
		_send_worker_error("protocol.invalid_plan", "plan.files must be an array")
		_finish_run_state()
		return
	var prepared_files: Array[Dictionary] = []
	for file_value: Variant in files_value:
		if not file_value is Dictionary:
			prepared_files.append(
					_empty_prepared_file([{ "message": "each plan file must be an object" }])
			)
			continue
		prepared_files.append(await _prepare_file(_dict(file_value), false))
	var manifest: Dictionary[String, Variant] = Protocol.discovery_manifest(
			prepared_files,
			_generation,
			str(plan.get("content_fingerprint", ""))
	)
	_send(Protocol.response_envelope("discover_finished", _request_id, manifest))
	_finish_run_state()


func _empty_prepared_file(errors: Array[Variant] = []) -> Dictionary[String, Variant]:
	return {
		"path": "",
		"instance": null,
		"cases": [],
		"root_info": null,
		"errors": errors,
		"discovery_error_count": errors.size(),
	}


func _prepare_file(
		file_plan: Dictionary[String, Variant],
		emit_errors: bool = true
) -> Dictionary[String, Variant]:
	var prepared: Dictionary[String, Variant] = _empty_prepared_file()
	var path: String = str(file_plan.get("file", file_plan.get("path", "")))
	if not path.begins_with("res://"):
		path = "res://" + path.trim_prefix("/")
	prepared["path"] = path
	var resource: Resource = ResourceLoader.load(path, "Script", ResourceLoader.CACHE_MODE_REPLACE)
	var script: Script = resource as Script
	if script == null or not script.can_instantiate():
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
	var case_ids_value: Variant = file_plan.get("case_ids", null)
	if case_ids_value is Array:
		prepared["case_ids"] = case_ids_value
	var instance_value: Variant = script.new()
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
		await instance.callv(&"suite", [])
	await instance._gdtest_finish_registration()
	var manifest: Dictionary[String, Variant] = instance._gdtest_manifest()
	var discovery_errors: Array[Variant] = instance._gdtest_drain_discovery_errors()
	var cases: Array[Variant] = _planned_cases(file_plan, instance, manifest, discovery_errors)
	if instance.has_method("before_case") and instance.has_method("before_each"):
		discovery_errors.append({ "message": "before_case and before_each cannot both be defined" })
	if instance.has_method("after_case") and instance.has_method("after_each"):
		discovery_errors.append({ "message": "after_case and after_each cannot both be defined" })
	var suite_name: String = await _suite_name(instance, script, path, discovery_errors)
	discovery_errors.append_array(instance._gdtest_drain_discovery_errors())
	discovery_errors = Protocol.discovery_errors(discovery_errors, path)
	if emit_errors:
		for normalized: Variant in discovery_errors:
			_send_discovery_error(normalized, path)
		prepared["instance"] = instance
	else:
		instance._gdtest_dispose()
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


func _apply_case_filters(
		prepared_files: Array[Dictionary],
		plan: Dictionary[String, Variant]
) -> void:
	var selection_applied: bool = bool(plan.get("selection_applied", false))
	var selected: Dictionary[String, bool] = {}
	if selection_applied:
		var selected_value: Variant = plan.get("selected_ids", [])
		if selected_value is Array:
			for id_value: Variant in selected_value:
				selected[str(id_value)] = true
	for prepared_index: int in range(prepared_files.size()):
		var prepared: Dictionary[String, Variant] = prepared_files[prepared_index]
		var case_ids_value: Variant = prepared.get("case_ids", null)
		var has_case_ids: bool = case_ids_value is Array
		if not selection_applied and not has_case_ids:
			continue
		var allowed: Dictionary[String, bool] = {}
		if has_case_ids:
			for id_value: Variant in case_ids_value:
				allowed[str(id_value)] = true
		var filtered: Array[Variant] = []
		for case_value: Variant in prepared.get("cases", []):
			if not case_value is Dictionary:
				continue
			var case_id: String = str(case_value.get("id", ""))
			if (
					(not has_case_ids or allowed.has(case_id))
					and (not selection_applied or selected.has(case_id))
			):
				filtered.append(case_value)
		prepared["cases"] = filtered
		prepared_files[prepared_index] = prepared


func _plan_has_focus(prepared_files: Array[Dictionary], plan: Dictionary[String, Variant]) -> bool:
	var stamped_focus: Variant = plan.get("has_focus", null)
	if stamped_focus is bool:
		return stamped_focus
	for prepared_value: Variant in prepared_files:
		var prepared: Dictionary[String, Variant] = _dict(prepared_value)
		for case_value: Variant in prepared.get("cases", []):
			if case_value is Dictionary and bool(case_value.get("is_only", false)):
				return true
	return false


func _run_prepared_file(
		prepared: Dictionary[String, Variant],
		focus_only: bool
) -> Dictionary[String, int]:
	var totals: Dictionary[String, int] = Protocol.empty_totals()
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
	_send_event(
			"suite_started",
			{ "id": root_info.id, "name": root_info.name, "file": path.trim_prefix("res://") }
	)
	if not cases.is_empty():
		await _leak_tracker.execute_file_lifecycle(
				instance,
				root_info,
				cases,
				path,
				totals,
				_run_case
		)
	totals["error"] += _drain_framework_errors(instance, path)
	instance._gdtest_dispose()
	_send_event(
			"suite_finished",
			{ "id": root_info.id, "status": Protocol.aggregate_status(totals), "counts": totals }
	)
	_active_test = null
	return totals


func _release_skipped_prepared_files(
		prepared_files: Array[Dictionary],
		start_index: int,
		totals: Dictionary[String, int]
) -> void:
	for index: int in range(start_index, prepared_files.size()):
		var prepared: Dictionary[String, Variant] = _dict(prepared_files[index])
		totals["error"] += int(prepared.get("discovery_error_count", 0))
		var instance_value: Variant = prepared.get("instance")
		if instance_value is GDTest:
			var instance: GDTest = instance_value
			instance._gdtest_dispose()


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
			planned.append(_dict(dynamic_case))
	var provided: Variant = file_plan.get("cases", [])
	if provided is Array:
		for value: Variant in provided:
			if value is Dictionary:
				_expand_method_case(planned, _dict(value), instance, discovery_errors)
		return planned
	var method_list: Array[Dictionary] = instance.get_script().get_script_method_list()
	for method_value: Variant in method_list:
		var method: Dictionary[String, Variant] = _dict(method_value)
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
		names.sort_custom(Protocol.utf8_less)
		for parameter_name: String in names:
			_append_parameter_row(
					planned,
					base_case,
					method,
					named_rows[parameter_name],
					parameter_name,
					Protocol.encode_component(parameter_name),
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
		if not _argument_matches(row[index], _dict(arguments[index])):
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
			return _dict(method_value)
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
	_send_event("case_started", Protocol.case_started(case_id, case_name, path, info.line))
	instance._gdtest_begin_case()
	_leak_tracker.push_scope(root)
	var started_usec: int = Time.get_ticks_usec()
	var task: RefCounted = CaseTask.new()
	task.start(instance, info, case_plan, method_name, parameters)
	var deadline_usec: int = started_usec + _case_timeout_usec
	while not task.finished and not _cancel_requested and Time.get_ticks_usec() < deadline_usec:
		_poll_messages()
		await process_frame
	var case_canceled: bool = _cancel_requested
	var timed_out: bool = not task.finished and not case_canceled
	if timed_out:
		instance._gdtest_record_error_at(
				"case timed out after %.3f seconds" % (float(_case_timeout_usec) / 1000000.0),
				path,
				info.line
		)
	var result: Dictionary[String, Variant]
	if not task.finished or case_canceled:
		task.finish_capture()
		_abort_run_requested = true
		_shutdown_requested = true
		_send_event("worker_tainted", Protocol.worker_tainted(case_id, timed_out))
		result = instance._gdtest_end_case_aborted()
		_leak_tracker.pop_without_check()
	else:
		result = await instance._gdtest_end_case(
				_leak_tracker.check_case_scope.bind(instance, case_id, path, info.line)
		)
		task.finish_capture()
	var status: String = "canceled" if case_canceled else str(result.get("status", "error"))
	_send_event(
			"case_finished",
			Protocol.case_finished(
					case_id,
					case_name,
					path,
					info.line,
					status,
					result,
					Time.get_ticks_usec() - started_usec
			)
	)
	_active_case_id = ""
	return status


func _taint_canceled_run() -> void:
	if _shutdown_requested:
		return
	_shutdown_requested = true
	_send_event("worker_tainted", Protocol.worker_tainted("", false))


func _is_run_aborted() -> bool:
	return _cancel_requested or _abort_run_requested


func _call_hook(
		instance: GDTest,
		method: StringName,
		arguments: Array[Variant],
		path: String,
		totals: Dictionary[String, int]
) -> bool:
	if instance.has_method(method):
		await instance.callv(method, arguments)
	var error_count: int = _drain_framework_errors(instance, path)
	totals["error"] += error_count
	return error_count == 0


func _drain_framework_errors(instance: GDTest, default_file: String) -> int:
	var drained: Array[Variant] = Protocol.framework_errors(
			instance._gdtest_drain_discovery_errors(),
			default_file
	)
	for diagnostic: Variant in drained:
		_send_event(
				"worker_error",
				Protocol.diagnostic_worker_error(
						diagnostic,
						default_file,
						"worker.framework",
						"framework",
						_active_case_id
				)
		)
	return drained.size()


func _on_assertion_failed(diagnostic: Dictionary[String, Variant]) -> void:
	var serialization_started_usec: int = Time.get_ticks_usec()
	var payload: Dictionary[String, Variant] = Protocol.assertion_failed(
			_active_case_id,
			diagnostic
	)
	_serialization_usec += Time.get_ticks_usec() - serialization_started_usec
	_send_event("assertion_failed", payload)


func _reload_command(command: Dictionary[String, Variant]) -> void:
	if _running:
		_send_worker_error("worker.busy", "cannot reload during an active run")
		return
	var payload: Dictionary[String, Variant] = _dict(command.get("payload", {}))
	var requested_generation: int = int(payload.get("generation", -1))
	if requested_generation < _generation:
		_send_worker_error("worker.stale_generation", "reload generation moved backwards")
		return
	_generation = requested_generation
	_send(
			Protocol.generation_ack(
					"reload_finished",
					str(command.get("request_id", "")),
					_generation
			)
	)


func _reset_command(command: Dictionary[String, Variant]) -> void:
	if _running:
		_send_worker_error("worker.busy", "cannot reset during an active run")
		return
	_active_test = null
	_active_case_id = ""
	AudioServer.set_bus_layout(_initial_audio_bus_layout)
	_send(
			Protocol.generation_ack(
					"reset_finished",
					str(command.get("request_id", "")),
					_generation
			)
	)


func _send_event(event_type: String, data: Dictionary[String, Variant]) -> void:
	_sequence += 1
	_send(
			Protocol.event_envelope(
					_request_id,
					event_type,
					_worker_id,
					_session_id,
					_generation,
					_run_id,
					_sequence,
					data
			)
	)


func _send_worker_error(code: String, text: String) -> void:
	_send_event("worker_error", Protocol.worker_error(code, text, _active_case_id))


func _send_discovery_error(value: Variant, default_file: String) -> void:
	_send_event(
			"worker_error",
			Protocol.diagnostic_worker_error(
					value,
					default_file,
					"worker.discovery",
					"",
					_active_case_id
			)
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


func _finish_run_state() -> void:
	_running = false
	_cancel_requested = false
	_run_id = ""
	_request_id = ""
	_active_case_id = ""
	_active_test = null
	_leak_tracker = null


func _dict(value: Variant) -> Dictionary[String, Variant]:
	return Protocol.string_dictionary(value)
