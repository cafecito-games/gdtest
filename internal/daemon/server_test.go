package daemon

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/protocol"
)

func TestServerRejectsProtocolMismatch(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "gdt-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	namespace, err := NewNamespace(root, ProtocolVersion, "protocol-build")
	if err != nil {
		t.Fatalf("NewNamespace: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{Namespace: namespace}
	errors := make(chan error, 1)
	go func() { errors <- server.Serve(ctx) }()
	waitForMetadata(t, namespace)
	connection, err := net.Dial("unix", namespace.SocketPath)
	if err != nil {
		cancel()
		t.Fatalf("dial: %v", err)
	}
	if err := protocol.NewEncoder(connection).Encode(Message{
		Version: 999, Type: MessageRequest, RequestID: "bad", Method: "daemon.status",
	}); err != nil {
		_ = connection.Close()
		cancel()
		t.Fatalf("encode: %v", err)
	}
	var response Message
	if err := protocol.NewDecoder(connection, 1024).Decode(&response); err != nil {
		_ = connection.Close()
		cancel()
		t.Fatalf("decode: %v", err)
	}
	_ = connection.Close()
	if response.Type != MessageError || !response.Terminal || !strings.Contains(string(response.Payload), "does not match") {
		t.Fatalf("response = %#v", response)
	}
	cancel()
	select {
	case err := <-errors:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}

func waitForMetadata(t *testing.T, namespace Namespace) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := namespace.ReadMetadata(); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("daemon metadata did not appear")
}
