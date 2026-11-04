extends GDTest


## Contains unsupported Vest APIs that migration must preserve.
func suite() -> void:
	on_begin.connect(func() -> void: pass)
	var fake := mock(Node)
	when(fake.queue_free).then_return(null)
	get_calls_of(fake.queue_free)
	benchmark("work", func() -> void: pass)
	capture_signal(on_event)
	get_signal_emissions(on_event)
	Vest.timeout(0.1)
	assert_that(1).is_not_null().is_equal_to(1)
