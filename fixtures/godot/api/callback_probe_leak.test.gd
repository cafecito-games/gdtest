extends GDTest

## Proves that a callback retained past its case and run is a bounded no-op without leaks.

## Metadata key storing the retained callable on the persistent worker root viewport.
const _CALLBACK_KEY: StringName = &"gdtest_retained_callback_probe"
## Metadata key storing a weak reference to the first run's GDTest suite instance.
const _SUITE_KEY: StringName = &"gdtest_retained_callback_suite"
## Metadata key storing a weak reference to the first case's callback probe.
const _PROBE_KEY: StringName = &"gdtest_retained_callback_instance"


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Callback probe leaks"


## Records one in-case invocation and stores the callable on the persistent root viewport.
func test_a_registers_callback_on_persistent_root() -> void:
	var probe: GDTestCallbackProbe = callback_probe(1)
	var handler: Callable = probe.callback()
	handler.call("in-case")
	expect_equal(probe.invocation_count(), 1)
	var root: Window = get_tree().root
	root.set_meta(_CALLBACK_KEY, handler)
	root.set_meta(_SUITE_KEY, weakref(self))
	root.set_meta(_PROBE_KEY, weakref(probe))


## Invokes the stale callable over one thousand times in a later case without cross-case effects.
func test_b_late_invocations_in_later_case_are_bounded_no_ops() -> void:
	var root: Window = get_tree().root
	var weak_probe: WeakRef = root.get_meta(_PROBE_KEY)
	expect_null(weak_probe.get_ref())
	var retained: Callable = root.get_meta(_CALLBACK_KEY)
	expect_true(retained.is_valid())
	for iteration: int in range(1100):
		retained.call("late-" + str(iteration))
	var fresh: GDTestCallbackProbe = callback_probe(1)
	fresh.callback().call("fresh")
	expect_equal(fresh.invocation_count(), 1)
	expect_equal(fresh.invocations()[0].sequence, 1)


## Runs in a second worker invocation and verifies the prior suite was freed despite retention.
func test_c_second_run_observes_freed_prior_suite_and_bounded_late_calls() -> void:
	var root: Window = get_tree().root
	var weak_suite: WeakRef = root.get_meta(_SUITE_KEY)
	expect_null(weak_suite.get_ref())
	var retained: Callable = root.get_meta(_CALLBACK_KEY)
	expect_true(retained.is_valid())
	for iteration: int in range(1500):
		retained.call("second-run-" + str(iteration))
	root.remove_meta(_CALLBACK_KEY)
	root.remove_meta(_SUITE_KEY)
	root.remove_meta(_PROBE_KEY)
	var fresh: GDTestCallbackProbe = callback_probe(0)
	fresh.callback().call()
	expect_equal(fresh.invocation_count(), 1)


## Executes with no late callback invocations so terminal metadata stays clean.
func test_d_clean_run_omits_late_callback_metadata() -> void:
	var probe: GDTestCallbackProbe = callback_probe(0)
	probe.callback().call()
	expect_equal(probe.invocation_count(), 1)
