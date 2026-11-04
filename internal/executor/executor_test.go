package executor

import (
	"testing"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventSinkFunc(t *testing.T) {
	t.Parallel()

	var got model.Event
	sink := EventSinkFunc(func(event model.Event) error {
		got = event
		return nil
	})
	want := model.Event{ProtocolVersion: model.ProtocolVersion, Type: model.EventRunStarted}
	require.NoError(t, sink.Emit(want))
	assert.Equal(t, want.Type, got.Type)
}
