extends GDTest

## Requests a callback probe from a class member initializer during `script.new()`.

## Inert probe captured while the script instance is being constructed outside any case.
var _member_probe: GDTestCallbackProbe = callback_probe(2)


## Supplies the public suite display name.
func get_suite_name() -> String:
	return "Callback probe in member initializer"


## Observes that the member-initialized probe is closed and its callable is invalid.
func test_member_initializer_probe_is_inert() -> void:
	expect_true(_member_probe.is_closed())
	expect_false(_member_probe.callback().is_valid())
	expect_equal(_member_probe.invocation_count(), 0)
