extends GDTest


## Owns one test node from a nested helper context.
class Rig extends RefCounted:
	## Node attached by the fixture.
	var node := Node.new()


	## Attaches the fixture node to the active SceneTree.
	func attach() -> void:
		(Engine.get_main_loop() as SceneTree).root.add_child(node)


	## Schedules the fixture node for cleanup.
	func dispose() -> void:
		node.queue_free()


# Existing comments and layout stay byte-for-byte outside edited spans.
## Returns Vest parameter rows.
func params_provider() -> Array[Variant]:
	return [[1, 2], [4, 5]]


## Receives suite lifecycle metadata.
func before_suite(info: GDTestSuiteInfo) -> void:
	if info.name.is_empty():
		return


## Receives case lifecycle metadata.
func after_case(info: GDTestCaseInfo) -> void:
	message(info.name)


## Verifies parameter migration and a simple fluent assertion.
func test_adds(value: int, expected: int) -> void:
	await sleep(0.0)
	expect_equal(value + 1, expected, "sum mismatch")


## Verifies nested tree access and inherited-name warning migration.
func test_nested_tree_and_shadowing() -> void:
	@warning_ignore("shadowed_variable_base_class")
	var message := "preserved"
	var rig := Rig.new()
	rig.attach()
	expect_equal(message, "preserved")
	rig.dispose()


## Supplies parameter rows for `test_adds`.
# gdkit:ignore = function-name
func test_adds__cases() -> Array[Variant]:
	return params_provider()
