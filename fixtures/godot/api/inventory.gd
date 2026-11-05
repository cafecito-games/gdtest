## Holds items.
class_name Inventory
extends RefCounted

## Most items allowed.
const LIMIT: int = 5


# gdkit:ignore = unused-argument
func _init(capacity: int, label: String = "bag") -> void:
	pass


## Adds an item.
# gdkit:ignore = unused-argument
func add(item: String, count: int = LIMIT) -> bool:
	return count > 0


## Clears every item.
func clear() -> void:
	pass


## Loads asynchronously.
# gdkit:ignore = unused-argument
func load_counts(path: String) -> Dictionary[String, int]:
	await Engine.get_main_loop().process_frame
	return {}
