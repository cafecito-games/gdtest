extends GDTest

## Exercises case-scoped callback probes for injected callables and cross-source ordering.


## Emits checkout progress while delegating stock updates and completion callbacks.
class CheckoutFlow:
	extends RefCounted

	## Emitted before and after stock is reserved.
	signal stage_reached(stage_name: String)

	var _inventory: Inventory
	var _tree: SceneTree


	## Keeps the inventory dependency and tree used for deferred delivery.
	func _init(inventory: Inventory, tree: SceneTree) -> void:
		_inventory = inventory
		_tree = tree


	## Reserves `count` of `item` after one frame and notifies `on_complete` or `on_error`.
	func purchase_async(
			item: String,
			count: int,
			on_complete: Callable,
			on_error: Callable
	) -> void:
		stage_reached.emit("validating")
		await _tree.process_frame
		if _inventory.add(item, count):
			stage_reached.emit("reserved")
			on_complete.call(item, count)
		else:
			on_error.call("out_of_stock:" + item)


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Callback probe example"


## Awaits an asynchronous completion callback and verifies the error callback never ran.
func test_awaits_injected_completion_callback() -> void:
	var inventory: Inventory = Inventory.new(3)
	var checkout: CheckoutFlow = CheckoutFlow.new(inventory, get_tree())
	var on_complete: GDTestCallbackProbe = callback_probe(2)
	var on_error: GDTestCallbackProbe = callback_probe(1)
	checkout.purchase_async("apple", 2, on_complete.callback(), on_error.callback())
	expect_true(await on_complete.wait_until_count(1, 1.0))
	expect_equal(on_complete.arguments_at(0), ["apple", 2])
	expect_equal(on_error.invocation_count(), 0)


## Orders signal emissions, double calls, and callback invocations on one shared sequence.
func test_orders_double_signal_and_callback_invocations() -> void:
	var inventory: InventoryDouble = InventoryDouble.new(double_context(), 3)
	inventory.gdtest.add.then_return(true)
	var checkout: CheckoutFlow = CheckoutFlow.new(inventory, get_tree())
	var stages: GDTestSignalProbe = watch_signal(checkout.stage_reached)
	var on_complete: GDTestCallbackProbe = callback_probe(2)
	var on_error: GDTestCallbackProbe = callback_probe(1)
	checkout.purchase_async("pear", 1, on_complete.callback(), on_error.callback())
	expect_true(await on_complete.wait_until_count(1, 1.0))
	var stage_records: Array[GDTestInvocation] = stages.emissions()
	var add_records: Array[GDTestInvocation] = inventory.gdtest.add.calls()
	var completion_records: Array[GDTestInvocation] = on_complete.invocations()
	expect_equal(stage_records[0].sequence, 1)
	expect_equal(add_records[0].sequence, 2)
	expect_equal(stage_records[1].sequence, 3)
	expect_equal(completion_records[0].sequence, 4)
	expect_equal(on_error.invocation_count(), 0)
