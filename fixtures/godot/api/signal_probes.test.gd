extends GDTest

## Exercises case-scoped signal probes against signals declared at every supported arity.


## Persistent emitter whose connections must be cleaned up by every case that watches it.
class SignalSource:
	extends Object

	## Emitted without arguments.
	signal zero

	## Emitted with one argument.
	signal one(first: Variant)

	## Emitted with two arguments.
	signal two(first: Variant, second: Variant)

	## Emitted with three arguments.
	signal three(first: Variant, second: Variant, third: Variant)

	## Emitted with four arguments.
	signal four(first: Variant, second: Variant, third: Variant, fourth: Variant)

	## Emitted with five arguments.
	signal five(first: Variant, second: Variant, third: Variant, fourth: Variant, fifth: Variant)

	## Emitted with six arguments.
	signal six(
			first: Variant,
			second: Variant,
			third: Variant,
			fourth: Variant,
			fifth: Variant,
			sixth: Variant
	)

	## Emitted with seven arguments.
	signal seven(
			first: Variant,
			second: Variant,
			third: Variant,
			fourth: Variant,
			fifth: Variant,
			sixth: Variant,
			seventh: Variant
	)

	## Emitted with eight arguments.
	signal eight(
			first: Variant,
			second: Variant,
			third: Variant,
			fourth: Variant,
			fifth: Variant,
			sixth: Variant,
			seventh: Variant,
			eighth: Variant
	)


var _source: SignalSource


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Signal probes"


## Creates the persistent emitter before the first case runs.
func before_all() -> void:
	_source = SignalSource.new()


## Frees the persistent emitter after every case in this file has finished.
func after_all() -> void:
	_source.free()


## Leaves a probe connected so the next case can prove the worker starts clean.
func test_a_probe_connects_and_is_left_for_cleanup() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.zero)
	expect_equal(_source.zero.get_connections().size(), 1)
	expect_equal(probe.emission_count(), 0)


## Proves the previous case's probe was disconnected before this case began.
func test_b_next_case_starts_without_probe_connections() -> void:
	expect_equal(_source.zero.get_connections().size(), 0)
	expect_equal(_source.one.get_connections().size(), 0)


## Records a zero-argument emission as an empty argument list.
func test_zero_argument_signal_records_empty_arguments() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.zero)
	expect_equal(probe.emission_count(), 0)
	_source.zero.emit()
	_source.zero.emit()
	expect_equal(probe.emission_count(), 2)
	expect_equal(probe.arguments_at(1), [])
	probe.stop_recording()


## Infers each declared arity from signal metadata and preserves argument order.
func test_every_supported_arity_records_ordered_arguments() -> void:
	var one_probe: GDTestSignalProbe = watch_signal(_source.one)
	var two_probe: GDTestSignalProbe = watch_signal(_source.two)
	var three_probe: GDTestSignalProbe = watch_signal(_source.three)
	var four_probe: GDTestSignalProbe = watch_signal(_source.four)
	var five_probe: GDTestSignalProbe = watch_signal(_source.five)
	var six_probe: GDTestSignalProbe = watch_signal(_source.six)
	var seven_probe: GDTestSignalProbe = watch_signal(_source.seven)
	var eight_probe: GDTestSignalProbe = watch_signal(_source.eight)
	_source.one.emit(1)
	_source.two.emit(1, 2)
	_source.three.emit(1, 2, 3)
	_source.four.emit(1, 2, 3, 4)
	_source.five.emit(1, 2, 3, 4, 5)
	_source.six.emit(1, 2, 3, 4, 5, 6)
	_source.seven.emit(1, 2, 3, 4, 5, 6, 7)
	_source.eight.emit(1, 2, 3, 4, 5, 6, 7, 8)
	expect_equal(one_probe.arguments_at(0), [1])
	expect_equal(two_probe.arguments_at(0), [1, 2])
	expect_equal(three_probe.arguments_at(0), [1, 2, 3])
	expect_equal(four_probe.arguments_at(0), [1, 2, 3, 4])
	expect_equal(five_probe.arguments_at(0), [1, 2, 3, 4, 5])
	expect_equal(six_probe.arguments_at(0), [1, 2, 3, 4, 5, 6])
	expect_equal(seven_probe.arguments_at(0), [1, 2, 3, 4, 5, 6, 7])
	expect_equal(eight_probe.arguments_at(0), [1, 2, 3, 4, 5, 6, 7, 8])
	for probe: GDTestSignalProbe in [
		one_probe,
		two_probe,
		three_probe,
		four_probe,
		five_probe,
		six_probe,
		seven_probe,
		eight_probe,
	]:
		probe.stop_recording()


## Gives invocations case-local sequence numbers and copies every returned list.
func test_invocations_have_sequences_and_defensive_copies() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.two)
	_source.two.emit("a", 1)
	_source.two.emit("b", 2)
	var emissions: Array[GDTestInvocation] = probe.emissions()
	expect_equal(emissions[0].sequence, 1)
	expect_equal(emissions[1].sequence, 2)
	var arguments: Array[Variant] = emissions[0].arguments
	arguments.append("mutated")
	expect_equal(emissions[0].arguments, ["a", 1])
	var copied_arguments: Array[Variant] = probe.arguments_at(1)
	copied_arguments[0] = "changed"
	expect_equal(probe.arguments_at(1), ["b", 2])
	emissions.clear()
	expect_equal(probe.emission_count(), 2)
	probe.stop_recording()


## Keeps pre-existing connections and records the same emission for independent probes.
func test_independent_probes_coexist_with_existing_connections() -> void:
	var hits: Array[int] = [0]
	var counter: Callable = func() -> void:
		hits[0] += 1
	_source.zero.connect(counter)
	var first: GDTestSignalProbe = watch_signal(_source.zero)
	var second: GDTestSignalProbe = watch_signal(_source.zero)
	_source.zero.emit()
	expect_equal(first.emission_count(), 1)
	expect_equal(second.emission_count(), 1)
	expect_equal(hits[0], 1)
	first.stop_recording()
	_source.zero.emit()
	expect_equal(first.emission_count(), 1)
	expect_equal(second.emission_count(), 2)
	expect_equal(hits[0], 2)
	second.stop_recording()
	_source.zero.disconnect(counter)


## Stops recording after a manual disconnect and tolerates repeated disconnects.
func test_manual_disconnect_is_idempotent() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.zero)
	probe.stop_recording()
	probe.stop_recording()
	_source.zero.emit()
	expect_equal(probe.emission_count(), 0)
	expect_equal(_source.zero.get_connections().size(), 0)


## Resumes when an emission occurs before the deadline.
func test_wait_until_count_resumes_on_emission() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.one)
	var emit_later: Callable = func() -> void:
		await sleep(0.0)
		_source.one.emit("ready")
	emit_later.call()
	var reached: bool = await probe.wait_until_count(1)
	expect_true(reached)
	expect_equal(probe.arguments_at(0), ["ready"])
	probe.stop_recording()


## Returns false when the requested count is not reached before the deadline.
func test_wait_until_count_reports_timeout() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.zero)
	var reached: bool = await probe.wait_until_count(1, 0.01)
	expect_false(reached)
	expect_equal(probe.emission_count(), 0)
	probe.stop_recording()


## Returns immediately when the count is already satisfied.
func test_wait_until_count_returns_true_for_recorded_emissions() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.zero)
	_source.zero.emit()
	var reached: bool = await probe.wait_until_count(1)
	expect_true(reached)
	probe.stop_recording()


## Infers arity for a built-in signal from the engine's signal metadata.
func test_built_in_signal_arity_is_inferred() -> void:
	var timer: Timer = Timer.new()
	var timeout_probe: GDTestSignalProbe = watch_signal(timer.timeout)
	timer.timeout.emit()
	expect_equal(timeout_probe.arguments_at(0), [])
	var parent: Node = Node.new()
	var child_probe: GDTestSignalProbe = watch_signal(parent.child_entered_tree)
	var child: Node = Node.new()
	parent.child_entered_tree.emit(child)
	expect_equal(child_probe.emission_count(), 1)
	expect_same(child_probe.arguments_at(0)[0], child)
	child.free()
	parent.free()
	timer.free()


## Preserves recorded values after the owner is freed and cleanup stays idempotent.
func test_freed_owner_preserves_recorded_values() -> void:
	var source: SignalSource = SignalSource.new()
	var probe: GDTestSignalProbe = watch_signal(source.one)
	source.one.emit("kept")
	source.free()
	expect_equal(probe.arguments_at(0), ["kept"])
	probe.stop_recording()
	probe.stop_recording()
	expect_equal(probe.emission_count(), 1)


## Snapshots nested collections at emission so later mutation cannot change recorded values.
func test_nested_arguments_are_snapshotted() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.one)
	var payload: Array[int] = [1]
	_source.one.emit(payload)
	payload[0] = 2
	var recorded: Array[Variant] = probe.arguments_at(0)
	expect_equal(recorded, [[1]])
	var nested: Array[Variant] = recorded[0]
	nested[0] = 3
	expect_equal(probe.arguments_at(0), [[1]])
	probe.stop_recording()
