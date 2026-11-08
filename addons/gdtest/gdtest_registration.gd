## Builds dynamic suite and case registration records for GDTest.
class_name GDTestRegistration
extends RefCounted


## Validates and expands one dynamic parameter row before registering its case.
static func register_row(
		description: String,
		callback: Callable,
		row: Variant,
		parameter_name: String,
		is_only: bool,
		timeout_seconds: float,
		ordinal: int,
		register_case: Callable,
		record_error: Callable
) -> void:
	if not row is Array:
		record_error.call("test_each row " + parameter_name + " must be an Array")
		return
	var parameters: Array[Variant] = []
	parameters.assign(row as Array[Variant])
	if callback.get_argument_count() != parameters.size():
		record_error.call(
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
	register_case.call(
			description,
			callback,
			parameters,
			parameter_name,
			is_only,
			display_name,
			timeout_seconds
	)


## Appends one immutable dynamic case record with stable identity and inherited suite metadata.
static func register_case(
		dynamic_cases: Array[Variant],
		case_ordinals: Dictionary[int, int],
		file: String,
		suite_stack: Array[GDTestSuiteInfo],
		location: Dictionary[String, Variant],
		description: String,
		callback: Callable,
		parameters: Array[Variant],
		parameter_name: String,
		is_only: bool,
		display_name: String,
		timeout_seconds: float
) -> void:
	var suite_path: Array[String] = []
	var inherited_only: bool = false
	for suite: GDTestSuiteInfo in suite_stack:
		suite_path.append(suite.name)
		inherited_only = inherited_only or suite.is_only
	var line: int = location.get("line", -1) as int
	var ordinal: int = case_ordinals.get(line, 0)
	case_ordinals[line] = ordinal + 1
	var full_name: Array[String] = suite_path.duplicate()
	full_name.append(description)
	var encoded_name: String = "/".join(full_name.map(GDTestProbeLedger.encode_component))
	var case_id: String = file.trim_prefix(
			"res://"
	) + "::dynamic@%d:%d[%s]" % [line, ordinal, encoded_name]
	dynamic_cases.append(
			{
				"id": case_id,
				"name": description if display_name.is_empty() else display_name,
				"file": str(location.get("file", file)),
				"line": line,
				"callback": callback,
				"parameters": parameters.duplicate(true),
				"parameter_name": parameter_name,
				"is_only": is_only or inherited_only,
				"timeout_seconds": timeout_seconds,
				"suite_path": suite_path,
				"suite_infos": suite_stack.duplicate(),
			}
	)


## Returns the innermost suite timeout or the file timeout when no dynamic suite is active.
static func inherited_timeout(
		suite_stack: Array[GDTestSuiteInfo],
		file_timeout_seconds: float
) -> float:
	if not suite_stack.is_empty():
		return suite_stack.back().timeout_seconds
	return file_timeout_seconds


## Validates one timeout override and resolves zero to its inherited value.
static func resolve_timeout(
		value: float,
		inherited: float,
		subject: String,
		record_error: Callable
) -> float:
	if not is_finite(value) or value < 0.0:
		record_error.call(subject + " timeout must be finite and non-negative")
		return inherited
	return inherited if value == 0.0 else value
