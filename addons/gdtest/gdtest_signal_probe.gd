## Case-scoped recorder for one signal, created by `GDTest.watch_signal`.
class_name GDTestSignalProbe
extends RefCounted

## Emitted after an invocation is recorded and when the probe disconnects.
signal state_changed

## Internal shared case-scoped probe ledger.
const ProbeLedger := preload("res://addons/gdtest/probe_ledger.gd")
const _MAX_ARITY: int = 8

var _owner: Object
var _signal: Signal
var _ledger: GDTestProbeLedger
var _recorder: Callable


## Returns the number of emissions recorded so far in this case.
func emission_count() -> int:
	return _ledger.count()


## Returns defensive copies of every recorded invocation in emission order.
func emissions() -> Array[GDTestInvocation]:
	return _ledger.invocations()


## Returns a defensive copy of the arguments recorded at `index`.
func arguments_at(index: int) -> Array[Variant]:
	var location: Dictionary[String, Variant] = _ledger.caller_location()
	return _ledger.arguments_at(index, location)


## Waits until at least `count` emissions are recorded and returns whether that happened.
##
## A `timeout_seconds` of zero waits without an internal deadline; the case timeout still
## applies. Returns `false` when the deadline expires or the probe is disconnected first.
func wait_until_count(count: int, timeout_seconds: float = 0.0) -> bool:
	var location: Dictionary[String, Variant] = _ledger.caller_location()
	return await _ledger.wait_until_count(
			count,
			timeout_seconds,
			location,
			Callable(self, &"_owner_is_valid"),
			true
	)


## Disconnects the recorder from its signal. Repeated calls have no further effect.
func stop_recording() -> void:
	if not _ledger.is_open():
		return
	if is_instance_valid(_owner) and _signal.is_connected(_recorder):
		_signal.disconnect(_recorder)
	_recorder = Callable()
	_ledger.close()


static func _gdtest_detached(context: GDTestProbeLedger.CaseContext) -> GDTestSignalProbe:
	var probe := GDTestSignalProbe.new()
	probe._ledger = ProbeLedger.new(context, "emissions", false)
	probe._ledger.state_changed.connect(probe._on_ledger_state_changed)
	return probe


static func _gdtest_create(
		what: Signal,
		context: GDTestProbeLedger.CaseContext,
		location: Dictionary[String, Variant]
) -> GDTestSignalProbe:
	var invalid_probe: GDTestSignalProbe = _gdtest_detached(context)
	if what.is_null():
		invalid_probe._ledger.fail("watch_signal requires a valid signal", location)
		return invalid_probe
	var owner: Object = what.get_object()
	if not is_instance_valid(owner):
		invalid_probe._ledger.fail(
				"watch_signal requires a signal whose owner is still valid",
				location
		)
		return invalid_probe
	var arity: int = _arity_of(owner, what.get_name())
	if arity < 0:
		invalid_probe._ledger.fail(
				(
						"watch_signal could not resolve a unique metadata entry for signal %s"
						% what.get_name()
				),
				location
		)
		return invalid_probe
	if arity > _MAX_ARITY:
		invalid_probe._ledger.fail(
				"watch_signal supports signals with at most %d arguments; %s has %d" % [
					_MAX_ARITY,
					what.get_name(),
					arity,
				],
				location
		)
		return invalid_probe
	var probe := GDTestSignalProbe.new()
	probe._ledger = ProbeLedger.new(context, "emissions", true)
	probe._ledger.state_changed.connect(probe._on_ledger_state_changed)
	probe._owner = owner
	probe._signal = what
	probe._recorder = _recorder_for(arity, probe)
	var error: Error = what.connect(probe._recorder)
	if error != OK:
		probe._ledger.close()
		probe._recorder = Callable()
		probe._ledger.fail(
				"watch_signal failed to connect %s: %s" % [what.get_name(), error_string(error)],
				location
		)
		return probe
	return probe


func _owner_is_valid() -> bool:
	return is_instance_valid(_owner)


func _on_ledger_state_changed() -> void:
	state_changed.emit()


func _record(arguments: Array[Variant]) -> void:
	_ledger.record(arguments)


func _record_0() -> void:
	_record([])


func _record_1(first: Variant) -> void:
	_record([first])


func _record_2(first: Variant, second: Variant) -> void:
	_record([first, second])


func _record_3(first: Variant, second: Variant, third: Variant) -> void:
	_record([first, second, third])


func _record_4(first: Variant, second: Variant, third: Variant, fourth: Variant) -> void:
	_record([first, second, third, fourth])


func _record_5(
		first: Variant,
		second: Variant,
		third: Variant,
		fourth: Variant,
		fifth: Variant
) -> void:
	_record([first, second, third, fourth, fifth])


func _record_6(
		first: Variant,
		second: Variant,
		third: Variant,
		fourth: Variant,
		fifth: Variant,
		sixth: Variant
) -> void:
	_record([first, second, third, fourth, fifth, sixth])


func _record_7(
		first: Variant,
		second: Variant,
		third: Variant,
		fourth: Variant,
		fifth: Variant,
		sixth: Variant,
		seventh: Variant
) -> void:
	_record([first, second, third, fourth, fifth, sixth, seventh])


func _record_8(
		first: Variant,
		second: Variant,
		third: Variant,
		fourth: Variant,
		fifth: Variant,
		sixth: Variant,
		seventh: Variant,
		eighth: Variant
) -> void:
	_record([first, second, third, fourth, fifth, sixth, seventh, eighth])


static func _recorder_for(arity: int, probe: GDTestSignalProbe) -> Callable:
	match arity:
		0:
			return Callable(probe, &"_record_0")
		1:
			return Callable(probe, &"_record_1")
		2:
			return Callable(probe, &"_record_2")
		3:
			return Callable(probe, &"_record_3")
		4:
			return Callable(probe, &"_record_4")
		5:
			return Callable(probe, &"_record_5")
		6:
			return Callable(probe, &"_record_6")
		7:
			return Callable(probe, &"_record_7")
		_:
			return Callable(probe, &"_record_8")


static func _arity_of(owner: Object, signal_name: StringName) -> int:
	var matches: int = 0
	var arity: int = -1
	for entry: Variant in owner.get_signal_list():
		if not entry is Dictionary:
			continue
		var metadata: Dictionary[Variant, Variant] = entry
		if StringName(str(metadata.get("name", ""))) != signal_name:
			continue
		matches += 1
		var arguments: Variant = metadata.get("args", [])
		if arguments is Array:
			var argument_list: Array[Variant] = arguments
			arity = argument_list.size()
		else:
			arity = 0
	return arity if matches == 1 else -1
