// Package service owns daemon-hosted project sessions and persistent workers.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
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
	WorkerIdleNS  int64          `json:"worker_idle_timeout_ns,omitempty"`
	Jobs          int            `json:"jobs,omitempty"`
	WarmWorkers   int            `json:"warm_workers,omitempty"`
	Isolation     string         `json:"isolation,omitempty"`
}

// DiscoverRequest contains the path-specific lease data and path-neutral plan
// used to complete engine-dependent discovery in a persistent worker.
type DiscoverRequest struct {
	SessionID    string         `json:"session_id"`
	Project      string         `json:"project"`
	Godot        string         `json:"godot"`
	Plan         model.TestPlan `json:"plan"`
	WorkerIdleNS int64          `json:"worker_idle_timeout_ns,omitempty"`
}

// WarmRequest prepares one worker for a project generation.
type WarmRequest struct {
	SessionID    string `json:"session_id"`
	Project      string `json:"project"`
	Godot        string `json:"godot"`
	Generation   uint64 `json:"generation"`
	WorkerIdleNS int64  `json:"worker_idle_timeout_ns,omitempty"`
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
	mu          sync.Mutex
	id          string
	project     string
	godot       string
	worker      managedWorker
	workerBorn  time.Time
	workerBytes uint64
	source      *sourcewatch.Reconciler
	lastUsed    time.Time
	runs        uint64
	workerRuns  uint64
	idleTTL     time.Duration
	lifecycle   projectidentity.Lifecycle
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
	mu               sync.Mutex
	sessions         map[string]*session
	factory          workerFactory
	maxWorkers       int
	maxWorkerAge     time.Duration
	memoryBudgetMB   int
	totalWorkerBytes uint64
	memoryUsage      func(int) (uint64, error)
	liveWorkers      int
	workerFreed      chan struct{}
	reapers          sync.WaitGroup
	scheduler        *scheduler.Scheduler
	sourceFactory    func(string) *sourcewatch.Reconciler
}

// ServiceOptions controls user-scoped process resource limits.
type ServiceOptions struct {
	MaxWorkers     int
	MaxWorkerAge   time.Duration
	MemoryBudgetMB int
}

// NewSessionService creates an empty service using real Godot workers.
func NewSessionService() *SessionService {
	return NewSessionServiceWithOptions(ServiceOptions{MaxWorkers: 4, MaxWorkerAge: 30 * time.Minute})
}

// NewSessionServiceWithOptions creates a production service with a global worker bound.
func NewSessionServiceWithOptions(options ServiceOptions) *SessionService {
	if options.MaxWorkers <= 0 {
		options.MaxWorkers = 4
	}
	if options.MaxWorkerAge <= 0 {
		options.MaxWorkerAge = 30 * time.Minute
	}
	result := newSessionServiceWithCapacity(func(ctx context.Context, workerOptions worker.ProcessOptions) (managedWorker, error) {
		return worker.Start(ctx, workerOptions)
	}, options.MaxWorkers)
	result.maxWorkerAge = options.MaxWorkerAge
	result.memoryBudgetMB = options.MemoryBudgetMB
	result.memoryUsage = worker.ResidentMemoryBytes
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
		maxWorkerAge: 30 * time.Minute,
		memoryUsage:  worker.ResidentMemoryBytes,
		workerFreed:  make(chan struct{}, 1), scheduler: globalScheduler,
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
		if err := validatePlan(value.Plan); err != nil {
			return nil, fmt.Errorf("invalid run plan: %w", err)
		}
		isolation, err := scheduler.ParseIsolation(value.Isolation)
		if err != nil {
			return nil, err
		}
		entry, err := s.get(value.SessionID, value.Project, value.Godot, value.WorkerIdleNS)
		if err != nil {
			return nil, err
		}
		result, err := s.reconcileSource(ctx, entry, sourcewatch.Initial)
		if err != nil {
			return nil, fmt.Errorf("reconcile source before run: %w", err)
		}
		if err := requireCurrentGeneration(value.Plan.Generation, result.Update.Generation); err != nil {
			return nil, err
		}
		submitted := time.Now()
		if len(value.Plan.Files) > 1 {
			return s.runFiles(ctx, entry, value, isolation, emit, submitted)
		}
		return s.schedule(ctx, value.SessionID, value.Plan.Generation, scheduler.Interactive, isolation, func(runCtx context.Context) (any, error) {
			return s.run(runCtx, entry, value, isolation, emit, time.Since(submitted))
		})
	case "session.discover":
		var value DiscoverRequest
		if err := decodeStrict(request.Payload, &value); err != nil {
			return nil, err
		}
		if value.SessionID == "" || value.Project == "" || value.Godot == "" || value.Plan.RunID == "" {
			return nil, errors.New("discover needs session_id, project, godot, and plan.run_id")
		}
		if err := validatePlan(value.Plan); err != nil {
			return nil, fmt.Errorf("invalid discovery plan: %w", err)
		}
		entry, err := s.get(value.SessionID, value.Project, value.Godot, value.WorkerIdleNS)
		if err != nil {
			return nil, err
		}
		result, err := s.reconcileSource(ctx, entry, sourcewatch.Initial)
		if err != nil {
			return nil, fmt.Errorf("reconcile source before discovery: %w", err)
		}
		if err := requireCurrentGeneration(value.Plan.Generation, result.Update.Generation); err != nil {
			return nil, err
		}
		return s.schedule(ctx, value.SessionID, value.Plan.Generation, scheduler.Interactive, scheduler.Reuse, func(runCtx context.Context) (any, error) {
			return s.discover(runCtx, entry, value)
		})
	case "session.warm":
		var value WarmRequest
		if err := decodeStrict(request.Payload, &value); err != nil {
			return nil, err
		}
		if value.SessionID == "" || value.Project == "" || value.Godot == "" {
			return nil, errors.New("warm needs session_id, project, and godot")
		}
		entry, err := s.get(value.SessionID, value.Project, value.Godot, value.WorkerIdleNS)
		if err != nil {
			return nil, err
		}
		result, err := s.reconcileSource(ctx, entry, sourcewatch.Initial)
		if err != nil {
			return nil, fmt.Errorf("reconcile source before warm: %w", err)
		}
		if result.Update.Generation > 0 {
			value.Generation = result.Update.Generation
		}
		return s.schedule(ctx, value.SessionID, value.Generation, scheduler.Background, scheduler.Reuse, func(runCtx context.Context) (any, error) {
			return s.warm(runCtx, entry, value)
		})
	case "daemon.sessions":
		return s.List(), nil
	default:
		return nil, fmt.Errorf("unsupported daemon method %q", request.Method)
	}
}

func (s *SessionService) runFiles(
	ctx context.Context,
	entry *session,
	request RunRequest,
	isolation scheduler.Isolation,
	emit daemon.EmitFunc,
	submitted time.Time,
) (any, error) {
	entry.mu.Lock()
	s.discardWorker(entry)
	entry.mu.Unlock()
	jobs := request.Jobs
	if jobs <= 0 || jobs > s.maxWorkers {
		jobs = s.maxWorkers
	}
	jobs = min(jobs, len(request.Plan.Files))
	var emitMu sync.Mutex
	aggregateStatus := "pass"
	statusRank := map[string]int{"pass": 0, "void": 0, "skip": 0, "todo": 0, "canceled": 1, "fail": 2, "error": 3}
	safeEmit := func(event any) error {
		emitMu.Lock()
		defer emitMu.Unlock()
		if modelEvent, ok := event.(model.Event); ok && modelEvent.Type == model.EventCaseFinished &&
			statusRank[modelEvent.Status] > statusRank[aggregateStatus] {
			aggregateStatus = modelEvent.Status
		}
		return emit(event)
	}
	if err := safeEmit(model.Event{
		ProtocolVersion: model.ProtocolVersion, Type: model.EventRunStarted,
		RunID: request.Plan.RunID, Generation: request.Plan.Generation,
	}); err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type fileResult struct {
		index int
		pid   int
		err   error
	}
	results := make(chan fileResult, len(request.Plan.Files))
	limit := make(chan struct{}, jobs)
	for index, file := range request.Plan.Files {
		index, file := index, file
		go func() {
			select {
			case limit <- struct{}{}:
			case <-runCtx.Done():
				results <- fileResult{index: index, err: runCtx.Err()}
				return
			}
			defer func() { <-limit }()
			fileRequest := request
			fileRequest.Plan.Files = []model.FilePlan{file}
			value, err := s.schedule(
				runCtx, request.SessionID, request.Plan.Generation,
				scheduler.Interactive, isolation,
				func(fileCtx context.Context) (any, error) {
					return s.runEphemeralFile(fileCtx, entry, fileRequest, safeEmit)
				},
			)
			result := fileResult{index: index, err: err}
			if values, ok := value.(map[string]any); ok {
				result.pid, _ = values["worker_pid"].(int)
			}
			if err != nil {
				cancel()
			}
			results <- result
		}()
	}
	pids := make([]int, len(request.Plan.Files))
	var joined error
	for range request.Plan.Files {
		result := <-results
		pids[result.index] = result.pid
		joined = errors.Join(joined, result.err)
	}
	latest, reconcileErr := s.reconcileSource(context.WithoutCancel(ctx), entry, sourcewatch.Filesystem)
	joined = errors.Join(joined, reconcileErr)
	currentGeneration := request.Plan.Generation
	if latest.Update.Generation > 0 {
		currentGeneration = latest.Update.Generation
	}
	stale := currentGeneration != request.Plan.Generation
	status := aggregateStatus
	if joined != nil {
		status = "error"
	}
	metadata := map[string]any{
		"requested_generation": request.Plan.Generation,
		"current_generation":   currentGeneration,
		"files":                len(request.Plan.Files),
		"jobs":                 jobs,
	}
	if emitErr := safeEmit(model.Event{
		ProtocolVersion: model.ProtocolVersion, Type: model.EventRunFinished,
		RunID: request.Plan.RunID, Status: status, Generation: request.Plan.Generation,
		Stale: stale, Metadata: metadata,
	}); emitErr != nil {
		joined = errors.Join(joined, emitErr)
	}
	if joined == nil && request.WarmWorkers > 0 {
		s.warmReplacement(entry, request.Plan.Generation)
	}
	return map[string]any{
		"worker_pids": pids,
		"reused":      false,
		"timings_ns":  map[string]int64{"daemon_total": time.Since(submitted).Nanoseconds()},
	}, joined
}

func (s *SessionService) warmReplacement(entry *session, generation uint64) {
	s.reapers.Add(1)
	go func() {
		defer s.reapers.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		entry.mu.Lock()
		defer entry.mu.Unlock()
		process, err := s.ensureWorker(ctx, entry, generation)
		if err == nil {
			_ = process.Ping(ctx)
		}
	}()
}

func (s *SessionService) runEphemeralFile(
	ctx context.Context,
	entry *session,
	request RunRequest,
	emit daemon.EmitFunc,
) (any, error) {
	if err := s.reserveWorkerSlot(ctx, entry); err != nil {
		return nil, err
	}
	factory := s.factory
	if factory == nil {
		factory = func(ctx context.Context, options worker.ProcessOptions) (managedWorker, error) {
			return worker.Start(ctx, options)
		}
	}
	process, err := factory(ctx, worker.ProcessOptions{
		Godot: entry.godot, Project: entry.project,
		SessionID: entry.id, Generation: request.Plan.Generation,
	})
	if err != nil {
		s.releaseWorkerSlot()
		return nil, err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = process.Shutdown(shutdownCtx)
		cancel()
		s.releaseWorkerSlot()
	}()
	sink := executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventWorkerReady, model.EventRunStarted, model.EventRunFinished:
			return nil
		default:
			return emit(event)
		}
	})
	if err := process.Run(ctx, request.Plan, sink); err != nil {
		return nil, err
	}
	entry.mu.Lock()
	entry.runs++
	entry.lastUsed = time.Now().UTC()
	entry.lifecycle.Touch(entry.lastUsed)
	entry.mu.Unlock()
	return map[string]any{"worker_pid": process.PID()}, nil
}

func (s *SessionService) discover(ctx context.Context, entry *session, request DiscoverRequest) (any, error) {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	latest, err := s.reconcileSource(ctx, entry, sourcewatch.Filesystem)
	if err != nil {
		return nil, fmt.Errorf("reconcile source at discovery start: %w", err)
	}
	if err := requireCurrentGeneration(request.Plan.Generation, latest.Update.Generation); err != nil {
		return nil, err
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
	memoryExceeded, memoryErr := s.recordWorkerMemory(entry)
	if s.memoryBudgetMB > 0 && (memoryErr != nil || memoryExceeded) {
		s.recycleWorker(entry)
	}
	return manifest, nil
}

func (s *SessionService) schedule(
	ctx context.Context,
	sessionID string,
	generation uint64,
	priority scheduler.Priority,
	isolation scheduler.Isolation,
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
		Session: sessionID, Generation: generation, Priority: priority, Isolation: isolation,
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

func (s *SessionService) run(
	ctx context.Context,
	entry *session,
	request RunRequest,
	isolation scheduler.Isolation,
	emit daemon.EmitFunc,
	queueWait time.Duration,
) (any, error) {
	started := time.Now()
	timings := map[string]int64{"queue_wait": queueWait.Nanoseconds()}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	reconcileStarted := time.Now()
	latest, err := s.reconcileSource(ctx, entry, sourcewatch.Filesystem)
	timings["generation_reconcile"] = time.Since(reconcileStarted).Nanoseconds()
	if err != nil {
		return nil, fmt.Errorf("reconcile source at run start: %w", err)
	}
	if err := requireCurrentGeneration(request.Plan.Generation, latest.Update.Generation); err != nil {
		return nil, err
	}
	if isolation == scheduler.Fresh {
		s.discardWorker(entry)
	}
	workerStarted := time.Now()
	process, err := s.ensureWorker(ctx, entry, request.Plan.Generation)
	timings["worker_acquisition_startup"] = time.Since(workerStarted).Nanoseconds()
	if err != nil {
		return nil, err
	}
	tainted := false
	sink := executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventWorkerTainted {
			tainted = true
		}
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
	executionStarted := time.Now()
	if err := process.Run(ctx, request.Plan, sink); err != nil {
		s.discardWorker(entry)
		return nil, err
	}
	timings["worker_execution_serialization"] = time.Since(executionStarted).Nanoseconds()
	reused := entry.workerRuns > 0
	entry.runs++
	entry.workerRuns++
	entry.lastUsed = time.Now().UTC()
	entry.lifecycle.Touch(entry.lastUsed)
	resetStarted := time.Now()
	if err := process.Reset(ctx); err != nil {
		s.discardWorker(entry)
		return nil, fmt.Errorf("reset worker after run: %w", err)
	}
	timings["worker_reset"] = time.Since(resetStarted).Nanoseconds()
	timings["daemon_total"] = time.Since(started).Nanoseconds()
	pid := process.PID()
	memoryExceeded, memoryErr := s.recordWorkerMemory(entry)
	if isolation == scheduler.Recycle || isolation == scheduler.Fresh || tainted ||
		(request.WorkerMaxRuns > 0 && entry.workerRuns >= uint64(request.WorkerMaxRuns)) ||
		(s.memoryBudgetMB > 0 && (memoryErr != nil || memoryExceeded)) {
		s.recycleWorker(entry)
	}
	return map[string]any{"worker_pid": pid, "reused": reused, "timings_ns": timings}, nil
}

func (s *SessionService) warm(ctx context.Context, entry *session, request WarmRequest) (any, error) {
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
	pid := process.PID()
	memoryExceeded, memoryErr := s.recordWorkerMemory(entry)
	if s.memoryBudgetMB > 0 && (memoryErr != nil || memoryExceeded) {
		s.recycleWorker(entry)
	}
	return map[string]any{"worker_pid": pid, "generation": process.Generation()}, nil
}

func (s *SessionService) get(id, project, godot string, workerIdleNS int64) (*session, error) {
	key := id
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]*session)
	}
	if existing := s.sessions[key]; existing != nil {
		if existing.project != project || existing.godot != godot {
			return nil, fmt.Errorf(
				"session %q is already bound to project %q and Godot %q",
				id, existing.project, existing.godot,
			)
		}
		if workerIdleNS > 0 && existing.idleTTL != time.Duration(workerIdleNS) {
			return nil, fmt.Errorf(
				"session %q worker idle timeout changed from %s to %s",
				id, existing.idleTTL, time.Duration(workerIdleNS),
			)
		}
		return existing, nil
	}
	idleTTL := 2 * time.Minute
	if workerIdleNS > 0 {
		idleTTL = time.Duration(workerIdleNS)
	}
	now := time.Now().UTC()
	created := &session{
		id: id, project: project, godot: godot, lastUsed: now, idleTTL: idleTTL,
		lifecycle: projectidentity.Lifecycle{ProjectRoot: project, LastUsed: now},
	}
	if s.sourceFactory != nil {
		created.source = s.sourceFactory(project)
	}
	s.sessions[key] = created
	return created, nil
}

func (s *SessionService) ensureWorker(ctx context.Context, entry *session, generation uint64) (managedWorker, error) {
	if generation == 0 {
		generation = 1
	}
	if entry.worker != nil && s.maxWorkerAge > 0 && !entry.workerBorn.IsZero() &&
		time.Since(entry.workerBorn) >= s.maxWorkerAge {
		s.discardWorker(entry)
	}
	if entry.worker != nil && entry.worker.Generation() == generation {
		return entry.worker, nil
	}
	hasSlot := entry.worker != nil
	if hasSlot {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = entry.worker.Shutdown(shutdownCtx)
		cancel()
		entry.worker = nil
		entry.workerBorn = time.Time{}
		entry.workerRuns = 0
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
	entry.workerBorn = time.Now().UTC()
	entry.workerBytes = 0
	entry.workerRuns = 0
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
	if s.scheduler != nil {
		s.scheduler.AdvanceGeneration(entry.id, result.Update.Generation)
	}
	return result, nil
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
	entry.workerBorn = time.Time{}
	s.forgetWorkerMemory(entry)
	entry.workerRuns = 0
	s.releaseWorkerSlot()
}

// recycleWorker detaches a completed worker immediately, then shuts it down
// outside the client critical path. Its global slot remains reserved until the
// exact child has exited, so replacement startup can never exceed maxWorkers.
// The caller must hold entry.mu.
func (s *SessionService) recycleWorker(entry *session) {
	if entry.worker == nil {
		return
	}
	process := entry.worker
	entry.worker = nil
	entry.workerBorn = time.Time{}
	s.forgetWorkerMemory(entry)
	entry.workerRuns = 0
	s.reapers.Add(1)
	go func() {
		defer s.reapers.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = process.Shutdown(shutdownCtx)
		cancel()
		s.releaseWorkerSlot()
	}()
}

func (s *SessionService) recordWorkerMemory(entry *session) (bool, error) {
	if s.memoryBudgetMB <= 0 || entry.worker == nil {
		return false, nil
	}
	memoryUsage := s.memoryUsage
	if memoryUsage == nil {
		memoryUsage = worker.ResidentMemoryBytes
	}
	resident, err := memoryUsage(entry.worker.PID())
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	if s.totalWorkerBytes >= entry.workerBytes {
		s.totalWorkerBytes -= entry.workerBytes
	} else {
		s.totalWorkerBytes = 0
	}
	entry.workerBytes = resident
	s.totalWorkerBytes += resident
	budget := uint64(s.memoryBudgetMB) * 1024 * 1024
	exceeded := s.totalWorkerBytes > budget
	s.mu.Unlock()
	return exceeded, nil
}

func (s *SessionService) forgetWorkerMemory(entry *session) {
	s.mu.Lock()
	if s.totalWorkerBytes >= entry.workerBytes {
		s.totalWorkerBytes -= entry.workerBytes
	} else {
		s.totalWorkerBytes = 0
	}
	entry.workerBytes = 0
	s.mu.Unlock()
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
		"workers":           s.WorkerCount(),
		"sessions":          len(s.List()),
		"max_workers":       s.maxWorkers,
		"worker_max_age_ns": s.maxWorkerAge.Nanoseconds(),
		"memory_budget_mb":  s.memoryBudgetMB,
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
		entryIdleTTL := idleTTL
		if entry.idleTTL > 0 {
			entryIdleTTL = entry.idleTTL
		}
		reason, err := entry.lifecycle.Reconcile(now, missingGrace, entryIdleTTL)
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
	reaped := make(chan struct{})
	go func() {
		s.reapers.Wait()
		close(reaped)
	}()
	select {
	case <-ctx.Done():
		joined = errors.Join(joined, ctx.Err())
	case <-reaped:
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

func validatePlan(planValue model.TestPlan) error {
	if planValue.ProtocolVersion != model.ProtocolVersion {
		return fmt.Errorf(
			"protocol version %d does not match %d",
			planValue.ProtocolVersion,
			model.ProtocolVersion,
		)
	}
	if planValue.Generation == 0 {
		return errors.New("generation must be positive")
	}
	for _, file := range planValue.Files {
		if err := validateProjectPath(file.Path); err != nil {
			return fmt.Errorf("file %q: %w", file.Path, err)
		}
	}
	return nil
}

func validateProjectPath(value string) error {
	if value == "" {
		return errors.New("path must not be empty")
	}
	if strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") {
		return errors.New("path must be normalized and project-relative")
	}
	clean := path.Clean(value)
	if clean != value || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("path must be normalized and remain inside the project")
	}
	return nil
}

func requireCurrentGeneration(requested, current uint64) error {
	if current == 0 || current == requested {
		return nil
	}
	return fmt.Errorf(
		"source generation changed from %d to %d; rediscover and retry",
		requested,
		current,
	)
}
