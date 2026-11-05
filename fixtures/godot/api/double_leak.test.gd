extends GDTest

## Proves that a double kept past its case neither records nor reports in a later case.

## Double created by the first case and deliberately kept for the later cases.
var _stale: InventoryDouble


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Double leaks"


## Creates a double, records one call, and keeps the double for later cases.
func test_a_double_survives_its_case() -> void:
	_stale = InventoryDouble.new(double_context(), 3)
	_stale.gdtest.add.then_return(true)
	expect_true(_stale.add("apple"))
	expect_equal(_stale.gdtest.add.call_count(), 1)


## Calls the stale double after its case ended; nothing is recorded or reported in this case.
func test_b_stale_call_records_nothing_in_later_case() -> void:
	expect_false(_stale.add("pear"))
	expect_equal(_stale.gdtest.add.call_count(), 1)


## Shows that a later case's own context starts a fresh sequence.
func test_c_fresh_context_starts_sequence_at_one() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_return(true)
	expect_true(inventory.add("plum"))
	expect_equal(inventory.gdtest.add.calls()[0].sequence, 1)
