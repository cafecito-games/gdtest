package worker

import (
	"context"
	"encoding/json"
	"errors"
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
)

func TestPersistentProcessReusesOneChild(t *testing.T) {
	t.Parallel()
	process := startHelperProcess(t, 3)
	pid := process.PID()
	if err := process.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	for index := range 2 {
		plan := helperPlan(fmt.Sprintf("run-%d", index), 3)
		var events []model.Event
		err := process.Run(context.Background(), plan, executor.EventSinkFunc(func(event model.Event) error {
			events = append(events, event)
			return nil
		}))
		if err != nil {
			t.Fatalf("Run %d: %v", index, err)
		}
		if process.PID() != pid {
			t.Fatalf("worker PID changed from %d to %d", pid, process.PID())
		}
		if len(events) < 4 || events[len(events)-1].Type != model.EventRunFinished {
			t.Fatalf("events = %#v", events)
		}
	}
	if err := process.Reset(context.Background()); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err := process.Reload(context.Background(), 4); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if process.Generation() != 4 {
		t.Fatalf("generation = %d, want 4", process.Generation())
	}
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
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context cancellation", err)
	}
	if err := process.Run(context.Background(), helperPlan("after-cancel", 1), executor.EventSinkFunc(func(model.Event) error {
		return nil
	})); err != nil {
		t.Fatalf("worker was not reusable after cancellation: %v", err)
	}
	shutdownProcess(t, process)
}

func TestPersistentProcessCrashNamesLastCase(t *testing.T) {
	t.Parallel()
	process := startHelperProcess(t, 1)
	err := process.Run(context.Background(), helperPlan("crash", 1), executor.EventSinkFunc(func(model.Event) error {
		return nil
	}))
	var crash *WorkerCrashError
	if !errors.As(err, &crash) {
		t.Fatalf("Run error = %T %v, want WorkerCrashError", err, err)
	}
	if crash.LastCase != "case-crash" {
		t.Fatalf("last case = %q, want case-crash", crash.LastCase)
	}
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
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return process
}

func shutdownProcess(t *testing.T, process *Process) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := process.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
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
