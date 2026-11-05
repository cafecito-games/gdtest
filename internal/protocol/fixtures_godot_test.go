package protocol

import (
	"context"
	"os"
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
	repository := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	root := copyProtocolFixtureProject(t, repository)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bootstrap := exec.CommandContext(ctx, "godot", "--headless", "--editor", "--path", root, "--quit")
	bootstrapOutput, err := bootstrap.CombinedOutput()
	require.NoErrorf(t, err, "bootstrap GDScript protocol fixture project:\n%s", bootstrapOutput)
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

func copyProtocolFixtureProject(t *testing.T, repository string) string {
	t.Helper()
	destination := t.TempDir()
	addonFiles, err := filepath.Glob(filepath.Join(repository, "addons", "gdtest", "*"))
	require.NoError(t, err)
	files := append([]string{filepath.Join(repository, "project.godot")}, addonFiles...)
	files = append(files,
		filepath.Join(repository, "fixtures", "protocol", "godot_fixture_runner.gd"),
		filepath.Join(repository, "fixtures", "protocol", "godot_fixture_runner.gd.uid"),
		filepath.Join(repository, "fixtures", "protocol", "run-events.jsonl"),
		filepath.Join(repository, "fixtures", "protocol", "worker-handshake.jsonl"),
	)
	for _, source := range files {
		info, statErr := os.Stat(source)
		require.NoError(t, statErr)
		if info.IsDir() {
			continue
		}
		relative, relativeErr := filepath.Rel(repository, source)
		require.NoError(t, relativeErr)
		target := filepath.Join(destination, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		data, readErr := os.ReadFile(source)
		require.NoError(t, readErr)
		require.NoError(t, os.WriteFile(target, data, 0o600))
	}
	return destination
}
