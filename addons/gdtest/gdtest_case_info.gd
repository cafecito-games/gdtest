## Immutable metadata describing a discovered gdtest case.
class_name GDTestCaseInfo
extends RefCounted

## Stable project-relative case identifier.
var id: String:
	get:
		return _id

## Human-readable case name.
var name: String:
	get:
		return _name

## Project-relative `res://` source path.
var file: String:
	get:
		return _file

## One-based definition line, or `-1` when unknown.
var line: int:
	get:
		return _line

## Method invoked by this case, or an empty name for a dynamic callback.
var method_name: StringName:
	get:
		return _method_name

## Defensive copy of the positional case parameters.
var parameters: Array[Variant]:
	get:
		return _parameters.duplicate(true)

## Parameter row name, or an empty string when not parameterized.
var parameter_name: String:
	get:
		return _parameter_name

## Whether this case is focused.
var is_only: bool:
	get:
		return _is_only

## Effective deadline for this case, including its case hooks.
var timeout_seconds: float:
	get:
		return _timeout_seconds

var _id: String
var _name: String
var _file: String
var _line: int
var _method_name: StringName
var _parameters: Array[Variant]
var _parameter_name: String
var _is_only: bool
var _timeout_seconds: float


func _init(
		p_id: String,
		p_name: String,
		p_file: String,
		p_line: int,
		p_method_name: StringName,
		p_parameters: Array[Variant],
		p_parameter_name: String,
		p_is_only: bool,
		p_timeout_seconds: float = 0.0
) -> void:
	_id = p_id
	_name = p_name
	_file = p_file
	_line = p_line
	_method_name = p_method_name
	_parameters = p_parameters.duplicate(true)
	_parameter_name = p_parameter_name
	_is_only = p_is_only
	_timeout_seconds = p_timeout_seconds
