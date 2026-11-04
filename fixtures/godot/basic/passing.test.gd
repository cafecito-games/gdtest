extends GDTest


## Verifies the direct equality assertion contract.
func test_equal_values() -> void:
	expect_equal(2 + 2, 4)


## Verifies asynchronous tests and the inherited SceneTree helper.
func test_async_frame() -> void:
	expect_equal(await sleep(), OK)
	expect_true(true)
