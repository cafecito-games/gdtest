package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/protocol"
	"github.com/cafecito-games/gdtest/internal/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistentProcessReusesOneChild(t *testing.T) {
	t.Parallel()
	process := startHelperProcess(t, 3)
	pid := process.PID()
	require.NoError(t, process.Ping(context.Background()))
	for index := range 2 {
		plan := helperPlan(fmt.Sprintf("run-%d", index), 3)
		var events []model.Event
		err := process.Run(context.Background(), plan, executor.EventSinkFunc(func(event model.Event) error {
			events = append(events, event)
			return nil
		}))
		require.NoError(t, err, "run %d", index)
		assert.Equal(t, pid, process.PID())
		require.GreaterOrEqual(t, len(events), 4)
		assert.Equal(t, model.EventRunFinished, events[len(events)-1].Type)
	}
	require.NoError(t, process.Reset(context.Background()))
	require.NoError(t, process.Reload(context.Background(), 4))
	assert.EqualValues(t, 4, process.Generation())
	shutdownProcess(t, process)
}

func TestPersistentProcessCapturesRunEngineOutput(t *testing.T) {
	t.Parallel()
	process := startHelperProcess(t, 1)
	defer shutdownProcess(t, process)

	var outputs []model.Event
	err := process.Run(context.Background(), helperPlan("output", 1), executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventOutput {
			outputs = append(outputs, event)
		}
		return nil
	}))
	require.NoError(t, err)
	require.NotEmpty(t, outputs)
	assert.Contains(t, outputs[0].Message, "captured worker output")
	assert.Empty(t, outputs[0].TestID)
	assert.Equal(t, "output", outputs[0].RunID)
	assert.Equal(t, "stdout", outputs[0].Metadata["stream"])
}

func TestPersistentProcessDiscoversSerializableManifest(t *testing.T) {
	t.Parallel()
	process := startHelperProcess(t, 3)
	pid := process.PID()
	plan := helperPlan("discover-plan", 3)
	plan.ContentFingerprint = "content-a"
	manifest, err := process.Discover(context.Background(), plan)
	require.NoError(t, err)
	assert.Equal(t, model.ProtocolVersion, manifest.ProtocolVersion)
	assert.EqualValues(t, 3, manifest.Generation)
	assert.Equal(t, "content-a", manifest.ContentFingerprint)
	require.Len(t, manifest.Files, 1)
	require.Len(t, manifest.Files[0].Cases, 1)
	assert.Equal(t, "tests/example.test.gd::dynamic@2:0[case]", manifest.Files[0].Cases[0].ID)
	assert.Equal(t, pid, process.PID())
	require.NoError(t, process.Ping(context.Background()))
	shutdownProcess(t, process)
}

func TestPersistentProcessStreamsBenchmarkSamples(t *testing.T) {
	t.Parallel()
	process := startHelperProcess(t, 3)
	defer shutdownProcess(t, process)
	plan := model.BenchmarkPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "bench-run", Generation: 3,
		Warmup: 1, Samples: 2, Iterations: 4,
		Files: []model.BenchmarkFilePlan{{Path: "bench/example.bench.gd", Benchmarks: []model.BenchmarkCasePlan{{
			ID: "bench/example.bench.gd::benchmark_example", MethodName: "benchmark_example", Line: 3,
		}}}},
	}
	var events []model.BenchmarkEvent
	err := process.Bench(context.Background(), plan, executor.BenchmarkEventSinkFunc(func(event model.BenchmarkEvent) error {
		events = append(events, event)
		return nil
	}))
	require.NoError(t, err)
	require.Len(t, events, 10)
	assert.Equal(t, model.EventBenchmarkRunStarted, events[0].Type)
	assert.Equal(t, model.EventBenchmarkSample, events[3].Type)
	assert.True(t, events[3].Warmup)
	assert.Equal(t, int64(25), events[5].DurationNS)
	assert.Equal(t, model.EventBenchmarkRunFinished, events[9].Type)
}

func TestPersistentProcessBenchmarkCancellation(t *testing.T) {
	t.Parallel()
	process := startHelperProcess(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	plan := model.BenchmarkPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: "bench-block", Generation: 1,
		Samples: 1, Iterations: 1,
		Files: []model.BenchmarkFilePlan{{Path: "bench/block.bench.gd", Benchmarks: []model.BenchmarkCasePlan{{
			ID: "bench/block.bench.gd::benchmark_block", MethodName: "benchmark_block",
		}}}},
	}
	err := process.Bench(ctx, plan, executor.BenchmarkEventSinkFunc(func(event model.BenchmarkEvent) error {
		if event.Type == model.EventBenchmarkSampleStarted {
			cancel()
		}
		return nil
	}))
	require.ErrorIs(t, err, context.Canceled)
	shutdownProcess(t, process)
}

func TestPersistentProcessCancellation(t *testing.T) {
	t.Parallel()
	process := startHelperProcess(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	err := process.Run(ctx, helperPlan("block", 1), executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventCaseStarted {
			once.Do(cancel)
		}
		return nil
	}))
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, process.Run(context.Background(), helperPlan("after-cancel", 1), executor.EventSinkFunc(func(model.Event) error {
		return nil
	})))
	shutdownProcess(t, process)
}

func TestPersistentProcessCrashNamesLastCase(t *testing.T) {
	t.Parallel()
	process := startHelperProcess(t, 1)
	err := process.Run(context.Background(), helperPlan("crash", 1), executor.EventSinkFunc(func(model.Event) error {
		return nil
	}))
	var crash *WorkerCrashError
	require.ErrorAs(t, err, &crash)
	assert.Equal(t, "case-crash", crash.LastCase)
}

func TestPersistentProcessUsesAndCleansOpaqueUserDataDirectory(t *testing.T) {
	t.Parallel()
	process := startHelperProcess(t, 1)
	assert.NotContains(t, process.userData, "session-test")
	assert.DirExists(t, process.userData)
	require.NoError(t, os.WriteFile(filepath.Join(process.userData, "sentinel"), []byte("owned"), 0o600))
	shutdownProcess(t, process)
	assert.NoDirExists(t, process.userData)

	name, directory, err := workerUserData("../../unsafe", "worker-safe")
	require.NoError(t, err)
	assert.NotContains(t, name, "unsafe")
	assert.NotContains(t, name, "..")
	assert.Equal(t, "worker-safe", filepath.Base(directory))
}

func TestStartBootstrapsOnceThenRetriesBeforeReturningWorker(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workerAttempts := 0
	bootstrapAttempts := 0
	var diagnostics strings.Builder
	process, err := Start(context.Background(), ProcessOptions{
		Project: root, SessionID: "bootstrap-retry", Generation: 1,
		StartupTimeout: 2 * time.Second, Stderr: &diagnostics,
		commandFactory: func(arguments []string) *exec.Cmd {
			if containsArgument(arguments, "--editor") {
				bootstrapAttempts++
				assert.Contains(t, arguments, "--quit")
				assert.NotContains(t, arguments, "--quit-after")
				return startupTestCommand("bootstrap-success", arguments)
			}
			workerAttempts++
			if workerAttempts == 1 {
				return startupTestCommand("startup-failure", arguments)
			}
			return startupTestCommand("worker-success", arguments)
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, workerAttempts)
	assert.Equal(t, 1, bootstrapAttempts)
	assert.NotContains(t, diagnostics.String(), "ACTIONABLE-STARTUP-MARKER",
		"a recovered startup failure must not leak transient diagnostics")
	startupTimings := process.TakeStartupTimings()
	assert.Positive(t, startupTimings["project_bootstrap_import"])
	assert.Empty(t, process.TakeStartupTimings(), "startup timings must be reported only once")
	require.NoError(t, process.Ping(context.Background()))
	shutdownProcess(t, process)
}

func TestStartFailureRetainsOnlyBoundedOutputTail(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workerAttempts := 0
	var userDataPaths []string
	process, err := Start(context.Background(), ProcessOptions{
		Project: root, SessionID: "startup-output", Generation: 1,
		StartupTimeout: 2 * time.Second,
		commandFactory: func(arguments []string) *exec.Cmd {
			if containsArgument(arguments, "--editor") {
				return startupTestCommand("bootstrap-success", arguments)
			}
			workerAttempts++
			argumentsByName := helperArguments(arguments)
			dataRoot, rootErr := godotDataRoot()
			require.NoError(t, rootErr)
			userDataPaths = append(userDataPaths, filepath.Join(
				dataRoot, filepath.FromSlash(argumentsByName["--gdtest-user-dir-name"]),
			))
			return startupTestCommand("startup-failure", arguments)
		},
	})
	require.Error(t, err)
	assert.Nil(t, process)
	assert.Equal(t, 2, workerAttempts)
	assert.Contains(t, err.Error(), "Godot stderr tail")
	assert.Contains(t, err.Error(), "ACTIONABLE-STARTUP-MARKER")
	assert.NotContains(t, err.Error(), "DISCARDED-PREFIX")
	assert.Less(t, len(err.Error()), startupOutputLimit+2048)
	for _, userDataPath := range userDataPaths {
		assert.NoDirExists(t, userDataPath)
	}
}

func TestStartCancellationStopsBootstrapAndDoesNotRetryWorker(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workerAttempts := 0
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	process, err := Start(ctx, ProcessOptions{
		Project: root, SessionID: "bootstrap-cancel", Generation: 1,
		StartupTimeout: 2 * time.Second,
		commandFactory: func(arguments []string) *exec.Cmd {
			if containsArgument(arguments, "--editor") {
				return startupTestCommand("bootstrap-block", arguments)
			}
			workerAttempts++
			return startupTestCommand("startup-failure", arguments)
		},
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Nil(t, process)
	assert.Equal(t, 1, workerAttempts)
}

func startHelperProcess(t *testing.T, generation uint64) *Process {
	t.Helper()
	process, err := Start(context.Background(), ProcessOptions{
		Project: t.TempDir(), SessionID: "session-test", Generation: generation,
		StartupTimeout: 5 * time.Second, CancelGrace: time.Second, Stderr: os.Stderr,
		commandFactory: func(arguments []string) *exec.Cmd {
			args := []string{"-test.run=TestGDTestWorkerHelper", "--"}
			args = append(args, arguments...)
			command := exec.Command(os.Args[0], args...)
			command.Env = append(os.Environ(), "GO_WANT_GDTEST_WORKER_HELPER=1")
			return command
		},
	})
	require.NoError(t, err)
	return process
}

func shutdownProcess(t *testing.T, process *Process) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, process.Shutdown(ctx))
}

func helperPlan(runID string, generation uint64) model.TestPlan {
	return model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: runID, Generation: generation,
		Files: []model.FilePlan{{Path: "tests/example.test.gd"}},
	}
}

// TestGDTestWorkerHelper acts as a separate fake worker process. It is selected
// only by the explicit environment variable set by startHelperProcess.
func TestGDTestWorkerHelper(t *testing.T) {
	if os.Getenv("GO_WANT_GDTEST_WORKER_HELPER") != "1" {
		return
	}
	arguments := helperArguments(os.Args)
	port, _ := strconv.Atoi(arguments["--gdtest-worker-port"])
	connection, err := net.Dial("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		os.Exit(20)
	}
	encoder := protocol.NewEncoder(connection)
	decoder := protocol.NewDecoder(connection, protocol.DefaultMaxMessageSize)
	generation, _ := strconv.ParseUint(arguments["--gdtest-generation"], 10, 64)
	hello, _ := json.Marshal(handshake{
		RuntimeVersion: version.Product,
		WorkerID:       arguments["--gdtest-worker-id"],
		ProjectPath:    arguments["--path"],
		SessionID:      arguments["--gdtest-session"],
		Generation:     generation,
		GodotVersion:   testGodotVersion(),
		Capabilities:   testCapabilities(),
	})
	_ = encoder.Encode(protocol.Envelope{
		Version: protocol.Version, Type: "hello",
		Token: arguments["--gdtest-worker-token"], Payload: hello,
	})
	sequence := uint64(0)
	sendEvent := func(runID string, kind model.EventType, data model.Event) {
		sequence++
		payload, _ := json.Marshal(struct {
			Kind       model.EventType `json:"kind"`
			WorkerID   string          `json:"worker_id"`
			SessionID  string          `json:"session_id"`
			Generation uint64          `json:"generation"`
			RunID      string          `json:"run_id"`
			Sequence   uint64          `json:"sequence"`
			Data       model.Event     `json:"data"`
		}{
			Kind: kind, WorkerID: arguments["--gdtest-worker-id"],
			SessionID: arguments["--gdtest-session"], Generation: generation,
			RunID: runID, Sequence: sequence, Data: data,
		})
		_ = encoder.Encode(protocol.Envelope{Version: protocol.Version, Type: "event", Payload: payload})
	}
	sendBenchmarkEvent := func(runID string, kind model.BenchmarkEventType, data model.BenchmarkEvent) {
		sequence++
		payload, _ := json.Marshal(struct {
			Kind       model.BenchmarkEventType `json:"kind"`
			WorkerID   string                   `json:"worker_id"`
			SessionID  string                   `json:"session_id"`
			Generation uint64                   `json:"generation"`
			RunID      string                   `json:"run_id"`
			Sequence   uint64                   `json:"sequence"`
			Data       model.BenchmarkEvent     `json:"data"`
		}{
			Kind: kind, WorkerID: arguments["--gdtest-worker-id"],
			SessionID: arguments["--gdtest-session"], Generation: generation,
			RunID: runID, Sequence: sequence, Data: data,
		})
		_ = encoder.Encode(protocol.Envelope{Version: protocol.Version, Type: "event", Payload: payload})
	}
	sendEvent("", model.EventWorkerReady, model.Event{})
	activeRun := ""
	activeBenchmark := false
	for {
		var message protocol.Envelope
		if err := decoder.Decode(&message); err != nil {
			_ = connection.Close()
			return
		}
		switch message.Type {
		case "ping":
			_ = encoder.Encode(protocol.Envelope{Version: protocol.Version, Type: "pong", RequestID: message.RequestID})
		case "reset":
			_ = encoder.Encode(protocol.Envelope{Version: protocol.Version, Type: "reset_finished", RequestID: message.RequestID})
		case "reload":
			_ = encoder.Encode(protocol.Envelope{Version: protocol.Version, Type: "reload_finished", RequestID: message.RequestID})
		case "discover":
			var payload struct {
				Generation uint64         `json:"generation"`
				Plan       model.TestPlan `json:"plan"`
			}
			_ = json.Unmarshal(message.Payload, &payload)
			manifest := model.DiscoveryManifest{
				ProtocolVersion: model.ProtocolVersion,
				Generation:      payload.Generation, ContentFingerprint: payload.Plan.ContentFingerprint,
				Files: []model.DiscoveryFile{{
					Path: "tests/example.test.gd", Name: "example", Line: 1,
					Cases: []model.DiscoveryCase{{
						ID: "tests/example.test.gd::dynamic@2:0[case]", Name: "case",
						File: "tests/example.test.gd", Line: 2,
					}},
				}},
			}
			encoded, _ := json.Marshal(manifest)
			_ = encoder.Encode(protocol.Envelope{
				Version: protocol.Version, Type: "discover_finished",
				RequestID: message.RequestID, Payload: encoded,
			})
		case "run":
			var payload struct {
				RunID string `json:"run_id"`
			}
			_ = json.Unmarshal(message.Payload, &payload)
			activeRun = payload.RunID
			sequence = 0
			sendEvent(activeRun, model.EventRunStarted, model.Event{})
			sendEvent(activeRun, model.EventCaseStarted, model.Event{TestID: "case-" + activeRun})
			if activeRun == "output" {
				_, _ = fmt.Fprintln(os.Stdout, "captured worker output")
				time.Sleep(10 * time.Millisecond)
			}
			if activeRun == "crash" {
				_ = connection.Close()
				os.Exit(23)
			}
			if activeRun != "block" {
				sendEvent(activeRun, model.EventCaseFinished, model.Event{TestID: "case-" + activeRun, Status: "pass"})
				sendEvent(activeRun, model.EventRunFinished, model.Event{Status: "pass"})
				activeRun = ""
			}
		case "bench":
			var payload struct {
				RunID string `json:"run_id"`
			}
			_ = json.Unmarshal(message.Payload, &payload)
			activeRun = payload.RunID
			activeBenchmark = true
			sequence = 0
			id := "bench/example.bench.gd::benchmark_example"
			sendBenchmarkEvent(activeRun, model.EventBenchmarkRunStarted, model.BenchmarkEvent{})
			sendBenchmarkEvent(activeRun, model.EventBenchmarkStarted, model.BenchmarkEvent{BenchmarkID: id})
			sendBenchmarkEvent(activeRun, model.EventBenchmarkSampleStarted, model.BenchmarkEvent{BenchmarkID: id, Warmup: true})
			if activeRun == "bench-block" {
				continue
			}
			sendBenchmarkEvent(activeRun, model.EventBenchmarkSample, model.BenchmarkEvent{BenchmarkID: id, Warmup: true, DurationNS: 99})
			sendBenchmarkEvent(activeRun, model.EventBenchmarkSampleStarted, model.BenchmarkEvent{BenchmarkID: id})
			sendBenchmarkEvent(activeRun, model.EventBenchmarkSample, model.BenchmarkEvent{BenchmarkID: id, DurationNS: 25})
			sendBenchmarkEvent(activeRun, model.EventBenchmarkSampleStarted, model.BenchmarkEvent{BenchmarkID: id, SampleIndex: 1})
			sendBenchmarkEvent(activeRun, model.EventBenchmarkSample, model.BenchmarkEvent{BenchmarkID: id, SampleIndex: 1, DurationNS: 30})
			sendBenchmarkEvent(activeRun, model.EventBenchmarkFinished, model.BenchmarkEvent{BenchmarkID: id, Status: "pass"})
			sendBenchmarkEvent(activeRun, model.EventBenchmarkRunFinished, model.BenchmarkEvent{Status: "pass"})
			activeRun = ""
			activeBenchmark = false
		case "cancel":
			if activeRun != "" {
				if activeBenchmark {
					sendBenchmarkEvent(activeRun, model.EventBenchmarkFinished, model.BenchmarkEvent{Status: "canceled"})
					sendBenchmarkEvent(activeRun, model.EventBenchmarkRunFinished, model.BenchmarkEvent{Status: "canceled"})
					activeBenchmark = false
				} else {
					sendEvent(activeRun, model.EventCaseFinished, model.Event{TestID: "case-" + activeRun, Status: "canceled"})
					sendEvent(activeRun, model.EventRunFinished, model.Event{Status: "canceled"})
				}
				activeRun = ""
			}
		case "shutdown":
			_ = connection.Close()
			return
		}
	}
}

// TestGDTestStartupHelper is a subprocess fixture for startup/bootstrap tests.
func TestGDTestStartupHelper(t *testing.T) {
	mode := os.Getenv("GO_WANT_GDTEST_STARTUP_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "startup-failure":
		_, _ = fmt.Fprint(os.Stderr, "DISCARDED-PREFIX")
		// Keep writes below the platform pipe atomic-write bound. One oversized
		// write can legally be short on a loaded CI runner and omit the marker
		// that this fixture is intended to place at the retained tail.
		chunk := strings.Repeat("x", 1024)
		for range startupOutputLimit/len(chunk) + 1 {
			_, _ = fmt.Fprint(os.Stderr, chunk)
		}
		_, _ = fmt.Fprintln(os.Stderr, "ACTIONABLE-STARTUP-MARKER")
		os.Exit(24)
	case "bootstrap-success":
		os.Exit(0)
	case "bootstrap-block":
		time.Sleep(time.Minute)
		os.Exit(0)
	default:
		os.Exit(25)
	}
}

func startupTestCommand(mode string, arguments []string) *exec.Cmd {
	if mode == "worker-success" {
		command := exec.Command(os.Args[0], append([]string{"-test.run=TestGDTestWorkerHelper", "--"}, arguments...)...)
		command.Env = append(os.Environ(), "GO_WANT_GDTEST_WORKER_HELPER=1")
		return command
	}
	command := exec.Command(os.Args[0], append([]string{"-test.run=TestGDTestStartupHelper", "--"}, arguments...)...)
	command.Env = append(os.Environ(), "GO_WANT_GDTEST_STARTUP_HELPER="+mode)
	return command
}

func containsArgument(arguments []string, expected string) bool {
	for _, argument := range arguments {
		if argument == expected {
			return true
		}
	}
	return false
}

func helperArguments(arguments []string) map[string]string {
	result := make(map[string]string)
	for index := 0; index+1 < len(arguments); index++ {
		switch arguments[index] {
		case "--path", "--gdtest-worker-port", "--gdtest-worker-token", "--gdtest-session", "--gdtest-generation", "--gdtest-worker-id", "--gdtest-user-dir-name":
			result[arguments[index]] = arguments[index+1]
			index++
		}
	}
	return result
}
