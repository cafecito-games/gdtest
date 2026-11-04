extends GDTest


## Exercises a standalone skipped case.
func test_skip_outcome() -> void:
	skip("not available", { "reason": "fixture" })


## Exercises a standalone todo case.
func test_todo_outcome() -> void:
	todo("future behavior", { "issue": 1 })


## Exercises a case without assertions or explicit outcomes.
func test_void_outcome() -> void:
	message("void remains observable")
	# This fixture intentionally verifies direct engine-output capture.
	# gdkit:ignore = no-engine-logging
	print("gdtest-output-marker")


## Exercises timeout and asynchronous condition helper behavior.
func test_until_contract() -> void:
	expect_equal(await until(func() -> bool: return false, 0.001), ERR_TIMEOUT)
	var frames: Array[int] = [0]
	var condition: Callable = func() -> bool:
		frames[0] += 1
		await sleep(0.0)
		return frames[0] >= 2
	expect_equal(await until(condition), OK)
