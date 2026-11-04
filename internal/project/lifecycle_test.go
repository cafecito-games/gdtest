package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLifecycleMissingGraceAndReappearance(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "worktree")
	require.NoError(t, os.Mkdir(root, 0o755))
	start := time.Unix(100, 0)
	lifecycle := Lifecycle{ProjectRoot: root, LastUsed: start}
	reason, err := lifecycle.Reconcile(start, time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, KeepSession, reason)
	require.NoError(t, os.Remove(root))
	reason, err = lifecycle.Reconcile(start.Add(time.Second), time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, KeepSession, reason)
	require.NoError(t, os.Mkdir(root, 0o755))
	reason, err = lifecycle.Reconcile(start.Add(30*time.Second), time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, KeepSession, reason)
	assert.True(t, lifecycle.MissingSince.IsZero())
	require.NoError(t, os.Remove(root))
	_, err = lifecycle.Reconcile(start.Add(time.Minute), time.Minute, time.Hour)
	require.NoError(t, err)
	reason, err = lifecycle.Reconcile(start.Add(2*time.Minute), time.Minute, time.Hour)
	require.NoError(t, err)
	assert.Equal(t, RemoveMissing, reason)
}

func TestLifecycleIdleTTL(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	start := time.Unix(100, 0)
	lifecycle := Lifecycle{ProjectRoot: root, LastUsed: start}
	reason, err := lifecycle.Reconcile(start.Add(time.Minute), time.Minute, 2*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, KeepSession, reason)
	reason, err = lifecycle.Reconcile(start.Add(2*time.Minute), time.Minute, 2*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, RemoveIdle, reason)
	lifecycle.Touch(start.Add(3 * time.Minute))
	reason, err = lifecycle.Reconcile(start.Add(3*time.Minute), time.Minute, 2*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, KeepSession, reason)
}
