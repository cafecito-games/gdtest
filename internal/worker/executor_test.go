package worker

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdtest/internal/protocol"
	"github.com/cafecito-games/gdtest/internal/version"
	"github.com/stretchr/testify/require"
)

func TestVerifyHandshake(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	payload, err := json.Marshal(handshake{
		RuntimeVersion: version.Product,
		ProjectPath:    project,
		SessionID:      "session-1",
		Generation:     4,
	})
	require.NoError(t, err)
	var stream bytes.Buffer
	require.NoError(t, protocol.NewEncoder(&stream).Encode(protocol.Envelope{
		Version: protocol.Version,
		Type:    "hello",
		Token:   "secret",
		Payload: payload,
	}))
	err = verifyHandshake(protocol.NewDecoder(&stream, 4096), "secret", project, "session-1", 4)
	require.NoError(t, err)
}

func TestVerifyHandshakeRejectsProjectMismatch(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	payload, err := json.Marshal(handshake{
		RuntimeVersion: version.Product,
		ProjectPath:    filepath.Join(project, "other"),
		SessionID:      "session-1",
		Generation:     4,
	})
	require.NoError(t, err)
	var stream bytes.Buffer
	require.NoError(t, protocol.NewEncoder(&stream).Encode(protocol.Envelope{
		Version: protocol.Version, Type: "hello", Token: "secret", Payload: payload,
	}))
	err = verifyHandshake(protocol.NewDecoder(&stream, 4096), "secret", project, "session-1", 4)
	require.ErrorContains(t, err, "does not match")
}
