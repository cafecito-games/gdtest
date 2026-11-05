## Test double for res://fixtures/godot/api/ledger.gd.
class_name LedgerDouble
extends "res://fixtures/godot/api/ledger.gd"

## Path of this generated script, used to locate callers in failure reports.
const _GDTEST_DOUBLE_PATH: String = "res://fixtures/godot/api/ledger_double.gd"

## Typed stubbing controls for every doubled method.
var gdtest: GDTestControls


## Creates a double bound to `context`; remaining arguments reach the real constructor.
func _init(context: GDTestDoubleContext) -> void:
	gdtest = GDTestControls.new(context)
	super()


## Records the call and returns the configured result.
func clear() -> void:
	var gdtest_method: ClearMethod = gdtest.clear
	var gdtest_outcome: int = gdtest_method._gdtest_next([])
	if gdtest_outcome == GDTestDoubleMethod.OUTCOME_REAL:
		super.clear()
	elif gdtest_outcome == GDTestDoubleMethod.OUTCOME_ANSWER:
		gdtest_method._gdtest_answer().call()


## Typed controllers for every doubled method of res://fixtures/godot/api/ledger.gd.
class GDTestControls extends RefCounted:
	## Controls calls to `clear`.
	var clear: ClearMethod


	## Creates controllers that report through `context`.
	func _init(context: GDTestDoubleContext) -> void:
		clear = ClearMethod.new(context, "Ledger.clear", _GDTEST_DOUBLE_PATH, 0, false)


## Typed controller for `clear`.
class ClearMethod extends GDTestDoubleMethod:
	## Marks the next call to `clear` as expected.
	func then_return() -> void:
		_gdtest_enqueue_value(null)
