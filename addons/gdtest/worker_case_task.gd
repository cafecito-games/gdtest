extends RefCounted

## Runs one case's hooks and body while the worker monitors its deadline.

## Prefix shared with the Go process-output classifier.
const MARKER_PREFIX: String = "@@GDTEST_CASE_CAPTURE "

## True after hooks and the case body have returned.
var finished: bool = false
var _capture_finished: bool = false
var _case_id: String = ""


## Starts the task and returns immediately when user code suspends.
func start(
		instance: GDTest,
		info: GDTestCaseInfo,
		case_plan: Dictionary[String, Variant],
		method_name: StringName,
		parameters: Array[Variant]
) -> void:
	_case_id = info.id
	_emit_marker("start")
	_execute(instance, info, case_plan, method_name, parameters)


## Closes the stderr capture interval exactly once.
func finish_capture() -> void:
	if _capture_finished:
		return
	_capture_finished = true
	_emit_marker("end")


func _execute(
		instance: GDTest,
		info: GDTestCaseInfo,
		case_plan: Dictionary[String, Variant],
		method_name: StringName,
		parameters: Array[Variant]
) -> void:
	if instance.has_method("before_case"):
		await instance.callv(&"before_case", [info])
	elif instance.has_method("before_each"):
		await instance.callv(&"before_each", [info])
	if not instance._gdtest_has_error():
		var callback: Variant = case_plan.get("callback")
		if callback is Callable:
			await callback.callv(parameters)
		elif not method_name.is_empty() and instance.has_method(method_name):
			await instance.callv(method_name, parameters)
		else:
			instance._gdtest_record_error_at("case callback is unavailable", info.file, info.line)
	if instance.has_method("after_case"):
		await instance.callv(&"after_case", [info])
	elif instance.has_method("after_each"):
		await instance.callv(&"after_each", [info])
	finished = true
	finish_capture()


func _emit_marker(phase: String) -> void:
	# This private marker shares stderr ordering with engine script errors and is
	# removed by the Go worker boundary before output reaches reporters.
	# gdkit:ignore = no-engine-logging
	printerr(MARKER_PREFIX + phase + " " + JSON.stringify(_case_id))
