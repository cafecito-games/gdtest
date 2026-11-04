package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gdtest "github.com/cafecito-games/gdtest"
	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/model"
	projectidentity "github.com/cafecito-games/gdtest/internal/project"
	"github.com/cafecito-games/gdtest/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinkedWorktreesAreIsolatedUnderGlobalWorkerBound(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("multi-worktree integration skipped: git is not on PATH")
	}
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("multi-worktree integration skipped: godot is not on PATH")
	}

	fixtureRoot := t.TempDir()
	repository := filepath.Join(fixtureRoot, "repository")
	worktreeA := filepath.Join(fixtureRoot, "worktree-a")
	worktreeB := filepath.Join(fixtureRoot, "worktree-b")
	worktreeRun(t, fixtureRoot, "git", "init", "--initial-branch=main", repository)
	worktreeRun(t, repository, "git", "config", "user.name", "gdtest")
	worktreeRun(t, repository, "git", "config", "user.email", "gdtest@example.invalid")
	worktreeWrite(t, filepath.Join(repository, "project.godot"), "config_version=5\n")
	worktreeWrite(t, filepath.Join(repository, "tests/isolation.test.gd"), failingWorktreeSource("base"))
	require.NoError(t, gdtest.InstallRuntime(repository))
	worktreeRun(t, repository, "git", "add", ".")
	worktreeRun(t, repository, "git", "commit", "-m", "worktree fixture")
	worktreeRun(t, repository, "git", "worktree", "add", "--detach", worktreeA, "HEAD")
	worktreeRun(t, repository, "git", "worktree", "add", "--detach", worktreeB, "HEAD")
	worktreeBRegistered := worktreeB
	worktreeA = canonicalWorktree(t, worktreeA)
	worktreeB = canonicalWorktree(t, worktreeB)
	worktreeWrite(t, filepath.Join(worktreeA, "tests/isolation.test.gd"), passingWorktreeSource("worktree-a"))
	worktreeWrite(t, filepath.Join(worktreeB, "tests/isolation.test.gd"), failingWorktreeSource("worktree-b"))
	bootstrapWorktree(t, worktreeA)
	bootstrapWorktree(t, worktreeB)

	sessions := service.NewSessionServiceWithOptions(service.ServiceOptions{MaxWorkers: 2})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		assert.NoError(t, sessions.Close(ctx))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	type labeledOutcome struct {
		label  string
		events []model.Event
		err    error
	}
	outcomes := make(chan labeledOutcome, 2)
	for _, item := range []struct {
		label string
		root  string
	}{
		{label: "a", root: worktreeA},
		{label: "b", root: worktreeB},
	} {
		item := item
		sessionID := worktreeSessionID(t, item.root)
		go func() {
			events, err := runWorktreePlan(ctx, sessions, item.root, sessionID)
			outcomes <- labeledOutcome{label: item.label, events: events, err: err}
		}()
	}
	results := make(map[string]labeledOutcome, 2)
	for range 2 {
		outcome := <-outcomes
		require.NoError(t, outcome.err, "worktree %s run", outcome.label)
		results[outcome.label] = outcome
	}
	assertWorktreeOutcome(t, results["a"].events, "pass", "")
	assertWorktreeOutcome(t, results["b"].events, "fail", "from-worktree-b")
	worktreeAID := stableCaseID(results["a"].events)
	worktreeBID := stableCaseID(results["b"].events)
	assert.NotEmpty(t, worktreeAID)
	assert.NotEmpty(t, worktreeBID)
	assert.Equal(t, worktreeAID, worktreeBID)
	assert.LessOrEqual(t, sessions.WorkerCount(), 2)

	stressRoots := make([]string, 6)
	for index := range stressRoots {
		stressRoots[index] = filepath.Join(fixtureRoot, fmt.Sprintf("stress-%02d", index))
		worktreeRun(t, repository, "git", "worktree", "add", "--detach", stressRoots[index], "HEAD")
		stressRoots[index] = canonicalWorktree(t, stressRoots[index])
		bootstrapWorktree(t, stressRoots[index])
	}
	var peak atomic.Int64
	stopMonitor := make(chan struct{})
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopMonitor:
				return
			case <-ticker.C:
				current := int64(sessions.WorkerCount())
				for {
					previous := peak.Load()
					if current <= previous || peak.CompareAndSwap(previous, current) {
						break
					}
				}
			}
		}
	}()
	var wait sync.WaitGroup
	stressErrors := make(chan error, len(stressRoots))
	for _, root := range stressRoots {
		root := root
		sessionID := worktreeSessionID(t, root)
		wait.Add(1)
		go func() {
			defer wait.Done()
			stressErrors <- warmWorktree(ctx, sessions, root, sessionID)
		}()
	}
	wait.Wait()
	close(stopMonitor)
	<-monitorDone
	close(stressErrors)
	for err := range stressErrors {
		require.NoError(t, err)
	}
	assert.LessOrEqual(t, sessions.WorkerCount(), 2)
	assert.LessOrEqual(t, peak.Load(), int64(2))

	worktreeRun(t, repository, "git", "worktree", "remove", "--force", worktreeBRegistered)
	events, err := runWorktreePlan(ctx, sessions, worktreeA, worktreeSessionID(t, worktreeA))
	require.NoError(t, err)
	assertWorktreeOutcome(t, events, "pass", "")
}

func runWorktreePlan(
	ctx context.Context,
	sessions *service.SessionService,
	root string,
	sessionID string,
) ([]model.Event, error) {
	const testID = "tests/isolation.test.gd::test_identity"
	request := service.RunRequest{
		SessionID: sessionID,
		Project:   root,
		Godot:     "godot",
		Plan: model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "run-" + filepath.Base(root),
			Generation:      1,
			Files: []model.FilePlan{{
				Path: "tests/isolation.test.gd",
				Cases: []model.CasePlan{{
					ID: testID, Name: "Identity", MethodName: "test_identity", Line: 5,
				}},
			}},
		},
		WorkerMaxRuns: 100,
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	events := make([]model.Event, 0, 8)
	_, err = sessions.Handle(ctx, daemon.Request{
		ID: "request-" + filepath.Base(root), Method: "session.run", Payload: payload,
	}, func(value any) error {
		event, ok := value.(model.Event)
		if !ok {
			return fmt.Errorf("daemon event has type %T, want model.Event", value)
		}
		events = append(events, event)
		return nil
	})
	return events, err
}

func warmWorktree(
	ctx context.Context,
	sessions *service.SessionService,
	root string,
	sessionID string,
) error {
	payload, err := json.Marshal(service.WarmRequest{
		SessionID: sessionID, Project: root, Godot: "godot", Generation: 1,
	})
	if err != nil {
		return err
	}
	_, err = sessions.Handle(ctx, daemon.Request{
		ID: "warm-" + filepath.Base(root), Method: "session.warm", Payload: payload,
	}, nil)
	return err
}

func worktreeSessionID(t *testing.T, root string) string {
	t.Helper()
	identity, err := projectidentity.Resolve(context.Background(), root)
	require.NoError(t, err)
	key, err := projectidentity.NewSessionKey(identity, "godot-4.7", "fixture-config", "fixture-build")
	require.NoError(t, err)
	return key.ID()
}

func assertWorktreeOutcome(t *testing.T, events []model.Event, status, failureMessage string) {
	t.Helper()
	var finishedStatus string
	var message string
	for _, event := range events {
		switch event.Type {
		case model.EventAssertionFail:
			message = event.Message
		case model.EventCaseFinished:
			finishedStatus = event.Status
		}
	}
	assert.Equal(t, status, finishedStatus, "events = %#v", events)
	if failureMessage != "" && message != failureMessage {
		assert.Equal(t, failureMessage, message)
	}
}

func stableCaseID(events []model.Event) string {
	for _, event := range events {
		if event.Type == model.EventCaseStarted {
			return event.TestID
		}
	}
	return ""
}

func passingWorktreeSource(label string) string {
	return fmt.Sprintf(
		"extends GDTest\n\n\n## Verifies the dirty source loaded from %s.\nfunc test_identity() -> void:\n\texpect_equal(\"%s\", \"%s\")\n",
		label,
		label,
		label,
	)
}

func failingWorktreeSource(label string) string {
	return fmt.Sprintf(
		"extends GDTest\n\n\n## Verifies the dirty source loaded from %s.\nfunc test_identity() -> void:\n\texpect_equal(\"%s\", \"other\", \"from-%s\")\n",
		label,
		label,
		label,
	)
}

func bootstrapWorktree(t *testing.T, root string) {
	t.Helper()
	worktreeRun(t, root, "godot", "--headless", "--editor", "--path", root, "--quit")
}

func canonicalWorktree(t *testing.T, root string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(root)
	require.NoError(t, err, "canonicalize worktree %s", root)
	return canonical
}

func worktreeRun(t *testing.T, directory, name string, arguments ...string) {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s %v in %s\n%s", name, arguments, directory, output)
}

func worktreeWrite(t *testing.T, filename, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	require.NoError(t, os.WriteFile(filename, []byte(contents), 0o644))
}
