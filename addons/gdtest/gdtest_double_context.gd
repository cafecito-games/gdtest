## Case-scoped owner of test-double call ordering and failure reporting.
class_name GDTestDoubleContext
extends RefCounted

var _allocate_sequence: Callable
var _report_error: Callable
var _open: bool = false


## Returns a context that records nothing and reports nothing; `is_open()` is false.
static func closed() -> GDTestDoubleContext:
	return GDTestDoubleContext.new()


## Returns a context that allocates sequence numbers and reports failures through the callables.
static func open(allocate_sequence: Callable, report_error: Callable) -> GDTestDoubleContext:
	var context := GDTestDoubleContext.new()
	context._allocate_sequence = allocate_sequence
	context._report_error = report_error
	context._open = true
	return context


## Returns whether this context still belongs to an executing case.
func is_open() -> bool:
	return _open


## Detaches the context from its case. Repeated calls have no further effect.
func close() -> void:
	_open = false
	_allocate_sequence = Callable()
	_report_error = Callable()


func _gdtest_next_sequence() -> int:
	if not _open:
		return 0
	return _allocate_sequence.call()


func _gdtest_report(text: String, location: Dictionary[String, Variant]) -> void:
	if not _open:
		# gdkit:ignore = no-engine-logging
		push_error("gdtest: " + text)
		return
	_report_error.call(text, location)
