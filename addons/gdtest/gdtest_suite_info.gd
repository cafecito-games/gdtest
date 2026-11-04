## Immutable metadata describing a discovered gdtest suite.
class_name GDTestSuiteInfo
extends RefCounted

## Stable project-relative suite identifier.
var id: String:
	get:
		return _id

## Human-readable suite name.
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

## Whether this suite is focused.
var is_only: bool:
	get:
		return _is_only

var _id: String
var _name: String
var _file: String
var _line: int
var _is_only: bool


func _init(p_id: String, p_name: String, p_file: String, p_line: int, p_is_only: bool) -> void:
	_id = p_id
	_name = p_name
	_file = p_file
	_line = p_line
	_is_only = p_is_only
