extends GDTest


## Converts a capture whose reads are count and argument lookups.
func test_converted_capture() -> void:
	var source := Source.new()
	var changed_probe := watch_signal(source.changed)
	source.changed.emit(1, "a")
	source.changed.emit(2, "b")
	expect_equal(changed_probe.emission_count(), 2)
	expect_equal(changed_probe.arguments_at(1)[1], "b")
	expect_equal(changed_probe.arguments_at(0), [1, "a"])


## Converts a capture without reads into a bare probe statement.
func test_capture_without_reads() -> void:
	var source := Source.new()
	watch_signal(source.pressed)
	source.pressed.emit()


## Keeps the capture's local name clear of existing identifiers.
func test_probe_name_avoids_collisions() -> void:
	var source := Source.new()
	var changed_probe := 1
	var changed_probe_2 := watch_signal(source.changed)
	source.changed.emit("x")
	expect_equal(changed_probe_2.arguments_at(0)[0], "x")
	expect_equal(changed_probe, 1)


## Preserves a capture nested in a conditional block.
func test_nested_capture_is_preserved() -> void:
	var source := Source.new()
	if true:
		capture_signal(source.pressed, 0)
	expect_equal(get_signal_emissions(source.pressed).size(), 0)


## Preserves a capture whose owner is reassigned.
func test_reassigned_owner_is_preserved() -> void:
	var source := Source.new()
	capture_signal(source.pressed, 0)
	source = Source.new()
	expect_equal(get_signal_emissions(source.pressed).size(), 0)


## Preserves a capture whose count is not a literal.
func test_non_literal_count_is_preserved() -> void:
	var source := Source.new()
	var count := 1
	capture_signal(source.pressed, count)
	expect_equal(get_signal_emissions(source.pressed).size(), 0)


## Preserves reads that escape the simple count and argument forms.
func test_unsupported_read_is_preserved() -> void:
	var source := Source.new()
	capture_signal(source.changed, 1)
	var emissions := get_signal_emissions(source.changed)
	expect_equal(emissions.size(), 0)


## Preserves a capture that repeats for the same signal in another test.
func test_repeated_capture_one() -> void:
	var source := Source.new()
	var repeated_probe := watch_signal(source.repeated)
	expect_equal(repeated_probe.emission_count(), 0)


## Preserves the second repeated capture.
func test_repeated_capture_two() -> void:
	var source := Source.new()
	var repeated_probe := watch_signal(source.repeated)
	expect_equal(repeated_probe.emission_count(), 0)


## Converts emptiness checks on a recorded signal.
func test_emptiness_checks_convert() -> void:
	var source := Source.new()
	var opened_probe := watch_signal(source.opened)
	expect_empty(opened_probe.emissions())
	expect_equal(opened_probe.emissions(), [])


## Preserves a capture whose signal is also read through an alias.
func test_aliased_read_is_preserved() -> void:
	var source := Source.new()
	var alias := source
	capture_signal(source.aliased, 0)
	expect_equal(get_signal_emissions(alias.aliased).size(), 0)


## Preserves a capture that lives outside any test function.
func suite() -> void:
	capture_signal(Source.new().shared, 0)
