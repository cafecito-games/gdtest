## Shared case-scoped invocation ledger and asynchronous wait primitive for gdtest probes.
extends RefCounted

## Emitted when an invocation is recorded, a wait deadline expires, or the ledger closes.
signal state_changed

## Maximum late post-close callback invocations counted per worker run or gate.
const MAX_LATE_CALLS: int = 1024

static var _worker_late_calls: int = 0

var _context: CaseContext
var _record_noun: String
var _records: Array[GDTestInvocation] = []
var _open: bool = false


## Detachable bridge to the owning `GDTest` instance so closed probes release case state.
class CaseContext:
	extends RefCounted

	var _tree: SceneTree
	var _allocate_sequence: Callable
	var _report_error: Callable
	var _locate_caller: Callable
	var _active: bool = false


	## Creates an active case context backed by the currently executing `GDTest` case.
	static func open(
			scene_tree: SceneTree,
			sequence_allocator: Callable,
			error_reporter: Callable,
			caller_locator: Callable
	) -> CaseContext:
		var context := CaseContext.new()
		context._tree = scene_tree
		context._allocate_sequence = sequence_allocator
		context._report_error = error_reporter
		context._locate_caller = caller_locator
		context._active = true
		return context


	## Creates an inactive context that can report errors until detached from its suite.
	static func inactive(
			scene_tree: SceneTree,
			error_reporter: Callable,
			caller_locator: Callable
	) -> CaseContext:
		var context := CaseContext.new()
		context._tree = scene_tree
		context._report_error = error_reporter
		context._locate_caller = caller_locator
		context._active = false
		return context


	## Returns the active worker `SceneTree`, or `null` after detachment.
	func tree() -> SceneTree:
		return _tree


	## Allocates the next one-based case sequence number, or zero when inactive.
	func allocate_sequence() -> int:
		if not _active or not _allocate_sequence.is_valid():
			return 0
		return int(_allocate_sequence.call())


	## Resolves the nearest non-runtime user stack frame.
	func caller_location() -> Dictionary[String, Variant]:
		if _locate_caller.is_valid():
			var resolved: Dictionary[String, Variant] = _locate_caller.call()
			return resolved
		return { "file": "", "line": -1 }


	## Reports a source-located framework or discovery diagnostic when still attached.
	func report_error(text: String, location: Dictionary[String, Variant]) -> void:
		if _report_error.is_valid():
			_report_error.call(text, location)


	## Severs references to the `SceneTree`, `GDTest` instance, and failure sink.
	func detach() -> void:
		_active = false
		_tree = null
		_allocate_sequence = Callable()
		_report_error = Callable()
		_locate_caller = Callable()


## Increments the bounded worker-level counter for post-close callback invocations.
static func record_late_call() -> void:
	if _worker_late_calls < MAX_LATE_CALLS:
		_worker_late_calls += 1


## Atomically drains and resets the bounded worker-level late callback invocation count.
static func take_late_call_count() -> int:
	var recorded: int = _worker_late_calls
	_worker_late_calls = 0
	return recorded


## Initializes the ledger with its case context, plural diagnostic noun, and initial open state.
func _init(context: CaseContext, record_noun: String, initially_open: bool) -> void:
	_context = context
	_record_noun = record_noun
	_open = initially_open


## Returns whether the ledger is still accepting new invocations.
func is_open() -> bool:
	return _open


## Returns the number of invocations recorded so far.
func count() -> int:
	return _records.size()


## Returns a shallow copy of the immutable recorded `GDTestInvocation` list.
func invocations() -> Array[GDTestInvocation]:
	return _records.duplicate()


## Resolves the caller's source file and line through the owning context.
func caller_location() -> Dictionary[String, Variant]:
	return _context.caller_location()


## Reports a framework or discovery diagnostic at `location`.
func fail(text: String, location: Dictionary[String, Variant]) -> void:
	_context.report_error(text, location)


## Appends one invocation with a shared case sequence number when the ledger is open.
func record(arguments: Array[Variant]) -> bool:
	if not _open:
		return false
	var sequence: int = _context.allocate_sequence()
	if sequence <= 0:
		return false
	_records.append(GDTestInvocation.new(sequence, arguments))
	state_changed.emit()
	return true


## Returns a defensive copy of the arguments recorded at `index`, or reports an error.
func arguments_at(index: int, location: Dictionary[String, Variant]) -> Array[Variant]:
	if index < 0 or index >= _records.size():
		fail(
				"arguments_at index %d is outside the %d recorded %s" % [
					index,
					_records.size(),
					_record_noun,
				],
				location
		)
		return []
	return _records[index].arguments


## Waits until `target_count` records exist, the ledger closes, or `timeout_seconds` expires.
func wait_until_count(
		target_count: int,
		timeout_seconds: float,
		location: Dictionary[String, Variant],
		can_continue: Callable = Callable(),
		poll_each_frame_without_timeout: bool = false
) -> bool:
	if target_count < 0:
		fail("wait_until_count count must be non-negative", location)
		return false
	if not is_finite(timeout_seconds) or timeout_seconds < 0.0:
		fail("wait_until_count timeout must be finite and non-negative", location)
		return false
	if _records.size() >= target_count:
		return true
	if not _open:
		return false
	var tree: SceneTree = _context.tree()
	if timeout_seconds > 0.0 and tree == null:
		return false
	var deadline_state: Dictionary[String, bool] = { "expired": false }
	var timer: SceneTreeTimer = null
	var on_timeout: Callable = Callable()
	if timeout_seconds > 0.0:
		timer = tree.create_timer(timeout_seconds)
		on_timeout = _on_deadline_expired.bind(deadline_state)
		timer.timeout.connect(on_timeout, CONNECT_ONE_SHOT)
	while _open and _records.size() < target_count and not deadline_state["expired"]:
		if can_continue.is_valid() and not bool(can_continue.call()):
			break
		if poll_each_frame_without_timeout and timeout_seconds == 0.0 and tree != null:
			await tree.process_frame
		else:
			await state_changed
	if timer != null and on_timeout.is_valid() and timer.timeout.is_connected(on_timeout):
		timer.timeout.disconnect(on_timeout)
	return _records.size() >= target_count


## Closes the ledger and wakes any suspended `wait_until_count` coroutines.
func close() -> bool:
	if not _open:
		return false
	_open = false
	state_changed.emit()
	return true


func _on_deadline_expired(deadline_state: Dictionary[String, bool]) -> void:
	deadline_state["expired"] = true
	state_changed.emit()
