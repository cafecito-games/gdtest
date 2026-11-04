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
		WorkerID:       "worker-1",
		ProjectPath:    project,
		SessionID:      "session-1",
		Generation:     4,
		GodotVersion:   testGodotVersion(),
		Capabilities:   testCapabilities(),
	})
	require.NoError(t, err)
	var stream bytes.Buffer
	require.NoError(t, protocol.NewEncoder(&stream).Encode(protocol.Envelope{
		Version: protocol.Version,
		Type:    "hello",
		Token:   "secret",
		Payload: payload,
	}))
	err = verifyHandshake(protocol.NewDecoder(&stream, 4096), "secret", project, "session-1", "worker-1", 4)
	require.NoError(t, err)
}

func TestVerifyHandshakeRejectsProjectMismatch(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	payload, err := json.Marshal(handshake{
		RuntimeVersion: version.Product,
		WorkerID:       "worker-1",
		ProjectPath:    filepath.Join(project, "other"),
		SessionID:      "session-1",
		Generation:     4,
		GodotVersion:   testGodotVersion(),
		Capabilities:   testCapabilities(),
	})
	require.NoError(t, err)
	var stream bytes.Buffer
	require.NoError(t, protocol.NewEncoder(&stream).Encode(protocol.Envelope{
		Version: protocol.Version, Type: "hello", Token: "secret", Payload: payload,
	}))
	err = verifyHandshake(protocol.NewDecoder(&stream, 4096), "secret", project, "session-1", "worker-1", 4)
	require.ErrorContains(t, err, "does not match")
}

func TestVerifyHandshakeRejectsIncompleteCapabilitiesAndEngineIdentity(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	for _, test := range []struct {
		name         string
		godotVersion map[string]any
		capabilities []string
		want         string
	}{
		{name: "engine identity", capabilities: testCapabilities(), want: "Godot version/build"},
		{name: "capability", godotVersion: testGodotVersion(), capabilities: []string{"run"}, want: "required capability"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload, err := json.Marshal(handshake{
				RuntimeVersion: version.Product,
				WorkerID:       "worker-1",
				ProjectPath:    project,
				SessionID:      "session-1",
				Generation:     4,
				GodotVersion:   test.godotVersion,
				Capabilities:   test.capabilities,
			})
			require.NoError(t, err)
			var stream bytes.Buffer
			require.NoError(t, protocol.NewEncoder(&stream).Encode(protocol.Envelope{
				Version: protocol.Version, Type: "hello", Token: "secret", Payload: payload,
			}))
			err = verifyHandshake(
				protocol.NewDecoder(&stream, 4096), "secret", project, "session-1", "worker-1", 4,
			)
			require.ErrorContains(t, err, test.want)
		})
	}
}

func testGodotVersion() map[string]any {
	return map[string]any{"major": 4, "minor": 7, "build": "official"}
}

func testCapabilities() []string {
	return []string{"run", "discover", "ping", "cancel", "reload", "reset", "shutdown"}
}
