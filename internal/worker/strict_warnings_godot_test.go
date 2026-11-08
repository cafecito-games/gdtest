package worker_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGodotRuntimeParsesUnderStrictWarnings loads every shipped runtime script in
// a project that promotes the unsafe_* GDScript warnings to errors and disables
// the addon warning exclusion. This guards the portability limit described in
// https://github.com/cafecito-games/gdtest/issues/29: the runtime must parse even
// when an adopter holds its own addon code to a strict warning profile.
func TestGodotRuntimeParsesUnderStrictWarnings(t *testing.T) {
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot strict-warnings test skipped: godot is not on PATH")
	}
	root := strictWarningsProject(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	bootstrap := exec.CommandContext(ctx, "godot", "--headless", "--editor", "--path", root, "--quit")
	output, err := bootstrap.CombinedOutput()
	require.NoErrorf(t, err, "bootstrap strict-warnings project:\n%s", output)

	run := exec.CommandContext(ctx, "godot", "--headless", "--path", root, "--script", "res://load_all.gd")
	output, err = run.CombinedOutput()
	require.NoErrorf(t, err, "runtime failed to parse under strict warnings:\n%s", output)
}

// strictWarningsProject stages the shipped addon into a fresh project whose
// project.godot treats unsafe_* warnings as errors for addons too.
func strictWarningsProject(t *testing.T) string {
	t.Helper()
	destination := t.TempDir()
	repository := repositoryRoot(t)
	addonFiles, err := filepath.Glob(filepath.Join(repository, "addons", "gdtest", "*"))
	require.NoError(t, err)
	for _, source := range addonFiles {
		info, statErr := os.Stat(source)
		require.NoError(t, statErr)
		if info.IsDir() {
			continue
		}
		relative, relErr := filepath.Rel(repository, source)
		require.NoError(t, relErr)
		target := filepath.Join(destination, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		data, readErr := os.ReadFile(source)
		require.NoError(t, readErr)
		require.NoError(t, os.WriteFile(target, data, 0o600))
	}

	project := `; Engine configuration for validating the shipped addon under strict warnings.
config_version=5

[application]
config/name="StrictWarnings"

[debug]
gdscript/warnings/exclude_addons=false
gdscript/warnings/unsafe_method_access=2
gdscript/warnings/unsafe_property_access=2
gdscript/warnings/unsafe_call_argument=2
`
	require.NoError(t, os.WriteFile(filepath.Join(destination, "project.godot"), []byte(project), 0o600))

	loader := `extends SceneTree

func _initialize() -> void:
	var failures := 0
	var directory := DirAccess.open("res://addons/gdtest")
	for filename: String in directory.get_files():
		if not filename.ends_with(".gd"):
			continue
		if load("res://addons/gdtest/" + filename) == null:
			failures += 1
	quit(1 if failures > 0 else 0)
`
	require.NoError(t, os.WriteFile(filepath.Join(destination, "load_all.gd"), []byte(loader), 0o600))
	return destination
}
