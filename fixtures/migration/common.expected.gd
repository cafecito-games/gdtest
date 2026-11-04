extends GDTest


# Existing comments and layout stay byte-for-byte outside edited spans.
## Returns Vest parameter rows.
func params_provider() -> Array[Variant]:
	return [[1, 2], [4, 5]]


## Receives suite lifecycle metadata.
func before_suite(info: GDTestSuiteInfo) -> void:
	if info.name.is_empty():
		return


## Receives case lifecycle metadata.
func after_case(info: GDTestCaseInfo) -> void:
	message(info.name)


## Verifies parameter migration and a simple fluent assertion.
func test_adds(value: int, expected: int) -> void:
	await sleep(0.0)
	expect_equal(value + 1, expected, "sum mismatch")


## Supplies parameter rows for `test_adds`.
# gdkit:ignore = function-name
func test_adds__cases() -> Array[Variant]:
	return params_provider()
