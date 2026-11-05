package protocol

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLanguageNeutralFixturesDecode(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"worker-handshake.jsonl", "run-events.jsonl", "benchmark-events.jsonl"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			file, err := os.Open(filepath.Join("..", "..", "fixtures", "protocol", name))
			require.NoError(t, err)
			defer func() { assert.NoError(t, file.Close()) }()
			decoder := NewDecoder(file, DefaultMaxMessageSize)
			count := 0
			for {
				var envelope Envelope
				err := decoder.Decode(&envelope)
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err, "decode message %d", count)
				assert.Equal(t, Version, envelope.Version)
				assert.NotEmpty(t, envelope.Type)
				count++
			}
			assert.Positive(t, count, "fixture contains no messages")
		})
	}
}
