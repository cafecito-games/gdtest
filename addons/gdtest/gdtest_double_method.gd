## Typed controller for one doubled method, holding its behaviours and recorded calls.
class_name GDTestDoubleMethod
extends RefCounted

## The call is neutral: the generated double returns its default value unchanged.
const OUTCOME_NEUTRAL: int = 0
## The call returns a value queued by `then_return`.
const OUTCOME_VALUE: int = 1
## The call returns the result of a callable queued by `then_answer`.
const OUTCOME_ANSWER: int = 2
## The call runs the source method through `super`.
const OUTCOME_REAL: int = 3

const _OUTCOME_KEY: String = "outcome"
const _VALUE_KEY: String = "value"
const _ANSWER_KEY: String = "answer"

var _context: GDTestDoubleContext
var _display_name: String
var _double_path: String
var _parameter_count: int
var _is_abstract: bool
var _behaviours: Array[Variant] = []
var _current: Dictionary[String, Variant] = {}
var _calls: Array[GDTestInvocation] = []


func _init(
		context: GDTestDoubleContext,
		display_name: String,
		double_path: String,
		parameter_count: int,
		is_abstract: bool
) -> void:
	_context = context
	_display_name = display_name
	_double_path = double_path
	_parameter_count = parameter_count
	_is_abstract = is_abstract
	_require_open()


## Queues `answer` as the next call's result, reporting an invalid callable instead.
func then_answer(answer: Callable) -> void:
	if not _require_open():
		return
	var location: Dictionary[String, Variant] = _caller_location()
	if not answer.is_valid():
		_report(_display_name + " then_answer requires a valid callable", location)
		return
	if answer.get_argument_count() != _parameter_count:
		_report(
				"%s then_answer callable takes %d arguments; expected %d" % [
					_display_name,
					answer.get_argument_count(),
					_parameter_count,
				],
				location
		)
		return
	_enqueue({ _OUTCOME_KEY: OUTCOME_ANSWER, _VALUE_KEY: null, _ANSWER_KEY: answer })


## Queues the source method for the next call, reporting an abstract method instead.
func then_call_real() -> void:
	if not _require_open():
		return
	if _is_abstract:
		_report(
				_display_name + " cannot call an abstract method through then_call_real",
				_caller_location()
		)
		return
	_enqueue({ _OUTCOME_KEY: OUTCOME_REAL, _VALUE_KEY: null, _ANSWER_KEY: Callable() })


## Returns defensive copies of every recorded call in call order.
func calls() -> Array[GDTestInvocation]:
	_require_open()
	return _calls.duplicate()


## Returns the number of recorded calls.
func call_count() -> int:
	_require_open()
	return _calls.size()


## Clears every queued behaviour and every recorded call.
func reset() -> void:
	if not _require_open():
		return
	_behaviours.clear()
	_current.clear()
	_calls.clear()


## Used only by generated code: queues a plain value for the next call.
func _gdtest_enqueue_value(value: Variant) -> void:
	if not _require_open():
		return
	_enqueue({ _OUTCOME_KEY: OUTCOME_VALUE, _VALUE_KEY: value, _ANSWER_KEY: Callable() })


## Used only by generated code: records the call and returns its behaviour's outcome.
func _gdtest_next(arguments: Array[Variant]) -> int:
	if not _require_open():
		return OUTCOME_NEUTRAL
	_calls.append(GDTestInvocation.new(_context._gdtest_next_sequence(), arguments))
	if _behaviours.is_empty():
		_report("unexpected call to " + _display_name, _caller_location())
		_current = {}
		return OUTCOME_NEUTRAL
	_current = _behaviours[0]
	if _behaviours.size() > 1:
		_behaviours.pop_front()
	var outcome: int = _current[_OUTCOME_KEY]
	return outcome


## Used only by generated code: returns the value of the behaviour `_gdtest_next` chose.
func _gdtest_value() -> Variant:
	_require_open()
	return _current.get(_VALUE_KEY)


## Used only by generated code: returns the answer of the behaviour `_gdtest_next` chose.
func _gdtest_answer() -> Callable:
	_require_open()
	var answer: Variant = _current.get(_ANSWER_KEY, Callable())
	return answer


## Used only by generated code: reports a return value of the wrong type.
func _gdtest_reject_return(value: Variant, expected_type: String) -> void:
	_report(
			"%s answer returned %s, expected %s" % [
				_display_name,
				type_string(typeof(value)),
				expected_type,
			],
			_caller_location()
	)


func _enqueue(behaviour: Dictionary[String, Variant]) -> void:
	_behaviours.append(behaviour)


func _require_open() -> bool:
	if _context != null and _context.is_open():
		return true
	# gdkit:ignore = no-engine-logging
	push_error(
			(
					"gdtest: %s: construct doubles with double_context() inside an executing case"
					% _display_name
			)
	)
	return false


func _report(text: String, location: Dictionary[String, Variant]) -> void:
	if _context == null:
		# gdkit:ignore = no-engine-logging
		push_error("gdtest: " + text)
		return
	_context._gdtest_report(text, location)


## Returns the first stack frame outside the runtime and the generated double.
func _caller_location() -> Dictionary[String, Variant]:
	for frame_value: Variant in get_stack():
		if not frame_value is Dictionary:
			continue
		var source: String = str(frame_value.get("source", ""))
		if source.contains("/addons/gdtest/") or source == _double_path:
			continue
		return { "file": source, "line": int(frame_value.get("line", -1)) }
	return { "file": _double_path, "line": -1 }
