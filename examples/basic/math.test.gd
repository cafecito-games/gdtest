extends GDTest


## Supplies named addition rows.
# gdkit:ignore = function-name
func test_adds__cases() -> Dictionary[String, Array]:
	return { "small": [1, 2, 3], "zero": [0, 0, 0] }


## Verifies integer addition.
func test_adds(left: int, right: int, expected: int) -> void:
	expect_equal(left + right, expected)


## Registers an asynchronous dynamic case.
func suite() -> void:
	test(
			"async dynamic case",
			func() -> void:
				await sleep()
				expect_true(true)
	)
