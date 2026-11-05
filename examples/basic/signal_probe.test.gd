extends GDTest


## Watches a signal that the case emits and checks the recorded arguments.
func test_watches_emitted_values() -> void:
	var tally: Tally = Tally.new()
	var probe: GDTestSignalProbe = watch_signal(tally.changed)
	tally.increment()
	expect_equal(probe.emission_count(), 1)
	expect_equal(probe.arguments_at(0), [1])
	probe.stop_recording()


## Counts increments and announces each new total.
class Tally:
	extends RefCounted

	## Emitted with the new total after every increment.
	signal changed(total: int)

	var _total: int = 0


	## Increments the total and emits it.
	func increment() -> void:
		_total += 1
		changed.emit(_total)
