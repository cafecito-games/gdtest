extends GDTest

var _before_count: int = 0
var _after_count: int = 0


## Uses the supported before-case alias.
func before_each(case_info: GDTestCaseInfo) -> void:
	expect_true(case_info.file.begins_with("res://"))
	_before_count += 1


## Uses the supported after-case alias.
func after_each(case_info: GDTestCaseInfo) -> void:
	if case_info.id.ends_with("test_alias_second"):
		expect_equal(_before_count, 2)
	_after_count += 1


## Verifies alias invocation on the first case.
func test_alias_first() -> void:
	expect_equal(_before_count, 1)
	expect_equal(_after_count, 0)


## Verifies alias invocation on the second case.
func test_alias_second() -> void:
	expect_equal(_before_count, 2)
	expect_equal(_after_count, 1)
