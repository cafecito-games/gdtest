// Package service owns daemon-hosted project sessions and persistent workers.
package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/worker"
)

// RunRequest contains the path-specific lease data and path-neutral plan.
type RunRequest struct {
	Project string         `json:"project"`
	Godot   string         `json:"godot"`
	Plan    model.TestPlan `json:"plan"`
}

// WarmRequest prepares one worker for a project generation.
type WarmRequest struct {
	Project    string `json:"project"`
	Godot      string `json:"godot"`
	Generation uint64 `json:"generation"`
}

type managedWorker interface {
	Run(context.Context, model.TestPlan, executor.EventSink) error
	Ping(context.Context) error
	Reset(context.Context) error
	Shutdown(context.Context) error
	PID() int
	Generation() uint64
}

type workerFactory func(context.Context, worker.ProcessOptions) (managedWorker, error)

type session struct {
	mu       sync.Mutex
	project  string
	godot    string
	worker   managedWorker
	lastUsed time.Time
	runs     uint64
}

// SessionInfo is the stable daemon-sessions response.
type SessionInfo struct {
	Project    string    `json:"project"`
	Godot      string    `json:"godot"`
	Generation uint64    `json:"generation"`
	WorkerPID  int       `json:"worker_pid"`
	Runs       uint64    `json:"runs"`
	LastUsed   time.Time `json:"last_used"`
}

// SessionService implements daemon operations with one persistent worker per live project.
type SessionService struct {
	mu       sync.Mutex
	sessions map[string]*session
	factory  workerFactory
}

// NewSessionService creates an empty service using real Godot workers.
func NewSessionService() *SessionService {
	return newSessionService(func(ctx context.Context, options worker.ProcessOptions) (managedWorker, error) {
		return worker.Start(ctx, options)
	})
}

func newSessionService(factory workerFactory) *SessionService {
	return &SessionService{sessions: make(map[string]*session), factory: factory}
}

// Handle implements daemon.Handler.
func (s *SessionService) Handle(ctx context.Context, request daemon.Request, emit daemon.EmitFunc) (any, error) {
	switch request.Method {
	case "session.run":
		var value RunRequest
		if err := decodeStrict(request.Payload, &value); err != nil {
			return nil, err
		}
		return s.run(ctx, value, emit)
	case "session.warm":
		var value WarmRequest
		if err := decodeStrict(request.Payload, &value); err != nil {
			return nil, err
		}
		return s.warm(ctx, value)
	case "daemon.sessions":
		return s.List(), nil
	default:
		return nil, fmt.Errorf("unsupported daemon method %q", request.Method)
	}
}

func (s *SessionService) run(ctx context.Context, request RunRequest, emit daemon.EmitFunc) (any, error) {
	if request.Project == "" || request.Godot == "" || request.Plan.RunID == "" {
		return nil, errors.New("run needs project, godot, and plan.run_id")
	}
	entry := s.get(request.Project, request.Godot)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	process, err := s.ensureWorker(ctx, entry, request.Plan.Generation)
	if err != nil {
		return nil, err
	}
	sink := executor.EventSinkFunc(func(event model.Event) error { return emit(event) })
	if err := process.Run(ctx, request.Plan, sink); err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = process.Shutdown(shutdownCtx)
		cancel()
		entry.worker = nil
		return nil, err
	}
	entry.runs++
	entry.lastUsed = time.Now().UTC()
	if err := process.Reset(ctx); err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = process.Shutdown(shutdownCtx)
		cancel()
		entry.worker = nil
		return nil, fmt.Errorf("reset worker after run: %w", err)
	}
	return map[string]any{"worker_pid": process.PID(), "reused": entry.runs > 1}, nil
}

func (s *SessionService) warm(ctx context.Context, request WarmRequest) (any, error) {
	if request.Project == "" || request.Godot == "" {
		return nil, errors.New("warm needs project and godot")
	}
	entry := s.get(request.Project, request.Godot)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	process, err := s.ensureWorker(ctx, entry, request.Generation)
	if err != nil {
		return nil, err
	}
	if err := process.Ping(ctx); err != nil {
		entry.worker = nil
		return nil, err
	}
	entry.lastUsed = time.Now().UTC()
	return map[string]any{"worker_pid": process.PID(), "generation": process.Generation()}, nil
}

func (s *SessionService) get(project, godot string) *session {
	key := project + "\x00" + godot
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]*session)
	}
	if existing := s.sessions[key]; existing != nil {
		return existing
	}
	created := &session{project: project, godot: godot, lastUsed: time.Now().UTC()}
	s.sessions[key] = created
	return created
}

func (s *SessionService) ensureWorker(ctx context.Context, entry *session, generation uint64) (managedWorker, error) {
	if generation == 0 {
		generation = 1
	}
	if entry.worker != nil && entry.worker.Generation() == generation {
		return entry.worker, nil
	}
	if entry.worker != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = entry.worker.Shutdown(shutdownCtx)
		cancel()
		entry.worker = nil
	}
	digest := sha256.Sum256([]byte(entry.project + "\x00" + entry.godot))
	factory := s.factory
	if factory == nil {
		factory = func(ctx context.Context, options worker.ProcessOptions) (managedWorker, error) {
			return worker.Start(ctx, options)
		}
	}
	process, err := factory(ctx, worker.ProcessOptions{
		Godot: entry.godot, Project: entry.project,
		SessionID: "session-" + hex.EncodeToString(digest[:8]), Generation: generation,
	})
	if err != nil {
		return nil, err
	}
	entry.worker = process
	entry.lastUsed = time.Now().UTC()
	return process, nil
}

// List snapshots live sessions in project-path order.
func (s *SessionService) List() []SessionInfo {
	s.mu.Lock()
	entries := make([]*session, 0, len(s.sessions))
	for _, entry := range s.sessions {
		entries = append(entries, entry)
	}
	s.mu.Unlock()
	result := make([]SessionInfo, 0, len(entries))
	for _, entry := range entries {
		entry.mu.Lock()
		if entry.worker != nil {
			result = append(result, SessionInfo{
				Project: entry.project, Godot: entry.godot, Generation: entry.worker.Generation(),
				WorkerPID: entry.worker.PID(), Runs: entry.runs, LastUsed: entry.lastUsed,
			})
		}
		entry.mu.Unlock()
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Project < result[j].Project })
	return result
}

// Close shuts down every exact child owned by this service.
func (s *SessionService) Close(ctx context.Context) error {
	s.mu.Lock()
	entries := make([]*session, 0, len(s.sessions))
	for _, entry := range s.sessions {
		entries = append(entries, entry)
	}
	s.mu.Unlock()
	var joined error
	for _, entry := range entries {
		entry.mu.Lock()
		if entry.worker != nil {
			joined = errors.Join(joined, entry.worker.Shutdown(ctx))
			entry.worker = nil
		}
		entry.mu.Unlock()
	}
	return joined
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode daemon request: %w", err)
	}
	return nil
}
