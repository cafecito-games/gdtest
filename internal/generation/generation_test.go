package generation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompareClassifiesFunctionBodyAndShape(t *testing.T) {
	t.Parallel()
	bodyA := inspect("test_example.gd", []byte("extends Node\n\nfunc test_value(value: int) -> void:\n\tassert(value == 1)\n"))
	bodyB := inspect("test_example.gd", []byte("extends Node\n\nfunc test_value(value: int) -> void:\n\tassert(value == 2)\n"))
	shape := inspect("test_example.gd", []byte("extends Node\n\nfunc test_value(value: String) -> void:\n\tassert(value == \"2\")\n"))
	assert.True(t, bodyA.Parsed)
	assert.True(t, bodyB.Parsed)
	assert.True(t, shape.Parsed)
	changes := Compare(snapshot(bodyA), snapshot(bodyB))
	require.Len(t, changes, 1)
	assert.Equal(t, BodyOnly, changes[0].Kind)
	changes = Compare(snapshot(bodyB), snapshot(shape))
	require.Len(t, changes, 1)
	assert.Equal(t, Structural, changes[0].Kind)
}

func TestCompareConservativelyClassifiesStructuralInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{"parse failure", "func broken(:\n", "func also_broken(:\n"},
		{"member initializer", "var mode := 1\n", "var mode := 2\n"},
		{"annotation", "@warning_ignore(\"unused_parameter\")\nfunc f(x):\n\tpass\n", "@warning_ignore(\"shadowed_variable\")\nfunc f(x):\n\tpass\n"},
		{"inner class", "class Inner:\n\tvar x := 1\n", "class Inner:\n\tvar x := 2\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldState := inspect("subject.gd", []byte(test.old))
			newState := inspect("subject.gd", []byte(test.new))
			changes := Compare(snapshot(oldState), snapshot(newState))
			require.Len(t, changes, 1)
			assert.Equal(t, Structural, changes[0].Kind)
		})
	}
}

func TestRuntimeAddonBodyChangeIsStructural(t *testing.T) {
	t.Parallel()
	oldState := inspect("addons/gdtest/worker.gd", []byte("func dispatch():\n\treturn 1\n"))
	newState := inspect("addons/gdtest/worker.gd", []byte("func dispatch():\n\treturn 2\n"))
	changes := Compare(snapshot(oldState), snapshot(newState))
	require.Len(t, changes, 1)
	assert.Equal(t, Structural, changes[0].Kind)
}

func TestTrackerMonotonicStaleAndConcurrentReaders(t *testing.T) {
	t.Parallel()
	var tracker Tracker
	one := snapshot(inspect("subject.gd", []byte("func f():\n\tpass\n")))
	two := snapshot(inspect("subject.gd", []byte("func f():\n\treturn 2\n")))
	update := tracker.Apply(one)
	assert.Equal(t, uint64(1), update.Generation)
	assert.Zero(t, update.Previous)
	update = tracker.Apply(one)
	assert.Equal(t, uint64(1), update.Generation)
	assert.Empty(t, update.Changes)

	var readers sync.WaitGroup
	for range 16 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 100 {
				_ = tracker.Generation()
				_ = tracker.Snapshot()
				_ = tracker.Stamp(1)
			}
		}()
	}
	update = tracker.Apply(two)
	readers.Wait()
	assert.Equal(t, uint64(2), update.Generation)
	assert.Equal(t, uint64(1), update.Previous)
	assert.False(t, update.Structural)
	stamp := tracker.Stamp(1)
	assert.True(t, stamp.Stale)
	assert.Equal(t, uint64(2), stamp.Current)
}

func TestBuildNormalizesAndContainsPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "tests"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests", "a.gd"), []byte("func f():\n\tpass\n"), 0o600))
	got, err := Build(context.Background(), root, []string{"tests/a.gd", "tests/../tests/a.gd"})
	require.NoError(t, err)
	assert.Len(t, got.Files, 1)
	assert.NotEmpty(t, got.Files["tests/a.gd"].ContentDigest)
	_, err = Build(context.Background(), root, []string{"../outside.gd"})
	require.Error(t, err)

	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.gd")
	require.NoError(t, os.WriteFile(outsideFile, []byte("pass\n"), 0o600))
	if err := os.Symlink(outsideFile, filepath.Join(root, "escape.gd")); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "privilege") {
			t.Skip(err)
		}
		require.NoError(t, err)
	}
	_, err = Build(context.Background(), root, []string{"escape.gd"})
	require.Error(t, err)
}

func TestSnapshotContentFingerprintIncludesEveryPathAndContent(t *testing.T) {
	t.Parallel()

	base := Snapshot{Files: map[string]FileState{
		"project.godot": {Path: "project.godot", ContentDigest: "project-a"},
		"tests/a.gd":    {Path: "tests/a.gd", ContentDigest: "script-a"},
	}}
	sameDifferentMapOrder := Snapshot{Files: map[string]FileState{
		"tests/a.gd":    {Path: "tests/a.gd", ContentDigest: "script-a"},
		"project.godot": {Path: "project.godot", ContentDigest: "project-a"},
	}}
	resourceChanged := Snapshot{Files: map[string]FileState{
		"project.godot": {Path: "project.godot", ContentDigest: "project-b"},
		"tests/a.gd":    {Path: "tests/a.gd", ContentDigest: "script-a"},
	}}

	assert.Equal(t, base.ContentFingerprint(), sameDifferentMapOrder.ContentFingerprint())
	assert.NotEqual(t, base.ContentFingerprint(), resourceChanged.ContentFingerprint())
}

func snapshot(state FileState) Snapshot {
	return Snapshot{Files: map[string]FileState{state.Path: state}}
}
