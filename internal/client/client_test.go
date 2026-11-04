package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/daemon"
)

func TestClientAutoStartsStreamsAndStopsDaemon(t *testing.T) {
	t.Parallel()

	namespace, err := daemon.NewNamespace(shortTempDir(t), daemon.ProtocolVersion, "test-build")
	if err != nil {
		t.Fatalf("NewNamespace: %v", err)
	}
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
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if fmt.Sprint(sequences) != "[1 2]" || string(response) != `{"done":true}` {
		t.Fatalf("events = %v, response = %s", sequences, response)
	}
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !json.Valid(status) || starts.Load() != 1 {
		t.Fatalf("status = %s, starts = %d", status, starts.Load())
	}
	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case err := <-serverErrors:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestCallCancellationClosesConnection(t *testing.T) {
	t.Parallel()

	namespace, err := daemon.NewNamespace(shortTempDir(t), daemon.ProtocolVersion, "cancel-build")
	if err != nil {
		t.Fatalf("NewNamespace: %v", err)
	}
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
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Call error = %v, want deadline", err)
	}
	stopServer()
	select {
	case err := <-serverErrors:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestExecutableStarterPreparesNamespaceBeforeOpeningLog(t *testing.T) {
	t.Parallel()

	namespace, err := daemon.NewNamespace(shortTempDir(t), daemon.ProtocolVersion, "starter-build")
	if err != nil {
		t.Fatalf("NewNamespace: %v", err)
	}
	err = ExecutableStarter("/definitely/missing/gdtest")(context.Background(), namespace)
	if err == nil {
		t.Fatal("ExecutableStarter unexpectedly succeeded")
	}
	if _, statErr := os.Stat(namespace.Root); statErr != nil {
		t.Fatalf("namespace was not prepared: %v", statErr)
	}
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "gdt-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}
