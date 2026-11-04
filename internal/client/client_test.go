package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/daemon"
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
	server := &daemon.Server{
		Namespace: namespace,
		Handler: daemon.HandlerFunc(func(ctx context.Context, _ daemon.Request, _ daemon.EmitFunc) (any, error) {
			<-ctx.Done()
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

func shortTempDir(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "gdt-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}
