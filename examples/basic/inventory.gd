## Tracks how many of each item a shop holds, up to a fixed number of distinct items.
class_name Inventory
extends RefCounted

## Most distinct items any inventory may hold.
const CAPACITY_LIMIT: int = 5

## Count added when the caller does not state one.
const DEFAULT_COUNT: int = 1

var _capacity: int
var _counts: Dictionary[String, int] = {}


## Creates an inventory that holds at most `capacity` distinct items.
func _init(capacity: int) -> void:
	_capacity = mini(capacity, CAPACITY_LIMIT)


## Adds `count` of `item` and reports whether the inventory had room for it.
func add(item: String, count: int = DEFAULT_COUNT) -> bool:
	if not _counts.has(item) and _counts.size() >= _capacity:
		return false
	_counts[item] = _counts.get(item, 0) + count
	return true


## Empties the inventory.
func clear() -> void:
	_counts.clear()


## Returns a copy of the counts after the next frame, as a remote lookup would.
func fetch_counts() -> Dictionary[String, int]:
	await Engine.get_main_loop().process_frame
	var snapshot: Dictionary[String, int] = _counts.duplicate()
	return snapshot
