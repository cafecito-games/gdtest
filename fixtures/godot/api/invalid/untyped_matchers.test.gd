extends GDTest

## Deliberately untyped fixtures testing Godot 4.7 call and return type boundary rejection.


## Untyped matcher returning a Dictionary instead of GDTestMatchResult.
class UntypedDictionaryReturnMatcher extends GDTestMatcher:
	func name() -> String:
		return "untyped_dictionary_return"

	func match(_actual):
		var dictionary_value: Variant = {"is_match": true}
		return dictionary_value


## Passes an invalid Dictionary type to expect_that as the matcher.
func test_untyped_caller_passes_dictionary_matcher() -> void:
	var invalid_matcher: Variant = {"name": "fake"}
	expect_that("sample", invalid_matcher)


## Executes an untyped matcher returning a Dictionary to trigger typed return boundary error.
func test_untyped_matcher_returns_dictionary() -> void:
	expect_that("sample", UntypedDictionaryReturnMatcher.new())


## Passes a previously freed Object instance to expect_that.
func test_untyped_caller_passes_freed_instance() -> void:
	var node: Node = Node.new()
	node.free()
	var freed_instance: Variant = node
	expect_that("sample", freed_instance)


## Attempts dynamic instantiation of abstract GDTestMatcher via Variant.
func test_dynamic_abstract_matcher_instantiation_returns_null() -> void:
	var abstract_class: Variant = GDTestMatcher
	var instance: Variant = abstract_class.new()
	expect_null(instance)
	expect_that("sample", instance)


## Attempts compiling direct GDTestMatcher.new() call.
func test_static_abstract_matcher_instantiation_rejected() -> void:
	var dynamic_script: GDScript = GDScript.new()
	dynamic_script.source_code = "extends RefCounted\nfunc build() -> GDTestMatcher:\n\treturn GDTestMatcher.new()\n"
	dynamic_script.reload()


## Attempts compiling a non-abstract GDTestMatcher subclass missing abstract methods.
func test_unimplemented_abstract_matcher_subclass() -> void:
	var dynamic_script: GDScript = GDScript.new()
	dynamic_script.source_code = "extends GDTestMatcher\nfunc name() -> String:\n\treturn \"incomplete\"\n"
	dynamic_script.reload()
