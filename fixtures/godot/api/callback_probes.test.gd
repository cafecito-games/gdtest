extends GDTest

## Exercises case-scoped callback probes across arities, waits, closure, and cross-source ordering.


## Emitter used to verify shared invocation sequence numbers across signal and callback probes.
class EventSource:
	extends Object

	## Emitted with one status payload.
	signal step(label: String)


## Asynchronous service that accepts injected completion and error callbacks.
class AsyncOperation:
	extends RefCounted

	var _tree: SceneTree


	## Keeps the tree used to schedule deferred callback delivery.
	func _init(tree: SceneTree) -> void:
		_tree = tree


	## Invokes `on_complete` after one process frame when `succeed` is true, or `on_error` otherwise.
	func run(succeed: bool, payload: String, on_complete: Callable, on_error: Callable) -> void:
		await _tree.process_frame
		if succeed:
			on_complete.call(payload, 200)
		else:
			on_error.call("failed:" + payload)


## Minimal GDTest subclass instantiated locally to verify RefCounted release on probe closure.
class IsolatedSuite:
	extends GDTest


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Callback probes"


## Defaults to zero arguments and records synchronous invocations before returning to the caller.
func test_default_zero_arity_records_synchronously() -> void:
	var probe: GDTestCallbackProbe = callback_probe()
	var handler: Callable = probe.callback()
	expect_false(probe.is_closed())
	expect_true(handler.is_valid())
	expect_equal(handler.get_argument_count(), 0)
	expect_same(probe.callback(), handler)
	var observed_during_caller: Array[int] = []
	handler.call()
	observed_during_caller.append(probe.invocation_count())
	handler.call()
	observed_during_caller.append(probe.invocation_count())
	expect_equal(observed_during_caller, [1, 2])
	expect_equal(probe.arguments_at(0), [])
	expect_equal(probe.arguments_at(1), [])


## Compiles and records exact-arity callbacks from zero through eight arguments.
func test_every_supported_arity_compiles_and_records_ordered_arguments() -> void:
	var probe_0: GDTestCallbackProbe = callback_probe(0)
	var probe_1: GDTestCallbackProbe = callback_probe(1)
	var probe_2: GDTestCallbackProbe = callback_probe(2)
	var probe_3: GDTestCallbackProbe = callback_probe(3)
	var probe_4: GDTestCallbackProbe = callback_probe(4)
	var probe_5: GDTestCallbackProbe = callback_probe(5)
	var probe_6: GDTestCallbackProbe = callback_probe(6)
	var probe_7: GDTestCallbackProbe = callback_probe(7)
	var probe_8: GDTestCallbackProbe = callback_probe(8)
	var all_probes: Array[GDTestCallbackProbe] = [
		probe_0,
		probe_1,
		probe_2,
		probe_3,
		probe_4,
		probe_5,
		probe_6,
		probe_7,
		probe_8,
	]
	for arity: int in range(all_probes.size()):
		expect_equal(all_probes[arity].callback().get_argument_count(), arity)
	probe_0.callback().call()
	probe_1.callback().call("a")
	probe_2.callback().call("a", 2)
	probe_3.callback().call("a", 2, true)
	probe_4.callback().call("a", 2, true, Vector2i(3, 4))
	probe_5.callback().call(1, 2, 3, 4, 5)
	probe_6.callback().call(1, 2, 3, 4, 5, 6)
	probe_7.callback().call(1, 2, 3, 4, 5, 6, 7)
	probe_8.callback().call(1, 2, 3, 4, 5, 6, 7, 8)
	expect_equal(probe_0.arguments_at(0), [])
	expect_equal(probe_1.arguments_at(0), ["a"])
	expect_equal(probe_2.arguments_at(0), ["a", 2])
	expect_equal(probe_3.arguments_at(0), ["a", 2, true])
	expect_equal(probe_4.arguments_at(0), ["a", 2, true, Vector2i(3, 4)])
	expect_equal(probe_5.arguments_at(0), [1, 2, 3, 4, 5])
	expect_equal(probe_6.arguments_at(0), [1, 2, 3, 4, 5, 6])
	expect_equal(probe_7.arguments_at(0), [1, 2, 3, 4, 5, 6, 7])
	expect_equal(probe_8.arguments_at(0), [1, 2, 3, 4, 5, 6, 7, 8])


## Returns defensive invocation lists and snapshots nested mutable collections at call time.
func test_invocations_and_nested_arguments_are_defensive_copies() -> void:
	var probe: GDTestCallbackProbe = callback_probe(2)
	var nested_items: Array[int] = [1, 2]
	var extra_items: Array[int] = [3]
	var nested_payload: Dictionary[String, Variant] = { "items": nested_items }
	probe.callback().call(nested_payload, "initial")
	nested_items.append(99)
	nested_payload["extra"] = extra_items
	var history: Array[GDTestInvocation] = probe.invocations()
	expect_equal(history.size(), 1)
	expect_equal(history[0].sequence, 1)
	var recorded_arguments: Array[Variant] = history[0].arguments
	expect_equal(recorded_arguments, [{ "items": [1, 2] }, "initial"])
	recorded_arguments.append("tampered")
	var recorded_dictionary: Dictionary[String, Variant] = recorded_arguments[0]
	var recorded_items: Array[int] = recorded_dictionary["items"]
	recorded_items[0] = -1
	expect_equal(probe.arguments_at(0), [{ "items": [1, 2] }, "initial"])
	var direct_arguments: Array[Variant] = probe.arguments_at(0)
	direct_arguments[1] = "rewritten"
	expect_equal(probe.arguments_at(0), [{ "items": [1, 2] }, "initial"])
	history.clear()
	expect_equal(probe.invocation_count(), 1)


## Awaits asynchronous completion and error callbacks without polling.
func test_wait_until_count_handles_async_completion_and_error_callbacks() -> void:
	var operation := AsyncOperation.new(get_tree())
	var on_complete: GDTestCallbackProbe = callback_probe(2)
	var on_error: GDTestCallbackProbe = callback_probe(1)
	operation.run(true, "sync-profile", on_complete.callback(), on_error.callback())
	var completed: bool = await on_complete.wait_until_count(1)
	expect_true(completed)
	expect_equal(on_complete.arguments_at(0), ["sync-profile", 200])
	expect_equal(on_error.invocation_count(), 0)
	operation.run(false, "sync-profile", on_complete.callback(), on_error.callback())
	var failed: bool = await on_error.wait_until_count(1, 1.0)
	expect_true(failed)
	expect_equal(on_error.arguments_at(0), ["failed:sync-profile"])
	expect_equal(on_complete.invocation_count(), 1)


## Returns immediately when the requested invocation count is already satisfied.
func test_wait_until_count_returns_immediately_when_already_satisfied() -> void:
	var probe: GDTestCallbackProbe = callback_probe(1)
	expect_true(await probe.wait_until_count(0))
	probe.callback().call("ready")
	expect_true(await probe.wait_until_count(1))
	probe.close()
	expect_true(await probe.wait_until_count(1))
	expect_false(await probe.wait_until_count(2))


## Returns false without inventing an assertion when a positive wait deadline expires.
func test_wait_until_count_returns_false_on_deadline_expiry() -> void:
	var probe: GDTestCallbackProbe = callback_probe(0)
	var reached: bool = await probe.wait_until_count(1, 0.01)
	expect_false(reached)
	expect_equal(probe.invocation_count(), 0)


## Wakes a pending wait and returns false when the probe is closed before reaching count.
func test_closing_probe_wakes_pending_unbounded_wait() -> void:
	var probe: GDTestCallbackProbe = callback_probe(0)
	var close_later: Callable = func() -> void:
		await sleep(0.0)
		probe.close()
	close_later.call()
	var reached: bool = await probe.wait_until_count(1)
	expect_false(reached)
	expect_true(probe.is_closed())


## Closes idempotently, preserves prior records, and ignores post-close invocations.
func test_close_is_idempotent_and_blocks_further_recording() -> void:
	var probe: GDTestCallbackProbe = callback_probe(1)
	var retained: Callable = probe.callback()
	retained.call("before-close")
	probe.close()
	expect_true(probe.is_closed())
	probe.close()
	expect_true(probe.is_closed())
	retained.call("after-close")
	probe.callback().call("after-second-close")
	expect_equal(probe.invocation_count(), 1)
	expect_equal(probe.arguments_at(0), ["before-close"])


## Shares one monotonic sequence across signal probes, callback probes, and generated doubles.
func test_a_cross_source_invocations_share_one_case_sequence() -> void:
	var source: EventSource = EventSource.new()
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_return(true)
	inventory.gdtest.clear.then_return()
	var signal_probe: GDTestSignalProbe = watch_signal(source.step)
	var progress_probe: GDTestCallbackProbe = callback_probe(2)
	var finish_probe: GDTestCallbackProbe = callback_probe(1)
	source.step.emit("begin")
	expect_true(inventory.add("apple", 2))
	progress_probe.callback().call("stocked", 2)
	inventory.clear()
	source.step.emit("cleared")
	finish_probe.callback().call("done")
	var signal_records: Array[GDTestInvocation] = signal_probe.emissions()
	var add_records: Array[GDTestInvocation] = inventory.gdtest.add.calls()
	var progress_records: Array[GDTestInvocation] = progress_probe.invocations()
	var clear_records: Array[GDTestInvocation] = inventory.gdtest.clear.calls()
	var finish_records: Array[GDTestInvocation] = finish_probe.invocations()
	expect_equal(signal_records[0].sequence, 1)
	expect_equal(add_records[0].sequence, 2)
	expect_equal(progress_records[0].sequence, 3)
	expect_equal(clear_records[0].sequence, 4)
	expect_equal(signal_records[1].sequence, 5)
	expect_equal(finish_records[0].sequence, 6)
	source.free()


## Resets the shared sequence at the start of the next case on the same persistent worker.
func test_b_next_case_resets_sequence_after_prior_probes() -> void:
	var probe: GDTestCallbackProbe = callback_probe(1)
	probe.callback().call("fresh")
	expect_equal(probe.invocations()[0].sequence, 1)


## Releases the test instance and probe once closed even while project code keeps the callable.
func test_closed_callable_does_not_retain_test_instance_or_probe() -> void:
	var isolated: GDTest = IsolatedSuite.new()
	isolated._gdtest_configure(get_tree(), "res://isolated.test.gd", Callable())
	isolated._gdtest_begin_case()
	var probe: GDTestCallbackProbe = isolated.callback_probe(1)
	probe.callback().call("recorded")
	var retained: Callable = probe.callback()
	var weak_test: WeakRef = weakref(isolated)
	var weak_probe: WeakRef = weakref(probe)
	var summary: Dictionary[String, Variant] = await isolated._gdtest_end_case()
	expect_equal(summary["status"], "void")
	probe = null
	isolated = null
	await sleep(0.0)
	expect_null(weak_probe.get_ref())
	expect_null(weak_test.get_ref())
	expect_true(retained.is_valid())
	retained.call("late")
