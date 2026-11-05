## Abstract source whose computation is supplied by a subclass.
@abstract
class_name Calculator
extends RefCounted


## Computes a result from `value`.
@abstract
func compute(value: int) -> int
