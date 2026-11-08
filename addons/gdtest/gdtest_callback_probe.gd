## Case-scoped exact-arity `Callable` recorder, created by `GDTest.callback_probe`.
class_name GDTestCallbackProbe
extends RefCounted

const _MAX_ARITY: int = 8

var _ledger: GDTestProbeLedger
var _gate: _CallbackGate
var _callback: Callable = Callable()


class _CallbackGate:
	extends RefCounted

	var _open: bool = true
	var _record: Callable
	var _late_calls: int = 0


	func _init(record_callable: Callable) -> void:
		_record = record_callable


	func invoke(arguments: Array[Variant]) -> void:
		if _open and _record.is_valid():
			_record.call(arguments)
			return
		if _late_calls < GDTestProbeLedger.MAX_LATE_CALLS:
			_late_calls += 1
		GDTestProbeLedger.record_late_call()


	func close() -> void:
		_open = false
		_record = Callable()


## Returns the exact-arity `Callable` to pass into project code, or an invalid `Callable` on misuse.
func callback() -> Callable:
	return _callback


## Returns the number of invocations recorded so far while this probe was open.
func invocation_count() -> int:
	return _ledger.count()


## Returns defensive copies of every recorded invocation in call order.
func invocations() -> Array[GDTestInvocation]:
	return _ledger.invocations()


## Returns a defensive copy of the arguments recorded at `index`.
func arguments_at(index: int) -> Array[Variant]:
	var location: Dictionary[String, Variant] = _ledger.caller_location()
	return _ledger.arguments_at(index, location)


## Waits until at least `count` invocations are recorded and returns whether that happened.
##
## A `timeout_seconds` of zero waits without an internal deadline; the case timeout still
## applies. Returns `false` when the deadline expires or the probe closes first.
func wait_until_count(count: int, timeout_seconds: float = 0.0) -> bool:
	var location: Dictionary[String, Variant] = _ledger.caller_location()
	return await _ledger.wait_until_count(count, timeout_seconds, location)


## Closes the probe so subsequent callback invocations become bounded no-ops.
func close() -> void:
	if _gate != null:
		_gate.close()
	_ledger.close()


## Returns whether the probe has been closed or was created in an inert state.
func is_closed() -> bool:
	return not _ledger.is_open()


static func _gdtest_detached(context: GDTestProbeLedger.CaseContext) -> GDTestCallbackProbe:
	var probe := GDTestCallbackProbe.new()
	probe._ledger = GDTestProbeLedger.new(context, "invocations", false)
	return probe


static func _gdtest_create(
		argument_count: int,
		context: GDTestProbeLedger.CaseContext,
		location: Dictionary[String, Variant]
) -> GDTestCallbackProbe:
	if argument_count < 0 or argument_count > _MAX_ARITY:
		var invalid_probe: GDTestCallbackProbe = _gdtest_detached(context)
		invalid_probe._ledger.fail(
				"callback_probe supports callbacks with between 0 and %d arguments; got %d" % [
					_MAX_ARITY,
					argument_count,
				],
				location
		)
		return invalid_probe
	var probe := GDTestCallbackProbe.new()
	probe._ledger = GDTestProbeLedger.new(context, "invocations", true)
	probe._gate = _CallbackGate.new(Callable(probe._ledger, &"record"))
	probe._callback = _build_callback(argument_count, probe._gate)
	return probe


static func _build_callback(argument_count: int, gate: _CallbackGate) -> Callable:
	match argument_count:
		0:
			return func() -> void: gate.invoke([])
		1:
			return func(first: Variant) -> void: gate.invoke([first])
		2:
			return func(first: Variant, second: Variant) -> void: gate.invoke([first, second])
		3:
			return func(first: Variant, second: Variant, third: Variant) -> void:
				gate.invoke([first, second, third])
		4:
			return func(first: Variant, second: Variant, third: Variant, fourth: Variant) -> void:
				gate.invoke([first, second, third, fourth])
		5:
			return func(
					first: Variant,
					second: Variant,
					third: Variant,
					fourth: Variant,
					fifth: Variant
			) -> void:
				gate.invoke([first, second, third, fourth, fifth])
		6:
			return func(
					first: Variant,
					second: Variant,
					third: Variant,
					fourth: Variant,
					fifth: Variant,
					sixth: Variant
			) -> void:
				gate.invoke([first, second, third, fourth, fifth, sixth])
		7:
			return func(
					first: Variant,
					second: Variant,
					third: Variant,
					fourth: Variant,
					fifth: Variant,
					sixth: Variant,
					seventh: Variant
			) -> void:
				gate.invoke([first, second, third, fourth, fifth, sixth, seventh])
		_:
			return func(
					first: Variant,
					second: Variant,
					third: Variant,
					fourth: Variant,
					fifth: Variant,
					sixth: Variant,
					seventh: Variant,
					eighth: Variant
			) -> void:
				gate.invoke([first, second, third, fourth, fifth, sixth, seventh, eighth])
