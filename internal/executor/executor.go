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
