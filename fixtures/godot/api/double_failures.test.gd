extends GDTest

## Exercises double misuse; every case must report a framework error at its test line.


## Returns a String where the double expects a bool.
func _answer_with_text(item: String, count: int) -> String:
	return item + str(count)


## Accepts one argument where the double expects two.
func _answer_with_one(item: String) -> bool:
	return not item.is_empty()


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Test double failures"


## Reports an unexpected call at the line of the call in this file.
func test_unexpected_call_reports_at_test_line() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.add("ghost")


## Reports a wrong-typed answer at the line of the call in this file.
func test_wrong_typed_answer_reports_at_test_line() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_answer(_answer_with_text)
	inventory.add("typed")


## Reports a wrong-arity answer at the configuration line in this file.
func test_wrong_arity_answer_reports_at_test_line() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_answer(_answer_with_one)
	expect_equal(inventory.gdtest.add.call_count(), 0)


## Records a constructor-time call; the report points at the source constructor that made it.
func test_constructor_call_reports_at_source_constructor() -> void:
	var ledger: LedgerDouble = LedgerDouble.new(double_context())
	expect_equal(ledger.gdtest.clear.calls()[0].sequence, 1)


## Rejects then_call_real on an abstract method and enqueues nothing for the next call.
func test_abstract_call_real_reports_and_enqueues_nothing() -> void:
	var calculator: CalculatorDouble = CalculatorDouble.new(double_context())
	calculator.gdtest.compute.then_call_real()
	calculator.compute(4)
