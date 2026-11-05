package scheduler

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.NotEqual(t, first.Slot, second.Slot, "simultaneous leases must use unique slots")
	select {
	case third := <-started:
		require.FailNow(t, "capacity exceeded", "unexpected lease: %#v", third)
	case <-time.After(30 * time.Millisecond):
	}
	for range 5 {
		release <- struct{}{}
		// Once one lease exits, another may start and need a release token.
	}
	for _, future := range futures {
		result := waitResult(t, future)
		require.NoError(t, result.Err)
	}
	assert.Equal(t, int32(2), peak.Load())
	assert.Equal(t, 2, scheduler.Stats().PeakActive)
}

func TestIsolationNamesRoundTrip(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		want Isolation
	}{
		{name: "", want: Reuse},
		{name: "reuse", want: Reuse},
		{name: "recycle", want: Recycle},
		{name: "fresh", want: Fresh},
		{name: "exclusive", want: Exclusive},
	} {
		actual, err := ParseIsolation(test.name)
		require.NoError(t, err)
		assert.Equal(t, test.want, actual)
		assert.Equal(t, test.want.String(), actual.String())
	}

	_, err := ParseIsolation("shared")
	require.EqualError(t, err, `isolation "shared" must be reuse, recycle, fresh, or exclusive`)
	assert.Equal(t, "isolation(99)", Isolation(99).String())
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
		require.FailNow(t, "a fifth session started above the global bound")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	for _, future := range futures {
		require.NoError(t, waitResult(t, future).Err)
	}
	assert.Equal(t, int32(4), peak.Load())
	assert.Equal(t, 4, scheduler.Stats().PeakActive)
}

func TestOneSessionCanUseMultipleFileLevelSlots(t *testing.T) {
	t.Parallel()
	scheduler := newTestScheduler(t, Options{Capacity: 2})

	started := make(chan int, 2)
	release := make(chan struct{})
	futures := make([]Future, 0, 2)
	for range 2 {
		futures = append(futures, scheduler.Submit(context.Background(), Request{
			Session: "project", Generation: 1, Priority: Interactive,
			Run: func(_ context.Context, lease Lease) error {
				started <- lease.Slot
				<-release
				return nil
			},
		}))
	}
	first := receiveInt(t, started)
	second := receiveInt(t, started)
	assert.NotEqual(t, first, second)
	close(release)
	for _, future := range futures {
		require.NoError(t, waitResult(t, future).Err)
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
			assert.Equal(t, Background, priority)
		case <-time.After(2 * time.Second):
			require.FailNow(t, "background work did not start")
		}
	}
	select {
	case <-started:
		require.FailNow(t, "background limit exceeded")
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
			assert.Equal(t, Interactive, priority)
		case <-time.After(2 * time.Second):
			require.FailNow(t, "interactive work did not fill spare slot")
		}
	}
	stats := scheduler.Stats()
	assert.Equal(t, 4, stats.Active)
	assert.Equal(t, 2, stats.BackgroundActive)
	close(release)
	for _, future := range futures {
		require.NoError(t, waitResult(t, future).Err)
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
	assert.Equal(t, "a1", receiveString(t, started))
	futures = append(futures, submit("a", "a2"), submit("b", "b1"), submit("c", "c1"))
	releases <- struct{}{}
	want := []string{"b1", "c1", "a2"}
	for _, expected := range want {
		assert.Equal(t, expected, receiveString(t, started))
		releases <- struct{}{}
	}
	for _, future := range futures {
		require.NoError(t, waitResult(t, future).Err)
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
	assert.Equal(t, "i0", receiveString(t, started))
	releases <- struct{}{}
	// seed consumed one interactive slot, then i0 reached the burst of two.
	assert.Equal(t, "background", receiveString(t, started))
	releases <- struct{}{}
	for range 3 {
		_ = receiveString(t, started)
		releases <- struct{}{}
	}
	for _, future := range futures {
		require.NoError(t, waitResult(t, future).Err)
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
	require.ErrorIs(t, waitResult(t, canceled).Err, context.Canceled)

	ranObsolete := atomic.Bool{}
	obsolete := scheduler.Submit(context.Background(), Request{
		Session: "generation", Generation: 1, Priority: Background,
		Run: func(context.Context, Lease) error { ranObsolete.Store(true); return nil },
	})
	scheduler.AdvanceGeneration("generation", 2)
	require.ErrorIs(t, waitResult(t, obsolete).Err, ErrObsoleteGeneration)
	future := scheduler.Submit(context.Background(), Request{
		Session: "generation", Generation: 1, Priority: Interactive,
		Run: func(context.Context, Lease) error { return nil },
	})
	require.ErrorIs(t, waitResult(t, future).Err, ErrObsoleteGeneration)
	assert.False(t, ranCanceled.Load())
	assert.False(t, ranObsolete.Load())
	close(block)
	require.NoError(t, waitResult(t, running).Err)
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
	assert.ElementsMatch(t, []string{"a", "b"}, []string{first, second})
	exclusive := makeRequest("exclusive", "exclusive", Exclusive, releaseExclusive)
	followerRelease := make(chan struct{})
	follower := makeRequest("follower", "follower", Reuse, followerRelease)
	close(releaseA)
	select {
	case got := <-started:
		require.FailNow(t, "work started before all workers drained", "started %q", got)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseB)
	assert.Equal(t, "exclusive", receiveString(t, started))
	select {
	case got := <-started:
		require.FailNow(t, "work started beside exclusive work", "started %q", got)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseExclusive)
	assert.Equal(t, "follower", receiveString(t, started))
	close(followerRelease)
	for _, future := range []Future{a, b, exclusive, follower} {
		require.NoError(t, waitResult(t, future).Err)
	}
}

func TestRetireRejectsQueuedAndFutureWorkAndKeepsOtherSessionsFair(t *testing.T) {
	t.Parallel()
	scheduler := newTestScheduler(t, Options{Capacity: 1})
	started := make(chan struct{})
	block := make(chan struct{})
	running := scheduler.Submit(context.Background(), Request{
		Session: "retired", Generation: 1, Priority: Interactive,
		Run: func(context.Context, Lease) error {
			close(started)
			<-block
			return nil
		},
	})
	waitSignal(t, started)
	ranQueued := atomic.Bool{}
	queued := scheduler.Submit(context.Background(), Request{
		Session: "retired", Generation: 5, Priority: Interactive,
		Run: func(context.Context, Lease) error { ranQueued.Store(true); return nil },
	})
	ranOther := atomic.Bool{}
	other := scheduler.Submit(context.Background(), Request{
		Session: "other", Generation: 1, Priority: Interactive,
		Run: func(context.Context, Lease) error { ranOther.Store(true); return nil },
	})

	scheduler.Retire("retired")
	require.ErrorIs(t, waitResult(t, queued).Err, ErrObsoleteGeneration)
	late := scheduler.Submit(context.Background(), Request{
		Session: "retired", Generation: ^uint64(0) - 1, Priority: Interactive,
		Run: func(context.Context, Lease) error { return nil },
	})
	require.ErrorIs(t, waitResult(t, late).Err, ErrObsoleteGeneration)

	close(block)
	require.NoError(t, waitResult(t, running).Err)
	require.NoError(t, waitResult(t, other).Err)
	assert.False(t, ranQueued.Load())
	assert.True(t, ranOther.Load())
	assert.Zero(t, scheduler.Stats().Queued)
}

func newTestScheduler(t *testing.T, options Options) *Scheduler {
	t.Helper()
	scheduler, err := New(options)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		assert.NoError(t, scheduler.Shutdown(ctx))
	})
	return scheduler
}

func waitResult(t *testing.T, future Future) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := future.Wait(ctx)
	require.NoError(t, err)
	return result
}

func receiveLease(t *testing.T, values <-chan Lease) Lease {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timed out waiting for lease")
		return Lease{}
	}
}

func receiveString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timed out waiting for start")
		return ""
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timed out waiting for signal")
	}
}

func receiveInt(t *testing.T, values <-chan int) int {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timed out waiting for integer")
		return 0
	}
}
