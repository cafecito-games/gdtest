package worker

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdtest/internal/protocol"
	"github.com/cafecito-games/gdtest/internal/version"
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
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var stream bytes.Buffer
	if err := protocol.NewEncoder(&stream).Encode(protocol.Envelope{
		Version: protocol.Version,
		Type:    "hello",
		Token:   "secret",
		Payload: payload,
	}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	err = verifyHandshake(protocol.NewDecoder(&stream, 4096), "secret", project, "session-1", 4)
	if err != nil {
		t.Fatalf("verifyHandshake: %v", err)
	}
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
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var stream bytes.Buffer
	if err := protocol.NewEncoder(&stream).Encode(protocol.Envelope{
		Version: protocol.Version, Type: "hello", Token: "secret", Payload: payload,
	}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	err = verifyHandshake(protocol.NewDecoder(&stream, 4096), "secret", project, "session-1", 4)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("verifyHandshake error = %v, want project mismatch", err)
	}
}
