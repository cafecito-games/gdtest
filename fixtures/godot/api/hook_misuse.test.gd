extends GDTest

## Exercises out-of-case helper misuse across execution hooks and before-hook gating.

## Emitted during suite setup to verify detached signal probes stay inert.
signal sample_emitted(value: int)

## Records whether `before_all` executed before suites opened.
var _before_all_ran: bool = false
## Records whether `watch_signal` in `before_suite` returned an inert probe.
var _signal_probe_inert: bool = false
## Records whether `double_context` in `before_suite` returned a closed context.
var _double_context_closed: bool = false
## Records whether a case inside a failed `before_suite` erroneously ran.
var _blocked_child_ran: bool = false
## Records whether a nested child `before_suite` inside a failed suite erroneously ran.
var _blocked_grandchild_before_suite_ran: bool = false
## Records whether a case body erroneously ran after `before_case` recorded an error.
var _before_case_body_ran: bool = false
## Records whether `after_case` still ran as cleanup after a `before_case` error.
var _after_case_cleanup_ran: bool = false


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Hook misuse"


## Registers a blocked suite, a healthy sibling suite, and in-case error cases.
func suite() -> void:
	define(
			"blocked suite",
			func() -> void:
				test(
						"blocked child",
						func() -> void:
							_blocked_child_ran = true
							expect_true(true)
				)
				define(
						"blocked grandchild",
						func() -> void:
							test(
									"blocked grandchild case",
									func() -> void:
										_blocked_child_ran = true
										expect_true(true)
							)
				)
	)
	define(
			"healthy suite",
			func() -> void:
				test(
						"healthy passing",
						func() -> void:
							expect_true(_before_all_ran)
							expect_true(_signal_probe_inert)
							expect_true(_double_context_closed)
							expect_false(_blocked_child_ran)
							expect_false(_blocked_grandchild_before_suite_ran)
				)
				test(
						"registration inside case",
						func() -> void:
							define("illegal suite", func() -> void: pass)
							test("illegal case", func() -> void: pass)
							expect_true(true)
				)
				test(
						"before_case error skips body",
						func() -> void:
							_before_case_body_ran = true
							expect_true(true)
				)
	)


## Marks file setup complete without error so suite-level hooks execute.
func before_all() -> void:
	_before_all_ran = true


## Exercises out-of-case helpers in `blocked suite` while leaving other suites healthy.
func before_suite(suite_info: GDTestSuiteInfo) -> void:
	if suite_info.name == "blocked grandchild":
		_blocked_grandchild_before_suite_ran = true
		return
	if suite_info.name != "blocked suite":
		return
	expect_true(true, "blocked before_suite assertion")
	await sleep(-1.0)
	var signal_probe: GDTestSignalProbe = watch_signal(sample_emitted)
	sample_emitted.emit(7)
	var context: GDTestDoubleContext = double_context()
	_signal_probe_inert = signal_probe.emission_count() == 0
	_double_context_closed = not context.is_open()


## Triggers a framework error in `before_case` for the targeted test only.
func before_case(case_info: GDTestCaseInfo) -> void:
	if case_info.name == "before_case error skips body":
		await sleep(-2.0)


## Verifies `after_case` cleanup still runs when `before_case` errored.
func after_case(case_info: GDTestCaseInfo) -> void:
	if case_info.name == "before_case error skips body":
		_after_case_cleanup_ran = not _before_case_body_ran


## Calls message, skip, ok, and registration helpers outside an active case when leaving suites.
func after_suite(suite_info: GDTestSuiteInfo) -> void:
	if suite_info.name == "blocked suite":
		message("blocked after_suite message")
		skip("blocked after_suite skip")
		return
	if suite_info.name == "healthy suite":
		ok("healthy after_suite ok")
		return
	define("late root suite", func() -> void: pass)


## Calls fail and todo helpers outside an active case after all suites complete.
func after_all() -> void:
	fail("after_all explicit fail")
	todo("after_all todo")


## Verifies root-level cases still execute after a sibling nested suite failed setup.
func test_root_passing() -> void:
	expect_false(_before_case_body_ran)
	expect_true(_after_case_cleanup_ran)
	expect_equal(2 + 2, 4)
