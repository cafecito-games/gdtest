## Immutable record of one signal emission captured by a case-scoped probe.
class_name GDTestInvocation
extends RefCounted

## Position of this invocation within its case, starting at one.
var sequence: int:
	get:
		return _sequence

## Defensive copy of the emitted arguments in signal parameter order.
var arguments: Array[Variant]:
	get:
		return _arguments.duplicate()

var _sequence: int
var _arguments: Array[Variant]


func _init(sequence_value: int, arguments_value: Array[Variant]) -> void:
	_sequence = sequence_value
	_arguments = arguments_value.duplicate()
