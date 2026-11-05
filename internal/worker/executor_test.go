package worker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/protocol"
	"github.com/cafecito-games/gdtest/internal/version"
	"github.com/stretchr/testify/require"
)

func TestVerifyHandshakeRejectsProtocolVersionMismatch(t *testing.T) {
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
		Version: protocol.Version - 1,
		Type:    "hello",
		Token:   "secret",
		Payload: payload,
	}))
	err = verifyHandshake(protocol.NewDecoder(&stream, 4096), "secret", project, "session-1", "worker-1", 4)
	require.EqualError(t, err, fmt.Sprintf(
		"expected worker protocol %d hello, got version %d type %q",
		protocol.Version, protocol.Version-1, "hello",
	))
}

func TestValidateWorkerPlanRejectsInvalidLeakCheck(t *testing.T) {
	t.Parallel()

	err := validateWorkerPlan(model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "run-1",
		Generation:      1,
		LeakCheck:       model.LeakCheck{Policy: "strict"},
	})
	require.EqualError(t, err, `test plan has invalid leak_check: leak_check.policy "strict" must be fail, warn, or off`)
}

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

func TestVerifyHandshakeRejectsUnsupportedGodotVersion(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	for _, test := range []struct {
		name         string
		godotVersion map[string]any
	}{
		{name: "older minor", godotVersion: map[string]any{"major": 4, "minor": 6, "build": "official"}},
		{name: "newer major", godotVersion: map[string]any{"major": 5, "minor": 0, "build": "official"}},
		{name: "non-numeric minor", godotVersion: map[string]any{"major": 4, "minor": "7", "build": "official"}},
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
				Capabilities:   testCapabilities(),
			})
			require.NoError(t, err)
			var stream bytes.Buffer
			require.NoError(t, protocol.NewEncoder(&stream).Encode(protocol.Envelope{
				Version: protocol.Version, Type: "hello", Token: "secret", Payload: payload,
			}))
			err = verifyHandshake(
				protocol.NewDecoder(&stream, 4096), "secret", project, "session-1", "worker-1", 4,
			)
			require.EqualError(t, err, fmt.Sprintf(
				"worker Godot version %v.%v is unsupported; gdtest requires Godot 4.7",
				test.godotVersion["major"], test.godotVersion["minor"],
			))
		})
	}
}

func testGodotVersion() map[string]any {
	return map[string]any{"major": 4, "minor": 7, "build": "official"}
}

func testCapabilities() []string {
	return []string{"run", "bench", "discover", "ping", "cancel", "reload", "reset", "shutdown"}
}
