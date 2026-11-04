extends GDTest


## Produces a stable structured failure for end-to-end reporter tests.
func test_structured_failure() -> void:
	message("fixture output")
	expect_equal({ "actual": [1, 2] }, { "expected": [3, 4] }, "values differ")
