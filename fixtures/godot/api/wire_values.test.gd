extends GDTest


## Emits nested and non-finite values for protocol conformance coverage.
func test_failure_wire_values() -> void:
	var nested: Array[Variant] = [Vector2(1.0, 2.0), { "color": Color(0.1, 0.2, 0.3, 0.4) }]
	expect_equal(nested, [], "nested wire values")
	expect_equal(NAN, INF, "non-finite wire values")
