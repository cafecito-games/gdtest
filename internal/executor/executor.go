// Package executor defines the backend-neutral execution boundary.
package executor

import (
	"context"

	"github.com/cafecito-games/gdtest/internal/model"
)

// EventSink consumes an immutable event exactly once.
type EventSink interface {
	Emit(model.Event) error
}

// EventSinkFunc adapts a function to EventSink.
type EventSinkFunc func(model.Event) error

// Emit implements EventSink.
func (f EventSinkFunc) Emit(event model.Event) error { return f(event) }

// Executor runs a serializable plan without exposing backend objects.
type Executor interface {
	Run(context.Context, model.TestPlan, EventSink) error
}

// BenchmarkEventSink consumes an immutable benchmark event exactly once.
type BenchmarkEventSink interface {
	EmitBenchmark(model.BenchmarkEvent) error
}

// BenchmarkEventSinkFunc adapts a function to BenchmarkEventSink.
type BenchmarkEventSinkFunc func(model.BenchmarkEvent) error

// EmitBenchmark implements BenchmarkEventSink.
func (f BenchmarkEventSinkFunc) EmitBenchmark(event model.BenchmarkEvent) error { return f(event) }

// BenchmarkExecutor runs a benchmark plan without exposing backend objects.
type BenchmarkExecutor interface {
	Bench(context.Context, model.BenchmarkPlan, BenchmarkEventSink) error
}
