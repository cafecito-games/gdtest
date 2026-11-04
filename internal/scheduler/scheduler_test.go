package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestGlobalBoundAndUniqueSlots(t *testing.T) {
	t.Parallel()
	scheduler := newTestScheduler(t, Options{Capacity: 2, BackgroundLimit: 1})

	started := make(chan Lease, 5)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	futures := make([]Future, 0, 5)
	for index := range 5 {
		futures = append(futures, scheduler.Submit(context.Background(), Request{
			Session: fmt.Sprintf("session-%d", index), Generation: 1, Priority: Interactive,
			Run: func(_ context.Context, lease Lease) error {
				current := active.Add(1)
				for {
					previous := peak.Load()
					if current <= previous || peak.CompareAndSwap(previous, current) {
						break
					}
				}
				started <- lease
				<-release
				active.Add(-1)
				return nil
			},
		}))
	}
	first := receiveLease(t, started)
	second := receiveLease(t, started)
	if first.Slot == second.Slot {
		t.Fatalf("simultaneous leases shared slot %d", first.Slot)
	}
	select {
	case third := <-started:
		t.Fatalf("capacity exceeded by lease %#v", third)
	case <-time.After(30 * time.Millisecond):
	}
	for range 5 {
		release <- struct{}{}
		// Once one lease exits, another may start and need a release token.
	}
	for _, future := range futures {
		result := waitResult(t, future)
		if result.Err != nil {
			t.Fatalf("scheduled work: %v", result.Err)
		}
	}
	if peak.Load() != 2 || scheduler.Stats().PeakActive != 2 {
		t.Fatalf("peak active = %d, stats = %#v", peak.Load(), scheduler.Stats())
	}
}

func TestManySessionsNeverExceedGlobalBound(t *testing.T) {
	t.Parallel()
	scheduler := newTestScheduler(t, Options{Capacity: 4})

	started := make(chan struct{}, 64)
	release := make(chan struct{})
	var active atomic.Int32
	var peak atomic.Int32
	futures := make([]Future, 0, 64)
	for index := range 64 {
		futures = append(futures, scheduler.Submit(context.Background(), Request{
			Session: fmt.Sprintf("stress-%02d", index), Generation: 1, Priority: Interactive,
			Run: func(context.Context, Lease) error {
				current := active.Add(1)
				for {
					previous := peak.Load()
					if current <= previous || peak.CompareAndSwap(previous, current) {
						break
					}
				}
				started <- struct{}{}
				<-release
				active.Add(-1)
				return nil
			},
		}))
	}
	for range 4 {
		waitSignal(t, started)
	}
	select {
	case <-started:
		t.Fatal("a fifth session started above the global bound")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	for _, future := range futures {
		if result := waitResult(t, future); result.Err != nil {
			t.Fatalf("stress work: %v", result.Err)
		}
	}
	if peak.Load() != 4 || scheduler.Stats().PeakActive != 4 {
		t.Fatalf("peak = %d, stats = %#v", peak.Load(), scheduler.Stats())
	}
}

func TestBackgroundWorkIsCappedWithoutBlockingInteractiveSlots(t *testing.T) {
	t.Parallel()
	scheduler := newTestScheduler(t, Options{Capacity: 4, BackgroundLimit: 2})

	started := make(chan Priority, 8)
	release := make(chan struct{})
	var futures []Future
	for index := range 4 {
		futures = append(futures, scheduler.Submit(context.Background(), Request{
			Session: fmt.Sprintf("background-%d", index), Generation: 1, Priority: Background,
			Run: func(context.Context, Lease) error { started <- Background; <-release; return nil },
		}))
	}
	for range 2 {
		select {
		case priority := <-started:
			if priority != Background {
				t.Fatalf("priority = %d", priority)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("background work did not start")
		}
	}
	select {
	case <-started:
		t.Fatal("background limit exceeded")
	case <-time.After(30 * time.Millisecond):
	}
	for index := range 2 {
		futures = append(futures, scheduler.Submit(context.Background(), Request{
			Session: fmt.Sprintf("interactive-fill-%d", index), Generation: 1, Priority: Interactive,
			Run: func(context.Context, Lease) error { started <- Interactive; <-release; return nil },
		}))
	}
	for range 2 {
		select {
		case priority := <-started:
			if priority != Interactive {
				t.Fatalf("priority = %d, want interactive", priority)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("interactive work did not fill spare slot")
		}
	}
	if stats := scheduler.Stats(); stats.Active != 4 || stats.BackgroundActive != 2 {
		t.Fatalf("stats = %#v", stats)
	}
	close(release)
	for _, future := range futures {
		if result := waitResult(t, future); result.Err != nil {
			t.Fatalf("scheduled work: %v", result.Err)
		}
	}
}

func TestRoundRobinFairnessAcrossSessions(t *testing.T) {
	t.Parallel()
	scheduler := newTestScheduler(t, Options{Capacity: 1})

	started := make(chan string, 4)
	releases := make(chan struct{}, 4)
	submit := func(session, name string) Future {
		return scheduler.Submit(context.Background(), Request{
			Session: session, Generation: 1, Priority: Interactive,
			Run: func(context.Context, Lease) error {
				started <- name
				<-releases
				return nil
			},
		})
	}
	futures := []Future{submit("a", "a1")}
	if got := receiveString(t, started); got != "a1" {
		t.Fatalf("first start = %q", got)
	}
	futures = append(futures, submit("a", "a2"), submit("b", "b1"), submit("c", "c1"))
	releases <- struct{}{}
	want := []string{"b1", "c1", "a2"}
	for _, expected := range want {
		if got := receiveString(t, started); got != expected {
			t.Fatalf("start order = %q, want %q", got, expected)
		}
		releases <- struct{}{}
	}
	for _, future := range futures {
		if result := waitResult(t, future); result.Err != nil {
			t.Fatalf("scheduled work: %v", result.Err)
		}
	}
}

func TestInteractivePriorityHasBoundedBurst(t *testing.T) {
	t.Parallel()
	scheduler := newTestScheduler(t, Options{Capacity: 1, InteractiveBurst: 2})

	started := make(chan string, 6)
	releases := make(chan struct{}, 6)
	submit := func(session, name string, priority Priority) Future {
		return scheduler.Submit(context.Background(), Request{
			Session: session, Generation: 1, Priority: priority,
			Run: func(context.Context, Lease) error {
				started <- name
				<-releases
				return nil
			},
		})
	}
	futures := []Future{submit("seed", "seed", Interactive)}
	_ = receiveString(t, started)
	futures = append(futures, submit("background", "background", Background))
	for index := range 4 {
		futures = append(futures, submit(fmt.Sprintf("interactive-%d", index), fmt.Sprintf("i%d", index), Interactive))
	}
	releases <- struct{}{}
	if got := receiveString(t, started); got != "i0" {
		t.Fatalf("first queued start = %q, want i0", got)
	}
	releases <- struct{}{}
	if got := receiveString(t, started); got != "background" {
		// seed consumed one interactive slot, then i0 reached the burst of two.
		t.Fatalf("bounded-priority start = %q, want background", got)
	}
	releases <- struct{}{}
	for range 3 {
		_ = receiveString(t, started)
		releases <- struct{}{}
	}
	for _, future := range futures {
		if result := waitResult(t, future); result.Err != nil {
			t.Fatalf("scheduled work: %v", result.Err)
		}
	}
}

func TestQueuedContextCancellationAndGenerationAdvance(t *testing.T) {
	t.Parallel()
	scheduler := newTestScheduler(t, Options{Capacity: 1})

	block := make(chan struct{})
	started := make(chan struct{})
	running := scheduler.Submit(context.Background(), Request{
		Session: "running", Generation: 1, Priority: Interactive,
		Run: func(context.Context, Lease) error {
			close(started)
			<-block
			return nil
		},
	})
	waitSignal(t, started)

	ctx, cancel := context.WithCancel(context.Background())
	ranCanceled := atomic.Bool{}
	canceled := scheduler.Submit(ctx, Request{
		Session: "cancel", Generation: 1, Priority: Interactive,
		Run: func(context.Context, Lease) error { ranCanceled.Store(true); return nil },
	})
	cancel()
	if result := waitResult(t, canceled); !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("canceled result = %v", result.Err)
	}

	ranObsolete := atomic.Bool{}
	obsolete := scheduler.Submit(context.Background(), Request{
		Session: "generation", Generation: 1, Priority: Background,
		Run: func(context.Context, Lease) error { ranObsolete.Store(true); return nil },
	})
	scheduler.AdvanceGeneration("generation", 2)
	if result := waitResult(t, obsolete); !errors.Is(result.Err, ErrObsoleteGeneration) {
		t.Fatalf("obsolete result = %v", result.Err)
	}
	if future := scheduler.Submit(context.Background(), Request{
		Session: "generation", Generation: 1, Priority: Interactive,
		Run: func(context.Context, Lease) error { return nil },
	}); !errors.Is(waitResult(t, future).Err, ErrObsoleteGeneration) {
		t.Fatal("future obsolete work was accepted")
	}
	if ranCanceled.Load() || ranObsolete.Load() {
		t.Fatal("canceled queued work ran")
	}
	close(block)
	if result := waitResult(t, running); result.Err != nil {
		t.Fatalf("running work: %v", result.Err)
	}
}

func TestExclusiveWaitsForAllSlotsAndBlocksFollowers(t *testing.T) {
	t.Parallel()
	scheduler := newTestScheduler(t, Options{Capacity: 2})

	started := make(chan string, 4)
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	releaseExclusive := make(chan struct{})
	makeRequest := func(session, name string, isolation Isolation, release <-chan struct{}) Future {
		return scheduler.Submit(context.Background(), Request{
			Session: session, Generation: 1, Priority: Interactive, Isolation: isolation,
			Run: func(context.Context, Lease) error { started <- name; <-release; return nil },
		})
	}
	a := makeRequest("a", "a", Reuse, releaseA)
	b := makeRequest("b", "b", Reuse, releaseB)
	first := receiveString(t, started)
	second := receiveString(t, started)
	if first == second || (first != "a" && first != "b") || (second != "a" && second != "b") {
		t.Fatalf("initial starts = %q, %q", first, second)
	}
	exclusive := makeRequest("exclusive", "exclusive", Exclusive, releaseExclusive)
	followerRelease := make(chan struct{})
	follower := makeRequest("follower", "follower", Reuse, followerRelease)
	close(releaseA)
	select {
	case got := <-started:
		t.Fatalf("started %q before all workers drained", got)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseB)
	if got := receiveString(t, started); got != "exclusive" {
		t.Fatalf("start after drain = %q, want exclusive", got)
	}
	select {
	case got := <-started:
		t.Fatalf("started %q beside exclusive work", got)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseExclusive)
	if got := receiveString(t, started); got != "follower" {
		t.Fatalf("start after exclusive = %q, want follower", got)
	}
	close(followerRelease)
	for _, future := range []Future{a, b, exclusive, follower} {
		if result := waitResult(t, future); result.Err != nil {
			t.Fatalf("scheduled work: %v", result.Err)
		}
	}
}

func newTestScheduler(t *testing.T, options Options) *Scheduler {
	t.Helper()
	scheduler, err := New(options)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := scheduler.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return scheduler
}

func waitResult(t *testing.T, future Future) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := future.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return result
}

func receiveLease(t *testing.T, values <-chan Lease) Lease {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for lease")
		return Lease{}
	}
}

func receiveString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for start")
		return ""
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for signal")
	}
}
