package performance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/client"
	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonPerformanceObjectives(t *testing.T) {
	if os.Getenv(performanceEnvironment) != "1" {
		t.Skip("daemon performance objectives run through scripts/benchmark")
	}
	samples := latencySampleCount(t)

	t.Run("ready daemon client", func(t *testing.T) {
		daemonClient, stop := startPerformanceDaemon(t, daemon.HandlerFunc(
			func(context.Context, daemon.Request, daemon.EmitFunc) (any, error) {
				return map[string]any{"ready": true}, nil
			},
		))
		defer stop()
		durations := sampleDurations(t, samples, func() {
			response, err := daemonClient.Status(context.Background())
			require.NoError(t, err)
			assert.Contains(t, string(response), `"protocol":1`)
		})
		logCorpusReport(t, "client_ready_daemon", 1, durations)
	})

	t.Run("warm trivial file", func(t *testing.T) {
		if _, err := exec.LookPath("godot"); err != nil {
			t.Skip("warm single-file performance skipped: godot is not on PATH")
		}
		root := performanceProject(t)
		file := writeCorpus(t, root, "tests/trivial.test.gd", `extends GDTest


## Measures one passing assertion through the persistent daemon worker.
func test_trivial() -> void:
	expect_true(true)
`)
		sessions := service.NewSessionServiceWithOptions(service.ServiceOptions{
			MaxWorkers: 1, MaxWorkerAge: 30 * time.Minute,
		})
		daemonClient, stop := startPerformanceDaemon(t, sessions)
		defer func() {
			stop()
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			assert.NoError(t, sessions.Close(closeCtx))
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		generation := uint64(1)
		// The first fresh-project bootstrap may make Godot publish a UID sidecar
		// for the generated test. A second warm reconciles that legitimate source
		// change before the measured steady-state requests begin.
		for range 2 {
			response, warmErr := daemonClient.Call(ctx, "session.warm", service.WarmRequest{
				SessionID: "performance", Project: root, Godot: "godot", Generation: generation,
			}, nil)
			require.NoError(t, warmErr)
			var warmed struct {
				Generation uint64 `json:"generation"`
			}
			require.NoError(t, json.Unmarshal(response, &warmed))
			require.Positive(t, warmed.Generation)
			generation = warmed.Generation
		}

		run := 0
		durations := sampleDurations(t, samples, func() {
			run++
			finished := 0
			response, callErr := daemonClient.Call(ctx, "session.run", service.RunRequest{
				SessionID: "performance", Project: root, Godot: "godot",
				Plan: model.TestPlan{
					ProtocolVersion:    model.ProtocolVersion,
					RunID:              fmt.Sprintf("latency-%d", run),
					Generation:         generation,
					ContentFingerprint: "latency-trivial",
					Files:              []model.FilePlan{file},
				},
				WorkerMaxRuns: 1000, Isolation: "reuse",
			}, func(data json.RawMessage) error {
				var event model.Event
				if decodeErr := json.Unmarshal(data, &event); decodeErr != nil {
					return decodeErr
				}
				if event.Type == model.EventCaseFinished {
					finished++
					if event.Status != "pass" || event.Assertions != 1 {
						return fmt.Errorf("trivial case status=%s assertions=%d", event.Status, event.Assertions)
					}
				}
				return nil
			})
			require.NoError(t, callErr)
			assert.Equal(t, 1, finished)
			var result struct {
				Reused bool `json:"reused"`
			}
			require.NoError(t, json.Unmarshal(response, &result))
			if run > 1 {
				assert.True(t, result.Reused)
			}
		})
		logCorpusReport(t, "warm_single_file", 1, durations)
	})
}

func latencySampleCount(t *testing.T) int {
	t.Helper()
	const fallback = 30
	value := os.Getenv("GDTEST_LATENCY_SAMPLES")
	if value == "" {
		return fallback
	}
	count, err := strconv.Atoi(value)
	require.NoError(t, err, "GDTEST_LATENCY_SAMPLES must be an integer")
	require.GreaterOrEqual(t, count, 1)
	require.LessOrEqual(t, count, 100)
	return count
}

func startPerformanceDaemon(t *testing.T, handler daemon.Handler) (client.Client, func()) {
	t.Helper()
	cacheRoot, err := os.MkdirTemp("/tmp", "gdtperf-")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, os.RemoveAll(cacheRoot)) })
	namespace, err := daemon.NewNamespace(cacheRoot, daemon.ProtocolVersion, "performance")
	require.NoError(t, err)
	server := &daemon.Server{Namespace: namespace, Handler: handler}
	serveCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(serveCtx) }()
	deadline := time.NewTimer(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
ready:
	for {
		select {
		case serveErr := <-done:
			require.NoError(t, serveErr, "performance daemon exited before readiness")
			require.FailNow(t, "performance daemon exited without readiness")
		case <-deadline.C:
			require.FailNow(t, "performance daemon readiness timed out")
		case <-ticker.C:
			if _, metadataErr := namespace.ReadMetadata(); metadataErr == nil {
				break ready
			}
		}
	}
	daemonClient := client.Client{Namespace: namespace}
	stop := func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = daemonClient.Stop(stopCtx)
		cancel()
		select {
		case serveErr := <-done:
			assert.NoError(t, serveErr)
		case <-stopCtx.Done():
			t.Errorf("performance daemon did not stop: %v", stopCtx.Err())
		}
	}
	return daemonClient, stop
}
