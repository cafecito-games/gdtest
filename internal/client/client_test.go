package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientAutoStartsStreamsAndStopsDaemon(t *testing.T) {
	t.Parallel()

	namespace, err := daemon.NewNamespace(shortTempDir(t), daemon.ProtocolVersion, "test-build")
	require.NoError(t, err)
	server := &daemon.Server{
		Namespace: namespace,
		Handler: daemon.HandlerFunc(func(_ context.Context, request daemon.Request, emit daemon.EmitFunc) (any, error) {
			if request.Method != "test.stream" {
				return nil, fmt.Errorf("unexpected method %s", request.Method)
			}
			if err := emit(map[string]any{"sequence": 1}); err != nil {
				return nil, err
			}
			if err := emit(map[string]any{"sequence": 2}); err != nil {
				return nil, err
			}
			return map[string]any{"done": true}, nil
		}),
	}
	serverErrors := make(chan error, 1)
	var starts atomic.Int32
	client := Client{
		Namespace: namespace,
		Starter: func(ctx context.Context, _ daemon.Namespace) error {
			starts.Add(1)
			go func() { serverErrors <- server.Serve(ctx) }()
			return nil
		},
		StartTimeout: 2 * time.Second,
	}
	var sequences []int
	response, err := client.Call(context.Background(), "test.stream", map[string]any{"value": 1}, func(payload json.RawMessage) error {
		var event struct {
			Sequence int `json:"sequence"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			return err
		}
		sequences = append(sequences, event.Sequence)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, sequences)
	assert.JSONEq(t, `{"done":true}`, string(response))
	status, err := client.Status(context.Background())
	require.NoError(t, err)
	assert.True(t, json.Valid(status), "status is not valid JSON: %s", status)
	assert.EqualValues(t, 1, starts.Load())
	require.NoError(t, client.Stop(context.Background()))
	select {
	case err := <-serverErrors:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "server did not stop")
	}
}

func TestCallCancellationClosesConnection(t *testing.T) {
	t.Parallel()

	namespace, err := daemon.NewNamespace(shortTempDir(t), daemon.ProtocolVersion, "cancel-build")
	require.NoError(t, err)
	handlerCanceled := make(chan struct{})
	server := &daemon.Server{
		Namespace: namespace,
		Handler: daemon.HandlerFunc(func(ctx context.Context, _ daemon.Request, _ daemon.EmitFunc) (any, error) {
			<-ctx.Done()
			close(handlerCanceled)
			return nil, ctx.Err()
		}),
	}
	serveCtx, stopServer := context.WithCancel(context.Background())
	defer stopServer()
	serverErrors := make(chan error, 1)
	client := Client{
		Namespace: namespace,
		Starter: func(context.Context, daemon.Namespace) error {
			go func() { serverErrors <- server.Serve(serveCtx) }()
			return nil
		},
		StartTimeout: 2 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = client.Call(ctx, "test.wait", struct{}{}, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case <-handlerCanceled:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "client disconnect did not cancel daemon handler")
	}
	stopServer()
	select {
	case err := <-serverErrors:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		require.FailNow(t, "server did not stop")
	}
}

func TestExecutableStarterPreparesNamespaceBeforeOpeningLog(t *testing.T) {
	t.Parallel()

	namespace, err := daemon.NewNamespace(shortTempDir(t), daemon.ProtocolVersion, "starter-build")
	require.NoError(t, err)
	err = ExecutableStarter("/definitely/missing/gdtest")(context.Background(), namespace)
	require.Error(t, err)
	require.DirExists(t, namespace.Root)
}

func TestCallRejectsOversizedRequestBeforeStartingDaemon(t *testing.T) {
	t.Parallel()

	namespace, err := daemon.NewNamespace(shortTempDir(t), daemon.ProtocolVersion, "bounded-build")
	require.NoError(t, err)
	var starts atomic.Int32
	client := Client{
		Namespace: namespace,
		MaxSize:   128,
		Starter: func(context.Context, daemon.Namespace) error {
			starts.Add(1)
			return nil
		},
	}

	_, err = client.Call(context.Background(), "test.large", map[string]string{
		"value": strings.Repeat("x", 256),
	}, nil)

	require.ErrorContains(t, err, "exceeds the 128-byte protocol limit")
	assert.Zero(t, starts.Load())
}

func TestCallReconnectsWhenDaemonDisappearsAfterReadinessCheck(t *testing.T) {
	t.Parallel()

	namespace, err := daemon.NewNamespace(shortTempDir(t), daemon.ProtocolVersion, "reconnect-build")
	require.NoError(t, err)
	var attempts atomic.Int32
	client := Client{
		Namespace: namespace,
		dialContext: func(context.Context, string, string) (net.Conn, error) {
			switch attempts.Add(1) {
			case 1, 3:
				connection, peer := net.Pipe()
				require.NoError(t, peer.Close())
				return connection, nil
			case 2:
				return nil, errors.New("daemon exited after readiness")
			case 4:
				connection, peer := net.Pipe()
				go func() {
					defer func() { _ = peer.Close() }()
					var request daemon.Message
					if decodeErr := protocol.NewDecoder(peer, protocol.DefaultMaxMessageSize).Decode(&request); decodeErr != nil {
						return
					}
					payload, _ := json.Marshal(map[string]bool{"reconnected": true})
					_ = protocol.NewEncoder(peer).Encode(daemon.Message{
						Version: daemon.ProtocolVersion, Type: daemon.MessageResponse,
						RequestID: request.RequestID, Method: request.Method, Payload: payload, Terminal: true,
					})
				}()
				return connection, nil
			default:
				return nil, errors.New("unexpected dial")
			}
		},
	}

	response, err := client.Call(context.Background(), "test.reconnect", struct{}{}, nil)

	require.NoError(t, err)
	assert.JSONEq(t, `{"reconnected":true}`, string(response))
	assert.Equal(t, int32(4), attempts.Load())
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "gdt-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}
