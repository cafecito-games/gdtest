extends GDTest


## Verifies that a zero timeout means no deadline.
func test_until_zero_timeout_succeeds() -> void:
	var attempts: Array[int] = [0]
	var condition: Callable = func() -> bool:
		attempts[0] += 1
		return attempts[0] >= 2
	expect_equal(await until(condition, 0.0), OK)


## Reports a negative sleep duration as a framework error.
func test_sleep_rejects_negative_duration() -> void:
	expect_equal(await sleep(-1.0), ERR_INVALID_PARAMETER)


## Reports a non-finite sleep duration as a framework error.
func test_sleep_rejects_non_finite_duration() -> void:
	expect_equal(await sleep(NAN), ERR_INVALID_PARAMETER)


## Reports a negative polling timeout as a framework error.
func test_until_rejects_negative_timeout() -> void:
	expect_equal(await until(func() -> bool: return true, -1.0), ERR_INVALID_PARAMETER)


## Reports a non-finite polling timeout as a framework error.
func test_until_rejects_non_finite_timeout() -> void:
	expect_equal(await until(func() -> bool: return true, NAN), ERR_INVALID_PARAMETER)


## Reports a negative polling interval as a framework error.
func test_until_rejects_negative_interval() -> void:
	expect_equal(await until(func() -> bool: return true, 1.0, -1.0), ERR_INVALID_PARAMETER)


## Reports a non-finite polling interval as a framework error.
func test_until_rejects_non_finite_interval() -> void:
	expect_equal(await until(func() -> bool: return true, 1.0, NAN), ERR_INVALID_PARAMETER)
