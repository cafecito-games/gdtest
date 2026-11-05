## Test double for res://fixtures/godot/api/inventory.gd.
class_name InventoryDouble
extends "res://fixtures/godot/api/inventory.gd"

## Path of this generated script, used to locate callers in failure reports.
const _GDTEST_DOUBLE_PATH: String = "res://fixtures/godot/api/inventory_double.gd"

## Typed stubbing controls for every doubled method.
var gdtest: GDTestControls


## Creates a double bound to `context`; remaining arguments reach the real constructor.
func _init(context: GDTestDoubleContext, capacity: int, label: String = "bag") -> void:
	gdtest = GDTestControls.new(context)
	super(capacity, label)


## Records the call and returns the configured result.
func add(item: String, count: int = LIMIT) -> bool:
	var gdtest_method: AddMethod = gdtest.add
	var gdtest_outcome: int = gdtest_method._gdtest_next([item, count])
	if gdtest_outcome == GDTestDoubleMethod.OUTCOME_REAL:
		return super.add(item, count)
	var gdtest_value: Variant = gdtest_method._gdtest_value()
	if gdtest_outcome == GDTestDoubleMethod.OUTCOME_ANSWER:
		gdtest_value = gdtest_method._gdtest_answer().call(item, count)
	var gdtest_result: bool
	if gdtest_outcome != GDTestDoubleMethod.OUTCOME_NEUTRAL:
		if gdtest_value is bool:
			gdtest_result = gdtest_value
		else:
			gdtest_method._gdtest_reject_return(gdtest_value, "bool")
	return gdtest_result


## Records the call and returns the configured result.
func clear() -> void:
	var gdtest_method: ClearMethod = gdtest.clear
	var gdtest_outcome: int = gdtest_method._gdtest_next([])
	if gdtest_outcome == GDTestDoubleMethod.OUTCOME_REAL:
		super.clear()
	elif gdtest_outcome == GDTestDoubleMethod.OUTCOME_ANSWER:
		gdtest_method._gdtest_answer().call()


## Records the call and returns the configured result.
func load_counts(path: String) -> Dictionary[String, int]:
	var gdtest_method: LoadCountsMethod = gdtest.load_counts
	var gdtest_outcome: int = gdtest_method._gdtest_next([path])
	if gdtest_outcome == GDTestDoubleMethod.OUTCOME_REAL:
		return await super.load_counts(path)
	var gdtest_value: Variant = gdtest_method._gdtest_value()
	if gdtest_outcome == GDTestDoubleMethod.OUTCOME_ANSWER:
		gdtest_value = await gdtest_method._gdtest_answer().call(path)
	var gdtest_result: Dictionary[String, int]
	if gdtest_outcome != GDTestDoubleMethod.OUTCOME_NEUTRAL:
		if gdtest_value is Dictionary and gdtest_result.is_same_typed(gdtest_value):
			gdtest_result = gdtest_value
		else:
			gdtest_method._gdtest_reject_return(gdtest_value, "Dictionary[String, int]")
	return gdtest_result


## Typed controllers for every doubled method of res://fixtures/godot/api/inventory.gd.
class GDTestControls extends RefCounted:
	## Controls calls to `add`.
	var add: AddMethod
	## Controls calls to `clear`.
	var clear: ClearMethod
	## Controls calls to `load_counts`.
	var load_counts: LoadCountsMethod


	## Creates controllers that report through `context`.
	func _init(context: GDTestDoubleContext) -> void:
		add = AddMethod.new(context, "Inventory.add", _GDTEST_DOUBLE_PATH, 2, false)
		clear = ClearMethod.new(context, "Inventory.clear", _GDTEST_DOUBLE_PATH, 0, false)
		load_counts = LoadCountsMethod.new(
				context,
				"Inventory.load_counts",
				_GDTEST_DOUBLE_PATH,
				1,
				false
		)


## Typed controller for `add`.
class AddMethod extends GDTestDoubleMethod:
	## Queues `value` as the next result of `add`.
	func then_return(value: bool) -> void:
		_gdtest_enqueue_value(value)


## Typed controller for `clear`.
class ClearMethod extends GDTestDoubleMethod:
	## Marks the next call to `clear` as expected.
	func then_return() -> void:
		_gdtest_enqueue_value(null)


## Typed controller for `load_counts`.
class LoadCountsMethod extends GDTestDoubleMethod:
	## Queues `value` as the next result of `load_counts`.
	func then_return(value: Dictionary[String, int]) -> void:
		_gdtest_enqueue_value(value)
