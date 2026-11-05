## Sells items from the inventory it was given.
class_name Shop
extends RefCounted

var _inventory: Inventory


## Keeps the inventory that stock is added to and reported from.
func _init(inventory: Inventory) -> void:
	_inventory = inventory


## Stocks `count` of `item` and reports whether the inventory accepted it.
func stock(item: String, count: int = Inventory.DEFAULT_COUNT) -> bool:
	return _inventory.add(item, count)


## Reports the stock levels for the day.
func stock_report() -> Dictionary[String, int]:
	return await _inventory.fetch_counts()


## Removes all stock at the end of the day.
func close_for_day() -> void:
	_inventory.clear()
