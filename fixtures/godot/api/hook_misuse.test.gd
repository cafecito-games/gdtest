extends GDTest

## Exercises out-of-case helper misuse in execution hooks and registration misuse in cases.

## Emitted during suite setup to verify detached signal probes stay inert.
signal sample_emitted(value: int)

## Records whether `watch_signal` in `before_suite` returned an inert probe.
var _signal_probe_inert: bool = false
## Records whether `double_context` in `before_suite` returned a closed context.
var _double_context_closed: bool = false


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Hook misuse"


## Registers one nested suite with a passing case and an illegal in-case registration call.
func suite() -> void:
	define(
			"nested suite",
			func() -> void:
				test(
						"nested passing",
						func() -> void:
							expect_true(_signal_probe_inert)
							expect_true(_double_context_closed)
				)
				test(
						"registration inside case",
						func() -> void:
							define("illegal suite", func() -> void: pass)
							test("illegal case", func() -> void: pass)
							expect_true(true)
				)
	)


## Calls assertion and async helpers outside an active case before the file runs.
func before_all() -> void:
	expect_true(true, "before_all assertion")
	await sleep(-1.0)


## Calls outcome and probe helpers outside an active case when entering suites.
func before_suite(suite_info: GDTestSuiteInfo) -> void:
	if suite_info.name == "Hook misuse":
		ok("root before_suite ok")
		return
	var signal_probe: GDTestSignalProbe = watch_signal(sample_emitted)
	sample_emitted.emit(7)
	var context: GDTestDoubleContext = double_context()
	_signal_probe_inert = signal_probe.emission_count() == 0
	_double_context_closed = not context.is_open()


## Calls message, skip, and registration helpers outside an active case when leaving suites.
func after_suite(suite_info: GDTestSuiteInfo) -> void:
	if suite_info.name == "nested suite":
		message("nested after_suite message")
		skip("nested after_suite skip")
		return
	define("late root suite", func() -> void: pass)


## Calls fail and todo helpers outside an active case after all suites complete.
func after_all() -> void:
	fail("after_all explicit fail")
	todo("after_all todo")


## Verifies root-level cases still execute and pass despite hook framework errors.
func test_root_passing() -> void:
	expect_equal(2 + 2, 4)
