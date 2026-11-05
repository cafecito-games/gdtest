## Test double for res://fixtures/godot/api/calculator.gd.
class_name CalculatorDouble
extends "res://fixtures/godot/api/calculator.gd"

## Path of this generated script, used to locate callers in failure reports.
const _GDTEST_DOUBLE_PATH: String = "res://fixtures/godot/api/calculator_double.gd"

## Typed stubbing controls for every doubled method.
var gdtest: GDTestControls


## Creates a double bound to `context`.
func _init(context: GDTestDoubleContext) -> void:
	gdtest = GDTestControls.new(context)


## Records the call and returns the configured result.
func compute(value: int) -> int:
	var gdtest_method: ComputeMethod = gdtest.compute
	var gdtest_outcome: int = gdtest_method._gdtest_next([value])
	var gdtest_value: Variant = gdtest_method._gdtest_value()
	if gdtest_outcome == GDTestDoubleMethod.OUTCOME_ANSWER:
		gdtest_value = gdtest_method._gdtest_answer().call(value)
	var gdtest_result: int
	if gdtest_outcome != GDTestDoubleMethod.OUTCOME_NEUTRAL:
		if gdtest_value is int:
			gdtest_result = gdtest_value
		else:
			gdtest_method._gdtest_reject_return(gdtest_value, "int")
	return gdtest_result


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
