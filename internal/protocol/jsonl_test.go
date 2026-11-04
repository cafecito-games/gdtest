package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestJSONLRoundTrip(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	want := Envelope{Version: Version, Type: "ping", RequestID: "request-1"}
	if err := NewEncoder(&buffer).Encode(want); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got Envelope
	if err := NewDecoder(&buffer, 1024).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Version != want.Version || got.Type != want.Type || got.RequestID != want.RequestID {
		t.Fatalf("decoded %#v, want %#v", got, want)
	}
}

func TestDecoderRejectsOversizedLine(t *testing.T) {
	t.Parallel()

	input := strings.NewReader(`{"type":"` + strings.Repeat("x", 40) + `"}` + "\n")
	var value Envelope
	err := NewDecoder(input, 16).Decode(&value)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Decode error = %v, want size error", err)
	}
}
