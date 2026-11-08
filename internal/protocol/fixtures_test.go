package protocol

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLanguageNeutralFixturesDecode(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 5, Version)
	for _, name := range []string{"worker-handshake.jsonl", "run-request.jsonl", "run-events.jsonl", "benchmark-events.jsonl"} {
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

func TestRunRequestFixtureCarriesShardFields(t *testing.T) {
	t.Parallel()

	file, err := os.Open(filepath.Join("..", "..", "fixtures", "protocol", "run-request.jsonl"))
	require.NoError(t, err)
	defer func() { assert.NoError(t, file.Close()) }()
	var envelope Envelope
	require.NoError(t, NewDecoder(file, DefaultMaxMessageSize).Decode(&envelope))
	assert.Equal(t, Version, envelope.Version)
	assert.Equal(t, "run", envelope.Type)
	assert.NotEmpty(t, envelope.RequestID)

	var payload struct {
		RunID      string         `json:"run_id"`
		Generation uint64         `json:"generation"`
		Plan       model.TestPlan `json:"plan"`
	}
	require.NoError(t, json.Unmarshal(envelope.Payload, &payload))
	assert.Equal(t, model.ProtocolVersion, payload.Plan.ProtocolVersion)
	require.Len(t, payload.Plan.Files, 1)
	assert.Equal(t, "tests/example.test.gd", payload.Plan.Files[0].Path)
	assert.Equal(t, []string{
		"tests/example.test.gd::dynamic@7:0",
		"tests/example.test.gd::test_passes",
		"tests/example.test.gd::test_rows[1]",
	}, payload.Plan.Files[0].CaseIDs)
	require.NotNil(t, payload.Plan.HasFocus)
	assert.True(t, *payload.Plan.HasFocus)
}
