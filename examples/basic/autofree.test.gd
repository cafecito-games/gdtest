extends GDTest

## Exercises scoped `autofree` registration and deferred `queue_free` teardown.

var _shared_container: Node


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Autofree example"


## Attaches a suite-scoped fixture container that gdtest frees after the suite closes.
func before_suite(_suite_info: GDTestSuiteInfo) -> void:
	_shared_container = autofree(Node.new())
	_shared_container.name = "AutofreeExampleContainer"
	get_tree().root.add_child(_shared_container)


## Registers a case-scoped `Timer` that gdtest frees before the next case begins.
func test_autofrees_case_node_attached_to_tree() -> void:
	var timer: Timer = autofree(Timer.new())
	timer.name = "CaseTimer"
	timer.one_shot = true
	_shared_container.add_child(timer)
	expect_true(timer.is_inside_tree())
	expect_equal(_shared_container.get_child_count(), 1)


## Confirms the previous case's node was freed and `queue_free` drains cleanly.
func test_drains_deferred_queue_free_before_leak_check() -> void:
	expect_equal(_shared_container.get_child_count(), 0)
	var transient: Node = Node.new()
	transient.name = "TransientBanner"
	_shared_container.add_child(transient)
	expect_true(transient.is_inside_tree())
	transient.queue_free()
