extends GDTest

## Exercises invalid signal probe inputs; every case must report a framework error.


## Emitter used by the invalid-input cases.
class SignalSource:
	extends Object

	## Emitted with one argument.
	signal one(first: Variant)

	## Emitted with nine arguments, one more than the supported maximum.
	signal nine(
			first: Variant,
			second: Variant,
			third: Variant,
			fourth: Variant,
			fifth: Variant,
			sixth: Variant,
			seventh: Variant,
			eighth: Variant,
			ninth: Variant
	)


var _source: SignalSource


## Creates the emitter before the first invalid-input case runs.
func before_all() -> void:
	_source = SignalSource.new()


## Frees the emitter after every invalid-input case has finished.
func after_all() -> void:
	_source.free()


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Signal probe failures"


## Rejects a null signal and returns an inert probe.
func test_null_signal_reports_framework_error() -> void:
	var probe: GDTestSignalProbe = watch_signal(Signal())
	expect_equal(probe.emission_count(), 0)


## Rejects a signal whose owner has already been freed.
func test_freed_owner_reports_framework_error() -> void:
	var doomed: SignalSource = SignalSource.new()
	var orphaned: Signal = doomed.one
	doomed.free()
	var probe: GDTestSignalProbe = watch_signal(orphaned)
	expect_equal(probe.emission_count(), 0)


## Rejects a signal with more arguments than the probe supports.
func test_arity_above_eight_reports_framework_error() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.nine)
	expect_equal(probe.emission_count(), 0)


## Rejects a negative count without waiting.
func test_negative_count_reports_framework_error() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.one)
	var reached: bool = await probe.wait_until_count(-1)
	expect_false(reached)
	probe.stop_recording()


## Rejects a negative timeout without waiting.
func test_negative_timeout_reports_framework_error() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.one)
	var reached: bool = await probe.wait_until_count(1, -1.0)
	expect_false(reached)
	probe.stop_recording()


## Rejects an index outside the recorded emissions and returns an empty list.
func test_out_of_range_index_reports_framework_error() -> void:
	var probe: GDTestSignalProbe = watch_signal(_source.one)
	expect_equal(probe.arguments_at(0), [])
	probe.stop_recording()
