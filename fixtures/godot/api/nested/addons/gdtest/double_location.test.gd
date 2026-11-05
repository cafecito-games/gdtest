extends GDTest

## Proves that a user test under a nested addons/gdtest directory keeps its caller location.


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Nested double location"


## Reports an unexpected call at the line of the call in this nested file.
func test_unexpected_call_reports_at_nested_test_line() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.add("ghost")
