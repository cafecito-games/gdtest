// Package service owns daemon-hosted project sessions and persistent workers.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	projectidentity "github.com/cafecito-games/gdtest/internal/project"
	"github.com/cafecito-games/gdtest/internal/scheduler"
	sourcewatch "github.com/cafecito-games/gdtest/internal/watch"
	"github.com/cafecito-games/gdtest/internal/worker"
)

// RunRequest contains the path-specific lease data and path-neutral plan.
type RunRequest struct {
	SessionID     string         `json:"session_id"`
	Project       string         `json:"project"`
	Godot         string         `json:"godot"`
	Plan          model.TestPlan `json:"plan"`
	WorkerMaxRuns int            `json:"worker_max_runs,omitempty"`
}

// DiscoverRequest contains the path-specific lease data and path-neutral plan
// used to complete engine-dependent discovery in a persistent worker.
type DiscoverRequest struct {
	SessionID string         `json:"session_id"`
	Project   string         `json:"project"`
	Godot     string         `json:"godot"`
	Plan      model.TestPlan `json:"plan"`
}

// WarmRequest prepares one worker for a project generation.
type WarmRequest struct {
	SessionID  string `json:"session_id"`
	Project    string `json:"project"`
	Godot      string `json:"godot"`
	Generation uint64 `json:"generation"`
}

type managedWorker interface {
	Run(context.Context, model.TestPlan, executor.EventSink) error
	Discover(context.Context, model.TestPlan) (model.DiscoveryManifest, error)
	Ping(context.Context) error
	Reset(context.Context) error
	Reload(context.Context, uint64) error
	Shutdown(context.Context) error
	PID() int
	Generation() uint64
}

type workerFactory func(context.Context, worker.ProcessOptions) (managedWorker, error)

type session struct {
	mu             sync.Mutex
	generationMu   sync.Mutex
	id             string
	project        string
	godot          string
	worker         managedWorker
	source         *sourcewatch.Reconciler
	lastStructural uint64
	lastUsed       time.Time
	runs           uint64
	lifecycle      projectidentity.Lifecycle
}

// SessionInfo is the stable daemon-sessions response.
type SessionInfo struct {
	ID         string    `json:"id"`
	Project    string    `json:"project"`
	Godot      string    `json:"godot"`
	Generation uint64    `json:"generation"`
	WorkerPID  int       `json:"worker_pid"`
	Runs       uint64    `json:"runs"`
	LastUsed   time.Time `json:"last_used"`
}

// SessionService implements daemon operations with one persistent worker per live project.
type SessionService struct {
	mu            sync.Mutex
	sessions      map[string]*session
	factory       workerFactory
	maxWorkers    int
	liveWorkers   int
	workerFreed   chan struct{}
	scheduler     *scheduler.Scheduler
	sourceFactory func(string) *sourcewatch.Reconciler
}

// ServiceOptions controls user-scoped process resource limits.
type ServiceOptions struct {
	MaxWorkers int
}

// NewSessionService creates an empty service using real Godot workers.
func NewSessionService() *SessionService {
	return NewSessionServiceWithOptions(ServiceOptions{MaxWorkers: 4})
}

// NewSessionServiceWithOptions creates a production service with a global worker bound.
func NewSessionServiceWithOptions(options ServiceOptions) *SessionService {
	if options.MaxWorkers <= 0 {
		options.MaxWorkers = 4
	}
	result := newSessionServiceWithCapacity(func(ctx context.Context, workerOptions worker.ProcessOptions) (managedWorker, error) {
		return worker.Start(ctx, workerOptions)
	}, options.MaxWorkers)
	return result
}

func newSessionService(factory workerFactory) *SessionService {
	return newSessionServiceWithCapacity(factory, 4)
}

func newSessionServiceWithCapacity(factory workerFactory, capacity int) *SessionService {
	globalScheduler, err := scheduler.New(scheduler.Options{Capacity: capacity, BackgroundLimit: 1})
	if err != nil {
		panic(err)
	}
	return &SessionService{
		sessions: make(map[string]*session), factory: factory, maxWorkers: capacity,
		workerFreed: make(chan struct{}, 1), scheduler: globalScheduler,
		sourceFactory: sourcewatch.New,
	}
}

// Handle implements daemon.Handler.
func (s *SessionService) Handle(ctx context.Context, request daemon.Request, emit daemon.EmitFunc) (any, error) {
	switch request.Method {
	case "session.run":
		var value RunRequest
		if err := decodeStrict(request.Payload, &value); err != nil {
			return nil, err
		}
		if value.SessionID == "" || value.Project == "" || value.Godot == "" || value.Plan.RunID == "" {
			return nil, errors.New("run needs session_id, project, godot, and plan.run_id")
		}
		entry := s.get(value.SessionID, value.Project, value.Godot)
		result, err := s.reconcileSource(ctx, entry, sourcewatch.Initial)
		if err != nil {
			return nil, fmt.Errorf("reconcile source before run: %w", err)
		}
		if result.Update.Generation > 0 {
			value.Plan.Generation = result.Update.Generation
		}
		return s.schedule(ctx, value.SessionID, value.Plan.Generation, scheduler.Interactive, func(runCtx context.Context) (any, error) {
			return s.run(runCtx, value, emit)
		})
	case "session.discover":
		var value DiscoverRequest
		if err := decodeStrict(request.Payload, &value); err != nil {
			return nil, err
		}
		if value.SessionID == "" || value.Project == "" || value.Godot == "" || value.Plan.RunID == "" {
			return nil, errors.New("discover needs session_id, project, godot, and plan.run_id")
		}
		entry := s.get(value.SessionID, value.Project, value.Godot)
		result, err := s.reconcileSource(ctx, entry, sourcewatch.Initial)
		if err != nil {
			return nil, fmt.Errorf("reconcile source before discovery: %w", err)
		}
		if result.Update.Generation > 0 {
			value.Plan.Generation = result.Update.Generation
		}
		return s.schedule(ctx, value.SessionID, value.Plan.Generation, scheduler.Interactive, func(runCtx context.Context) (any, error) {
			return s.discover(runCtx, value)
		})
	case "session.warm":
		var value WarmRequest
		if err := decodeStrict(request.Payload, &value); err != nil {
			return nil, err
		}
		if value.SessionID == "" || value.Project == "" || value.Godot == "" {
			return nil, errors.New("warm needs session_id, project, and godot")
		}
		entry := s.get(value.SessionID, value.Project, value.Godot)
		result, err := s.reconcileSource(ctx, entry, sourcewatch.Initial)
		if err != nil {
			return nil, fmt.Errorf("reconcile source before warm: %w", err)
		}
		if result.Update.Generation > 0 {
			value.Generation = result.Update.Generation
		}
		return s.schedule(ctx, value.SessionID, value.Generation, scheduler.Background, func(runCtx context.Context) (any, error) {
			return s.warm(runCtx, value)
		})
	case "daemon.sessions":
		return s.List(), nil
	default:
		return nil, fmt.Errorf("unsupported daemon method %q", request.Method)
	}
}

func (s *SessionService) discover(ctx context.Context, request DiscoverRequest) (any, error) {
	entry := s.get(request.SessionID, request.Project, request.Godot)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	latest, err := s.reconcileSource(ctx, entry, sourcewatch.Filesystem)
	if err != nil {
		return nil, fmt.Errorf("reconcile source at discovery start: %w", err)
	}
	if latest.Update.Generation > 0 {
		request.Plan.Generation = latest.Update.Generation
	}
	process, err := s.ensureWorker(ctx, entry, request.Plan.Generation)
	if err != nil {
		return nil, err
	}
	manifest, err := process.Discover(ctx, request.Plan)
	if err != nil {
		s.discardWorker(entry)
		return nil, err
	}
	entry.lastUsed = time.Now().UTC()
	entry.lifecycle.Touch(entry.lastUsed)
	if err := process.Reset(ctx); err != nil {
		s.discardWorker(entry)
		return nil, fmt.Errorf("reset worker after discovery: %w", err)
	}
	return manifest, nil
}

func (s *SessionService) schedule(
	ctx context.Context,
	sessionID string,
	generation uint64,
	priority scheduler.Priority,
	operation func(context.Context) (any, error),
) (any, error) {
	if s.scheduler == nil {
		return operation(ctx)
	}
	if generation == 0 {
		generation = 1
	}
	var value any
	future := s.scheduler.Submit(ctx, scheduler.Request{
		Session: sessionID, Generation: generation, Priority: priority, Isolation: scheduler.Reuse,
		Run: func(runCtx context.Context, _ scheduler.Lease) error {
			var err error
			value, err = operation(runCtx)
			return err
		},
	})
	result, err := future.Wait(ctx)
	if err != nil {
		return nil, err
	}
	return value, result.Err
}

func (s *SessionService) run(ctx context.Context, request RunRequest, emit daemon.EmitFunc) (any, error) {
	entry := s.get(request.SessionID, request.Project, request.Godot)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	latest, err := s.reconcileSource(ctx, entry, sourcewatch.Filesystem)
	if err != nil {
		return nil, fmt.Errorf("reconcile source at run start: %w", err)
	}
	if latest.Update.Generation > 0 {
		request.Plan.Generation = latest.Update.Generation
	}
	process, err := s.ensureWorker(ctx, entry, request.Plan.Generation)
	if err != nil {
		return nil, err
	}
	sink := executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventRunFinished {
			if _, reconcileErr := s.reconcileSource(ctx, entry, sourcewatch.Filesystem); reconcileErr != nil {
				return fmt.Errorf("reconcile source after run: %w", reconcileErr)
			}
		}
		if entry.source != nil {
			stamp := entry.source.Stamp(request.Plan.Generation)
			event.Stale = stamp.Stale
			if stamp.Stale {
				if event.Metadata == nil {
					event.Metadata = make(map[string]any)
				}
				event.Metadata["requested_generation"] = stamp.Requested
				event.Metadata["current_generation"] = stamp.Current
			}
		}
		return emit(event)
	})
	if err := process.Run(ctx, request.Plan, sink); err != nil {
		s.discardWorker(entry)
		return nil, err
	}
	entry.runs++
	entry.lastUsed = time.Now().UTC()
	entry.lifecycle.Touch(entry.lastUsed)
	if err := process.Reset(ctx); err != nil {
		s.discardWorker(entry)
		return nil, fmt.Errorf("reset worker after run: %w", err)
	}
	pid := process.PID()
	reused := entry.runs > 1
	if request.WorkerMaxRuns > 0 && entry.runs >= uint64(request.WorkerMaxRuns) {
		s.discardWorker(entry)
	}
	return map[string]any{"worker_pid": pid, "reused": reused}, nil
}

func (s *SessionService) warm(ctx context.Context, request WarmRequest) (any, error) {
	entry := s.get(request.SessionID, request.Project, request.Godot)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	latest, err := s.reconcileSource(ctx, entry, sourcewatch.Filesystem)
	if err != nil {
		return nil, fmt.Errorf("reconcile source before worker warmup: %w", err)
	}
	if latest.Update.Generation > 0 {
		request.Generation = latest.Update.Generation
	}
	process, err := s.ensureWorker(ctx, entry, request.Generation)
	if err != nil {
		return nil, err
	}
	if err := process.Ping(ctx); err != nil {
		s.discardWorker(entry)
		return nil, err
	}
	entry.lastUsed = time.Now().UTC()
	entry.lifecycle.Touch(entry.lastUsed)
	return map[string]any{"worker_pid": process.PID(), "generation": process.Generation()}, nil
}

func (s *SessionService) get(id, project, godot string) *session {
	key := id
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]*session)
	}
	if existing := s.sessions[key]; existing != nil {
		return existing
	}
	now := time.Now().UTC()
	created := &session{
		id: id, project: project, godot: godot, lastUsed: now,
		lifecycle: projectidentity.Lifecycle{ProjectRoot: project, LastUsed: now},
	}
	if s.sourceFactory != nil {
		created.source = s.sourceFactory(project)
	}
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
	if entry.worker != nil && entry.worker.Generation() < generation && s.canReload(entry, entry.worker.Generation()) {
		if err := entry.worker.Reload(ctx, generation); err == nil {
			return entry.worker, nil
		}
	}
	hasSlot := entry.worker != nil
	if hasSlot {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = entry.worker.Shutdown(shutdownCtx)
		cancel()
		entry.worker = nil
	}
	if !hasSlot {
		if err := s.reserveWorkerSlot(ctx, entry); err != nil {
			return nil, err
		}
	}
	factory := s.factory
	if factory == nil {
		factory = func(ctx context.Context, options worker.ProcessOptions) (managedWorker, error) {
			return worker.Start(ctx, options)
		}
	}
	process, err := factory(ctx, worker.ProcessOptions{
		Godot: entry.godot, Project: entry.project,
		SessionID: entry.id, Generation: generation,
	})
	if err != nil {
		s.releaseWorkerSlot()
		return nil, err
	}
	entry.worker = process
	entry.lastUsed = time.Now().UTC()
	entry.lifecycle.Touch(entry.lastUsed)
	return process, nil
}

func (s *SessionService) reconcileSource(
	ctx context.Context,
	entry *session,
	trigger sourcewatch.Trigger,
) (sourcewatch.Result, error) {
	if entry.source == nil {
		return sourcewatch.Result{}, nil
	}
	result, err := entry.source.Reconcile(ctx, trigger)
	if err != nil {
		return sourcewatch.Result{}, err
	}
	if len(result.Update.Changes) == 0 {
		return result, nil
	}
	entry.generationMu.Lock()
	if result.Update.Structural {
		entry.lastStructural = result.Update.Generation
	}
	entry.generationMu.Unlock()
	if s.scheduler != nil {
		s.scheduler.AdvanceGeneration(entry.id, result.Update.Generation)
	}
	return result, nil
}

func (s *SessionService) canReload(entry *session, workerGeneration uint64) bool {
	entry.generationMu.Lock()
	defer entry.generationMu.Unlock()
	return entry.lastStructural <= workerGeneration
}

func (s *SessionService) reserveWorkerSlot(ctx context.Context, current *session) error {
	for {
		s.mu.Lock()
		if s.maxWorkers <= 0 {
			s.maxWorkers = 4
		}
		if s.workerFreed == nil {
			s.workerFreed = make(chan struct{}, 1)
		}
		if s.liveWorkers < s.maxWorkers {
			s.liveWorkers++
			s.mu.Unlock()
			return nil
		}
		candidates := make([]*session, 0, len(s.sessions))
		for _, candidate := range s.sessions {
			if candidate != current {
				candidates = append(candidates, candidate)
			}
		}
		notification := s.workerFreed
		s.mu.Unlock()
		var oldest *session
		for _, candidate := range candidates {
			if !candidate.mu.TryLock() {
				continue
			}
			if candidate.worker == nil {
				candidate.mu.Unlock()
				continue
			}
			if oldest == nil || candidate.lastUsed.Before(oldest.lastUsed) {
				if oldest != nil {
					oldest.mu.Unlock()
				}
				oldest = candidate
			} else {
				candidate.mu.Unlock()
			}
		}
		if oldest != nil {
			s.discardWorker(oldest)
			oldest.mu.Unlock()
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-notification:
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *SessionService) discardWorker(entry *session) {
	if entry.worker == nil {
		return
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = entry.worker.Shutdown(shutdownCtx)
	cancel()
	entry.worker = nil
	s.releaseWorkerSlot()
}

func (s *SessionService) releaseWorkerSlot() {
	s.mu.Lock()
	if s.liveWorkers > 0 {
		s.liveWorkers--
	}
	if s.workerFreed == nil {
		s.workerFreed = make(chan struct{}, 1)
	}
	notification := s.workerFreed
	s.mu.Unlock()
	select {
	case notification <- struct{}{}:
	default:
	}
}

// WorkerCount returns the current global process count for diagnostics and tests.
func (s *SessionService) WorkerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.liveWorkers
}

// DaemonStatus reports global scheduling and process bounds.
func (s *SessionService) DaemonStatus() any {
	status := map[string]any{
		"workers":     s.WorkerCount(),
		"sessions":    len(s.List()),
		"max_workers": s.maxWorkers,
	}
	if s.scheduler != nil {
		status["scheduler"] = s.scheduler.Stats()
	}
	return status
}

// Reconcile removes idle or disappeared project sessions without touching other worktrees.
func (s *SessionService) Reconcile(
	now time.Time,
	missingGrace time.Duration,
	idleTTL time.Duration,
) (map[string]projectidentity.RemovalReason, error) {
	s.mu.Lock()
	entries := make(map[string]*session, len(s.sessions))
	for key, entry := range s.sessions {
		entries[key] = entry
	}
	s.mu.Unlock()
	removed := make(map[string]projectidentity.RemovalReason)
	for key, entry := range entries {
		if !entry.mu.TryLock() {
			continue
		}
		reason, err := entry.lifecycle.Reconcile(now, missingGrace, idleTTL)
		if err != nil {
			entry.mu.Unlock()
			return removed, err
		}
		if reason == projectidentity.KeepSession {
			entry.mu.Unlock()
			continue
		}
		if s.scheduler != nil {
			s.scheduler.AdvanceGeneration(entry.id, ^uint64(0))
		}
		s.discardWorker(entry)
		entry.mu.Unlock()
		s.mu.Lock()
		if s.sessions[key] == entry {
			delete(s.sessions, key)
			removed[entry.id] = reason
		}
		s.mu.Unlock()
	}
	return removed, nil
}

// ReconcileSources performs an authoritative scan for every live session.
// It is safe to call periodically because each project reconciler serializes
// its own scans and only content changes advance a generation.
func (s *SessionService) ReconcileSources(
	ctx context.Context,
) (map[string]sourcewatch.Result, map[string]error) {
	s.mu.Lock()
	entries := make([]*session, 0, len(s.sessions))
	for _, entry := range s.sessions {
		entries = append(entries, entry)
	}
	s.mu.Unlock()
	updates := make(map[string]sourcewatch.Result)
	failures := make(map[string]error)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			failures[entry.id] = err
			break
		}
		result, err := s.reconcileSource(ctx, entry, sourcewatch.Periodic)
		if err != nil {
			failures[entry.id] = err
			continue
		}
		if len(result.Update.Changes) > 0 {
			updates[entry.id] = result
		}
	}
	return updates, failures
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
				ID: entry.id, Project: entry.project, Godot: entry.godot, Generation: entry.worker.Generation(),
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
	var joined error
	if s.scheduler != nil {
		joined = errors.Join(joined, s.scheduler.Shutdown(ctx))
	}
	s.mu.Lock()
	entries := make([]*session, 0, len(s.sessions))
	for _, entry := range s.sessions {
		entries = append(entries, entry)
	}
	s.mu.Unlock()
	for _, entry := range entries {
		entry.mu.Lock()
		if entry.worker != nil {
			joined = errors.Join(joined, entry.worker.Shutdown(ctx))
			entry.worker = nil
			s.releaseWorkerSlot()
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
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fmt.Errorf("decode daemon request: %w", err)
	}
	return nil
}
