extends VestTest


## Owns one test node from a nested helper context.
class Rig extends RefCounted:
	## Node attached by the fixture.
	var node := Node.new()


	## Attaches the fixture node to the active SceneTree.
	func attach() -> void:
		Vest.get_tree().root.add_child(node)


	## Schedules the fixture node for cleanup.
	func dispose() -> void:
		node.queue_free()


# Existing comments and layout stay byte-for-byte outside edited spans.
## Returns Vest parameter rows.
func params_provider() -> Array[Variant]:
	return [[1, 2], [4, 5]]


## Receives suite lifecycle metadata.
func before_suite(info: VestDefs.Suite) -> void:
	if info.name.is_empty():
		return


## Receives case lifecycle metadata.
func after_case(info: VestDefs.Case) -> void:
	Vest.message(info.name)


## Verifies parameter migration and a simple fluent assertion.
func test_adds(value: int, expected: int, _params: String = "params_provider") -> void:
	await Vest.sleep(0.0)
	assert_that(value + 1).is_equal_to(expected, "sum mismatch")


## Verifies nested tree access and inherited-name warning migration.
func test_nested_tree_and_shadowing() -> void:
	var message := "preserved"
	var rig := Rig.new()
	rig.attach()
	expect_equal(message, "preserved")
	rig.dispose()
