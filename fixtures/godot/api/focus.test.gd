extends GDTest


## Must be suppressed whenever plan-wide focus is honored.
func test_unfocused_case() -> void:
	fail("plan-wide focus did not suppress this case")


## Focused method whose logical ID excludes the focus suffix.
# Focus suffix is a reserved gdtest public API spelling.
# gdkit:ignore = function-name
func test_focused_method__only(value: int) -> void:
	expect_equal(value, 7)


## Provider name is based on the logical method name.
# Provider suffix is a reserved gdtest public API spelling.
# gdkit:ignore = function-name
func test_focused_method__cases() -> Array[Variant]:
	return [[7]]
