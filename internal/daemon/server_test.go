package daemon

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerRejectsProtocolMismatch(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "gdt-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	namespace, err := NewNamespace(root, ProtocolVersion, "protocol-build")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{Namespace: namespace}
	errors := make(chan error, 1)
	go func() { errors <- server.Serve(ctx) }()
	waitForMetadata(t, namespace)
	connection, err := net.Dial("unix", namespace.SocketPath)
	require.NoError(t, err)
	require.NoError(t, protocol.NewEncoder(connection).Encode(Message{
		Version: 999, Type: MessageRequest, RequestID: "bad", Method: "daemon.status",
	}))
	var response Message
	require.NoError(t, protocol.NewDecoder(connection, 1024).Decode(&response))
	require.NoError(t, connection.Close())
	assert.Equal(t, MessageError, response.Type)
	assert.True(t, response.Terminal)
	assert.Contains(t, string(response.Payload), "does not match")
	cancel()
	select {
	case err := <-errors:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "server did not stop")
	}
}

func TestServerServesNamespaceWhoseCachePathExceedsSocketLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows local transport is deferred")
	}
	cacheRoot := filepath.Join(t.TempDir(), strings.Repeat("long-cache-segment-", 10))
	namespace, err := NewNamespace(cacheRoot, ProtocolVersion, "long-server-build")
	require.NoError(t, err)
	assert.LessOrEqual(t, len([]byte(namespace.SocketPath)), safeUnixSocketPathBytes)

	ctx, cancel := context.WithCancel(context.Background())
	server := &Server{Namespace: namespace}
	errors := make(chan error, 1)
	go func() { errors <- server.Serve(ctx) }()
	waitForMetadata(t, namespace)

	connection, err := net.Dial("unix", namespace.SocketPath)
	require.NoError(t, err)
	require.NoError(t, protocol.NewEncoder(connection).Encode(Message{
		Version: ProtocolVersion, Type: MessageRequest, RequestID: "status", Method: "daemon.status",
	}))
	var response Message
	require.NoError(t, protocol.NewDecoder(connection, 4096).Decode(&response))
	require.NoError(t, connection.Close())
	assert.Equal(t, MessageResponse, response.Type)
	assert.True(t, response.Terminal)

	cancel()
	select {
	case err := <-errors:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "server did not stop")
	}
	assert.NoFileExists(t, namespace.SocketPath)
}

func waitForMetadata(t *testing.T, namespace Namespace) {
	t.Helper()
	require.Eventually(t, func() bool {
		if _, err := namespace.ReadMetadata(); err == nil {
			return true
		}
		return false
	}, 2*time.Second, time.Millisecond, "daemon metadata did not appear")
}
