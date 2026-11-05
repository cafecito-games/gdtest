extends GDTest

## Injects a generated InventoryDouble into Shop and asserts on the calls it receives.


## Accepts stock counts up to two units.
func _accepts_small_counts(_item: String, count: int) -> bool:
	return count <= 2


## Returns a snapshot after one frame, matching the real asynchronous lookup.
func _snapshot_after_frame() -> Dictionary[String, int]:
	await Engine.get_main_loop().process_frame
	var snapshot: Dictionary[String, int] = { "apple": 2 }
	return snapshot


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Inventory double"


## Returns configured results in first-in order, then repeats the last one.
func test_queued_results_are_consumed_in_order_then_last_is_sticky() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	var shop: Shop = Shop.new(inventory)
	inventory.gdtest.add.then_return(true)
	inventory.gdtest.add.then_return(false)
	expect_true(shop.stock("apple"))
	expect_false(shop.stock("pear"))
	expect_false(shop.stock("plum"))
	expect_equal(inventory.gdtest.add.call_count(), 3)


## Forwards each call's arguments, including the default count, to the answer.
func test_then_answer_receives_arguments_and_default_count() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	var shop: Shop = Shop.new(inventory)
	inventory.gdtest.add.then_answer(_accepts_small_counts)
	expect_true(shop.stock("apple"))
	expect_false(shop.stock("pear", 4))
	expect_equal(inventory.gdtest.add.calls()[0].arguments, ["apple", 1])
	expect_equal(inventory.gdtest.add.calls()[1].arguments, ["pear", 4])


## Awaits an asynchronous answer before the typed collection reaches the caller.
func test_then_answer_async_result_is_awaited() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	var shop: Shop = Shop.new(inventory)
	inventory.gdtest.fetch_counts.then_answer(_snapshot_after_frame)
	var report: Dictionary[String, int] = await shop.stock_report()
	expect_equal(report, { "apple": 2 })


## Runs the source methods through super so the double keeps real state.
func test_then_call_real_runs_source_methods() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	var shop: Shop = Shop.new(inventory)
	inventory.gdtest.add.then_call_real()
	inventory.gdtest.fetch_counts.then_call_real()
	expect_true(shop.stock("plum", 2))
	var report: Dictionary[String, int] = await shop.stock_report()
	expect_equal(report, { "plum": 2 })
	expect_equal(inventory.gdtest.fetch_counts.call_count(), 1)


## Marks a void call as expected and records it without a configured value.
func test_void_then_return_marks_call_expected() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	var shop: Shop = Shop.new(inventory)
	inventory.gdtest.clear.then_return()
	shop.close_for_day()
	expect_equal(inventory.gdtest.clear.call_count(), 1)


## Clears recorded calls and queued results so later configuration starts fresh.
func test_reset_clears_results_and_calls() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	var shop: Shop = Shop.new(inventory)
	inventory.gdtest.add.then_return(true)
	expect_true(shop.stock("apple"))
	inventory.gdtest.add.reset()
	expect_equal(inventory.gdtest.add.call_count(), 0)
	inventory.gdtest.add.then_return(false)
	expect_false(shop.stock("pear"))
	expect_equal(inventory.gdtest.add.call_count(), 1)
