## Resolves project, file, and method case-timeout policy for the worker.
class_name GDTestWorkerTimeouts
extends RefCounted


## Returns whether a reflected method is an executable test rather than a reserved provider.
static func is_test_method(method_name: String) -> bool:
	return (
			method_name.begins_with("test_")
			and not method_name.ends_with("__cases")
			and not method_name.ends_with("__timeout")
	)


## Resolves a prepared case's effective timeout in seconds.
static func case_timeout_seconds(case_plan: Dictionary[String, Variant], inherited: float) -> float:
	return case_plan.get("timeout_seconds", inherited)


## Converts the plan's nanosecond timeout into the worker's microsecond deadline.
static func plan_timeout_usec(plan: Dictionary[String, Variant], default_timeout_usec: int) -> int:
	var case_timeout_ns: int = plan.get("case_timeout_ns", 0)
	if case_timeout_ns <= 0:
		return default_timeout_usec
	return maxi(1, case_timeout_ns / 1000)


## Configures one test instance with its validated file-level timeout.
static func configure_file(
		instance: GDTest,
		tree: SceneTree,
		path: String,
		failure_sink: Callable,
		plan_timeout_usec: int
) -> float:
	var file_timeout_seconds: float = float(plan_timeout_usec) / 1000000.0
	instance._gdtest_configure(tree, path, failure_sink, file_timeout_seconds)
	if instance.has_method("get_case_timeout"):
		var timeout_value: Variant = instance.call(&"get_case_timeout")
		if timeout_value is float:
			var float_value: float = timeout_value
			if is_finite(float_value) and float_value >= 0.0:
				if float_value > 0.0:
					file_timeout_seconds = float_value
				instance._gdtest_configure(tree, path, failure_sink, file_timeout_seconds)
				return file_timeout_seconds
		instance._gdtest_record_discovery_error(
				"get_case_timeout must synchronously return a finite non-negative float"
		)
	instance._gdtest_configure(tree, path, failure_sink, file_timeout_seconds)
	return file_timeout_seconds


## Evaluates one reserved method timeout provider or returns the inherited value.
static func method_timeout(
		instance: GDTest,
		provider_name: StringName,
		inherited: float,
		discovery_errors: Array[Variant]
) -> float:
	if not instance.has_method(provider_name):
		return inherited
	var value: Variant = instance.call(provider_name)
	if value is float:
		var float_value: float = value
		if is_finite(float_value) and float_value >= 0.0:
			return inherited if float_value == 0.0 else float_value
	discovery_errors.append(
			{
				"message": str(
						provider_name
				) + " must synchronously return a finite non-negative float",
			}
	)
	return inherited
