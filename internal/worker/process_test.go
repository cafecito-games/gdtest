package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
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
		ProjectPath:    arguments["--path"],
		SessionID:      arguments["--gdtest-session"],
		Generation:     generation,
	})
	_ = encoder.Encode(protocol.Envelope{
		Version: protocol.Version, Type: "hello",
		Token: arguments["--gdtest-worker-token"], Payload: hello,
	})
	sequence := uint64(0)
	sendEvent := func(runID string, kind model.EventType, data model.Event) {
		sequence++
		payload, _ := json.Marshal(struct {
			Kind     model.EventType `json:"kind"`
			RunID    string          `json:"run_id"`
			Sequence uint64          `json:"sequence"`
			Data     model.Event     `json:"data"`
		}{Kind: kind, RunID: runID, Sequence: sequence, Data: data})
		_ = encoder.Encode(protocol.Envelope{Version: protocol.Version, Type: "event", Payload: payload})
	}
	sendEvent("", model.EventWorkerReady, model.Event{})
	activeRun := ""
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
			if activeRun == "crash" {
				_ = connection.Close()
				os.Exit(23)
			}
			if activeRun != "block" {
				sendEvent(activeRun, model.EventCaseFinished, model.Event{TestID: "case-" + activeRun, Status: "pass"})
				sendEvent(activeRun, model.EventRunFinished, model.Event{Status: "pass"})
				activeRun = ""
			}
		case "cancel":
			if activeRun != "" {
				sendEvent(activeRun, model.EventCaseFinished, model.Event{TestID: "case-" + activeRun, Status: "canceled"})
				sendEvent(activeRun, model.EventRunFinished, model.Event{Status: "canceled"})
				activeRun = ""
			}
		case "shutdown":
			_ = connection.Close()
			return
		}
	}
}

func helperArguments(arguments []string) map[string]string {
	result := make(map[string]string)
	for index := 0; index+1 < len(arguments); index++ {
		switch arguments[index] {
		case "--path", "--gdtest-worker-port", "--gdtest-worker-token", "--gdtest-session", "--gdtest-generation", "--gdtest-worker-id":
			result[arguments[index]] = arguments[index+1]
			index++
		}
	}
	return result
}
