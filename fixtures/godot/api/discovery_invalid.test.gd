extends GDTest


## Deliberately violates the suite-name return contract.
func get_suite_name() -> Variant:
	return 42


## Receives a row from a deliberately invalid provider.
func test_duplicate_provider_name(value: int) -> void:
	expect_equal(value, 1)


## Returns keys whose string forms collide.
# Provider suffix is a reserved gdtest public API spelling.
# gdkit:ignore = function-name
func test_duplicate_provider_name__cases() -> Dictionary[Variant, Variant]:
	return { 1: [1], "1": [1] }


## Receives the otherwise valid row from a provider that performs an assertion.
func test_assertion_in_provider(value: int) -> void:
	expect_equal(value, 1)


## Deliberately attempts a case-only assertion during discovery.
# Provider suffix is a reserved gdtest public API spelling.
# gdkit:ignore = function-name
func test_assertion_in_provider__cases() -> Array[Variant]:
	expect_true(true)
	return [[1]]
