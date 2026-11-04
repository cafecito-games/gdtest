package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/protocol"
)

// ProcessOptions describes one persistent worker lease. A Process never changes
// projects or sessions; Reload is the only operation that advances its generation.
type ProcessOptions struct {
	Godot          string
	Project        string
	SessionID      string
	Generation     uint64
	Stdout         io.Writer
	Stderr         io.Writer
	StartupTimeout time.Duration
	CancelGrace    time.Duration

	commandFactory func([]string) *exec.Cmd
}

// Process is one authenticated, persistent Godot worker. Operations other than
// Cancel are serialized because the worker executes at most one command at a time.
type Process struct {
	options ProcessOptions
	project string
	cmd     *exec.Cmd
	conn    net.Conn
	encoder *protocol.Encoder

	opMu       sync.Mutex
	writeMu    sync.Mutex
	stateMu    sync.Mutex
	closed     bool
	runID      string
	lastCase   string
	generation uint64

	messages    chan processMessage
	processDone chan struct{}
	waitErr     error
	outputWait  sync.WaitGroup
}

type processMessage struct {
	envelope protocol.Envelope
	err      error
}

// WorkerCrashError reports an unexpected process or transport exit and the
// last case whose start event was observed. It is never treated as a test failure.
type WorkerCrashError struct {
	PID      int
	LastCase string
	Err      error
}

func (e *WorkerCrashError) Error() string {
	if e.LastCase != "" {
		return fmt.Sprintf("Godot worker %d crashed while running %s: %v", e.PID, e.LastCase, e.Err)
	}
	return fmt.Sprintf("Godot worker %d crashed: %v", e.PID, e.Err)
}

// Unwrap exposes the process or transport error.
func (e *WorkerCrashError) Unwrap() error { return e.Err }

// Start launches and authenticates a persistent worker.
func Start(ctx context.Context, options ProcessOptions) (*Process, error) {
	if options.Godot == "" {
		options.Godot = "godot"
	}
	if options.SessionID == "" {
		return nil, errors.New("worker session ID is required")
	}
	if options.StartupTimeout <= 0 {
		options.StartupTimeout = StartupTimeout
	}
	if options.CancelGrace <= 0 {
		options.CancelGrace = 2 * time.Second
	}
	project, err := filepath.Abs(options.Project)
	if err != nil {
		return nil, fmt.Errorf("canonicalize project: %w", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("open worker listener: %w", err)
	}
	defer func() { _ = listener.Close() }()
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	workerID := "worker-" + token[:12]
	port := listener.Addr().(*net.TCPAddr).Port
	args := []string{
		"--headless", "--path", project,
		"--script", "res://addons/gdtest/worker.gd", "--",
		"--gdtest-worker-port", strconv.Itoa(port),
		"--gdtest-worker-token", token,
		"--gdtest-session", options.SessionID,
		"--gdtest-generation", strconv.FormatUint(options.Generation, 10),
		"--gdtest-worker-id", workerID,
	}
	var command *exec.Cmd
	if options.commandFactory != nil {
		command = options.commandFactory(args)
	} else {
		command = exec.Command(options.Godot, args...)
	}
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("capture Godot stdout: %w", err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("capture Godot stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start Godot worker %q in %s: %w", options.Godot, project, err)
	}
	p := &Process{
		options: options, project: project, cmd: command,
		generation: options.Generation,
		messages:   make(chan processMessage, 256), processDone: make(chan struct{}),
	}
	var outputMu sync.Mutex
	stdout := io.Writer(io.Discard)
	stderr := io.Writer(io.Discard)
	if options.Stdout != nil {
		stdout = &lockedWriter{mu: &outputMu, writer: options.Stdout}
	}
	if options.Stderr != nil {
		stderr = &lockedWriter{mu: &outputMu, writer: options.Stderr}
	}
	p.outputWait.Add(2)
	go drainOutput(&p.outputWait, stdout, stdoutPipe)
	go drainOutput(&p.outputWait, stderr, stderrPipe)
	go func() {
		err := command.Wait()
		p.stateMu.Lock()
		p.waitErr = err
		p.stateMu.Unlock()
		close(p.processDone)
	}()

	connection, err := accept(ctx, listener, options.StartupTimeout)
	if err != nil {
		p.killAndWait()
		return nil, fmt.Errorf("worker startup: %w", err)
	}
	p.conn = connection
	decoder := protocol.NewDecoder(connection, protocol.DefaultMaxMessageSize)
	if err := verifyHandshake(decoder, token, project, options.SessionID, options.Generation); err != nil {
		p.killAndWait()
		return nil, err
	}
	p.encoder = protocol.NewEncoder(connection)
	go p.readLoop(decoder)
	return p, nil
}

// PID returns the exact recorded child process ID.
func (p *Process) PID() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Generation returns the source generation currently loaded by the worker.
func (p *Process) Generation() uint64 {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.generation
}

// Run executes one plan while retaining the process for a subsequent run.
func (p *Process) Run(ctx context.Context, plan model.TestPlan, sink executor.EventSink) error {
	p.opMu.Lock()
	defer p.opMu.Unlock()
	if sink == nil {
		return errors.New("worker event sink is required")
	}
	if plan.Generation != p.Generation() {
		return fmt.Errorf("plan generation %d does not match worker generation %d", plan.Generation, p.Generation())
	}
	p.stateMu.Lock()
	if p.closed {
		p.stateMu.Unlock()
		return errors.New("worker is closed")
	}
	p.runID = plan.RunID
	p.lastCase = ""
	p.stateMu.Unlock()
	defer func() {
		p.stateMu.Lock()
		p.runID = ""
		p.lastCase = ""
		p.stateMu.Unlock()
	}()
	payload, err := json.Marshal(struct {
		RunID      string         `json:"run_id"`
		Generation uint64         `json:"generation"`
		Plan       model.TestPlan `json:"plan"`
	}{RunID: plan.RunID, Generation: plan.Generation, Plan: plan})
	if err != nil {
		return fmt.Errorf("encode test plan: %w", err)
	}
	if err := p.send(protocol.Envelope{Version: protocol.Version, Type: "run", RequestID: plan.RunID, Payload: payload}); err != nil {
		return err
	}
	receiveContext := ctx
	canceled := false
	var cancelWait context.CancelFunc
	defer func() {
		if cancelWait != nil {
			cancelWait()
		}
	}()
	for {
		message, err := p.receive(receiveContext)
		if err != nil {
			if !canceled && ctx.Err() != nil {
				canceled = true
				_ = p.Cancel(context.Background(), plan.RunID)
				var cancel context.CancelFunc
				receiveContext, cancel = context.WithTimeout(context.Background(), p.options.CancelGrace)
				cancelWait = cancel
				continue
			}
			if canceled {
				p.killAndWait()
				return ctx.Err()
			}
			return p.crashError(err)
		}
		if message.Type != "event" {
			return fmt.Errorf("unexpected worker message type %q during run", message.Type)
		}
		event, err := decodeProcessEvent(message)
		if err != nil {
			return err
		}
		if event.RunID != "" && event.RunID != plan.RunID {
			return fmt.Errorf("worker event run ID %q does not match %q", event.RunID, plan.RunID)
		}
		if event.Type == model.EventCaseStarted {
			p.stateMu.Lock()
			p.lastCase = event.TestID
			p.stateMu.Unlock()
		}
		if err := sink.Emit(event); err != nil {
			p.killAndWait()
			return err
		}
		if event.Type == model.EventWorkerError {
			return p.crashError(errors.New(event.Message))
		}
		if event.Type == model.EventRunFinished {
			if canceled || ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
	}
}

// Ping checks that the worker is responsive.
func (p *Process) Ping(ctx context.Context) error {
	return p.simpleCommand(ctx, "ping", "pong", nil)
}

// Reset asks an idle worker to clear reusable per-run state.
func (p *Process) Reset(ctx context.Context) error {
	return p.simpleCommand(ctx, "reset", "reset_finished", map[string]any{})
}

// Reload advances the worker to a newer compatible generation.
func (p *Process) Reload(ctx context.Context, generation uint64) error {
	if generation < p.Generation() {
		return fmt.Errorf("reload generation %d is older than %d", generation, p.Generation())
	}
	if err := p.simpleCommand(ctx, "reload", "reload_finished", map[string]any{"generation": generation}); err != nil {
		return err
	}
	p.stateMu.Lock()
	p.generation = generation
	p.stateMu.Unlock()
	return nil
}

// Cancel requests cooperative cancellation of the active run.
func (p *Process) Cancel(_ context.Context, runID string) error {
	payload, err := json.Marshal(map[string]any{"run_id": runID})
	if err != nil {
		return err
	}
	return p.send(protocol.Envelope{Version: protocol.Version, Type: "cancel", RequestID: runID, Payload: payload})
}

// Shutdown asks the exact child process to exit and kills only that process if
// it does not honor the request before ctx expires.
func (p *Process) Shutdown(ctx context.Context) error {
	p.opMu.Lock()
	defer p.opMu.Unlock()
	p.stateMu.Lock()
	if p.closed {
		p.stateMu.Unlock()
		return nil
	}
	p.closed = true
	p.stateMu.Unlock()
	_ = p.send(protocol.Envelope{Version: protocol.Version, Type: "shutdown"})
	select {
	case <-p.processDone:
		_ = p.conn.Close()
		p.outputWait.Wait()
		return nil
	case <-ctx.Done():
		p.killAndWait()
		return ctx.Err()
	}
}

func (p *Process) simpleCommand(ctx context.Context, commandType, responseType string, value any) error {
	p.opMu.Lock()
	defer p.opMu.Unlock()
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	requestID, err := randomToken()
	if err != nil {
		return err
	}
	if err := p.send(protocol.Envelope{Version: protocol.Version, Type: commandType, RequestID: requestID, Payload: payload}); err != nil {
		return err
	}
	for {
		message, err := p.receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				p.killAndWait()
				return ctx.Err()
			}
			return p.crashError(err)
		}
		if message.Type == "event" {
			event, decodeErr := decodeProcessEvent(message)
			if decodeErr != nil {
				return decodeErr
			}
			if event.Type == model.EventWorkerReady {
				continue
			}
			return fmt.Errorf("unexpected event %q while waiting for %s", event.Type, responseType)
		}
		if message.Type != responseType {
			return fmt.Errorf("unexpected worker message type %q, want %q", message.Type, responseType)
		}
		if message.RequestID != requestID {
			return fmt.Errorf("worker response request ID %q does not match %q", message.RequestID, requestID)
		}
		return nil
	}
}

func (p *Process) send(message protocol.Envelope) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.stateMu.Lock()
	closed := p.closed
	p.stateMu.Unlock()
	if closed && message.Type != "shutdown" {
		return errors.New("worker is closed")
	}
	if err := p.encoder.Encode(message); err != nil {
		return fmt.Errorf("send worker %s command: %w", message.Type, err)
	}
	return nil
}

func (p *Process) receive(ctx context.Context) (protocol.Envelope, error) {
	select {
	case <-ctx.Done():
		return protocol.Envelope{}, ctx.Err()
	case result := <-p.messages:
		if result.err != nil {
			return protocol.Envelope{}, result.err
		}
		if result.envelope.Version != protocol.Version {
			return protocol.Envelope{}, fmt.Errorf("worker protocol version %d does not match %d", result.envelope.Version, protocol.Version)
		}
		return result.envelope, nil
	}
}

func (p *Process) readLoop(decoder *protocol.Decoder) {
	for {
		var message protocol.Envelope
		if err := decoder.Decode(&message); err != nil {
			p.messages <- processMessage{err: err}
			return
		}
		p.messages <- processMessage{envelope: message}
	}
}

func (p *Process) crashError(err error) error {
	p.stateMu.Lock()
	lastCase := p.lastCase
	waitErr := p.waitErr
	p.stateMu.Unlock()
	if waitErr != nil && (errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)) {
		err = waitErr
	}
	return &WorkerCrashError{PID: p.PID(), LastCase: lastCase, Err: err}
}

func (p *Process) killAndWait() {
	p.stateMu.Lock()
	p.closed = true
	p.stateMu.Unlock()
	if p.conn != nil {
		_ = p.conn.Close()
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	if p.processDone != nil {
		<-p.processDone
	}
	p.outputWait.Wait()
}

func decodeProcessEvent(message protocol.Envelope) (model.Event, error) {
	var wireEvent struct {
		Kind  model.EventType `json:"kind"`
		RunID string          `json:"run_id"`
		Data  model.Event     `json:"data"`
	}
	if err := json.Unmarshal(message.Payload, &wireEvent); err != nil {
		return model.Event{}, fmt.Errorf("decode worker event: %w", err)
	}
	if wireEvent.Kind == "" {
		return model.Event{}, errors.New("worker event has no kind")
	}
	event := wireEvent.Data
	event.ProtocolVersion = model.ProtocolVersion
	event.Type = wireEvent.Kind
	event.RunID = wireEvent.RunID
	return event, nil
}
