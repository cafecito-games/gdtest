## Test double for res://fixtures/godot/api/inventory.gd.
class_name InventoryDouble
extends "res://fixtures/godot/api/inventory.gd"

## Path of this script, used to locate callers in failure reports.
const _GDTEST_DOUBLE_PATH: String = "res://fixtures/godot/api/inventory_double.gd"

## Typed stubbing controls for every doubled method.
var gdtest: GDTestControls


## Creates a double bound to `context`; remaining arguments reach the real constructor.
func _init(context: GDTestDoubleContext, capacity: int, label: String = "bag") -> void:
	gdtest = GDTestControls.new(context)
	super(capacity, label)


## Records the call and returns the configured result.
func add(item: String, count: int = LIMIT) -> bool:
	var method: AddMethod = gdtest.add
	var outcome: int = method._gdtest_next([item, count])
	if outcome == GDTestDoubleMethod.OUTCOME_REAL:
		return super.add(item, count)
	var value: Variant = method._gdtest_value()
	if outcome == GDTestDoubleMethod.OUTCOME_ANSWER:
		value = method._gdtest_answer().call(item, count)
	var result: bool
	if outcome != GDTestDoubleMethod.OUTCOME_NEUTRAL:
		if value is bool:
			result = value
		else:
			method._gdtest_reject_return(value, "bool")
	return result


## Records the call and returns the configured result.
func clear() -> void:
	var method: ClearMethod = gdtest.clear
	var outcome: int = method._gdtest_next([])
	if outcome == GDTestDoubleMethod.OUTCOME_REAL:
		super.clear()
	elif outcome == GDTestDoubleMethod.OUTCOME_ANSWER:
		method._gdtest_answer().call()


## Records the call and returns the configured result.
func load_counts(path: String) -> Dictionary[String, int]:
	var method: LoadCountsMethod = gdtest.load_counts
	var outcome: int = method._gdtest_next([path])
	if outcome == GDTestDoubleMethod.OUTCOME_REAL:
		return await super.load_counts(path)
	var value: Variant = method._gdtest_value()
	if outcome == GDTestDoubleMethod.OUTCOME_ANSWER:
		value = await method._gdtest_answer().call(path)
	var result: Dictionary[String, int]
	if outcome != GDTestDoubleMethod.OUTCOME_NEUTRAL:
		if value is Dictionary and result.is_same_typed(value):
			result = value
		else:
			method._gdtest_reject_return(value, "Dictionary[String, int]")
	return result


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
