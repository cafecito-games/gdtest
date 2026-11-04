extends GDTest


## Exercises a failed built-in assertion without terminating the worker.
func test_builtin_assert_error() -> void:
	assert(false, "gdtest built-in assertion marker")


## Exercises an uncaught GDScript runtime error in a case body.
func test_runtime_error() -> void:
	var values: Array[int] = []
	expect_equal(values[1], 1)


## Exercises the external case deadline with a genuinely suspended method.
func test_case_timeout() -> void:
	await sleep(10.0)


## Exercises the process watchdog when user code never yields to the worker.
func test_busy_loop_timeout() -> void:
	var spins: int = 0
	var deadline_usec: int = Time.get_ticks_usec() + 10000000
	while Time.get_ticks_usec() < deadline_usec:
		spins += 1
	expect_true(spins > 0)


## Proves a runtime error does not prevent later cases from executing.
func test_worker_continues_after_error() -> void:
	expect_true(true)
