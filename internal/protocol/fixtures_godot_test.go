package protocol

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLanguageNeutralFixturesDecodeInGDScript(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("GDScript protocol fixture test skipped: godot is not on PATH")
	}
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"godot",
		"--headless",
		"--path",
		root,
		"--script",
		"res://fixtures/protocol/godot_fixture_runner.gd",
	)
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "GDScript protocol fixture validation:\n%s", output)
	assert.Contains(t, string(output), "gdtest protocol fixtures passed in GDScript")
}
