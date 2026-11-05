package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
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
	options  ProcessOptions
	project  string
	workerID string
	userData string
	cmd      *exec.Cmd
	conn     net.Conn
	encoder  *protocol.Encoder

	opMu       sync.Mutex
	writeMu    sync.Mutex
	stateMu    sync.Mutex
	startupMu  sync.Mutex
	closed     bool
	runID      string
	lastCase   string
	generation uint64
	sequence   uint64

	outputMu       sync.Mutex
	capturedOutput []model.Event
	capturedBytes  int
	outputDropped  bool
	stderrCase     string
	caseCaptureEnd map[string]chan struct{}
	runtimeErrors  map[string][]runtimeDiagnostic

	messages    chan processMessage
	processDone chan struct{}
	waitErr     error
	cleanupErr  error
	startupTime map[string]int64
	outputWait  sync.WaitGroup
}

type processMessage struct {
	envelope protocol.Envelope
	err      error
}

type runtimeDiagnostic struct {
	message  string
	location *model.Location
}

const caseCaptureMarker = "@@GDTEST_CASE_CAPTURE "

var runtimeLocationPattern = regexp.MustCompile(`\(res://(.+):(\d+)\)$`)

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

const startupOutputLimit = 64 << 10

const (
	defaultCaseTimeout = 30 * time.Second
	caseWatchdogGrace  = 250 * time.Millisecond
)

// StartupError identifies the worker startup stage and retains bounded Godot
// output so fresh-checkout and runtime mismatches remain actionable.
type StartupError struct {
	Stage   string
	Err     error
	Project string
	Command string
	Stdout  string
	Stderr  string

	retryable bool
}

func (e *StartupError) Error() string {
	message := fmt.Sprintf("Godot worker startup failed during %s: %v", e.Stage, e.Err)
	if e.Project != "" {
		message += "\nProject: " + e.Project
	}
	if e.Command != "" {
		message += "\nCommand: " + e.Command
	}
	if text := strings.TrimSpace(e.Stdout); text != "" {
		message += "\nGodot stdout tail:\n" + text
	}
	if text := strings.TrimSpace(e.Stderr); text != "" {
		message += "\nGodot stderr tail:\n" + text
	}
	if e.Project != "" {
		message += "\nSuggested action: run gdtest doctor --project " + e.Project
	}
	return message
}

// Unwrap exposes the underlying process, transport, or handshake error.
func (e *StartupError) Unwrap() error { return e.Err }

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
	project, err := canonicalProjectPath(options.Project)
	if err != nil {
		return nil, fmt.Errorf("canonicalize project: %w", err)
	}
	process, err := startOnce(ctx, options, project)
	if err == nil {
		return process, nil
	}
	var startup *StartupError
	if ctx.Err() != nil || !errors.As(err, &startup) || !startup.retryable {
		return nil, err
	}
	bootstrapStarted := time.Now()
	if bootstrapErr := bootstrapProject(ctx, options, project); bootstrapErr != nil {
		return nil, errors.Join(err, fmt.Errorf("godot project bootstrap failed: %w", bootstrapErr))
	}
	bootstrapDuration := time.Since(bootstrapStarted)
	process, retryErr := startOnce(ctx, options, project)
	if retryErr != nil {
		return nil, fmt.Errorf("worker startup still failed after one headless import/bootstrap attempt: %w", retryErr)
	}
	process.startupTime = map[string]int64{"project_bootstrap_import": bootstrapDuration.Nanoseconds()}
	return process, nil
}

// TakeStartupTimings returns one worker's unreported startup phases exactly
// once. Reused workers return no stale bootstrap time on later operations.
func (p *Process) TakeStartupTimings() map[string]int64 {
	p.startupMu.Lock()
	defer p.startupMu.Unlock()
	if len(p.startupTime) == 0 {
		return nil
	}
	result := make(map[string]int64, len(p.startupTime))
	for name, duration := range p.startupTime {
		result[name] = duration
	}
	p.startupTime = nil
	return result
}

func startOnce(ctx context.Context, options ProcessOptions, project string) (*Process, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, &StartupError{
			Stage: "listener setup", Err: fmt.Errorf("open worker listener: %w", err), Project: project,
		}
	}
	defer func() { _ = listener.Close() }()
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	workerID := "worker-" + token[:12]
	userDataName, userDataPath, err := workerUserData(options.SessionID, workerID)
	if err != nil {
		return nil, &StartupError{
			Stage: "user data isolation", Err: err, Project: project,
		}
	}
	if err := os.MkdirAll(userDataPath, 0o700); err != nil {
		return nil, &StartupError{
			Stage:   "user data isolation",
			Err:     fmt.Errorf("create isolated worker user data %s: %w", userDataPath, err),
			Project: project,
		}
	}
	cleanupOnFailure := true
	defer func() {
		if cleanupOnFailure {
			_ = cleanupWorkerUserData(userDataPath)
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	args := []string{
		"--headless", "--path", project,
		"--script", "res://addons/gdtest/worker.gd", "--",
		"--gdtest-worker-port", strconv.Itoa(port),
		"--gdtest-worker-token", token,
		"--gdtest-session", options.SessionID,
		"--gdtest-generation", strconv.FormatUint(options.Generation, 10),
		"--gdtest-worker-id", workerID,
		"--gdtest-user-dir-name", userDataName,
	}
	var command *exec.Cmd
	if options.commandFactory != nil {
		command = options.commandFactory(args)
	} else {
		command = exec.Command(options.Godot, args...)
	}
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return nil, &StartupError{
			Stage: "output capture", Err: fmt.Errorf("capture Godot stdout: %w", err), Project: project,
		}
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return nil, &StartupError{
			Stage: "output capture", Err: fmt.Errorf("capture Godot stderr: %w", err), Project: project,
		}
	}
	if err := command.Start(); err != nil {
		return nil, &StartupError{
			Stage: "process launch", Err: fmt.Errorf("start Godot worker %q in %s: %w", options.Godot, project, err),
			Project: project, Command: options.Godot + " --headless --path " + project,
		}
	}
	p := &Process{
		options: options, project: project, workerID: workerID, userData: userDataPath, cmd: command,
		generation: options.Generation,
		messages:   make(chan processMessage, 256), processDone: make(chan struct{}),
		caseCaptureEnd: make(map[string]chan struct{}), runtimeErrors: make(map[string][]runtimeDiagnostic),
	}
	var outputMu sync.Mutex
	stdoutTail := &tailBuffer{limit: startupOutputLimit}
	stderrTail := &tailBuffer{limit: startupOutputLimit}
	stdoutDestination := io.Writer(io.Discard)
	stderrDestination := io.Writer(io.Discard)
	if options.Stdout != nil {
		stdoutDestination = options.Stdout
	}
	if options.Stderr != nil {
		stderrDestination = options.Stderr
	}
	// Keep startup output private until the authenticated worker is ready. A
	// fresh checkout may fail once while Godot discovers global classes; those
	// transient diagnostics are retained in StartupError but must not look like
	// test failures when the bootstrap-and-retry path succeeds.
	stdoutWriter := &lockedWriter{mu: &outputMu, writer: stdoutTail}
	stderrWriter := &lockedWriter{mu: &outputMu, writer: stderrTail}
	stdout := &processOutputWriter{
		process: p, stream: "stdout",
		writer: stdoutWriter,
	}
	stderr := &processOutputWriter{
		process: p, stream: "stderr",
		writer: stderrWriter,
	}
	p.outputWait.Add(2)
	go drainProcessOutput(&p.outputWait, stdout, stdoutPipe)
	go drainProcessOutput(&p.outputWait, stderr, stderrPipe)
	go func() {
		err := command.Wait()
		cleanupErr := cleanupWorkerUserData(userDataPath)
		if cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
		p.stateMu.Lock()
		p.waitErr = err
		p.cleanupErr = cleanupErr
		p.stateMu.Unlock()
		close(p.processDone)
	}()
	cleanupOnFailure = false

	connection, err := acceptProcess(ctx, listener, options.StartupTimeout, p.processDone)
	if err != nil {
		p.killAndWait()
		p.stateMu.Lock()
		waitErr := p.waitErr
		p.stateMu.Unlock()
		if waitErr != nil {
			err = errors.Join(err, fmt.Errorf("godot process exit: %w", waitErr))
		}
		return nil, startupFailure("connection", err, stdoutTail, stderrTail, true, options, project)
	}
	p.conn = connection
	if err := connection.SetDeadline(time.Now().Add(options.StartupTimeout)); err != nil {
		p.killAndWait()
		return nil, startupFailure(
			"handshake deadline", fmt.Errorf("set worker handshake deadline: %w", err),
			stdoutTail, stderrTail, true, options, project,
		)
	}
	decoder := protocol.NewDecoder(connection, protocol.DefaultMaxMessageSize)
	if err := verifyHandshake(decoder, token, project, options.SessionID, workerID, options.Generation); err != nil {
		p.killAndWait()
		return nil, startupFailure(
			"authenticated handshake", err, stdoutTail, stderrTail, true, options, project,
		)
	}
	var readyMessage protocol.Envelope
	if err := decoder.Decode(&readyMessage); err != nil {
		p.killAndWait()
		return nil, startupFailure(
			"worker readiness", fmt.Errorf("read worker-ready event: %w", err),
			stdoutTail, stderrTail, true, options, project,
		)
	}
	readyEvent, err := p.decodeEvent(readyMessage)
	if err != nil || readyEvent.Type != model.EventWorkerReady {
		p.killAndWait()
		if err == nil {
			err = fmt.Errorf("expected worker_ready event, got %q", readyEvent.Type)
		}
		return nil, startupFailure("worker readiness", err, stdoutTail, stderrTail, true, options, project)
	}
	p.sequence = 0
	if err := connection.SetDeadline(time.Time{}); err != nil {
		p.killAndWait()
		return nil, startupFailure(
			"handshake completion", fmt.Errorf("clear worker handshake deadline: %w", err),
			stdoutTail, stderrTail, true, options, project,
		)
	}
	p.encoder = protocol.NewEncoder(connection)
	outputMu.Lock()
	stdoutWriter.writer = stdoutDestination
	stderrWriter.writer = stderrDestination
	outputMu.Unlock()
	go p.readLoop(decoder)
	return p, nil
}

func workerUserData(sessionID, workerID string) (string, string, error) {
	sessionDigest := sha256.Sum256([]byte(sessionID))
	sessionName := fmt.Sprintf("%x", sessionDigest[:16])
	relative := filepath.ToSlash(filepath.Join("gdtest", "workers", sessionName, workerID))
	base, err := godotDataRoot()
	if err != nil {
		return "", "", err
	}
	return relative, filepath.Join(base, filepath.FromSlash(relative)), nil
}

func godotDataRoot() (string, error) {
	switch runtime.GOOS {
	case "windows":
		if value := os.Getenv("APPDATA"); value != "" {
			return value, nil
		}
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly":
		if value := os.Getenv("XDG_DATA_HOME"); value != "" {
			return value, nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for Godot data: %w", err)
		}
		return filepath.Join(home, ".local", "share"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for Godot data: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	}
	return "", fmt.Errorf("isolated Godot user data is unsupported on %s", runtime.GOOS)
}

func cleanupWorkerUserData(directory string) error {
	if directory == "" {
		return nil
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("remove isolated worker user data %s: %w", directory, err)
	}
	_ = os.Remove(filepath.Dir(directory))
	return nil
}

func bootstrapProject(ctx context.Context, options ProcessOptions, project string) error {
	arguments := []string{"--headless", "--editor", "--path", project, "--quit"}
	var command *exec.Cmd
	if options.commandFactory != nil {
		command = options.commandFactory(arguments)
	} else {
		command = exec.CommandContext(ctx, options.Godot, arguments...)
	}
	var output tailBuffer
	output.limit = startupOutputLimit
	var outputMu sync.Mutex
	command.Stdout = &lockedWriter{mu: &outputMu, writer: &output}
	command.Stderr = &lockedWriter{mu: &outputMu, writer: &output}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start %s --headless --editor: %w", options.Godot, err)
	}
	stopCancellation := context.AfterFunc(ctx, func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	})
	defer stopCancellation()
	if err := command.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("run %s --headless --editor: %w\nGodot output tail:\n%s", options.Godot, err, strings.TrimSpace(output.String()))
	}
	return nil
}

func startupFailure(
	stage string,
	err error,
	stdout *tailBuffer,
	stderr *tailBuffer,
	retryable bool,
	options ProcessOptions,
	project string,
) error {
	return &StartupError{
		Stage: stage, Err: err, Project: project,
		Command: fmt.Sprintf("%s --headless --path %s --script res://addons/gdtest/worker.gd", options.Godot, project),
		Stdout:  stdout.String(), Stderr: stderr.String(), retryable: retryable,
	}
}

type tailBuffer struct {
	limit int
	data  []byte
}

func (b *tailBuffer) Write(value []byte) (int, error) {
	written := len(value)
	if b.limit <= 0 {
		return written, nil
	}
	if len(value) >= b.limit {
		b.data = append(b.data[:0], value[len(value)-b.limit:]...)
		return written, nil
	}
	overflow := len(b.data) + len(value) - b.limit
	if overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, value...)
	return written, nil
}

func (b *tailBuffer) String() string { return string(bytes.Clone(b.data)) }

func acceptProcess(
	ctx context.Context,
	listener net.Listener,
	timeout time.Duration,
	processDone <-chan struct{},
) (net.Conn, error) {
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
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-processDone:
		return nil, errors.New("godot process exited before connecting to the worker listener")
	case <-timer.C:
		return nil, fmt.Errorf("worker did not connect within %s", timeout)
	case accepted := <-result:
		return accepted.connection, accepted.err
	}
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
	if err := validateWorkerPlan(plan); err != nil {
		return err
	}
	p.sequence = 0
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
	p.clearCapturedOutput()
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
	runtimeCaseError := false
	var cancelWait context.CancelFunc
	var caseDeadline time.Time
	var activeCase model.Event
	var activeCaseStarted time.Time
	defer func() {
		if cancelWait != nil {
			cancelWait()
		}
	}()
	caseTimeout := plan.CaseTimeout
	if caseTimeout <= 0 {
		caseTimeout = defaultCaseTimeout
	}
	for {
		message, err := p.receiveBefore(receiveContext, caseDeadline)
		if err != nil {
			if !caseDeadline.IsZero() && errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				p.killAndWait()
				if outputErr := p.emitCapturedOutputFor(sink, activeCase.TestID); outputErr != nil {
					return outputErr
				}
				message := fmt.Sprintf("case timed out after %s", caseTimeout)
				if emitErr := sink.Emit(model.Event{
					ProtocolVersion: model.ProtocolVersion, Type: model.EventCaseFinished,
					RunID: plan.RunID, TestID: activeCase.TestID, Name: activeCase.Name,
					Status: "error", Message: message, Location: activeCase.Location,
					Duration: time.Since(activeCaseStarted),
				}); emitErr != nil {
					return emitErr
				}
				if emitErr := sink.Emit(model.Event{
					ProtocolVersion: model.ProtocolVersion, Type: model.EventWorkerTainted,
					RunID: plan.RunID, TestID: activeCase.TestID, Message: "case timeout",
					Metadata: map[string]any{"reason": "case_timeout"},
				}); emitErr != nil {
					return emitErr
				}
				return sink.Emit(model.Event{
					ProtocolVersion: model.ProtocolVersion, Type: model.EventRunFinished,
					RunID: plan.RunID, Status: "error", Generation: plan.Generation,
				})
			}
			if !canceled && ctx.Err() != nil {
				caseDeadline = time.Time{}
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
		event, err := p.decodeEvent(message)
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
			p.beginCaseCapture(event.TestID)
			activeCase = event
			activeCaseStarted = time.Now()
			caseDeadline = activeCaseStarted.Add(caseTimeout + caseWatchdogGrace)
		}
		if event.Type == model.EventCaseFinished {
			caseDeadline = time.Time{}
			if !canceled {
				receiveContext = ctx
			}
			p.waitForCaseCapture(ctx, event.TestID)
			if diagnostic, ok := p.takeRuntimeDiagnostic(event.TestID); ok {
				event.Status = "error"
				event.Message = diagnostic.message
				event.Location = diagnostic.location
				runtimeCaseError = true
			}
		}
		if event.Type == model.EventRunFinished && runtimeCaseError {
			event.Status = "error"
		}
		switch event.Type {
		case model.EventCaseFinished:
			if err := p.emitCapturedOutputFor(sink, event.TestID); err != nil {
				p.killAndWait()
				return err
			}
		case model.EventRunFinished:
			if err := p.emitCapturedOutput(sink); err != nil {
				p.killAndWait()
				return err
			}
		}
		if err := sink.Emit(event); err != nil {
			p.killAndWait()
			return err
		}
		if event.Type == model.EventCaseStarted {
			if err := p.emitCapturedOutputFor(sink, event.TestID); err != nil {
				p.killAndWait()
				return err
			}
		}
		if event.Type == model.EventWorkerError {
			if !recoverableRunDiagnostic(event) {
				return fmt.Errorf("worker rejected run: %s: %s", event.Name, event.Message)
			}
		}
		if event.Type == model.EventRunFinished {
			if canceled || ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
	}
}

// Bench executes one benchmark plan while retaining the process for reuse.
func (p *Process) Bench(
	ctx context.Context,
	plan model.BenchmarkPlan,
	sink executor.BenchmarkEventSink,
) error {
	p.opMu.Lock()
	defer p.opMu.Unlock()
	if sink == nil {
		return errors.New("worker benchmark event sink is required")
	}
	if err := validateBenchmarkPlan(plan); err != nil {
		return err
	}
	p.sequence = 0
	if plan.Generation != p.Generation() {
		return fmt.Errorf("benchmark plan generation %d does not match worker generation %d", plan.Generation, p.Generation())
	}
	p.stateMu.Lock()
	if p.closed {
		p.stateMu.Unlock()
		return errors.New("worker is closed")
	}
	p.runID = plan.RunID
	p.lastCase = ""
	p.stateMu.Unlock()
	p.clearCapturedOutput()
	defer func() {
		p.stateMu.Lock()
		p.runID = ""
		p.lastCase = ""
		p.stateMu.Unlock()
	}()
	payload, err := json.Marshal(struct {
		RunID      string              `json:"run_id"`
		Generation uint64              `json:"generation"`
		Plan       model.BenchmarkPlan `json:"plan"`
	}{RunID: plan.RunID, Generation: plan.Generation, Plan: plan})
	if err != nil {
		return fmt.Errorf("encode benchmark plan: %w", err)
	}
	if err := p.send(protocol.Envelope{Version: protocol.Version, Type: "bench", RequestID: plan.RunID, Payload: payload}); err != nil {
		return err
	}
	receiveContext := ctx
	deadline := time.Now().Add(defaultCaseTimeout + caseWatchdogGrace)
	canceled := false
	var cancelWait context.CancelFunc
	defer func() {
		if cancelWait != nil {
			cancelWait()
		}
	}()
	for {
		message, receiveErr := p.receiveBefore(receiveContext, deadline)
		if receiveErr != nil {
			if !canceled && ctx.Err() != nil {
				canceled = true
				_ = p.Cancel(context.Background(), plan.RunID)
				var cancel context.CancelFunc
				receiveContext, cancel = context.WithTimeout(context.Background(), p.options.CancelGrace)
				cancelWait = cancel
				deadline = time.Time{}
				continue
			}
			if canceled {
				p.killAndWait()
				return ctx.Err()
			}
			if errors.Is(receiveErr, context.DeadlineExceeded) {
				p.killAndWait()
				return fmt.Errorf("benchmark %s timed out after %s", p.lastBenchmark(), defaultCaseTimeout)
			}
			return p.crashError(receiveErr)
		}
		if message.Type != "event" {
			return fmt.Errorf("unexpected worker message type %q during benchmark", message.Type)
		}
		event, decodeErr := p.decodeBenchmarkEvent(message)
		if decodeErr != nil {
			return decodeErr
		}
		if event.RunID != plan.RunID {
			return fmt.Errorf("worker benchmark event run ID %q does not match %q", event.RunID, plan.RunID)
		}
		deadline = time.Now().Add(defaultCaseTimeout + caseWatchdogGrace)
		if !canceled {
			if diagnostic, captureID, exists := p.benchmarkRuntimeDiagnostic(event); exists {
				benchmarkID := event.BenchmarkID
				if benchmarkID == "" {
					benchmarkID = p.lastBenchmark()
				}
				failure := model.BenchmarkEvent{
					ProtocolVersion: model.ProtocolVersion, Type: model.EventBenchmarkWorkerError,
					RunID: plan.RunID, BenchmarkID: benchmarkID,
					Message: diagnostic.message, Location: diagnostic.location,
					Metadata: map[string]any{"capture_id": captureID},
				}
				_ = sink.EmitBenchmark(failure)
				return fmt.Errorf("benchmark %s runtime error: %s", benchmarkID, diagnostic.message)
			}
		}
		if event.Type == model.EventBenchmarkStarted {
			p.stateMu.Lock()
			p.lastCase = event.BenchmarkID
			p.stateMu.Unlock()
		}
		if canceled {
			if event.Type == model.EventBenchmarkRunFinished {
				return ctx.Err()
			}
			continue
		}
		if err := sink.EmitBenchmark(event); err != nil {
			p.killAndWait()
			return err
		}
		if event.Type == model.EventBenchmarkWorkerError {
			return fmt.Errorf("worker rejected benchmark: %s", event.Message)
		}
		if event.Type == model.EventBenchmarkFinished && event.Status != "pass" {
			return fmt.Errorf("benchmark %s ended with status %s: %s", event.BenchmarkID, event.Status, event.Message)
		}
		if event.Type == model.EventBenchmarkRunFinished {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if event.Status != "pass" {
				return fmt.Errorf("benchmark run ended with status %s", event.Status)
			}
			return nil
		}
	}
}

func (p *Process) benchmarkRuntimeDiagnostic(event model.BenchmarkEvent) (runtimeDiagnostic, string, bool) {
	if event.Metadata == nil {
		return runtimeDiagnostic{}, "", false
	}
	captures := make([]string, 0, 2)
	if value, ok := event.Metadata["completed_capture"].(string); ok && value != "" {
		captures = append(captures, value)
	}
	if values, ok := event.Metadata["completed_captures"].([]any); ok {
		for _, value := range values {
			if capture, valid := value.(string); valid && capture != "" {
				captures = append(captures, capture)
			}
		}
	}
	for _, capture := range captures {
		p.waitForCaseCapture(context.Background(), capture)
		if diagnostic, exists := p.takeRuntimeDiagnostic(capture); exists {
			return diagnostic, capture, true
		}
	}
	return runtimeDiagnostic{}, "", false
}

func (p *Process) lastBenchmark() string {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.lastCase
}

func (p *Process) receiveBefore(ctx context.Context, deadline time.Time) (protocol.Envelope, error) {
	if deadline.IsZero() {
		return p.receive(ctx)
	}
	receiveContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	return p.receive(receiveContext)
}

func recoverableRunDiagnostic(event model.Event) bool {
	code := event.Name
	if event.Metadata != nil {
		if value, ok := event.Metadata["code"].(string); ok {
			code = value
		}
	}
	switch code {
	case "worker.discovery", "worker.script_load", "worker.invalid_test":
		return true
	default:
		return false
	}
}

// Discover completes engine-dependent registration and provider expansion
// without executing hooks or case bodies.
func (p *Process) Discover(ctx context.Context, plan model.TestPlan) (model.DiscoveryManifest, error) {
	p.opMu.Lock()
	defer p.opMu.Unlock()
	if err := validateWorkerPlan(plan); err != nil {
		return model.DiscoveryManifest{}, err
	}
	p.sequence = 0
	if plan.Generation != p.Generation() {
		return model.DiscoveryManifest{}, fmt.Errorf(
			"discovery plan generation %d does not match worker generation %d",
			plan.Generation,
			p.Generation(),
		)
	}
	p.stateMu.Lock()
	closed := p.closed
	p.stateMu.Unlock()
	if closed {
		return model.DiscoveryManifest{}, errors.New("worker is closed")
	}
	requestID, err := randomToken()
	if err != nil {
		return model.DiscoveryManifest{}, err
	}
	payload, err := json.Marshal(struct {
		Generation uint64         `json:"generation"`
		Plan       model.TestPlan `json:"plan"`
	}{Generation: plan.Generation, Plan: plan})
	if err != nil {
		return model.DiscoveryManifest{}, fmt.Errorf("encode discovery plan: %w", err)
	}
	if err := p.send(protocol.Envelope{
		Version: protocol.Version, Type: "discover", RequestID: requestID, Payload: payload,
	}); err != nil {
		return model.DiscoveryManifest{}, err
	}
	for {
		message, receiveErr := p.receive(ctx)
		if receiveErr != nil {
			if ctx.Err() != nil {
				p.killAndWait()
				return model.DiscoveryManifest{}, ctx.Err()
			}
			return model.DiscoveryManifest{}, p.crashError(receiveErr)
		}
		if message.Type == "event" {
			event, decodeErr := p.decodeEvent(message)
			if decodeErr != nil {
				return model.DiscoveryManifest{}, decodeErr
			}
			if event.Type == model.EventWorkerReady {
				continue
			}
			if event.Type == model.EventWorkerError {
				return model.DiscoveryManifest{}, fmt.Errorf(
					"worker rejected discovery: %s: %s", event.Name, event.Message,
				)
			}
			return model.DiscoveryManifest{}, fmt.Errorf(
				"unexpected event %q during discovery", event.Type,
			)
		}
		if message.Type != "discover_finished" {
			return model.DiscoveryManifest{}, fmt.Errorf(
				"unexpected worker message type %q during discovery", message.Type,
			)
		}
		if message.RequestID != requestID {
			return model.DiscoveryManifest{}, fmt.Errorf(
				"discovery response request ID %q does not match %q", message.RequestID, requestID,
			)
		}
		var manifest model.DiscoveryManifest
		if err := json.Unmarshal(message.Payload, &manifest); err != nil {
			return model.DiscoveryManifest{}, fmt.Errorf("decode discovery manifest: %w", err)
		}
		if err := validateDiscoveryManifest(manifest, plan); err != nil {
			return model.DiscoveryManifest{}, err
		}
		return manifest, nil
	}
}

func validateDiscoveryManifest(manifest model.DiscoveryManifest, plan model.TestPlan) error {
	if manifest.ProtocolVersion != model.ProtocolVersion {
		return fmt.Errorf(
			"discovery manifest protocol %d does not match %d",
			manifest.ProtocolVersion,
			model.ProtocolVersion,
		)
	}
	if manifest.Generation != plan.Generation {
		return fmt.Errorf(
			"discovery manifest generation %d does not match plan %d",
			manifest.Generation,
			plan.Generation,
		)
	}
	if manifest.ContentFingerprint != plan.ContentFingerprint {
		return errors.New("discovery manifest content fingerprint does not match plan")
	}
	seen := make(map[string]struct{})
	for _, file := range manifest.Files {
		if !validManifestPath(file.Path) {
			return fmt.Errorf("discovery manifest has invalid project-relative path %q", file.Path)
		}
		for _, testCase := range file.Cases {
			if testCase.ID == "" {
				return errors.New("discovery manifest contains an empty case ID")
			}
			idFile, _, hasSeparator := strings.Cut(testCase.ID, "::")
			if !hasSeparator || idFile != file.Path || !validManifestPath(testCase.File) {
				return fmt.Errorf("discovery manifest contains an invalid case identity %q", testCase.ID)
			}
			if _, exists := seen[testCase.ID]; exists {
				return fmt.Errorf("discovery manifest contains duplicate case ID %q", testCase.ID)
			}
			seen[testCase.ID] = struct{}{}
			for _, suite := range testCase.Suites {
				if suite.ID == "" || !validManifestPath(suite.File) {
					return fmt.Errorf("discovery manifest contains invalid suite metadata for %q", testCase.ID)
				}
			}
		}
	}
	for _, discoveryError := range manifest.Errors {
		if discoveryError.Location != nil && !validManifestPath(discoveryError.Location.File) {
			return fmt.Errorf("discovery manifest contains invalid diagnostic path %q", discoveryError.Location.File)
		}
	}
	return nil
}

func validManifestPath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, `\`) ||
		strings.HasPrefix(value, "res://") || strings.HasPrefix(value, "/") ||
		(len(value) >= 3 && value[1] == ':' && value[2] == '/') {
		return false
	}
	cleanPath := filepath.ToSlash(filepath.Clean(value))
	return cleanPath == value && cleanPath != "." && cleanPath != ".." &&
		!strings.HasPrefix(cleanPath, "../")
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
		p.stateMu.Lock()
		cleanupErr := p.cleanupErr
		p.stateMu.Unlock()
		return cleanupErr
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
			event, decodeErr := p.decodeEvent(message)
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

func (p *Process) decodeEvent(message protocol.Envelope) (model.Event, error) {
	if message.Type != "event" {
		return model.Event{}, fmt.Errorf("expected worker event, got %q", message.Type)
	}
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
		return model.Event{}, fmt.Errorf("decode worker event: %w", err)
	}
	if wireEvent.Kind == "" {
		return model.Event{}, errors.New("worker event has no kind")
	}
	if wireEvent.WorkerID != p.workerID || wireEvent.SessionID != p.options.SessionID {
		return model.Event{}, errors.New("worker event identity does not match authenticated worker")
	}
	if wireEvent.Generation != p.Generation() {
		return model.Event{}, fmt.Errorf(
			"worker event generation %d does not match %d",
			wireEvent.Generation,
			p.Generation(),
		)
	}
	if wireEvent.Sequence != p.sequence+1 {
		return model.Event{}, fmt.Errorf(
			"worker event sequence %d does not follow %d",
			wireEvent.Sequence,
			p.sequence,
		)
	}
	p.sequence = wireEvent.Sequence
	event := wireEvent.Data
	event.ProtocolVersion = model.ProtocolVersion
	event.Type = wireEvent.Kind
	event.RunID = wireEvent.RunID
	if event.Location != nil {
		location := *event.Location
		location.File = strings.TrimPrefix(location.File, "res://")
		if location.File == "" {
			event.Location = nil
			return event, nil
		}
		if !validManifestPath(location.File) {
			return model.Event{}, fmt.Errorf(
				"worker event contains invalid diagnostic path %q",
				event.Location.File,
			)
		}
		event.Location = &location
	}
	return event, nil
}

func (p *Process) decodeBenchmarkEvent(message protocol.Envelope) (model.BenchmarkEvent, error) {
	if message.Type != "event" {
		return model.BenchmarkEvent{}, fmt.Errorf("expected worker benchmark event, got %q", message.Type)
	}
	var wireEvent struct {
		Kind       model.BenchmarkEventType `json:"kind"`
		WorkerID   string                   `json:"worker_id"`
		SessionID  string                   `json:"session_id"`
		Generation uint64                   `json:"generation"`
		RunID      string                   `json:"run_id"`
		Sequence   uint64                   `json:"sequence"`
		Data       model.BenchmarkEvent     `json:"data"`
	}
	if err := json.Unmarshal(message.Payload, &wireEvent); err != nil {
		return model.BenchmarkEvent{}, fmt.Errorf("decode worker benchmark event: %w", err)
	}
	if wireEvent.Kind == "" {
		return model.BenchmarkEvent{}, errors.New("worker benchmark event has no kind")
	}
	switch wireEvent.Kind {
	case model.EventBenchmarkRunStarted,
		model.EventBenchmarkStarted,
		model.EventBenchmarkSampleStarted,
		model.EventBenchmarkSample,
		model.EventBenchmarkFinished,
		model.EventBenchmarkRunFinished,
		model.EventBenchmarkWorkerError:
	default:
		return model.BenchmarkEvent{}, fmt.Errorf("worker benchmark event has unknown kind %q", wireEvent.Kind)
	}
	if wireEvent.WorkerID != p.workerID || wireEvent.SessionID != p.options.SessionID {
		return model.BenchmarkEvent{}, errors.New("worker benchmark event identity does not match authenticated worker")
	}
	if wireEvent.Generation != p.Generation() {
		return model.BenchmarkEvent{}, fmt.Errorf("worker benchmark event generation %d does not match %d", wireEvent.Generation, p.Generation())
	}
	if wireEvent.Sequence != p.sequence+1 {
		return model.BenchmarkEvent{}, fmt.Errorf("worker benchmark event sequence %d does not follow %d", wireEvent.Sequence, p.sequence)
	}
	p.sequence = wireEvent.Sequence
	event := wireEvent.Data
	event.ProtocolVersion = model.ProtocolVersion
	event.Type = wireEvent.Kind
	event.RunID = wireEvent.RunID
	if event.Location != nil {
		location := *event.Location
		location.File = strings.TrimPrefix(location.File, "res://")
		if location.File == "" || !validManifestPath(location.File) {
			return model.BenchmarkEvent{}, fmt.Errorf("worker benchmark event contains invalid diagnostic path %q", event.Location.File)
		}
		event.Location = &location
	}
	return event, nil
}

func validateWorkerPlan(plan model.TestPlan) error {
	if plan.ProtocolVersion != model.ProtocolVersion {
		return fmt.Errorf(
			"test plan protocol %d does not match %d",
			plan.ProtocolVersion,
			model.ProtocolVersion,
		)
	}
	if plan.RunID == "" || plan.Generation == 0 {
		return errors.New("test plan needs a run ID and positive generation")
	}
	if plan.CaseTimeout < 0 {
		return errors.New("test plan case timeout cannot be negative")
	}
	for _, file := range plan.Files {
		if !validManifestPath(file.Path) {
			return fmt.Errorf("test plan has invalid project-relative path %q", file.Path)
		}
	}
	return nil
}

func validateBenchmarkPlan(plan model.BenchmarkPlan) error {
	if plan.ProtocolVersion != model.ProtocolVersion {
		return fmt.Errorf("benchmark plan protocol %d does not match %d", plan.ProtocolVersion, model.ProtocolVersion)
	}
	if plan.RunID == "" || plan.Generation == 0 {
		return errors.New("benchmark plan needs a run ID and positive generation")
	}
	if plan.Warmup < 0 || plan.Samples <= 0 || plan.Iterations <= 0 {
		return errors.New("benchmark plan warmup must be non-negative and samples and iterations must be positive")
	}
	seen := make(map[string]struct{})
	for _, file := range plan.Files {
		if !validManifestPath(file.Path) {
			return fmt.Errorf("benchmark plan has invalid project-relative path %q", file.Path)
		}
		for _, item := range file.Benchmarks {
			if item.ID == "" || item.MethodName == "" {
				return errors.New("benchmark plan entries need IDs and method names")
			}
			if _, exists := seen[item.ID]; exists {
				return fmt.Errorf("benchmark plan contains duplicate ID %q", item.ID)
			}
			seen[item.ID] = struct{}{}
		}
	}
	return nil
}

const maxCapturedOutputBytes = 1 << 20

type processOutputWriter struct {
	process *Process
	stream  string
	writer  io.Writer
	pending []byte
}

func (w *processOutputWriter) Write(data []byte) (int, error) {
	if w.stream != "stderr" {
		written, err := w.writer.Write(data)
		if written > 0 {
			w.process.captureOutput(w.stream, data[:written], "")
		}
		return written, err
	}
	w.pending = append(w.pending, data...)
	for {
		newline := bytes.IndexByte(w.pending, '\n')
		if newline < 0 {
			break
		}
		line := append([]byte(nil), w.pending[:newline+1]...)
		w.pending = w.pending[newline+1:]
		if err := w.process.captureStderrLine(line, w.writer); err != nil {
			return 0, err
		}
	}
	return len(data), nil
}

func (w *processOutputWriter) flush() {
	if len(w.pending) == 0 {
		return
	}
	line := append([]byte(nil), w.pending...)
	w.pending = nil
	_ = w.process.captureStderrLine(line, w.writer)
}

func drainProcessOutput(wait *sync.WaitGroup, destination *processOutputWriter, source io.Reader) {
	defer wait.Done()
	_, _ = io.Copy(destination, source)
	destination.flush()
}

func (p *Process) captureOutput(stream string, data []byte, testID string) {
	p.stateMu.Lock()
	runID := p.runID
	p.stateMu.Unlock()
	if runID == "" || len(data) == 0 {
		return
	}
	p.outputMu.Lock()
	defer p.outputMu.Unlock()
	remaining := maxCapturedOutputBytes - p.capturedBytes
	if remaining <= 0 {
		p.outputDropped = true
		return
	}
	if len(data) > remaining {
		data = data[:remaining]
		p.outputDropped = true
	}
	text := string(append([]byte(nil), data...))
	p.capturedOutput = append(p.capturedOutput, model.Event{
		ProtocolVersion: model.ProtocolVersion,
		Type:            model.EventOutput,
		RunID:           runID,
		TestID:          testID,
		Message:         text,
		Metadata:        map[string]any{"stream": stream},
	})
	p.capturedBytes += len(data)
}

func (p *Process) captureStderrLine(line []byte, destination io.Writer) error {
	text := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
	if strings.HasPrefix(text, caseCaptureMarker) {
		parts := strings.SplitN(strings.TrimPrefix(text, caseCaptureMarker), " ", 2)
		if len(parts) == 2 {
			var caseID string
			if json.Unmarshal([]byte(parts[1]), &caseID) == nil {
				p.captureMarker(parts[0], caseID)
				return nil
			}
		}
	}
	if _, err := destination.Write(line); err != nil {
		return err
	}
	p.outputMu.Lock()
	caseID := p.stderrCase
	if caseID != "" && strings.HasPrefix(text, "SCRIPT ERROR:") {
		message := strings.TrimSpace(strings.TrimPrefix(text, "SCRIPT ERROR:"))
		p.runtimeErrors[caseID] = append(p.runtimeErrors[caseID], runtimeDiagnostic{message: message})
	} else if caseID != "" && strings.Contains(text, "at:") {
		matches := runtimeLocationPattern.FindStringSubmatch(strings.TrimSpace(text))
		diagnostics := p.runtimeErrors[caseID]
		if len(matches) == 3 && len(diagnostics) > 0 && diagnostics[len(diagnostics)-1].location == nil {
			lineNumber, err := strconv.Atoi(matches[2])
			if err == nil {
				diagnostics[len(diagnostics)-1].location = &model.Location{File: matches[1], Line: lineNumber}
				p.runtimeErrors[caseID] = diagnostics
			}
		}
	}
	p.outputMu.Unlock()
	p.captureOutput("stderr", line, caseID)
	return nil
}

func (p *Process) captureMarker(phase, caseID string) {
	p.outputMu.Lock()
	defer p.outputMu.Unlock()
	switch phase {
	case "start":
		p.stderrCase = caseID
		if _, exists := p.caseCaptureEnd[caseID]; !exists {
			p.caseCaptureEnd[caseID] = make(chan struct{})
		}
	case "end":
		if p.stderrCase == caseID {
			p.stderrCase = ""
		}
		waiter, exists := p.caseCaptureEnd[caseID]
		if !exists {
			waiter = make(chan struct{})
			p.caseCaptureEnd[caseID] = waiter
		}
		select {
		case <-waiter:
		default:
			close(waiter)
		}
	}
}

func (p *Process) beginCaseCapture(caseID string) {
	p.outputMu.Lock()
	if _, exists := p.caseCaptureEnd[caseID]; !exists {
		p.caseCaptureEnd[caseID] = make(chan struct{})
	}
	p.outputMu.Unlock()
}

func (p *Process) waitForCaseCapture(ctx context.Context, caseID string) {
	p.outputMu.Lock()
	waiter := p.caseCaptureEnd[caseID]
	p.outputMu.Unlock()
	if waiter == nil {
		return
	}
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-waiter:
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (p *Process) takeRuntimeDiagnostic(caseID string) (runtimeDiagnostic, bool) {
	p.outputMu.Lock()
	defer p.outputMu.Unlock()
	diagnostics := p.runtimeErrors[caseID]
	delete(p.runtimeErrors, caseID)
	delete(p.caseCaptureEnd, caseID)
	if len(diagnostics) == 0 {
		return runtimeDiagnostic{}, false
	}
	messages := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		messages = append(messages, diagnostic.message)
	}
	return runtimeDiagnostic{message: strings.Join(messages, "; "), location: diagnostics[0].location}, true
}

func (p *Process) clearCapturedOutput() {
	p.outputMu.Lock()
	p.capturedOutput = nil
	p.capturedBytes = 0
	p.outputDropped = false
	p.outputMu.Unlock()
}

func (p *Process) emitCapturedOutput(sink executor.EventSink) error {
	p.outputMu.Lock()
	events := p.capturedOutput
	dropped := p.outputDropped
	p.capturedOutput = nil
	p.capturedBytes = 0
	p.outputDropped = false
	p.outputMu.Unlock()
	for _, event := range events {
		if err := sink.Emit(event); err != nil {
			return err
		}
	}
	if dropped {
		p.stateMu.Lock()
		runID := p.runID
		p.stateMu.Unlock()
		return sink.Emit(model.Event{
			ProtocolVersion: model.ProtocolVersion,
			Type:            model.EventOutput,
			RunID:           runID,
			Message:         "gdtest truncated worker output after 1048576 bytes",
			Metadata:        map[string]any{"truncated": true},
		})
	}
	return nil
}

func (p *Process) emitCapturedOutputFor(sink executor.EventSink, testID string) error {
	p.outputMu.Lock()
	selected := make([]model.Event, 0)
	retained := make([]model.Event, 0, len(p.capturedOutput))
	for _, event := range p.capturedOutput {
		if event.TestID == testID {
			selected = append(selected, event)
		} else {
			retained = append(retained, event)
		}
	}
	p.capturedOutput = retained
	p.outputMu.Unlock()
	for _, event := range selected {
		if err := sink.Emit(event); err != nil {
			return err
		}
	}
	return nil
}
