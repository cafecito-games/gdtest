extends GDTestBenchmark

var _values: Array[int] = []


## Rebuilds input outside the timed benchmark method.
func before_sample(_info: GDTestBenchmarkInfo) -> void:
	_values = [5, 3, 1, 4, 2]


## Measures sorting one independent integer array.
func benchmark_sort_integers() -> void:
	_values.sort()
