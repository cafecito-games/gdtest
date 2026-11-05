extends RefCounted

## Runs untimed sample hooks around a timed benchmark method batch.

## Prefix shared with the Go process-output classifier.
const MARKER_PREFIX: String = "@@GDTEST_CASE_CAPTURE "

## True after hooks and every benchmark iteration have returned.
var finished: bool = false
## Sum of monotonic method-call durations, excluding hooks.
var duration_usec: int = 0
var _capture_finished: bool = false
var _benchmark_id: String = ""
var _clock: Callable


## Starts the task and returns immediately when user code suspends. Tests may
## inject a monotonic fake clock; production always uses Time.get_ticks_usec.
func start(
		instance: GDTestBenchmark,
		info: GDTestBenchmarkInfo,
		capture_id: String = "",
		clock: Callable = Callable()
) -> void:
	_benchmark_id = info.id
	if not capture_id.is_empty():
		_benchmark_id = capture_id
	_clock = clock if clock.is_valid() else Callable(Time, &"get_ticks_usec")
	_execute(instance, info)


## Closes the stderr capture interval exactly once.
func finish_capture() -> void:
	if _capture_finished:
		return
	_capture_finished = true
	_emit_marker("end")


func _execute(instance: GDTestBenchmark, info: GDTestBenchmarkInfo) -> void:
	_emit_marker("start")
	if instance.has_method("before_sample"):
		await instance.callv(&"before_sample", [info])
	for _iteration: int in range(info.iterations):
		var started_usec: int = int(_clock.call())
		await instance.callv(info.method_name, [])
		duration_usec += int(_clock.call()) - started_usec
	if instance.has_method("after_sample"):
		await instance.callv(&"after_sample", [info])
	finish_capture()
	finished = true


func _emit_marker(phase: String) -> void:
	# This private marker shares stderr ordering with engine script errors and is
	# removed by the Go worker boundary before output reaches reporters.
	# gdkit:ignore = no-engine-logging
	printerr(MARKER_PREFIX + phase + " " + JSON.stringify(_benchmark_id))
