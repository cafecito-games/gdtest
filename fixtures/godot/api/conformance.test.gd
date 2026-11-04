extends GDTest

var _events: Array[String] = []
var _async_registration_finished: bool = false
var _async_case_finished: bool = false
var _helper_ready: bool = false


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "API conformance"


## Registers nested, parameterized, and asynchronous dynamic cases.
func suite() -> void:
	await sleep(0.0)
	define(
			"outer suite",
			func() -> void:
				await sleep(0.0)
				define(
						"inner/suite",
						func() -> void:
							test(
									"async case",
									func() -> void:
										await sleep(0.0)
										_async_case_finished = true
										expect_true(_async_registration_finished)
							)
							test_each(
									"dynamic rows",
									{ "z": [2, 3, 5], "a/b": [1, 4, 5] },
									func(left: int, right: int, expected: int) -> void:
										expect_equal(left + right, expected)
							)
				)
	)
	_async_registration_finished = true


## Records file-level setup.
func before_all() -> void:
	_events.append("before_all")


## Records root and nested suite entry in order.
func before_suite(suite_info: GDTestSuiteInfo) -> void:
	_events.append("before_suite:" + suite_info.name)


## Records every case entry and verifies metadata defensive copies.
func before_case(case_info: GDTestCaseInfo) -> void:
	_events.append("before_case:" + case_info.name)
	var parameters: Array[Variant] = case_info.parameters
	parameters.clear()
	expect_true(case_info.file.begins_with("res://"))
	expect_false(case_info.id.begins_with("/"))


## Records every case exit.
func after_case(case_info: GDTestCaseInfo) -> void:
	_events.append("after_case:" + case_info.name)


## Records nested and root suite exit in order.
func after_suite(suite_info: GDTestSuiteInfo) -> void:
	_events.append("after_suite:" + suite_info.name)


## Records file-level cleanup.
func after_all() -> void:
	_events.append("after_all")


## Verifies dynamic children ran before source-ordered method tests.
func test_dynamic_children_run_first() -> void:
	expect_true(_async_case_finished)
	expect_contains(_events, "after_suite:inner/suite")
	expect_contains(_events, "after_suite:outer suite")


## Exercises every passing assertion and explicit outcome contract.
func test_assertion_surface() -> void:
	var shared: Array[int] = [1]
	expect(true)
	expect_not(false)
	expect_true(true)
	expect_false(false)
	expect_equal({ "value": 1 }, { "value": 1 })
	expect_not_equal(1, 2)
	expect_same(shared, shared)
	expect_not_same(shared, shared.duplicate())
	expect_approx(1.0, 1.001, 0.01)
	expect_not_approx(1.0, 1.1, 0.01)
	expect_null(null)
	expect_not_null(self)
	expect_empty([])
	expect_not_empty([1])
	expect_contains("gdtest", "test")
	expect_does_not_contain(PackedInt32Array([1, 2]), 3)
	ok("structured pass", { "kind": "api" })
	message("messages preserve order")


## Verifies named provider rows and argument delivery.
func test_named_provider(left: int, right: int, expected: int) -> void:
	expect_equal(left + right, expected)


## Returns named rows deliberately out of order.
# Provider suffix is a reserved gdtest public API spelling.
# gdkit:ignore = function-name
func test_named_provider__cases() -> Dictionary[String, Variant]:
	return { "z last": [4, 5, 9], "a/first": [1, 2, 3] }


## Verifies indexed provider rows and argument delivery.
func test_indexed_provider(value: String, expected: String) -> void:
	expect_equal(value.capitalize(), expected)


## Returns indexed parameter rows.
# Provider suffix is a reserved gdtest public API spelling.
# gdkit:ignore = function-name
func test_indexed_provider__cases() -> Array[Variant]:
	return [["hello", "Hello"], ["hello world", "Hello World"]]


## Verifies frame-based helpers and monotonic timing.
func test_async_helpers() -> void:
	var started: float = time()
	expect_equal(await sleep(0.0), OK)
	_helper_ready = false
	get_tree().process_frame.connect(func() -> void: _helper_ready = true, CONNECT_ONE_SHOT)
	expect_equal(await until(func() -> bool: return _helper_ready, 1.0), OK)
	expect_true(time() >= started)
