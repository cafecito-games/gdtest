extends GDTest


## Waits long enough for cancellation and exact-process termination tests.
func test_cancel_me() -> void:
	await sleep(10.0)
	expect_true(true)
