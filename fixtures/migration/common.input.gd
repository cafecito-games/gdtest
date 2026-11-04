extends VestTest


# Existing comments and layout stay byte-for-byte outside edited spans.
## Returns Vest parameter rows.
func params_provider() -> Array[Variant]:
	return [[1, 2], [4, 5]]


## Receives suite lifecycle metadata.
func before_suite(info: VestDefs.Suite) -> void:
	Vest.message(info.name)


## Receives case lifecycle metadata.
func after_case(info: VestDefs.Case) -> void:
	Vest.message(info.name)


## Verifies parameter migration and a simple fluent assertion.
func test_adds(value: int, expected: int, _params: String = "params_provider") -> void:
	await Vest.sleep(0.0)
	assert_that(value + 1).is_equal_to(expected, "sum mismatch")
