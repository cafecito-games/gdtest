package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLifecycleMissingGraceAndReappearance(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "worktree")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	start := time.Unix(100, 0)
	lifecycle := Lifecycle{ProjectRoot: root, LastUsed: start}
	if reason, err := lifecycle.Reconcile(start, time.Minute, time.Hour); err != nil || reason != KeepSession {
		t.Fatalf("initial reconcile = %q, %v", reason, err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if reason, err := lifecycle.Reconcile(start.Add(time.Second), time.Minute, time.Hour); err != nil || reason != KeepSession {
		t.Fatalf("grace reconcile = %q, %v", reason, err)
	}
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if reason, err := lifecycle.Reconcile(start.Add(30*time.Second), time.Minute, time.Hour); err != nil || reason != KeepSession {
		t.Fatalf("restored reconcile = %q, %v", reason, err)
	}
	if !lifecycle.MissingSince.IsZero() {
		t.Fatalf("restored project retained missing time %s", lifecycle.MissingSince)
	}
	if err := os.Remove(root); err != nil {
		t.Fatalf("Remove again: %v", err)
	}
	if _, err := lifecycle.Reconcile(start.Add(time.Minute), time.Minute, time.Hour); err != nil {
		t.Fatalf("begin second grace: %v", err)
	}
	if reason, err := lifecycle.Reconcile(start.Add(2*time.Minute), time.Minute, time.Hour); err != nil || reason != RemoveMissing {
		t.Fatalf("expired grace = %q, %v", reason, err)
	}
}

func TestLifecycleIdleTTL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	start := time.Unix(100, 0)
	lifecycle := Lifecycle{ProjectRoot: root, LastUsed: start}
	if reason, err := lifecycle.Reconcile(start.Add(time.Minute), time.Minute, 2*time.Minute); err != nil || reason != KeepSession {
		t.Fatalf("active reconcile = %q, %v", reason, err)
	}
	if reason, err := lifecycle.Reconcile(start.Add(2*time.Minute), time.Minute, 2*time.Minute); err != nil || reason != RemoveIdle {
		t.Fatalf("idle reconcile = %q, %v", reason, err)
	}
	lifecycle.Touch(start.Add(3 * time.Minute))
	if reason, err := lifecycle.Reconcile(start.Add(3*time.Minute), time.Minute, 2*time.Minute); err != nil || reason != KeepSession {
		t.Fatalf("touched reconcile = %q, %v", reason, err)
	}
}
