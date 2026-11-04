extends RefCounted

## Maximum recursive wire-value depth.
const MAX_NESTING: int = 32
## Maximum items serialized from one collection.
const MAX_COLLECTION_ITEMS: int = 10000


## Encodes one bounded protocol message as JSON Lines bytes.
static func encode_line(message: Dictionary[String, Variant]) -> PackedByteArray:
	return (JSON.stringify(message) + "\n").to_utf8_buffer()


## Decodes one JSON Lines object or returns an empty dictionary.
static func decode_line(line: String) -> Dictionary[String, Variant]:
	var parsed: Variant = JSON.parse_string(line)
	if not parsed is Dictionary:
		return {}
	var result: Dictionary[String, Variant] = {}
	for key: Variant in parsed:
		result[str(key)] = parsed[key]
	return result


## Converts a Variant to a stable tagged wire representation.
static func wire_value(value: Variant, depth: int = 0) -> Dictionary[String, Variant]:
	if depth >= MAX_NESTING:
		return { "type": "truncated", "display": "maximum nesting exceeded" }
	match typeof(value):
		TYPE_NIL:
			return { "type": "null", "value": null }
		TYPE_BOOL:
			return { "type": "bool", "value": value }
		TYPE_INT:
			return { "type": "int", "value": str(value) }
		TYPE_FLOAT:
			var number: float = value
			if is_nan(number):
				return { "type": "float", "value": "nan" }
			if is_inf(number):
				return { "type": "float", "value": "-inf" if number < 0.0 else "+inf" }
			return { "type": "float", "value": number }
		TYPE_STRING:
			return { "type": "string", "value": value }
		TYPE_STRING_NAME:
			return { "type": "string_name", "value": str(value) }
		TYPE_ARRAY:
			var input_array: Array[Variant] = value
			var array_values: Array[Variant] = []
			var array_limit: int = mini(input_array.size(), MAX_COLLECTION_ITEMS)
			for index: int in range(array_limit):
				array_values.append(wire_value(input_array[index], depth + 1))
			return {
				"type": "array",
				"value": array_values,
				"truncated": input_array.size() > array_limit,
			}
		TYPE_DICTIONARY:
			var input_dictionary: Dictionary[Variant, Variant] = value
			var entries: Array[Variant] = []
			var keys: Array[Variant] = input_dictionary.keys()
			keys.sort_custom(
					func(left: Variant, right: Variant) -> bool: return str(left) < str(right)
			)
			var dictionary_limit: int = mini(keys.size(), MAX_COLLECTION_ITEMS)
			for index: int in range(dictionary_limit):
				var key: Variant = keys[index]
				entries.append(
						{
							"key": wire_value(key, depth + 1),
							"value": wire_value(input_dictionary[key], depth + 1),
						}
				)
			return {
				"type": "dictionary",
				"value": entries,
				"truncated": keys.size() > dictionary_limit,
			}
		_:
			return { "type": type_string(typeof(value)).to_snake_case(), "display": str(value) }
