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

// DiscoverResponse separates engine discovery from worker startup and the
// optional clean-checkout import/bootstrap phase.
type DiscoverResponse struct {
	Manifest  model.DiscoveryManifest `json:"manifest"`
	TimingsNS map[string]int64        `json:"timings_ns,omitempty"`
}

// GenerationRequest reconciles and returns one project's current source generation
// without starting or pinging a worker.
type GenerationRequest struct {
	SessionID    string `json:"session_id"`
	Project      string `json:"project"`
	Godot        string `json:"godot"`
	WorkerIdleNS int64  `json:"worker_idle_timeout_ns,omitempty"`
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

type startupTimingWorker interface {
	TakeStartupTimings() map[string]int64
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
	log              func(map[string]any)
}

// ServiceOptions controls user-scoped process resource limits.
type ServiceOptions struct {
	MaxWorkers     int
	MaxWorkerAge   time.Duration
	MemoryBudgetMB int
	Log            func(map[string]any)
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
	result.log = options.Log
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
		submitted := time.Now()
		if len(value.Plan.Files) > 1 {
			return s.runFiles(ctx, entry, value, isolation, emit, submitted)
		}
		result, err := s.reconcileSource(ctx, entry, sourcewatch.Initial)
		if err != nil {
			return nil, fmt.Errorf("reconcile source before run: %w", err)
		}
		if err := requireCurrentGeneration(value.Plan.Generation, result.Update.Generation); err != nil {
			return nil, err
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
	case "session.generation":
		var value GenerationRequest
		if err := decodeStrict(request.Payload, &value); err != nil {
			return nil, err
		}
		if value.SessionID == "" || value.Project == "" || value.Godot == "" {
			return nil, errors.New("generation needs session_id, project, and godot")
		}
		entry, err := s.get(value.SessionID, value.Project, value.Godot, value.WorkerIdleNS)
		if err != nil {
			return nil, err
		}
		result, err := s.reconcileSource(ctx, entry, sourcewatch.Initial)
		if err != nil {
			return nil, fmt.Errorf("reconcile source for generation: %w", err)
		}
		generation := result.Update.Generation
		if generation == 0 && entry.source != nil {
			generation = entry.source.Generation()
		}
		if generation == 0 {
			generation = 1
		}
		entry.mu.Lock()
		entry.lastUsed = time.Now().UTC()
		entry.lifecycle.Touch(entry.lastUsed)
		entry.mu.Unlock()
		return map[string]any{"generation": generation}, nil
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
	defer entry.mu.Unlock()
	latest, err := s.reconcileSource(ctx, entry, sourcewatch.Initial)
	if err != nil {
		return nil, fmt.Errorf("reconcile source before multi-file run: %w", err)
	}
	if err := requireCurrentGeneration(request.Plan.Generation, latest.Update.Generation); err != nil {
		return nil, err
	}
	if isolation == scheduler.Fresh ||
		(request.WorkerMaxRuns > 0 && entry.workerRuns >= uint64(request.WorkerMaxRuns)) {
		s.discardWorker(entry)
	}
	previousPrimary := entry.worker
	workerLimit := s.maxWorkers
	if workerLimit <= 0 {
		workerLimit = 4
	}
	jobs := request.Jobs
	if jobs <= 0 || jobs > workerLimit {
		jobs = workerLimit
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
	var primary managedWorker
	aggregateTimings := make(map[string]int64)
	primarySubmitted := time.Now()
	_, err = s.schedule(
		ctx, request.SessionID, request.Plan.Generation,
		scheduler.Interactive, isolation,
		func(setupCtx context.Context) (any, error) {
			aggregateTimings["queue_wait"] = time.Since(primarySubmitted).Nanoseconds()
			workerStarted := time.Now()
			var setupErr error
			primary, setupErr = s.ensureWorker(setupCtx, entry, request.Plan.Generation)
			aggregateTimings["worker_acquisition_startup"] = time.Since(workerStarted).Nanoseconds()
			return nil, setupErr
		},
	)
	if err != nil {
		_ = safeEmit(model.Event{
			ProtocolVersion: model.ProtocolVersion, Type: model.EventRunFinished,
			RunID: request.Plan.RunID, Status: "error", Generation: request.Plan.Generation,
		})
		return nil, err
	}
	primaryReused := previousPrimary != nil && primary == previousPrimary
	lanes := make([]*fileWorkerLane, jobs)
	lanes[0] = &fileWorkerLane{
		process: primary, primary: true,
		workerRuns: entry.workerRuns, workerBytes: entry.workerBytes,
	}
	for index := 1; index < jobs; index++ {
		lanes[index] = &fileWorkerLane{}
	}
	defer func() {
		for _, lane := range lanes[1:] {
			s.retireFileWorker(entry, lane)
		}
	}()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type fileResult struct {
		index   int
		pid     int
		timings map[string]int64
		err     error
	}
	results := make(chan fileResult, len(request.Plan.Files))
	available := make(chan *fileWorkerLane, len(lanes))
	for _, lane := range lanes {
		available <- lane
	}
	for index, file := range request.Plan.Files {
		index, file := index, file
		go func() {
			var lane *fileWorkerLane
			select {
			case lane = <-available:
			case <-runCtx.Done():
				results <- fileResult{index: index, err: runCtx.Err()}
				return
			}
			fileRequest := request
			fileRequest.Plan.Files = []model.FilePlan{file}
			fileSubmitted := time.Now()
			value, err := s.schedule(
				runCtx, request.SessionID, request.Plan.Generation,
				scheduler.Interactive, isolation,
				func(fileCtx context.Context) (any, error) {
					return s.runFileOnWorker(
						fileCtx, entry, lane, fileRequest, safeEmit, time.Since(fileSubmitted),
					)
				},
			)
			result := fileResult{index: index, err: err}
			if values, ok := value.(map[string]any); ok {
				result.pid, _ = values["worker_pid"].(int)
				result.timings, _ = values["timings_ns"].(map[string]int64)
			}
			if err != nil {
				cancel()
			} else {
				available <- lane
			}
			results <- result
		}()
	}
	pids := make([]int, len(request.Plan.Files))
	var joined error
	succeeded := uint64(0)
	for range request.Plan.Files {
		result := <-results
		pids[result.index] = result.pid
		joined = errors.Join(joined, result.err)
		mergeMaximumTimings(aggregateTimings, result.timings)
		if result.err == nil {
			succeeded++
		}
	}
	entry.runs += succeeded
	entry.lastUsed = time.Now().UTC()
	entry.lifecycle.Touch(entry.lastUsed)
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
	eventTimings := make(map[string]int64, len(aggregateTimings)+1)
	mergeMaximumTimings(eventTimings, aggregateTimings)
	eventTimings["daemon_total"] = time.Since(submitted).Nanoseconds()
	metadata := map[string]any{
		"requested_generation": request.Plan.Generation,
		"current_generation":   currentGeneration,
		"files":                len(request.Plan.Files),
		"jobs":                 jobs,
		"timings_ns":           eventTimings,
	}
	if emitErr := safeEmit(model.Event{
		ProtocolVersion: model.ProtocolVersion, Type: model.EventRunFinished,
		RunID: request.Plan.RunID, Status: status, Generation: request.Plan.Generation,
		Stale: stale, Metadata: metadata,
	}); emitErr != nil {
		joined = errors.Join(joined, emitErr)
	}
	if joined != nil || isolation == scheduler.Recycle || isolation == scheduler.Fresh {
		s.discardWorker(entry)
	}
	if joined == nil && request.WarmWorkers > 0 {
		s.warmReplacement(entry, request.Plan.Generation)
	}
	aggregateTimings["daemon_total"] = time.Since(submitted).Nanoseconds()
	return map[string]any{
		"worker_pids": pids,
		"reused":      primaryReused,
		"timings_ns":  aggregateTimings,
	}, joined
}

type fileWorkerLane struct {
	process     managedWorker
	primary     bool
	workerRuns  uint64
	workerBytes uint64
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

func (s *SessionService) runFileOnWorker(
	ctx context.Context,
	entry *session,
	lane *fileWorkerLane,
	request RunRequest,
	emit daemon.EmitFunc,
	queueWait time.Duration,
) (any, error) {
	timings := map[string]int64{"queue_wait": queueWait.Nanoseconds()}
	workerStarted := time.Now()
	process, err := s.ensureFileWorker(ctx, entry, lane, request.Plan.Generation)
	timings["worker_acquisition_startup"] = time.Since(workerStarted).Nanoseconds()
	if err != nil {
		return nil, err
	}
	mergeStartupTimings(timings, process)
	tainted := false
	sink := executor.EventSinkFunc(func(event model.Event) error {
		switch event.Type {
		case model.EventWorkerReady, model.EventRunStarted:
			return nil
		case model.EventRunFinished:
			mergeWorkerTimings(timings, event.Metadata)
			return nil
		case model.EventWorkerTainted:
			tainted = true
			return emit(event)
		default:
			return emit(event)
		}
	})
	executionStarted := time.Now()
	if err := process.Run(ctx, request.Plan, sink); err != nil {
		s.retireFileWorker(entry, lane)
		return nil, err
	}
	timings["worker_execution_serialization"] = time.Since(executionStarted).Nanoseconds()
	resetStarted := time.Now()
	if err := process.Reset(ctx); err != nil {
		s.retireFileWorker(entry, lane)
		return nil, fmt.Errorf("reset worker after file: %w", err)
	}
	timings["worker_reset"] = time.Since(resetStarted).Nanoseconds()
	lane.workerRuns++
	if lane.primary {
		entry.workerRuns++
	}
	memoryExceeded, memoryErr := s.recordFileWorkerMemory(entry, lane)
	if tainted ||
		(request.WorkerMaxRuns > 0 && lane.workerRuns >= uint64(request.WorkerMaxRuns)) ||
		(s.memoryBudgetMB > 0 && (memoryErr != nil || memoryExceeded)) {
		s.retireFileWorker(entry, lane)
	}
	return map[string]any{"worker_pid": process.PID(), "timings_ns": timings}, nil
}

func mergeMaximumTimings(destination, source map[string]int64) {
	for name, duration := range source {
		current, exists := destination[name]
		if !exists || duration > current {
			destination[name] = duration
		}
	}
}

func (s *SessionService) ensureFileWorker(
	ctx context.Context,
	entry *session,
	lane *fileWorkerLane,
	generation uint64,
) (managedWorker, error) {
	if lane.process != nil {
		return lane.process, nil
	}
	if lane.primary {
		process, err := s.ensureWorker(ctx, entry, generation)
		if err == nil {
			lane.process = process
			lane.workerRuns = entry.workerRuns
			lane.workerBytes = entry.workerBytes
		}
		return process, err
	}
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
		SessionID: entry.id, Generation: generation,
	})
	if err != nil {
		s.releaseWorkerSlot()
		return nil, err
	}
	lane.process = process
	lane.workerRuns = 0
	lane.workerBytes = 0
	s.logLifecycle("worker_started", entry, process, "", map[string]any{"pooled": true})
	return process, nil
}

func (s *SessionService) retireFileWorker(entry *session, lane *fileWorkerLane) {
	if lane == nil || lane.process == nil {
		return
	}
	if lane.primary {
		if entry.worker == lane.process {
			s.discardWorker(entry)
		}
	} else {
		s.logLifecycle("worker_discarded", entry, lane.process, "", map[string]any{"pooled": true})
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = lane.process.Shutdown(shutdownCtx)
		cancel()
		s.mu.Lock()
		if s.totalWorkerBytes >= lane.workerBytes {
			s.totalWorkerBytes -= lane.workerBytes
		} else {
			s.totalWorkerBytes = 0
		}
		s.mu.Unlock()
		s.releaseWorkerSlot()
	}
	lane.process = nil
	lane.workerRuns = 0
	lane.workerBytes = 0
}

func (s *SessionService) recordFileWorkerMemory(entry *session, lane *fileWorkerLane) (bool, error) {
	if s.memoryBudgetMB <= 0 || lane.process == nil {
		return false, nil
	}
	if lane.primary {
		exceeded, err := s.recordWorkerMemory(entry)
		lane.workerBytes = entry.workerBytes
		return exceeded, err
	}
	memoryUsage := s.memoryUsage
	if memoryUsage == nil {
		memoryUsage = worker.ResidentMemoryBytes
	}
	resident, err := memoryUsage(lane.process.PID())
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	if s.totalWorkerBytes >= lane.workerBytes {
		s.totalWorkerBytes -= lane.workerBytes
	} else {
		s.totalWorkerBytes = 0
	}
	lane.workerBytes = resident
	s.totalWorkerBytes += resident
	exceeded := s.totalWorkerBytes > uint64(s.memoryBudgetMB)*1024*1024
	s.mu.Unlock()
	return exceeded, nil
}

func (s *SessionService) discover(ctx context.Context, entry *session, request DiscoverRequest) (any, error) {
	started := time.Now()
	timings := make(map[string]int64)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	reconcileStarted := time.Now()
	latest, err := s.reconcileSource(ctx, entry, sourcewatch.Filesystem)
	timings["generation_reconcile"] = time.Since(reconcileStarted).Nanoseconds()
	if err != nil {
		return nil, fmt.Errorf("reconcile source at discovery start: %w", err)
	}
	if err := requireCurrentGeneration(request.Plan.Generation, latest.Update.Generation); err != nil {
		return nil, err
	}
	workerStarted := time.Now()
	process, err := s.ensureWorker(ctx, entry, request.Plan.Generation)
	timings["worker_acquisition_startup"] = time.Since(workerStarted).Nanoseconds()
	if err != nil {
		return nil, err
	}
	mergeStartupTimings(timings, process)
	discoveryStarted := time.Now()
	manifest, err := process.Discover(ctx, request.Plan)
	timings["engine_discovery"] = time.Since(discoveryStarted).Nanoseconds()
	if err != nil {
		s.logLifecycle("discovery_failed", entry, process, request.Plan.RunID, map[string]any{"error": err.Error()})
		s.discardWorker(entry)
		return nil, err
	}
	s.logLifecycle("discovery_finished", entry, process, request.Plan.RunID, nil)
	entry.lastUsed = time.Now().UTC()
	entry.lifecycle.Touch(entry.lastUsed)
	resetStarted := time.Now()
	if err := process.Reset(ctx); err != nil {
		s.discardWorker(entry)
		return nil, fmt.Errorf("reset worker after discovery: %w", err)
	}
	timings["worker_reset"] = time.Since(resetStarted).Nanoseconds()
	memoryExceeded, memoryErr := s.recordWorkerMemory(entry)
	if s.memoryBudgetMB > 0 && (memoryErr != nil || memoryExceeded) {
		s.recycleWorker(entry)
	}
	timings["daemon_discovery_total"] = time.Since(started).Nanoseconds()
	return DiscoverResponse{Manifest: manifest, TimingsNS: timings}, nil
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
	mergeStartupTimings(timings, process)
	tainted := false
	s.logLifecycle("run_started", entry, process, request.Plan.RunID, nil)
	sink := executor.EventSinkFunc(func(event model.Event) error {
		if event.Type == model.EventWorkerTainted {
			tainted = true
		}
		if event.Type == model.EventRunFinished {
			mergeWorkerTimings(timings, event.Metadata)
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
		s.logLifecycle("run_failed", entry, process, request.Plan.RunID, map[string]any{"error": err.Error()})
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
	s.logLifecycle("run_finished", entry, process, request.Plan.RunID, map[string]any{"tainted": tainted})
	return map[string]any{"worker_pid": pid, "reused": reused, "timings_ns": timings}, nil
}

func mergeWorkerTimings(timings map[string]int64, metadata map[string]any) {
	value, ok := metadata["timings_usec"]
	if !ok {
		return
	}
	workerTimings, ok := value.(map[string]any)
	if !ok {
		return
	}
	for _, name := range []string{
		"script_load_dynamic_definition",
		"hooks_cases",
		"result_serialization",
	} {
		raw, exists := workerTimings[name]
		if !exists {
			continue
		}
		var microseconds int64
		valid := true
		switch timing := raw.(type) {
		case float64:
			microseconds = int64(timing)
		case int:
			microseconds = int64(timing)
		case int64:
			microseconds = timing
		case uint64:
			if timing <= uint64(^uint64(0)>>1) {
				microseconds = int64(timing)
			} else {
				valid = false
			}
		default:
			valid = false
		}
		if valid && microseconds >= 0 {
			timings[name] = microseconds * int64(time.Microsecond)
		}
	}
}

func mergeStartupTimings(timings map[string]int64, process managedWorker) {
	provider, ok := process.(startupTimingWorker)
	if !ok {
		return
	}
	for name, duration := range provider.TakeStartupTimings() {
		if duration >= 0 {
			timings[name] = duration
		}
	}
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
		s.logLifecycle("worker_reused", entry, entry.worker, "", nil)
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
	s.logLifecycle("worker_started", entry, process, "", nil)
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
	s.logLifecycle(
		"source_generation_advanced",
		entry,
		nil,
		"",
		map[string]any{"generation": result.Update.Generation, "changes": len(result.Update.Changes)},
	)
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
	s.logLifecycle("worker_discarded", entry, entry.worker, "", nil)
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
	s.logLifecycle("worker_recycled", entry, process, "", nil)
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

func (s *SessionService) logLifecycle(
	event string,
	entry *session,
	process managedWorker,
	runID string,
	fields map[string]any,
) {
	if s.log == nil {
		return
	}
	record := make(map[string]any, len(fields)+5)
	for key, value := range fields {
		record[key] = value
	}
	record["event"] = event
	record["level"] = lifecycleLogLevel(event)
	if entry != nil {
		record["session_id"] = entry.id
	}
	if process != nil {
		record["worker_id"] = fmt.Sprintf("pid:%d", process.PID())
		record["generation"] = process.Generation()
	}
	if runID != "" {
		record["run_id"] = runID
	}
	s.log(record)
}

func lifecycleLogLevel(event string) string {
	if strings.HasSuffix(event, "_failed") || strings.HasSuffix(event, "_error") {
		return "error"
	}
	switch event {
	case "discovery_finished", "worker_reused":
		return "debug"
	default:
		return "info"
	}
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
