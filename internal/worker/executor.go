// Package worker manages authenticated Godot worker processes.
package worker

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/protocol"
	"github.com/cafecito-games/gdtest/internal/version"
)

// StartupTimeout bounds process launch and authentication.
const StartupTimeout = 15 * time.Second

// GodotExecutor runs plans through a dedicated foreground Godot process.
type GodotExecutor struct {
	Godot      string
	Project    string
	SessionID  string
	Generation uint64
	Stderr     io.Writer
	Stdout     io.Writer
}

// Run starts one authenticated worker, streams one plan, then shuts it down.
func (e *GodotExecutor) Run(ctx context.Context, plan model.TestPlan, sink executor.EventSink) error {
	project, err := canonicalProjectPath(e.Project)
	if err != nil {
		return fmt.Errorf("canonicalize project: %w", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("open worker listener: %w", err)
	}
	defer func() { _ = listener.Close() }()
	token, err := randomToken()
	if err != nil {
		return err
	}
	workerID := "worker-" + token[:12]
	port := listener.Addr().(*net.TCPAddr).Port
	command := exec.CommandContext(
		ctx,
		e.Godot,
		"--headless",
		"--path",
		project,
		"--script",
		"res://addons/gdtest/worker.gd",
		"--",
		"--gdtest-worker-port",
		strconv.Itoa(port),
		"--gdtest-worker-token",
		token,
		"--gdtest-session",
		e.SessionID,
		"--gdtest-generation",
		strconv.FormatUint(e.Generation, 10),
		"--gdtest-worker-id",
		workerID,
	)
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("capture Godot stdout: %w", err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return fmt.Errorf("capture Godot stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Godot worker %q in %s: %w", e.Godot, project, err)
	}
	var drain sync.WaitGroup
	var outputMu sync.Mutex
	stdout := io.Writer(io.Discard)
	stderr := io.Writer(io.Discard)
	if e.Stdout != nil {
		stdout = &lockedWriter{mu: &outputMu, writer: e.Stdout}
	}
	if e.Stderr != nil {
		stderr = &lockedWriter{mu: &outputMu, writer: e.Stderr}
	}
	drain.Add(2)
	go drainOutput(&drain, stdout, stdoutPipe)
	go drainOutput(&drain, stderr, stderrPipe)
	defer func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
		drain.Wait()
	}()

	connection, err := accept(ctx, listener, StartupTimeout)
	if err != nil {
		return fmt.Errorf("worker startup: %w", err)
	}
	defer func() { _ = connection.Close() }()
	decoder := protocol.NewDecoder(connection, protocol.DefaultMaxMessageSize)
	encoder := protocol.NewEncoder(connection)
	if err := verifyHandshake(decoder, token, project, e.SessionID, workerID, e.Generation); err != nil {
		return err
	}
	if err := sink.Emit(model.Event{ProtocolVersion: model.ProtocolVersion, Type: model.EventWorkerReady, RunID: plan.RunID}); err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		RunID      string         `json:"run_id"`
		Generation uint64         `json:"generation"`
		Plan       model.TestPlan `json:"plan"`
	}{RunID: plan.RunID, Generation: plan.Generation, Plan: plan})
	if err != nil {
		return fmt.Errorf("encode test plan: %w", err)
	}
	if err := encoder.Encode(protocol.Envelope{
		Version: protocol.Version, Type: "run", RequestID: plan.RunID, Payload: payload,
	}); err != nil {
		return fmt.Errorf("send run command: %w", err)
	}
	var lastSequence uint64
	var sequenceRunID string
	for {
		var message protocol.Envelope
		if err := decoder.Decode(&message); err != nil {
			return fmt.Errorf("read worker event: %w", err)
		}
		if message.Version != protocol.Version {
			return fmt.Errorf("worker protocol version %d does not match %d", message.Version, protocol.Version)
		}
		switch message.Type {
		case "event":
			var wireEvent struct {
				Kind       model.EventType `json:"kind"`
				WorkerID   string          `json:"worker_id"`
				SessionID  string          `json:"session_id"`
				Generation uint64          `json:"generation"`
				RunID      string          `json:"run_id"`
				Sequence   uint64          `json:"sequence"`
				Data       model.Event     `json:"data"`
			}
			if err := json.Unmarshal(message.Payload, &wireEvent); err != nil {
				return fmt.Errorf("decode worker event: %w", err)
			}
			if wireEvent.Kind == "" {
				return errors.New("worker event has no kind")
			}
			if wireEvent.WorkerID != workerID || wireEvent.SessionID != e.SessionID ||
				wireEvent.Generation != e.Generation {
				return errors.New("worker event identity does not match authenticated worker")
			}
			if wireEvent.RunID != sequenceRunID {
				sequenceRunID = wireEvent.RunID
				lastSequence = 0
			}
			if wireEvent.Sequence != lastSequence+1 {
				return fmt.Errorf("worker event sequence %d does not follow %d", wireEvent.Sequence, lastSequence)
			}
			lastSequence = wireEvent.Sequence
			event := wireEvent.Data
			event.ProtocolVersion = model.ProtocolVersion
			event.Type = wireEvent.Kind
			event.RunID = wireEvent.RunID
			if err := sink.Emit(event); err != nil {
				return err
			}
			if event.Type == model.EventRunFinished {
				_ = encoder.Encode(protocol.Envelope{Version: protocol.Version, Type: "shutdown"})
				return nil
			}
		case "error":
			return fmt.Errorf("worker error: %s", strings.TrimSpace(string(message.Payload)))
		default:
			return fmt.Errorf("unexpected worker message type %q", message.Type)
		}
	}
}

type handshake struct {
	RuntimeVersion string         `json:"runtime_version"`
	WorkerID       string         `json:"worker_id"`
	ProjectPath    string         `json:"project_path"`
	SessionID      string         `json:"session_id"`
	Generation     uint64         `json:"generation"`
	GodotVersion   map[string]any `json:"godot_version"`
	Capabilities   []string       `json:"capabilities"`
}

func verifyHandshake(
	decoder *protocol.Decoder,
	token string,
	project string,
	session string,
	workerID string,
	generation uint64,
) error {
	var message protocol.Envelope
	if err := decoder.Decode(&message); err != nil {
		return fmt.Errorf("read worker handshake: %w", err)
	}
	if message.Version != protocol.Version || message.Type != "hello" {
		return fmt.Errorf("expected worker protocol %d hello, got version %d type %q", protocol.Version, message.Version, message.Type)
	}
	var hello handshake
	if err := json.Unmarshal(message.Payload, &hello); err != nil {
		return fmt.Errorf("decode worker handshake: %w", err)
	}
	if message.Token != token {
		return errors.New("worker authentication failed")
	}
	if hello.RuntimeVersion != version.Product {
		return fmt.Errorf("runtime version %q does not match executable %q", hello.RuntimeVersion, version.Product)
	}
	cleanProject, err := canonicalProjectPath(hello.ProjectPath)
	expectedProject, expectedErr := canonicalProjectPath(project)
	if err != nil || expectedErr != nil || cleanProject != expectedProject {
		return fmt.Errorf("worker project %q does not match session project %q", hello.ProjectPath, project)
	}
	if hello.SessionID != session || hello.WorkerID != workerID || hello.Generation != generation {
		return errors.New("worker session or generation does not match lease")
	}
	if len(hello.GodotVersion) == 0 || hello.GodotVersion["major"] == nil ||
		hello.GodotVersion["minor"] == nil || hello.GodotVersion["build"] == nil {
		return errors.New("worker handshake has incomplete Godot version/build identity")
	}
	major, majorOK := hello.GodotVersion["major"].(float64)
	minor, minorOK := hello.GodotVersion["minor"].(float64)
	if !majorOK || !minorOK || major != 4 || minor != 7 {
		return fmt.Errorf(
			"worker Godot version %v.%v is unsupported; gdtest requires Godot 4.7",
			hello.GodotVersion["major"], hello.GodotVersion["minor"],
		)
	}
	required := []string{"run", "bench", "discover", "ping", "cancel", "reload", "reset", "shutdown"}
	capabilities := make(map[string]struct{}, len(hello.Capabilities))
	for _, capability := range hello.Capabilities {
		capabilities[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := capabilities[capability]; !ok {
			return fmt.Errorf("worker handshake is missing required capability %q", capability)
		}
	}
	return nil
}

func canonicalProjectPath(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func randomToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create worker token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func accept(ctx context.Context, listener net.Listener, timeout time.Duration) (net.Conn, error) {
	if deadlineListener, ok := listener.(*net.TCPListener); ok {
		_ = deadlineListener.SetDeadline(time.Now().Add(timeout))
	}
	result := make(chan struct {
		connection net.Conn
		err        error
	}, 1)
	go func() {
		connection, err := listener.Accept()
		result <- struct {
			connection net.Conn
			err        error
		}{connection: connection, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case accepted := <-result:
		return accepted.connection, accepted.err
	}
}

func drainOutput(wait *sync.WaitGroup, destination io.Writer, source io.Reader) {
	defer wait.Done()
	if destination == nil {
		destination = io.Discard
	}
	_, _ = io.Copy(destination, source)
}

type lockedWriter struct {
	mu     *sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(data)
}
