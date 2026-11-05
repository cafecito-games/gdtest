extends GDTest

## Exercises hand-written doubles of the generated shape through the case-scoped double runtime.


## Emitter whose emissions share the case sequence with double calls.
class Emitter:
	extends Object

	## Emitted with one payload.
	signal ping(value: Variant)


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Test doubles"


## Answers synchronously from the arguments the double forwarded.
func _answer_by_length(item: String, count: int) -> bool:
	return item.length() == count


## Answers after one frame so the double must await the callable.
func _answer_counts_after_frame(_path: String) -> Dictionary[String, int]:
	await Engine.get_main_loop().process_frame
	var counts: Dictionary[String, int] = { "apple": 2 }
	return counts


## Returns a typed value through then_return and keeps returning the last queued value.
func test_then_return_yields_typed_value_and_last_behaviour_is_sticky() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_return(true)
	expect_true(inventory.add("apple"))
	expect_true(inventory.add("pear"))
	var counts: Dictionary[String, int] = { "plum": 4 }
	inventory.gdtest.load_counts.then_return(counts)
	var loaded: Dictionary[String, int] = await inventory.load_counts("stock.json")
	expect_equal(loaded, { "plum": 4 })
	expect_equal(inventory.gdtest.add.call_count(), 2)
	expect_equal(inventory.gdtest.load_counts.call_count(), 1)


## Consumes queued behaviours in first-in order before the last one takes over.
func test_queued_behaviours_are_consumed_in_order() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_return(true)
	inventory.gdtest.add.then_return(false)
	expect_true(inventory.add("first"))
	expect_false(inventory.add("second"))
	expect_false(inventory.add("third"))


## Runs a synchronous answer with the forwarded arguments.
func test_then_answer_sync_receives_call_arguments() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_answer(_answer_by_length)
	expect_true(inventory.add("pear", 4))
	expect_false(inventory.add("pear", 3))
	expect_equal(inventory.gdtest.add.calls()[1].arguments, ["pear", 3])


## Awaits an asynchronous answer before the double returns its result.
func test_then_answer_async_result_is_awaited() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.load_counts.then_answer(_answer_counts_after_frame)
	var loaded: Dictionary[String, int] = await inventory.load_counts("stock.json")
	expect_equal(loaded, { "apple": 2 })


## Runs the source method through super when a real call is queued.
func test_then_call_real_runs_source_methods() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_call_real()
	inventory.gdtest.load_counts.then_call_real()
	expect_true(inventory.add("plum", 2))
	var loaded: Dictionary[String, int] = await inventory.load_counts("stock.json")
	expect_true(loaded.is_empty())
	expect_equal(inventory.gdtest.add.call_count(), 1)
	expect_equal(inventory.gdtest.load_counts.call_count(), 1)


## Fills in the declared default argument and forwards it to the call record.
func test_default_argument_is_recorded_from_inherited_constant() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_return(true)
	expect_true(inventory.add("apple"))
	expect_equal(inventory.gdtest.add.calls()[0].arguments, ["apple", 5])


## Marks a void call as expected and records it.
func test_void_then_return_marks_call_expected() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.clear.then_return()
	inventory.clear()
	expect_equal(inventory.gdtest.clear.call_count(), 1)


## Clears behaviours and recorded calls so later configuration starts fresh.
func test_reset_clears_behaviours_and_calls() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_return(true)
	expect_true(inventory.add("apple"))
	inventory.gdtest.add.reset()
	expect_equal(inventory.gdtest.add.call_count(), 0)
	inventory.gdtest.add.then_return(false)
	expect_false(inventory.add("pear"))
	expect_equal(inventory.gdtest.add.call_count(), 1)


## Gives doubled calls and signal emissions one ordered sequence within the case.
func test_calls_and_signal_probe_share_one_sequence() -> void:
	var emitter: Emitter = Emitter.new()
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_return(true)
	inventory.gdtest.clear.then_return()
	var probe: GDTestSignalProbe = watch_signal(emitter.ping)
	inventory.add("apple")
	emitter.ping.emit("sent")
	inventory.clear()
	expect_equal(inventory.gdtest.add.calls()[0].sequence, 1)
	expect_equal(probe.emissions()[0].sequence, 2)
	expect_equal(inventory.gdtest.clear.calls()[0].sequence, 3)
	emitter.free()
