## Case-scoped recorder for one signal, created by `GDTest.watch_signal`.
class_name GDTestSignalProbe
extends RefCounted

## Emitted after an invocation is recorded and when the probe disconnects.
signal state_changed

const _MAX_ARITY: int = 8

var _owner: Object
var _signal: Signal
var _connected: bool = false
var _emissions: Array[GDTestInvocation] = []
var _tree: SceneTree
var _recorder: Callable
var _allocate_sequence: Callable
var _report_error: Callable
var _locate_caller: Callable


## Returns a probe that records nothing because it was requested outside a case.
static func detached(
		tree: SceneTree,
		allocate_sequence: Callable,
		report_error: Callable,
		locate_caller: Callable
) -> GDTestSignalProbe:
	var probe := GDTestSignalProbe.new()
	probe._tree = tree
	probe._allocate_sequence = allocate_sequence
	probe._report_error = report_error
	probe._locate_caller = locate_caller
	return probe


## Connects a recorder to `what` and returns a probe, even when the connection failed.
static func create(
		what: Signal,
		tree: SceneTree,
		allocate_sequence: Callable,
		report_error: Callable,
		locate_caller: Callable,
		location: Dictionary[String, Variant]
) -> GDTestSignalProbe:
	var probe: GDTestSignalProbe = detached(tree, allocate_sequence, report_error, locate_caller)
	if what.is_null():
		probe._fail("watch_signal requires a valid signal", location)
		return probe
	var owner: Object = what.get_object()
	if not is_instance_valid(owner):
		probe._fail("watch_signal requires a signal whose owner is still valid", location)
		return probe
	var arity: int = _arity_of(owner, what.get_name())
	if arity < 0:
		probe._fail(
				(
						"watch_signal could not resolve a unique metadata entry for signal %s"
						% what.get_name()
				),
				location
		)
		return probe
	if arity > _MAX_ARITY:
		probe._fail(
				"watch_signal supports signals with at most %d arguments; %s has %d" % [
					_MAX_ARITY,
					what.get_name(),
					arity,
				],
				location
		)
		return probe
	probe._owner = owner
	probe._signal = what
	probe._recorder = _recorder_for(arity, probe)
	var error: Error = what.connect(probe._recorder)
	if error != OK:
		probe._fail(
				"watch_signal failed to connect %s: %s" % [what.get_name(), error_string(error)],
				location
		)
		return probe
	probe._connected = true
	return probe


## Returns the number of emissions recorded so far in this case.
func emission_count() -> int:
	return _emissions.size()


## Returns defensive copies of every recorded invocation in emission order.
func emissions() -> Array[GDTestInvocation]:
	return _emissions.duplicate()


## Returns a defensive copy of the arguments recorded at `index`.
func arguments_at(index: int) -> Array[Variant]:
	var location: Dictionary[String, Variant] = _caller_location()
	if index < 0 or index >= _emissions.size():
		_fail(
				"arguments_at index %d is outside the %d recorded emissions" % [
					index,
					_emissions.size(),
				],
				location
		)
		return []
	return _emissions[index].arguments


## Waits until at least `count` emissions are recorded and returns whether that happened.
##
## A `timeout_seconds` of zero waits without an internal deadline; the case timeout still
## applies. Returns `false` when the deadline expires or the probe is disconnected first.
func wait_until_count(count: int, timeout_seconds: float = 0.0) -> bool:
	var location: Dictionary[String, Variant] = _caller_location()
	if count < 0:
		_fail("wait_until_count count must be non-negative", location)
		return false
	if not is_finite(timeout_seconds) or timeout_seconds < 0.0:
		_fail("wait_until_count timeout must be finite and non-negative", location)
		return false
	if _emissions.size() >= count:
		return true
	if not _connected:
		return false
	var deadline_state: Dictionary[String, bool] = { "expired": false }
	if timeout_seconds > 0.0 and _tree != null:
		_tree.create_timer(
				timeout_seconds
		).timeout.connect(_on_deadline_expired.bind(deadline_state), CONNECT_ONE_SHOT)
	while _connected and _emissions.size() < count and not deadline_state["expired"]:
		await state_changed
	return _emissions.size() >= count


## Disconnects the recorder from its signal. Repeated calls have no further effect.
func stop_recording() -> void:
	if not _connected:
		return
	_connected = false
	if is_instance_valid(_owner) and _signal.is_connected(_recorder):
		_signal.disconnect(_recorder)
	state_changed.emit()


func _record(arguments: Array[Variant]) -> void:
	if not _connected:
		return
	var sequence: int = _allocate_sequence.call()
	_emissions.append(GDTestInvocation.new(sequence, arguments))
	state_changed.emit()


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


func _on_deadline_expired(deadline_state: Dictionary[String, bool]) -> void:
	deadline_state["expired"] = true
	state_changed.emit()


func _caller_location() -> Dictionary[String, Variant]:
	var location: Dictionary[String, Variant] = _locate_caller.call()
	return location


func _fail(text: String, location: Dictionary[String, Variant]) -> void:
	_report_error.call(text, location)


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


## Returns the declared argument count of the one metadata entry named `signal_name`, or -1.
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
		arity = arguments.size() if arguments is Array else 0
	return arity if matches == 1 else -1
