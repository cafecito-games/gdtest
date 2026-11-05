## Test double for res://fixtures/godot/api/calculator.gd.
class_name CalculatorDouble
extends "res://fixtures/godot/api/calculator.gd"

## Path of this script, used to locate callers in failure reports.
const _GDTEST_DOUBLE_PATH: String = "res://fixtures/godot/api/calculator_double.gd"

## Typed stubbing controls for every doubled method.
var gdtest: GDTestControls


## Creates a double bound to `context`.
func _init(context: GDTestDoubleContext) -> void:
	gdtest = GDTestControls.new(context)


## Records the call and returns the configured result.
func compute(value: int) -> int:
	var method: ComputeMethod = gdtest.compute
	var outcome: int = method._gdtest_next([value])
	var returned: Variant = method._gdtest_value()
	if outcome == GDTestDoubleMethod.OUTCOME_ANSWER:
		returned = method._gdtest_answer().call(value)
	var result: int
	if outcome != GDTestDoubleMethod.OUTCOME_NEUTRAL:
		if returned is int:
			result = returned
		else:
			method._gdtest_reject_return(returned, "int")
	return result


## Typed controllers for every doubled method of res://fixtures/godot/api/calculator.gd.
class GDTestControls extends RefCounted:
	## Controls calls to `compute`.
	var compute: ComputeMethod


	## Creates controllers that report through `context`.
	func _init(context: GDTestDoubleContext) -> void:
		compute = ComputeMethod.new(context, "Calculator.compute", _GDTEST_DOUBLE_PATH, 1, true)


## Typed controller for `compute`.
class ComputeMethod extends GDTestDoubleMethod:
	## Queues `value` as the next result of `compute`.
	func then_return(value: int) -> void:
		_gdtest_enqueue_value(value)
