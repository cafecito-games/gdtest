extends SceneTree

## Shipped protocol codec under test.
const Protocol := preload("res://addons/gdtest/protocol.gd")


## Validates the shared language-neutral protocol fixtures in GDScript.
func _initialize() -> void:
	var failure: String = _validate_handshake()
	if failure.is_empty():
		failure = _validate_events()
	if not failure.is_empty():
		# gdkit:ignore = no-engine-logging
		printerr(failure)
		quit(1)
		return
	# gdkit:ignore = no-engine-logging
	print("gdtest protocol fixtures passed in GDScript")
	quit(0)


## Validates the authenticated worker hello fixture.
func _validate_handshake() -> String:
	var messages: Array[Dictionary] = _read_fixture("worker-handshake.jsonl")
	if messages.size() != 1:
		return "worker handshake fixture must contain exactly one message"
	var message: Dictionary[String, Variant] = messages[0]
	if int(message.get("version", -1)) != 1 or str(message.get("type", "")) != "hello":
		return "worker handshake fixture has an invalid envelope"
	if str(message.get("token", "")).is_empty():
		return "worker handshake fixture has no authentication token"
	var payload_value: Variant = message.get("payload", {})
	if not payload_value is Dictionary:
		return "worker handshake payload must be an object"
	var payload: Dictionary[String, Variant] = _string_dictionary(payload_value)
	for field: String in [
		"runtime_version",
		"worker_id",
		"session_id",
		"project_path",
		"godot_version",
	]:
		if str(payload.get(field, "")).is_empty():
			return "worker handshake payload is missing " + field
	var capabilities: Variant = payload.get("capabilities", [])
	if not capabilities is Array or capabilities.size() < 3:
		return "worker handshake capabilities are incomplete"
	# The whole-function diagnostic is located on the final return.
	# gdkit:ignore = max-returns
	return ""


## Validates event identity and ordering in the shared run fixture.
func _validate_events() -> String:
	var messages: Array[Dictionary] = _read_fixture("run-events.jsonl")
	var expected_kinds: Array[String] = [
		"run_started",
		"case_started",
		"case_finished",
		"run_finished",
	]
	if messages.size() != expected_kinds.size():
		return "run event fixture has the wrong message count"
	for index: int in range(messages.size()):
		var message: Dictionary[String, Variant] = messages[index]
		if int(message.get("version", -1)) != 1 or str(message.get("type", "")) != "event":
			return "run event fixture has an invalid envelope at row %d" % [index + 1]
		if str(message.get("request_id", "")) != "run-1":
			return "run event fixture request identity changed at row %d" % [index + 1]
		var payload_value: Variant = message.get("payload", {})
		if not payload_value is Dictionary:
			return "run event fixture payload must be an object at row %d" % [index + 1]
		var payload: Dictionary[String, Variant] = _string_dictionary(payload_value)
		if str(payload.get("kind", "")) != expected_kinds[index]:
			return "run event fixture kind is out of order at row %d" % [index + 1]
		if int(payload.get("sequence", -1)) != index + 1:
			return "run event fixture sequence is not contiguous at row %d" % [index + 1]
		if str(payload.get("worker_id", "")) != "worker-1":
			return "run event fixture worker identity changed at row %d" % [index + 1]
		if str(payload.get("session_id", "")) != "session-1":
			return "run event fixture session identity changed at row %d" % [index + 1]
	# The whole-function diagnostic is located on the final return.
	# gdkit:ignore = max-returns
	return ""


## Decodes every non-empty JSONL row through the shipped protocol implementation.
func _read_fixture(name: String) -> Array[Dictionary]:
	var path: String = "res://fixtures/protocol/" + name
	var file: FileAccess = FileAccess.open(path, FileAccess.READ)
	if file == null:
		return []
	var messages: Array[Dictionary] = []
	while not file.eof_reached():
		var line: String = file.get_line().strip_edges()
		if line.is_empty():
			continue
		var message: Dictionary[String, Variant] = Protocol.decode_line(line)
		if message.is_empty():
			return []
		messages.append(message)
	return messages


## Converts a runtime dictionary into the protocol's string-keyed form.
func _string_dictionary(value: Variant) -> Dictionary[String, Variant]:
	var result: Dictionary[String, Variant] = {}
	if not value is Dictionary:
		return result
	for key: Variant in value:
		result[str(key)] = value[key]
	return result
