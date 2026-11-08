## Executes one benchmark plan through its owning persistent worker.
class_name GDTestWorkerBenchmarkRunner
extends RefCounted

## Prefix shared with the Go process-output classifier.
const MARKER_PREFIX: String = "@@GDTEST_CASE_CAPTURE "
## Default external deadline for one sample including its untimed hooks.
const DEFAULT_SAMPLE_TIMEOUT_USEC: int = 30000000

var _worker: SceneTree
var _generation: int = 0
var _completed_capture: String = ""
var _self_reference: RefCounted


## Starts a benchmark command and returns immediately when user code suspends.
func start(worker: SceneTree, command: Dictionary[String, Variant]) -> void:
	_self_reference = self
	_worker = worker
	_generation = _worker.get("_generation")
	_execute(command)


func _execute(command: Dictionary[String, Variant]) -> void:
	_worker.set("_running", true)
	_worker.set("_cancel_requested", false)
	_worker.set("_abort_run_requested", false)
	_worker.set("_sequence", 0)
	var payload: Dictionary[String, Variant] = _dict(command.get("payload", {}))
	_worker.set("_run_id", str(payload.get("run_id", "")))
	_worker.set("_request_id", str(command.get("request_id", "")))
	var requested_generation: int = payload.get("generation", -1)
	if str(_worker.get("_run_id")).is_empty():
		_error("protocol.invalid_benchmark", "run_id is required", "")
		_finish()
		return
	if requested_generation != _generation:
		_error(
				"worker.stale_generation",
				"worker generation %d cannot benchmark generation %d" % [
					_generation,
					requested_generation,
				],
				""
		)
		_finish()
		return
	var plan_value: Variant = payload.get("plan", {})
	if not plan_value is Dictionary:
		_error("protocol.invalid_plan", "benchmark plan must be an object", "")
		_finish()
		return
	var plan: Dictionary[String, Variant] = _dict(plan_value)
	var warmup: int = plan.get("warmup", -1)
	var samples: int = plan.get("samples", 0)
	var iterations: int = plan.get("iterations", 0)
	var files_value: Variant = plan.get("files", [])
	if warmup < 0 or samples <= 0 or iterations <= 0 or not files_value is Array:
		_error("protocol.invalid_plan", "benchmark plan options are invalid", "")
		_finish()
		return
	var planned_benchmarks: int = 0
	for file_value: Variant in files_value:
		if file_value is Dictionary:
			var planned_value: Variant = _dict(file_value).get("benchmarks", [])
			if planned_value is Array:
				var planned_array: Array[Variant] = planned_value
				planned_benchmarks += planned_array.size()
	_send(
			"benchmark_run_started",
			{
				"generation": _generation,
				"metadata": {
					"godot_version": Engine.get_version_info(),
					"os": OS.get_name(),
					"cpu_model": OS.get_processor_name(),
					"logical_cpu_count": OS.get_processor_count(),
					"planned_benchmarks": planned_benchmarks,
				},
			}
	)
	var status: String = "pass"
	for file_value: Variant in files_value:
		if _canceled() or status != "pass":
			break
		if not file_value is Dictionary:
			_error("protocol.invalid_plan", "each benchmark file must be an object", "")
			status = "error"
			break
		status = await _run_file(_dict(file_value), warmup, samples, iterations)
	if _canceled():
		status = "canceled"
	var finished_data: Dictionary[String, Variant] = { "status": status, "generation": _generation }
	if not _completed_capture.is_empty():
		finished_data["metadata"] = { "completed_capture": _completed_capture }
		_completed_capture = ""
	_send("benchmark_run_finished", finished_data)
	_finish()


func _run_file(
		file_plan: Dictionary[String, Variant],
		warmup: int,
		samples: int,
		iterations: int
) -> String:
	var path: String = str(file_plan.get("path", ""))
	if not path.begins_with("res://"):
		path = "res://" + path.trim_prefix("/")
	var instance: GDTestBenchmark = _load_benchmark(path)
	if instance == null:
		return "error"
	instance._gdtest_configure(_worker)
	var benchmarks_value: Variant = file_plan.get("benchmarks", [])
	if not benchmarks_value is Array:
		_error("protocol.invalid_plan", "file benchmarks must be an array", "")
		return "error"
	var benchmarks: Array[Variant] = benchmarks_value
	var first_id: String = _benchmark_id_at(benchmarks, 0)
	var initial_captures: Array[String] = []
	if not _completed_capture.is_empty():
		initial_captures.append(_completed_capture)
		_completed_capture = ""
	if not benchmarks.is_empty() and instance.has_method("before_all"):
		var before_all_capture: String = first_id + "#before_all"
		await _call_hook(instance, &"before_all", [], before_all_capture)
		initial_captures.append(before_all_capture)
	for benchmark_index: int in range(benchmarks.size()):
		var benchmark_value: Variant = benchmarks[benchmark_index]
		if _canceled():
			return "canceled"
		if not benchmark_value is Dictionary:
			_error("protocol.invalid_plan", "benchmark entry must be an object", "")
			return "error"
		var captures_for_benchmark: Array[String] = []
		if benchmark_index == 0:
			captures_for_benchmark = initial_captures
		var benchmark_status: String = await _run_benchmark(
				instance,
				path,
				_dict(benchmark_value),
				warmup,
				samples,
				iterations,
				captures_for_benchmark
		)
		if benchmark_status != "pass":
			return benchmark_status
	if not benchmarks.is_empty() and instance.has_method("after_all"):
		_completed_capture = _benchmark_id_at(benchmarks, benchmarks.size() - 1) + "#after_all"
		await _call_hook(instance, &"after_all", [], _completed_capture)
	return "pass"


func _load_benchmark(path: String) -> GDTestBenchmark:
	var resource: Resource = ResourceLoader.load(path, "Script", ResourceLoader.CACHE_MODE_REPLACE)
	var script: GDScript = resource as GDScript
	if script == null or not script.can_instantiate():
		_error("worker.script_load", "unable to load benchmark script " + path, "")
		return null
	var instance_value: Variant = script.new()
	if not instance_value is GDTestBenchmark:
		_error("worker.invalid_benchmark", path + " does not extend GDTestBenchmark", "")
		return null
	return instance_value


func _run_benchmark(
		instance: GDTestBenchmark,
		path: String,
		benchmark_plan: Dictionary[String, Variant],
		warmup: int,
		samples: int,
		iterations: int,
		completed_captures: Array[String]
) -> String:
	var benchmark_id: String = str(benchmark_plan.get("id", ""))
	var benchmark_name: String = str(benchmark_plan.get("name", benchmark_id))
	var method_name := StringName(str(benchmark_plan.get("method_name", "")))
	var line: int = benchmark_plan.get("line", -1)
	if benchmark_id.is_empty() or method_name.is_empty() or not instance.has_method(method_name):
		_error("worker.invalid_benchmark", "benchmark method is unavailable", benchmark_id)
		return "error"
	_worker.set("_active_case_id", benchmark_id)
	var started_data: Dictionary[String, Variant] = {
		"benchmark_id": benchmark_id,
		"name": benchmark_name,
		"location": { "file": path, "line": line },
		"iterations": iterations,
	}
	if not completed_captures.is_empty():
		started_data["metadata"] = { "completed_captures": completed_captures }
	_send("benchmark_started", started_data)
	var lifecycle_info := GDTestBenchmarkInfo.new(
			benchmark_id,
			benchmark_name,
			path,
			line,
			method_name,
			false,
			-1,
			iterations
	)
	var before_benchmark_capture: String = ""
	if instance.has_method("before_benchmark"):
		before_benchmark_capture = benchmark_id + "#before_benchmark"
		await _call_hook(instance, &"before_benchmark", [lifecycle_info], before_benchmark_capture)
	for index: int in range(warmup):
		var warmup_status: String = await _run_sample(
				instance,
				lifecycle_info,
				true,
				index,
				before_benchmark_capture if index == 0 else ""
		)
		if warmup_status != "pass":
			return warmup_status
	for index: int in range(samples):
		var prior_capture: String = ""
		if warmup == 0 and index == 0:
			prior_capture = before_benchmark_capture
		var sample_status: String = await _run_sample(
				instance,
				lifecycle_info,
				false,
				index,
				prior_capture
		)
		if sample_status != "pass":
			return sample_status
	var after_benchmark_capture: String = ""
	if instance.has_method("after_benchmark"):
		after_benchmark_capture = benchmark_id + "#after_benchmark"
		await _call_hook(instance, &"after_benchmark", [lifecycle_info], after_benchmark_capture)
	var finished_data: Dictionary[String, Variant] = {
		"benchmark_id": benchmark_id,
		"status": "pass",
		"iterations": iterations,
	}
	if not after_benchmark_capture.is_empty():
		finished_data["metadata"] = { "completed_capture": after_benchmark_capture }
	_send("benchmark_finished", finished_data)
	_worker.set("_active_case_id", "")
	return "pass"


func _run_sample(
		instance: GDTestBenchmark,
		lifecycle_info: GDTestBenchmarkInfo,
		warmup: bool,
		index: int,
		completed_capture: String
) -> String:
	var info := GDTestBenchmarkInfo.new(
			lifecycle_info.id,
			lifecycle_info.name,
			lifecycle_info.file,
			lifecycle_info.line,
			lifecycle_info.method_name,
			warmup,
			index,
			lifecycle_info.iterations
	)
	var started_data: Dictionary[String, Variant] = {
		"benchmark_id": info.id,
		"sample_index": index,
		"warmup": warmup,
		"iterations": info.iterations,
	}
	if not completed_capture.is_empty():
		started_data["metadata"] = { "completed_capture": completed_capture }
	_send("benchmark_sample_started", started_data)
	var task: GDTestWorkerBenchmarkTask = GDTestWorkerBenchmarkTask.new()
	var capture_id: String = "%s#%s:%d" % [info.id, "warmup" if warmup else "sample", index]
	task.start(instance, info, capture_id)
	var deadline_usec: int = Time.get_ticks_usec() + DEFAULT_SAMPLE_TIMEOUT_USEC
	while not task.finished and not _canceled() and Time.get_ticks_usec() < deadline_usec:
		_worker.call("_poll_messages")
		await _worker.process_frame
	var timed_out: bool = not task.finished and not _canceled()
	if not task.finished:
		task.finish_capture()
		_worker.set("_abort_run_requested", true)
		_worker.set("_shutdown_requested", true)
		_send(
				"benchmark_finished",
				{
					"benchmark_id": info.id,
					"status": "error" if timed_out else "canceled",
					"message": (
							"benchmark sample timed out" if timed_out
							else "benchmark sample canceled"
					),
				}
		)
		return "error" if timed_out else "canceled"
	var normalized_duration_ns: int = task.duration_usec * 1000 / info.iterations
	_send(
			"benchmark_sample",
			{
				"benchmark_id": info.id,
				"sample_index": index,
				"warmup": warmup,
				"duration_ns": normalized_duration_ns,
				"iterations": info.iterations,
				"metadata": { "completed_capture": capture_id },
			}
	)
	return "pass"


func _send(event_type: String, data: Dictionary[String, Variant]) -> void:
	_worker.call("_send_event", event_type, data)


func _error(code: String, text: String, benchmark_id: String) -> void:
	_send(
			"benchmark_worker_error",
			{ "benchmark_id": benchmark_id, "message": text, "metadata": { "code": code } }
	)


func _canceled() -> bool:
	return _worker.get("_cancel_requested")


func _call_hook(
		instance: GDTestBenchmark,
		method: StringName,
		arguments: Array[Variant],
		capture_id: String
) -> void:
	_emit_marker("start", capture_id)
	await instance.callv(method, arguments)
	_emit_marker("end", capture_id)


func _emit_marker(phase: String, capture_id: String) -> void:
	# gdkit:ignore = no-engine-logging
	printerr(MARKER_PREFIX + phase + " " + JSON.stringify(capture_id))


func _benchmark_id_at(benchmarks: Array[Variant], index: int) -> String:
	if index < 0 or index >= benchmarks.size() or not benchmarks[index] is Dictionary:
		return ""
	return str(_dict(benchmarks[index]).get("id", ""))


func _finish() -> void:
	_worker.call("_finish_run_state")
	_self_reference = null


func _dict(value: Variant) -> Dictionary[String, Variant]:
	var result: Dictionary[String, Variant] = {}
	if value is Dictionary:
		for key: Variant in value:
			result[str(key)] = value[key]
	return result
