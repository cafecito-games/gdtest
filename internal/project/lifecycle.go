package project

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// RemovalReason describes why a live session should be evicted.
type RemovalReason string

// Session removal decisions.
const (
	KeepSession   RemovalReason = ""
	RemoveMissing RemovalReason = "missing"
	RemoveIdle    RemovalReason = "idle"
)

// Lifecycle tracks disappearance grace and idle time without creating worktree state.
type Lifecycle struct {
	ProjectRoot  string
	LastUsed     time.Time
	MissingSince time.Time
}

// Touch records meaningful session activity.
func (l *Lifecycle) Touch(now time.Time) { l.LastUsed = now }

// Reconcile checks the canonical project path and returns a conservative eviction decision.
func (l *Lifecycle) Reconcile(now time.Time, missingGrace, idleTTL time.Duration) (RemovalReason, error) {
	if l.ProjectRoot == "" {
		return KeepSession, errors.New("lifecycle project root is required")
	}
	info, err := os.Stat(l.ProjectRoot)
	switch {
	case err == nil:
		if !info.IsDir() {
			return KeepSession, fmt.Errorf("session project root %s is not a directory", l.ProjectRoot)
		}
		l.MissingSince = time.Time{}
	case errors.Is(err, os.ErrNotExist):
		if l.MissingSince.IsZero() {
			l.MissingSince = now
		}
		if missingGrace <= 0 || !now.Before(l.MissingSince.Add(missingGrace)) {
			return RemoveMissing, nil
		}
		return KeepSession, nil
	default:
		return KeepSession, fmt.Errorf("inspect session project root: %w", err)
	}
	if idleTTL > 0 && !l.LastUsed.IsZero() && !now.Before(l.LastUsed.Add(idleTTL)) {
		return RemoveIdle, nil
	}
	return KeepSession, nil
}
