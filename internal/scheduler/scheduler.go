// Package scheduler provides bounded, fair scheduling across project sessions.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Stable scheduler errors.
var (
	ErrClosed             = errors.New("scheduler is closed")
	ErrObsoleteGeneration = errors.New("work belongs to an obsolete generation")
)

// Priority separates latency-sensitive work from background maintenance.
type Priority uint8

const (
	// Background is used for warming and watch reconciliation.
	Background Priority = iota
	// Interactive is used for explicit client runs.
	Interactive
)

// Isolation controls whether another lease may run beside this one.
type Isolation uint8

const (
	// Reuse uses an ordinary scheduler slot.
	Reuse Isolation = iota
	// Recycle uses an ordinary slot and replaces the worker after the run.
	Recycle
	// Fresh uses an ordinary slot but tells the caller to create a fresh worker.
	Fresh
	// Exclusive reserves the whole scheduler until this work completes.
	Exclusive
)

// ParseIsolation parses the stable runner-facing isolation names. An empty
// value selects the default bounded-reuse policy.
func ParseIsolation(value string) (Isolation, error) {
	switch value {
	case "", "reuse":
		return Reuse, nil
	case "recycle":
		return Recycle, nil
	case "fresh":
		return Fresh, nil
	case "exclusive":
		return Exclusive, nil
	default:
		return Reuse, fmt.Errorf(
			"isolation %q must be reuse, recycle, fresh, or exclusive",
			value,
		)
	}
}

// String returns the stable runner-facing isolation name.
func (i Isolation) String() string {
	switch i {
	case Reuse:
		return "reuse"
	case Recycle:
		return "recycle"
	case Fresh:
		return "fresh"
	case Exclusive:
		return "exclusive"
	default:
		return fmt.Sprintf("isolation(%d)", i)
	}
}

// Options configures one user-level global scheduler.
type Options struct {
	Capacity         int
	BackgroundLimit  int
	InteractiveBurst int
}

// Request is one indivisible file-level scheduling unit.
type Request struct {
	Session    string
	Generation uint64
	Priority   Priority
	Isolation  Isolation
	Run        func(context.Context, Lease) error
}

// Lease identifies one unique global worker slot for the duration of Run.
type Lease struct {
	Slot       int
	Session    string
	Generation uint64
	Isolation  Isolation
}

// Result is delivered exactly once for every accepted request.
type Result struct {
	Lease Lease
	Err   error
}

// Future is a single-result scheduling handle.
type Future struct {
	done <-chan Result
}

// Done receives one result and is then closed.
func (f Future) Done() <-chan Result { return f.done }

// Wait waits for completion or for the waiting context. It does not cancel the
// submitted request; submit it with a cancelable context to do that.
func (f Future) Wait(ctx context.Context) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case result, ok := <-f.done:
		if !ok {
			return Result{}, ErrClosed
		}
		return result, nil
	}
}

// Stats is a point-in-time scheduler snapshot.
type Stats struct {
	Capacity         int
	Active           int
	PeakActive       int
	BackgroundActive int
	Queued           int
}

type job struct {
	id         uint64
	ctx        context.Context
	request    Request
	result     chan Result
	cancelStop func() bool
}

type sessionQueue struct {
	interactive []*job
	background  []*job
}

// Scheduler owns a single global capacity budget shared by all sessions.
type Scheduler struct {
	mu   sync.Mutex
	cond *sync.Cond

	capacity         int
	backgroundLimit  int
	interactiveBurst int

	sessions    map[string]*sessionQueue
	order       []string
	cursor      int
	lastSession string
	minimumGen  map[string]uint64
	slots       []bool

	active            int
	peakActive        int
	backgroundActive  int
	interactiveStreak int
	exclusiveActive   bool
	nextID            uint64
	closed            bool
	wait              sync.WaitGroup
}

// New creates and starts a scheduler.
func New(options Options) (*Scheduler, error) {
	if options.Capacity < 1 {
		return nil, errors.New("scheduler capacity must be positive")
	}
	if options.BackgroundLimit == 0 {
		options.BackgroundLimit = 1
	}
	if options.BackgroundLimit < 1 || options.BackgroundLimit > options.Capacity {
		return nil, errors.New("background limit must be between one and capacity")
	}
	if options.InteractiveBurst == 0 {
		options.InteractiveBurst = 3
	}
	if options.InteractiveBurst < 1 {
		return nil, errors.New("interactive burst must be positive")
	}
	scheduler := &Scheduler{
		capacity: options.Capacity, backgroundLimit: options.BackgroundLimit,
		interactiveBurst: options.InteractiveBurst,
		sessions:         make(map[string]*sessionQueue),
		minimumGen:       make(map[string]uint64), slots: make([]bool, options.Capacity),
	}
	scheduler.cond = sync.NewCond(&scheduler.mu)
	scheduler.wait.Add(1)
	go scheduler.dispatch()
	return scheduler, nil
}

// Submit queues work and returns immediately. A rejected request still gets a
// completed Future so callers use one result path.
func (s *Scheduler) Submit(ctx context.Context, request Request) Future {
	result := make(chan Result, 1)
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateRequest(request); err != nil {
		complete(result, Result{Lease: Lease{Slot: -1}, Err: err})
		return Future{done: result}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		complete(result, Result{Lease: Lease{Slot: -1}, Err: ErrClosed})
		return Future{done: result}
	}
	if request.Generation < s.minimumGen[request.Session] {
		s.mu.Unlock()
		complete(result, Result{Lease: Lease{Slot: -1}, Err: ErrObsoleteGeneration})
		return Future{done: result}
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		complete(result, Result{Lease: Lease{Slot: -1}, Err: err})
		return Future{done: result}
	}
	s.nextID++
	queued := &job{id: s.nextID, ctx: ctx, request: request, result: result}
	queue := s.sessions[request.Session]
	if queue == nil {
		queue = &sessionQueue{}
		s.sessions[request.Session] = queue
		s.order = append(s.order, request.Session)
		s.rebaseCursorLocked()
	}
	if request.Priority == Interactive {
		queue.interactive = append(queue.interactive, queued)
	} else {
		queue.background = append(queue.background, queued)
	}
	queued.cancelStop = context.AfterFunc(ctx, func() { s.cancel(queued.id, ctx.Err()) })
	s.cond.Broadcast()
	s.mu.Unlock()
	return Future{done: result}
}

// AdvanceGeneration rejects queued and future work older than generation.
// Active work is allowed to finish so its caller can label the result stale.
func (s *Scheduler) AdvanceGeneration(session string, generation uint64) {
	if session == "" || generation == 0 {
		return
	}
	var canceled []*job
	s.mu.Lock()
	if generation > s.minimumGen[session] {
		s.minimumGen[session] = generation
	}
	if queue := s.sessions[session]; queue != nil {
		queue.interactive, canceled = removeObsolete(queue.interactive, generation, canceled)
		queue.background, canceled = removeObsolete(queue.background, generation, canceled)
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	for _, queued := range canceled {
		if queued.cancelStop != nil {
			queued.cancelStop()
		}
		complete(queued.result, Result{Lease: Lease{Slot: -1}, Err: ErrObsoleteGeneration})
	}
}

// Retire permanently rejects queued and future work for session and forgets
// its queue. Active work is allowed to finish. Callers must use a fresh session
// key for any later incarnation of the same project session: a retired key
// never accepts work again.
func (s *Scheduler) Retire(session string) {
	if session == "" {
		return
	}
	s.AdvanceGeneration(session, ^uint64(0))
	s.mu.Lock()
	defer s.mu.Unlock()
	queue := s.sessions[session]
	if queue == nil || len(queue.interactive) != 0 || len(queue.background) != 0 {
		return
	}
	delete(s.sessions, session)
	for index, candidate := range s.order {
		if candidate == session {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	if s.lastSession == session {
		s.lastSession = ""
	}
	if len(s.order) == 0 {
		s.cursor = 0
	} else {
		s.cursor %= len(s.order)
	}
	s.rebaseCursorLocked()
}

// Stats returns resource use and queue depth.
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	queued := 0
	for _, queue := range s.sessions {
		queued += len(queue.interactive) + len(queue.background)
	}
	return Stats{
		Capacity: s.capacity, Active: s.active, PeakActive: s.peakActive,
		BackgroundActive: s.backgroundActive, Queued: queued,
	}
}

// Shutdown refuses new work, cancels queued work, and waits for active work.
func (s *Scheduler) Shutdown(ctx context.Context) error {
	var canceled []*job
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		for _, queue := range s.sessions {
			canceled = append(canceled, queue.interactive...)
			canceled = append(canceled, queue.background...)
			queue.interactive = nil
			queue.background = nil
		}
		s.cond.Broadcast()
	}
	s.mu.Unlock()
	for _, queued := range canceled {
		if queued.cancelStop != nil {
			queued.cancelStop()
		}
		complete(queued.result, Result{Lease: Lease{Slot: -1}, Err: ErrClosed})
	}
	done := make(chan struct{})
	go func() {
		s.wait.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func validateRequest(request Request) error {
	if request.Session == "" {
		return errors.New("scheduler request needs a session")
	}
	if request.Generation == 0 {
		return errors.New("scheduler request generation must be positive")
	}
	if request.Priority != Interactive && request.Priority != Background {
		return fmt.Errorf("unknown scheduler priority %d", request.Priority)
	}
	if request.Isolation > Exclusive {
		return fmt.Errorf("unknown scheduler isolation %d", request.Isolation)
	}
	if request.Run == nil {
		return errors.New("scheduler request needs work")
	}
	return nil
}

func (s *Scheduler) dispatch() {
	defer s.wait.Done()
	for {
		s.mu.Lock()
		for {
			if s.closed && s.active == 0 {
				s.mu.Unlock()
				return
			}
			queued := s.pickLocked()
			if queued == nil {
				s.cond.Wait()
				continue
			}
			if queued.request.Isolation == Exclusive && s.active != 0 {
				s.cond.Wait()
				continue
			}
			if s.exclusiveActive || s.active >= s.capacity {
				s.cond.Wait()
				continue
			}
			s.advanceLocked(queued.request.Session)
			s.removeLocked(queued)
			if queued.cancelStop != nil {
				queued.cancelStop()
			}
			slot := s.allocateSlotLocked()
			s.active++
			if s.active > s.peakActive {
				s.peakActive = s.active
			}
			if queued.request.Priority == Background {
				s.backgroundActive++
				s.interactiveStreak = 0
			} else {
				s.interactiveStreak++
			}
			if queued.request.Isolation == Exclusive {
				s.exclusiveActive = true
			}
			lease := Lease{
				Slot: slot, Session: queued.request.Session,
				Generation: queued.request.Generation, Isolation: queued.request.Isolation,
			}
			s.wait.Add(1)
			s.mu.Unlock()
			go s.run(queued, lease)
			break
		}
	}
}

func (s *Scheduler) run(queued *job, lease Lease) {
	defer s.wait.Done()
	err := queued.request.Run(queued.ctx, lease)
	s.mu.Lock()
	s.active--
	s.slots[lease.Slot] = false
	if queued.request.Priority == Background {
		s.backgroundActive--
	}
	if queued.request.Isolation == Exclusive {
		s.exclusiveActive = false
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	complete(queued.result, Result{Lease: lease, Err: err})
}

func (s *Scheduler) pickLocked() *job {
	if s.closed || s.exclusiveActive || s.active >= s.capacity {
		return nil
	}
	interactive := s.firstLocked(Interactive)
	background := s.firstLocked(Background)
	if interactive != nil && (background == nil || s.interactiveStreak < s.interactiveBurst) {
		return interactive
	}
	if background != nil {
		return background
	}
	if interactive != nil {
		return interactive
	}
	return nil
}

func (s *Scheduler) advanceLocked(session string) {
	for index, candidate := range s.order {
		if candidate == session {
			s.cursor = (index + 1) % len(s.order)
			s.lastSession = session
			return
		}
	}
}

func (s *Scheduler) firstLocked(priority Priority) *job {
	if priority == Background && s.backgroundActive >= s.backgroundLimit {
		return nil
	}
	count := len(s.order)
	for offset := 0; offset < count; offset++ {
		index := (s.cursor + offset) % count
		session := s.order[index]
		queue := s.sessions[session]
		var jobs []*job
		if priority == Interactive {
			jobs = queue.interactive
		} else {
			jobs = queue.background
		}
		if len(jobs) == 0 {
			continue
		}
		return jobs[0]
	}
	return nil
}

func (s *Scheduler) rebaseCursorLocked() {
	if s.lastSession == "" {
		return
	}
	for index, session := range s.order {
		if session == s.lastSession {
			s.cursor = (index + 1) % len(s.order)
			return
		}
	}
}

func (s *Scheduler) removeLocked(target *job) {
	queue := s.sessions[target.request.Session]
	if target.request.Priority == Interactive {
		queue.interactive = removeID(queue.interactive, target.id)
	} else {
		queue.background = removeID(queue.background, target.id)
	}
}

func (s *Scheduler) allocateSlotLocked() int {
	for slot, used := range s.slots {
		if !used {
			s.slots[slot] = true
			return slot
		}
	}
	panic("scheduler capacity invariant violated")
}

func (s *Scheduler) cancel(id uint64, err error) {
	if err == nil {
		err = context.Canceled
	}
	s.mu.Lock()
	var found *job
	for _, queue := range s.sessions {
		for _, jobs := range [][]*job{queue.interactive, queue.background} {
			for _, queued := range jobs {
				if queued.id == id {
					found = queued
					break
				}
			}
			if found != nil {
				break
			}
		}
		if found != nil {
			break
		}
	}
	if found != nil {
		s.removeLocked(found)
		s.cond.Broadcast()
	}
	s.mu.Unlock()
	if found != nil {
		complete(found.result, Result{Lease: Lease{Slot: -1}, Err: err})
	}
}

func removeID(jobs []*job, id uint64) []*job {
	for index, queued := range jobs {
		if queued.id == id {
			return append(jobs[:index], jobs[index+1:]...)
		}
	}
	return jobs
}

func removeObsolete(jobs []*job, generation uint64, removed []*job) ([]*job, []*job) {
	kept := jobs[:0]
	for _, queued := range jobs {
		if queued.request.Generation < generation {
			removed = append(removed, queued)
		} else {
			kept = append(kept, queued)
		}
	}
	return kept, removed
}

func complete(channel chan Result, result Result) {
	channel <- result
	close(channel)
}
