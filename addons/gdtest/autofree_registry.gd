## Scope-stacked registry that frees registered `Node` instances in reverse order.
class_name GDTestAutofreeRegistry
extends RefCounted

var _scopes: Array[_Scope] = []
var _closing_depth: int = 0


class _Scope:
	extends RefCounted

	var _instance_ids: Array[int] = []


	func instance_ids() -> Array[int]:
		return _instance_ids


## Reports whether a test file, suite, or case scope is currently accepting registrations.
func has_active_scope() -> bool:
	return _closing_depth == 0 and not _scopes.is_empty()


## Opens a nested autofree scope for a file, suite, or case.
func push_scope() -> void:
	_scopes.append(_Scope.new())


## Marks a scope teardown window so registrations during teardown are rejected.
func begin_closing() -> void:
	_closing_depth += 1


## Ends a scope teardown window started by `begin_closing`.
func end_closing() -> void:
	if _closing_depth > 0:
		_closing_depth -= 1


## Registers `node` with the innermost open scope using narrowest-scope-wins semantics.
func register(node: Node) -> void:
	if _scopes.is_empty():
		return
	var target_index: int = _scopes.size() - 1
	var instance_id: int = node.get_instance_id()
	for scope_index: int in range(_scopes.size()):
		var entries: Array[int] = _scopes[scope_index].instance_ids()
		var existing_index: int = entries.find(instance_id)
		if existing_index < 0:
			continue
		if scope_index == target_index:
			return
		entries.remove_at(existing_index)
		break
	_scopes[target_index].instance_ids().append(instance_id)


## Frees the innermost scope's registrations and awaits one frame on `tree` to drain `queue_free()`.
func release_and_drain(tree: SceneTree) -> void:
	begin_closing()
	release_current_scope()
	if tree != null:
		await tree.process_frame
	end_closing()


## Frees the innermost scope's valid registrations in reverse order, skipping covered descendants.
func release_current_scope() -> void:
	if _scopes.is_empty():
		return
	var scope: _Scope = _scopes.pop_back()
	var entries: Array[int] = scope.instance_ids()
	var registered_ids: Dictionary[int, bool] = {}
	for instance_id: int in entries:
		if is_instance_id_valid(instance_id):
			registered_ids[instance_id] = true
	for index: int in range(entries.size() - 1, -1, -1):
		var instance_id: int = entries[index]
		if not is_instance_id_valid(instance_id):
			continue
		var resolved: Object = instance_from_id(instance_id)
		if resolved == null or not resolved is Node:
			continue
		var node: Node = resolved
		if _has_registered_ancestor(node, registered_ids):
			continue
		node.free()
	for index: int in range(entries.size() - 1, -1, -1):
		var instance_id: int = entries[index]
		if not is_instance_id_valid(instance_id):
			continue
		var resolved: Object = instance_from_id(instance_id)
		if resolved != null and resolved is Node:
			resolved.free()


## Drops the innermost scope without freeing its nodes when timed out or canceled.
func discard_current_scope() -> void:
	if not _scopes.is_empty():
		_scopes.pop_back()


## Clears all tracked scopes and resets teardown state.
func clear() -> void:
	_scopes.clear()
	_closing_depth = 0


func _has_registered_ancestor(node: Node, registered_ids: Dictionary[int, bool]) -> bool:
	var current: Node = node.get_parent()
	while current != null and is_instance_valid(current):
		if registered_ids.has(current.get_instance_id()):
			return true
		current = current.get_parent()
	return false
