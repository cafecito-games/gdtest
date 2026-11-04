package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconcileTransitionsAndOverflow(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "project.godot", "[application]\n")
	write(t, root, "tests/example.gd", "extends Node\nfunc test_value():\n\tassert(true)\n")
	reconciler := New(root)

	initial, err := reconciler.Reconcile(context.Background(), Initial)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), initial.Update.Generation)
	assert.True(t, initial.FullRescan)
	assert.Len(t, initial.Paths, 2)

	write(t, root, "tests/example.gd", "extends Node\nfunc test_value():\n\tassert(false)\n")
	body, err := reconciler.Reconcile(context.Background(), Filesystem)
	require.NoError(t, err)
	assert.Equal(t, uint64(2), body.Update.Generation)
	assert.False(t, body.Update.Structural)
	assert.Len(t, body.Update.Changes, 1)
	stamp := reconciler.Stamp(1)
	assert.True(t, stamp.Stale)
	assert.Equal(t, uint64(2), stamp.Current)

	require.NoError(t, os.Remove(filepath.Join(root, "tests", "example.gd")))
	overflow, err := reconciler.Reconcile(context.Background(), Overflow)
	require.NoError(t, err)
	assert.Equal(t, Overflow, overflow.Trigger)
	assert.True(t, overflow.FullRescan)
	assert.Equal(t, uint64(3), overflow.Update.Generation)
	assert.True(t, overflow.Update.Structural)
}

func TestReconcileDisappearanceDoesNotAdvanceGeneration(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "subject.gd", "func f():\n\tpass\n")
	reconciler := New(root)
	_, err := reconciler.Reconcile(context.Background(), Initial)
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(root))
	_, err = reconciler.Reconcile(context.Background(), Periodic)
	require.ErrorIs(t, err, ErrProjectMissing)
	assert.Equal(t, uint64(1), reconciler.Generation())
}

func TestReconcileRetriesFilesThatDisappearDuringScan(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	reconciler := New(root)
	reconciler.discover = func(context.Context, string) ([]string, error) {
		return []string{"gone.gd"}, nil
	}
	_, err := reconciler.Reconcile(context.Background(), Filesystem)
	require.ErrorIs(t, err, ErrUnstableScan)
	assert.Zero(t, reconciler.Generation())
}

func TestRelevantPathsAreDeterministicAndIgnoreMutableState(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "z.tscn", "[gd_scene]\n")
	write(t, root, "a.gd", "pass\n")
	write(t, root, "README.md", "not runtime input\n")
	write(t, root, ".git/ignored.gd", "pass\n")
	write(t, root, ".godot/imported.gd", "pass\n")
	write(t, root, ".gdtest/state.json", "{}\n")
	paths, err := RelevantPaths(context.Background(), root)
	require.NoError(t, err)
	want := []string{"a.gd", "z.tscn"}
	assert.Equal(t, want, paths)
}

func write(t *testing.T, root, name, content string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	require.NoError(t, os.WriteFile(filename, []byte(content), 0o600))
}
