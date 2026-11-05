## Immutable record of one signal, callback, or test-double invocation captured in a case.
class_name GDTestInvocation
extends RefCounted

## Position of this invocation within its case, starting at one.
var sequence: int:
	get:
		return _sequence

## Defensive copy of the recorded arguments in parameter order.
var arguments: Array[Variant]:
	get:
		return _arguments.duplicate(true)

var _sequence: int
var _arguments: Array[Variant]


func _init(sequence_value: int, arguments_value: Array[Variant]) -> void:
	_sequence = sequence_value
	_arguments = arguments_value.duplicate(true)
