## Tracks SceneTree and orphan `Node` baselines per scope and formats leak diagnostics.
extends RefCounted

## Internal protocol codec script.
const Protocol: GDScript = preload("res://addons/gdtest/protocol.gd")
## Maximum number of subtree-root leak records serialized per scope report.
const MAX_REPORTED_LEAKS: int = 20

var _policy: String = "fail"
var _ignored_scripts: Dictionary[String, bool] = {}
var _ignored_identifiers: Array[String] = []
var _scopes: Array[_ScopeBaseline] = []
var _tree: SceneTree
var _emit_event: Callable
var _is_aborted: Callable
var _call_hook: Callable


class _ScopeBaseline:
	extends RefCounted

	var _orphans: Dictionary[int, bool] = {}
	var _tree: Dictionary[int, bool] = {}


	func _init(orphan_ids: Dictionary[int, bool], tree_ids: Dictionary[int, bool]) -> void:
		_orphans = orphan_ids
		_tree = tree_ids


	func orphans() -> Dictionary[int, bool]:
		return _orphans


	func tree() -> Dictionary[int, bool]:
		return _tree


class _LeakRoot:
	extends RefCounted

	var _kind: String
	var _class_name_text: String
	var _node_name: String
	var _script_path: String
	var _tree_path: String
	var _descendants: int
	var _instance_id: int


	func _init(
			kind_value: String,
			class_value: String,
			name_value: String,
			script_value: String,
			path_value: String,
			descendants_value: int,
			instance_id_value: int
	) -> void:
		_kind = kind_value
		_class_name_text = class_value
		_node_name = name_value
		_script_path = script_value
		_tree_path = path_value
		_descendants = descendants_value
		_instance_id = instance_id_value


	func kind() -> String:
		return _kind


	func class_name_text() -> String:
		return _class_name_text


	func node_name() -> String:
		return _node_name


	func script_path() -> String:
		return _script_path


	func tree_path() -> String:
		return _tree_path


	func descendants() -> int:
		return _descendants


	func instance_id() -> int:
		return _instance_id


	func to_record() -> Dictionary[String, Variant]:
		return {
			"kind": _kind,
			"class": _class_name_text,
			"name": _node_name,
			"script": _script_path,
			"path": _tree_path,
			"descendants": _descendants,
		}


## Builds a configured leak tracker bound to `tree` and the worker's lifecycle callbacks.
static func create(
		tree: SceneTree,
		plan: Dictionary[String, Variant],
		emit_event: Callable,
		is_aborted: Callable,
		call_hook: Callable
) -> RefCounted:
	var tracker := from_plan(plan)
	tracker._tree = tree
	tracker._emit_event = emit_event
	tracker._is_aborted = is_aborted
	tracker._call_hook = call_hook
	return tracker


## Builds a configured leak tracker from the serialized worker `TestPlan`.
static func from_plan(plan: Dictionary[String, Variant]) -> RefCounted:
	var tracker := new()
	var raw_configuration: Variant = plan.get("leak_check", {})
	if raw_configuration is Dictionary:
		var configuration: Dictionary[String, Variant] = Protocol.string_dictionary(
				raw_configuration
		)
		var configured_policy: String = str(configuration.get("policy", ""))
		if not configured_policy.is_empty():
			tracker._policy = configured_policy
		var ignore_value: Variant = configuration.get("ignore", [])
		if ignore_value is Array:
			for entry_value: Variant in ignore_value:
				var entry: String = str(entry_value)
				if entry.begins_with("res://"):
					tracker._ignored_scripts[entry] = true
				elif not entry.is_empty():
					tracker._ignored_identifiers.append(entry)
	return tracker


## Opens both the autofree scope on `instance` and the matching leak baseline on the worker tree.
func open_scope(instance: GDTest, runtime_owned_ids: Dictionary[int, bool] = {}) -> void:
	instance._gdtest_autofree_registry().push_scope()
	push_scope(_tree.root if _tree != null else null, runtime_owned_ids)


## Captures the current orphan and `SceneTree` baselines for a new scope.
func push_scope(root: Node, runtime_owned_ids: Dictionary[int, bool] = {}) -> void:
	_scopes.append(
			_ScopeBaseline.new(
					_capture_orphans(runtime_owned_ids),
					_capture_tree(root, runtime_owned_ids)
			)
	)


## Discards both the innermost autofree scope on `instance` and its leak baseline.
func abort_scope(instance: GDTest) -> void:
	instance._gdtest_autofree_registry().discard_current_scope()
	pop_without_check()


## Pops the innermost leak baseline without evaluating leaks.
func pop_without_check() -> void:
	if not _scopes.is_empty():
		_scopes.pop_back()


## Executes one prepared test file's file, suite, and case scopes with leak tracking.
func execute_file_lifecycle(
		instance: GDTest,
		root_info: GDTestSuiteInfo,
		cases: Array[Variant],
		path: String,
		totals: Dictionary[String, int],
		run_case: Callable,
		drain_errors: Callable
) -> void:
	open_scope(instance)
	if await _call_hook.call(instance, &"before_all", [], path, totals):
		open_scope(instance)
		if await _call_hook.call(instance, &"before_suite", [root_info], path, totals):
			await _execute_suite_cases(instance, cases, path, totals, run_case, drain_errors)
		await close_suite_scope(instance, root_info, path, totals)
	await close_file_scope(instance, path, root_info.line, totals)


func _execute_suite_cases(
		instance: GDTest,
		cases: Array[Variant],
		path: String,
		totals: Dictionary[String, int],
		run_case: Callable,
		drain_errors: Callable
) -> void:
	var open_suites: Array[GDTestSuiteInfo] = []
	var blocked_suite_depth: int = -1
	for case_value: Variant in cases:
		if _is_aborted.is_valid() and bool(_is_aborted.call()):
			break
		var case_plan: Dictionary[String, Variant] = Protocol.string_dictionary(case_value)
		var desired_suites: Array[GDTestSuiteInfo] = _suite_infos(case_plan.get("suite_infos", []))
		var common_count: int = _common_suite_count(open_suites, desired_suites)
		for index: int in range(open_suites.size() - 1, common_count - 1, -1):
			await close_suite_scope(instance, open_suites[index], path, totals)
		open_suites.resize(common_count)
		if blocked_suite_depth >= common_count:
			blocked_suite_depth = -1
		if blocked_suite_depth < 0:
			for index: int in range(common_count, desired_suites.size()):
				open_suites.append(desired_suites[index])
				open_scope(instance)
				if not await _call_hook.call(
						instance,
						&"before_suite",
						[desired_suites[index]],
						path,
						totals
				):
					blocked_suite_depth = index
					break
		if blocked_suite_depth >= 0:
			continue
		var status: String = await run_case.call(instance, path, case_plan)
		totals[status] = totals.get(status, 0) + 1
		totals["error"] += int(drain_errors.call(instance, path))
	for index: int in range(open_suites.size() - 1, -1, -1):
		await close_suite_scope(instance, open_suites[index], path, totals)


func _suite_infos(value: Variant) -> Array[GDTestSuiteInfo]:
	var result: Array[GDTestSuiteInfo] = []
	if not value is Array:
		return result
	for suite_value: Variant in value:
		if suite_value is GDTestSuiteInfo:
			result.append(suite_value)
	return result


func _common_suite_count(left: Array[GDTestSuiteInfo], right: Array[GDTestSuiteInfo]) -> int:
	var result: int = 0
	while result < left.size() and result < right.size() and left[result].id == right[result].id:
		result += 1
	return result


## Runs `after_suite`, releases suite `autofree` registrations, and checks suite leaks.
func close_suite_scope(
		instance: GDTest,
		suite_info: GDTestSuiteInfo,
		path: String,
		totals: Dictionary[String, int]
) -> void:
	await _call_hook.call(instance, &"after_suite", [suite_info], path, totals)
	var subject: String = "`before_suite`/`after_suite` of suite `%s`" % suite_info.name
	var suite_file: String = suite_info.file if not suite_info.file.is_empty() else path
	await _close_non_case_scope(instance, subject, suite_file, suite_info.line, totals)


## Runs `after_all`, releases file `autofree` registrations, and checks file leaks.
func close_file_scope(
		instance: GDTest,
		path: String,
		line: int,
		totals: Dictionary[String, int]
) -> void:
	await _call_hook.call(instance, &"after_all", [], path, totals)
	await _close_non_case_scope(instance, "`before_all`/`after_all`", path, line, totals)


func _close_non_case_scope(
		instance: GDTest,
		subject: String,
		path: String,
		line: int,
		totals: Dictionary[String, int]
) -> void:
	if _is_aborted.is_valid() and bool(_is_aborted.call()):
		abort_scope(instance)
		return
	await instance._gdtest_autofree_registry().release_and_drain(_tree)
	if _is_aborted.is_valid() and bool(_is_aborted.call()):
		pop_without_check()
		return
	var report: Dictionary[String, Variant] = finish_scope(
			_tree.root if _tree != null else null,
			subject
	)
	if report.is_empty():
		return
	var diagnostic: Dictionary[String, Variant] = {
		"assertion": "leak",
		"message": report["message"],
		"omit_actual": true,
		"location": { "file": path, "line": line },
		"metadata": report["metadata"],
	}
	if _policy == "fail":
		totals["fail"] = totals.get("fail", 0) + 1
		_emit_event.call("assertion_failed", Protocol.assertion_failed("", diagnostic))
		return
	if _policy == "warn":
		_emit_event.call("case_warning", Protocol.case_warning("", diagnostic))


## Evaluates the completed case scope and records or emits its configured leak diagnostic.
func check_case_scope(instance: GDTest, case_id: String, path: String, line: int) -> void:
	if _is_aborted.is_valid() and bool(_is_aborted.call()):
		pop_without_check()
		return
	var report: Dictionary[String, Variant] = finish_scope(
			_tree.root if _tree != null else null,
			""
	)
	if report.is_empty():
		return
	if _policy == "fail":
		var metadata: Dictionary[String, Variant] = Protocol.string_dictionary(report["metadata"])
		instance._gdtest_record_leak_failure(str(report["message"]), path, line, metadata)
		return
	if _policy == "warn":
		var diagnostic: Dictionary[String, Variant] = {
			"assertion": "leak",
			"message": report["message"],
			"omit_actual": true,
			"location": { "file": path, "line": line },
			"metadata": report["metadata"],
		}
		_emit_event.call("case_warning", Protocol.case_warning(case_id, diagnostic))


## Pops the current scope baseline, absorbs survivors into ancestors, and builds a leak report.
func finish_scope(
		root: Node,
		subject: String,
		runtime_owned_ids: Dictionary[int, bool] = {}
) -> Dictionary[String, Variant]:
	if _scopes.is_empty():
		return {}
	var baseline: _ScopeBaseline = _scopes.pop_back()
	var post_orphans: Dictionary[int, bool] = _capture_orphans(runtime_owned_ids)
	var post_tree: Dictionary[int, bool] = _capture_tree(root, runtime_owned_ids)
	var new_orphan_ids: Array[int] = _set_difference(post_orphans, baseline.orphans())
	var new_tree_ids: Array[int] = _set_difference(post_tree, baseline.tree())
	_absorb_into_ancestors(new_orphan_ids, new_tree_ids)
	if _policy == "off" or new_orphan_ids.is_empty() and new_tree_ids.is_empty():
		return {}
	var stray_roots: Array[_LeakRoot] = _collapse_leaks(new_tree_ids, baseline.tree(), "stray")
	var orphan_roots: Array[_LeakRoot] = _collapse_leaks(
			new_orphan_ids,
			baseline.orphans(),
			"orphan"
	)
	var all_roots: Array[_LeakRoot] = []
	all_roots.append_array(stray_roots)
	all_roots.append_array(orphan_roots)
	if all_roots.is_empty():
		return {}
	all_roots.sort_custom(_leak_less)
	var total_roots: int = all_roots.size()
	var reported_count: int = mini(total_roots, MAX_REPORTED_LEAKS)
	var omitted: int = total_roots - reported_count
	var records: Array[Variant] = []
	var descriptions: Array[String] = []
	for index: int in range(reported_count):
		var item: _LeakRoot = all_roots[index]
		records.append(item.to_record())
		descriptions.append(_describe_leak(item))
	var metadata: Dictionary[String, Variant] = { "leaks": records }
	if omitted > 0:
		metadata["omitted"] = omitted
	return {
		"policy": _policy,
		"message": _format_message(subject, total_roots, descriptions, omitted),
		"metadata": metadata,
	}


func _capture_orphans(runtime_owned_ids: Dictionary[int, bool]) -> Dictionary[int, bool]:
	var captured: Dictionary[int, bool] = {}
	for instance_id: int in Node.get_orphan_node_ids():
		if not runtime_owned_ids.has(instance_id):
			captured[instance_id] = true
	return captured


func _capture_tree(root: Node, runtime_owned_ids: Dictionary[int, bool]) -> Dictionary[int, bool]:
	var captured: Dictionary[int, bool] = {}
	if root == null or not is_instance_valid(root):
		return captured
	var pending: Array[Node] = [root]
	while not pending.is_empty():
		var current: Node = pending.pop_back()
		if current == null or not is_instance_valid(current):
			continue
		var instance_id: int = current.get_instance_id()
		if runtime_owned_ids.has(instance_id):
			continue
		captured[instance_id] = true
		for child: Node in current.get_children(true):
			pending.append(child)
	return captured


func _set_difference(
		current_ids: Dictionary[int, bool],
		baseline_ids: Dictionary[int, bool]
) -> Array[int]:
	var added: Array[int] = []
	for instance_id: int in current_ids:
		if not baseline_ids.has(instance_id):
			added.append(instance_id)
	return added


func _absorb_into_ancestors(new_orphan_ids: Array[int], new_tree_ids: Array[int]) -> void:
	for ancestor: _ScopeBaseline in _scopes:
		for instance_id: int in new_orphan_ids:
			ancestor.orphans()[instance_id] = true
			ancestor.tree()[instance_id] = true
		for instance_id: int in new_tree_ids:
			ancestor.tree()[instance_id] = true
			ancestor.orphans()[instance_id] = true


func _collapse_leaks(
		candidate_ids: Array[int],
		baseline_ids: Dictionary[int, bool],
		kind: String
) -> Array[_LeakRoot]:
	var leaked_nodes: Dictionary[int, Node] = {}
	for instance_id: int in candidate_ids:
		if not is_instance_id_valid(instance_id):
			continue
		var resolved: Object = instance_from_id(instance_id)
		if resolved == null or not is_instance_valid(resolved) or not resolved is Node:
			continue
		var node: Node = resolved
		if _is_exempt_subtree(node):
			continue
		leaked_nodes[instance_id] = node
	var root_nodes: Dictionary[int, Node] = {}
	var descendant_counts: Dictionary[int, int] = {}
	for instance_id: int in leaked_nodes:
		var node: Node = leaked_nodes[instance_id]
		if _is_collapsed_root(node, leaked_nodes, baseline_ids):
			root_nodes[instance_id] = node
			descendant_counts[instance_id] = 0
	for instance_id: int in leaked_nodes:
		if root_nodes.has(instance_id):
			continue
		var node: Node = leaked_nodes[instance_id]
		var ancestor_id: int = _find_collapsed_root_id(node, leaked_nodes, root_nodes, baseline_ids)
		if ancestor_id != 0 and descendant_counts.has(ancestor_id):
			descendant_counts[ancestor_id] += 1
		else:
			root_nodes[instance_id] = node
			descendant_counts[instance_id] = 0
	var results: Array[_LeakRoot] = []
	for instance_id: int in root_nodes:
		var node: Node = root_nodes[instance_id]
		results.append(_build_leak_root(node, kind, descendant_counts[instance_id], instance_id))
	return results


func _is_collapsed_root(
		node: Node,
		leaked_nodes: Dictionary[int, Node],
		baseline_ids: Dictionary[int, bool]
) -> bool:
	var parent: Node = node.get_parent()
	if parent == null or not is_instance_valid(parent):
		return true
	var parent_id: int = parent.get_instance_id()
	if baseline_ids.has(parent_id):
		return true
	return not leaked_nodes.has(parent_id)


func _find_collapsed_root_id(
		node: Node,
		leaked_nodes: Dictionary[int, Node],
		root_nodes: Dictionary[int, Node],
		baseline_ids: Dictionary[int, bool]
) -> int:
	var current: Node = node.get_parent()
	var steps: int = 0
	var limit: int = leaked_nodes.size()
	while current != null and is_instance_valid(current) and steps <= limit:
		var current_id: int = current.get_instance_id()
		if root_nodes.has(current_id):
			return current_id
		if baseline_ids.has(current_id) or not leaked_nodes.has(current_id):
			return 0
		current = current.get_parent()
		steps += 1
	return 0


func _build_leak_root(
		node: Node,
		kind: String,
		descendant_count: int,
		instance_id: int
) -> _LeakRoot:
	var script_path: String = ""
	var attached: Variant = node.get_script()
	if attached is Script:
		var script: Script = attached
		script_path = script.resource_path
	var tree_path: String = ""
	if kind == "stray" and node.is_inside_tree():
		tree_path = String(node.get_path())
	return _LeakRoot.new(
			kind,
			node.get_class(),
			String(node.name),
			script_path,
			tree_path,
			descendant_count,
			instance_id
	)


func _is_exempt_subtree(node: Node) -> bool:
	var current: Node = node
	while current != null and is_instance_valid(current):
		if _matches_ignore(current):
			return true
		current = current.get_parent()
	return false


func _matches_ignore(node: Node) -> bool:
	for identifier: String in _ignored_identifiers:
		if node.is_class(identifier):
			return true
	var attached: Variant = node.get_script()
	if not attached is Script:
		return false
	var current_script: Script = attached
	while current_script != null:
		if (
				not current_script.resource_path.is_empty()
				and _ignored_scripts.has(current_script.resource_path)
		):
			return true
		var global_name: String = String(current_script.get_global_name())
		if not global_name.is_empty() and _ignored_identifiers.has(global_name):
			return true
		current_script = current_script.get_base_script()
	return false


func _leak_less(left: _LeakRoot, right: _LeakRoot) -> bool:
	var left_is_stray: bool = left.kind() == "stray"
	var right_is_stray: bool = right.kind() == "stray"
	if left_is_stray != right_is_stray:
		return left_is_stray
	if left_is_stray and left.tree_path() != right.tree_path():
		return Protocol.utf8_less(left.tree_path(), right.tree_path())
	if not left_is_stray and left.class_name_text() != right.class_name_text():
		return Protocol.utf8_less(left.class_name_text(), right.class_name_text())
	if not left_is_stray and left.node_name() != right.node_name():
		return Protocol.utf8_less(left.node_name(), right.node_name())
	return left.instance_id() < right.instance_id()


func _describe_leak(item: _LeakRoot) -> String:
	var summary: String = '%s %s "%s"' % [item.kind(), item.class_name_text(), item.node_name()]
	if item.kind() == "stray" and not item.tree_path().is_empty():
		summary += " at " + item.tree_path()
	if not item.script_path().is_empty():
		summary += " (" + item.script_path() + ")"
	if item.descendants() == 1:
		summary += " (+1 descendant)"
	elif item.descendants() > 1:
		summary += " (+%d descendants)" % item.descendants()
	return summary


func _format_message(
		subject: String,
		total_roots: int,
		descriptions: Array[String],
		omitted: int
) -> String:
	var noun: String = "1 node" if total_roots == 1 else "%d nodes" % total_roots
	var prefix: String = "leaked " + noun if subject.is_empty() else "%s leaked %s" % [
		subject,
		noun,
	]
	var body: String = ", ".join(descriptions)
	if omitted > 0:
		body += ", and %d more" % omitted
	return prefix + ": " + body
