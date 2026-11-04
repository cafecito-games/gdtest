package protocol

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLanguageNeutralFixturesDecode(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"worker-handshake.jsonl", "run-events.jsonl"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			file, err := os.Open(filepath.Join("..", "..", "fixtures", "protocol", name))
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer func() {
				if err := file.Close(); err != nil {
					t.Errorf("close fixture: %v", err)
				}
			}()
			decoder := NewDecoder(file, DefaultMaxMessageSize)
			count := 0
			for {
				var envelope Envelope
				err := decoder.Decode(&envelope)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("decode message %d: %v", count, err)
				}
				if envelope.Version != Version || envelope.Type == "" {
					t.Fatalf("invalid envelope: %#v", envelope)
				}
				count++
			}
			if count == 0 {
				t.Fatal("fixture contains no messages")
			}
		})
	}
}
