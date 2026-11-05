## Base class for statically discovered gdtest benchmark scripts.
class_name GDTestBenchmark
extends RefCounted

var _gdtest_tree: SceneTree


## Returns the persistent worker SceneTree for asynchronous benchmark code.
func get_tree() -> SceneTree:
	return _gdtest_tree


## Waits for at least the requested duration using the worker SceneTree.
func sleep(seconds: float) -> void:
	if _gdtest_tree == null:
		return
	await _gdtest_tree.create_timer(maxf(seconds, 0.0)).timeout


## Configures worker-owned state without exposing it through the benchmark plan.
func _gdtest_configure(tree: SceneTree) -> void:
	_gdtest_tree = tree
