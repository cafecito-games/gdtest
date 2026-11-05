extends GDTest

## Requests a double context from a discovery provider, where no case is executing.


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Double context outside a case"


## Requests a context during discovery and passes its open state to the case as a row.
# Provider suffix is a reserved gdtest public API spelling.
# gdkit:ignore = function-name
func test_context_is_closed_outside_case__cases() -> Array[Variant]:
	var context: GDTestDoubleContext = double_context()
	return [[context.is_open()]]


## Receives the open state captured by the discovery provider.
func test_context_is_closed_outside_case(is_open: bool) -> void:
	expect_false(is_open)
