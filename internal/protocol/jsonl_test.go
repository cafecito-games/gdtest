package protocol

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONLRoundTrip(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	want := Envelope{Version: Version, Type: "ping", RequestID: "request-1"}
	require.NoError(t, NewEncoder(&buffer).Encode(want))
	var got Envelope
	require.NoError(t, NewDecoder(&buffer, 1024).Decode(&got))
	assert.Equal(t, want, got)
}

func TestDecoderRejectsOversizedLine(t *testing.T) {
	t.Parallel()

	input := strings.NewReader(`{"type":"` + strings.Repeat("x", 40) + `"}` + "\n")
	var value Envelope
	err := NewDecoder(input, 16).Decode(&value)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}
