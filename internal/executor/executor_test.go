package executor

import (
	"testing"

	"github.com/cafecito-games/gdtest/internal/model"
)

func TestEventSinkFunc(t *testing.T) {
	t.Parallel()

	var got model.Event
	sink := EventSinkFunc(func(event model.Event) error {
		got = event
		return nil
	})
	want := model.Event{ProtocolVersion: model.ProtocolVersion, Type: model.EventRunStarted}
	if err := sink.Emit(want); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got.Type != want.Type {
		t.Fatalf("event type = %q, want %q", got.Type, want.Type)
	}
}
