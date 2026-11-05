package doubles

// reservedMemberNames are declared by every generated double, so a source member
// with one of these names would collide with the generated declaration.
var reservedMemberNames = []string{"gdtest", "GDTestControls", "_GDTEST_DOUBLE_PATH"}

// reservedParameterName and reservedParameterPrefix protect the generated
// members and locals that a doubled method or constructor body refers to.
const (
	reservedParameterName   = "gdtest"
	reservedParameterPrefix = "gdtest_"
)

// constructorContextParameter is the first parameter of every generated
// constructor, so a mirrored constructor parameter must not reuse its name.
const constructorContextParameter = "context"

// controllerSuffix completes the generated controller class name of a method.
const controllerSuffix = "Method"

// objectMethodNames are the non-virtual methods of Object and RefCounted in
// Godot 4.7. A doubled method with one of these names would replace an engine
// method, so it is refused.
var objectMethodNames = []string{
	"add_user_signal", "call", "call_deferred", "callv", "can_translate_messages",
	"cancel_free", "connect", "disconnect", "emit_signal", "free", "get",
	"get_class", "get_incoming_connections", "get_indexed", "get_instance_id",
	"get_meta", "get_meta_list", "get_method_argument_count", "get_method_list",
	"get_property_list", "get_script", "get_signal_connection_list",
	"get_signal_list", "get_translation_domain", "has_connections", "has_meta",
	"has_method", "has_signal", "has_user_signal", "is_blocking_signals",
	"is_class", "is_connected", "is_queued_for_deletion", "notification",
	"notify_property_list_changed", "property_can_revert", "property_get_revert",
	"remove_meta", "remove_user_signal", "set", "set_block_signals",
	"set_deferred", "set_indexed", "set_message_translation", "set_meta",
	"set_script", "set_translation_domain", "to_string", "tr", "tr_n",
	"get_reference_count", "init_ref", "reference", "unreference",
}

// builtinTypeNames are the Variant types of Godot 4.7 that a generated double
// checks with a plain `is` test. Object and Nil are excluded: Object is a class,
// and Nil is not spelled as a return type.
var builtinTypeNames = []string{
	"bool", "int", "float", "String", "StringName", "NodePath", "Vector2",
	"Vector2i", "Rect2", "Rect2i", "Vector3", "Vector3i", "Transform2D",
	"Vector4", "Vector4i", "Plane", "Quaternion", "AABB", "Basis", "Transform3D",
	"Projection", "Color", "RID", "Callable", "Signal", "Dictionary", "Array",
	"PackedByteArray", "PackedInt32Array", "PackedInt64Array",
	"PackedFloat32Array", "PackedFloat64Array", "PackedStringArray",
	"PackedVector2Array", "PackedVector3Array", "PackedColorArray",
	"PackedVector4Array",
}

// globalEnumNames are the enums of Godot 4.7 declared in @GlobalScope, which a
// return type may name without a qualifier, and the Variant enums that a return
// type names with the Variant qualifier. Enum values are integers, so a generated
// double checks them with `is int`. The list is taken from the engine's own class
// reference for Godot 4.7 @GlobalScope and Variant.
var globalEnumNames = []string{
	"ClockDirection", "Corner", "Error", "EulerOrder", "HorizontalAlignment",
	"InlineAlignment", "JoyAxis", "JoyButton", "Key", "KeyLocation",
	"KeyModifierMask", "MethodFlags", "MIDIMessage", "MouseButton",
	"MouseButtonMask", "Orientation", "PropertyHint", "PropertyUsageFlags", "Side",
	"VerticalAlignment", "Variant.Operator", "Variant.Type",
}
