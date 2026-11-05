## Immutable metadata for one benchmark hook invocation.
class_name GDTestBenchmarkInfo
extends RefCounted

## Stable project-relative benchmark identifier.
var id: String:
	get:
		return _id
## Display name derived from the benchmark method.
var name: String:
	get:
		return _name
## Project resource path of the benchmark script.
var file: String:
	get:
		return _file
## Source line of the benchmark method.
var line: int:
	get:
		return _line
## Method invoked inside the timed interval.
var method_name: StringName:
	get:
		return _method_name
## True while executing a discarded warmup sample.
var warmup: bool:
	get:
		return _warmup
## Zero-based sample or warmup index.
var sample_index: int:
	get:
		return _sample_index
## Method invocations in this sample.
var iterations: int:
	get:
		return _iterations

var _id: String
var _name: String
var _file: String
var _line: int
var _method_name: StringName
var _warmup: bool
var _sample_index: int
var _iterations: int


## Creates immutable benchmark metadata.
func _init(
		benchmark_id: String,
		benchmark_name: String,
		benchmark_file: String,
		benchmark_line: int,
		benchmark_method_name: StringName,
		is_warmup: bool,
		index: int,
		iteration_count: int
) -> void:
	_id = benchmark_id
	_name = benchmark_name
	_file = benchmark_file
	_line = benchmark_line
	_method_name = benchmark_method_name
	_warmup = is_warmup
	_sample_index = index
	_iterations = iteration_count
