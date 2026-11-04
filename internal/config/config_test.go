package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	config, err := Load(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "godot", config.Godot)
	assert.Len(t, config.Tests, 1)
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, Filename), []byte(`{"mystery": true}`), 0o600))
	_, err := Load(root)
	require.Error(t, err)
	assert.ErrorContains(t, err, "unknown field")
}

func TestLoadAppliesProjectValuesOverDefaults(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	data := []byte(`{"godot":"custom-godot","worker_idle_timeout":"3s","worker_max_runs":4}`)
	require.NoError(t, os.WriteFile(filepath.Join(root, Filename), data, 0o600))
	config, err := Load(root)
	require.NoError(t, err)
	assert.Equal(t, "custom-godot", config.Godot)
	assert.Equal(t, 3*time.Second, time.Duration(config.WorkerIdleTimeout))
}

func TestLoadWithUserAppliesUserGodotUnlessProjectOverridesIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	user := UserDefaults()
	user.Godot = "user-godot"
	loaded, err := LoadWithUser(root, user)
	require.NoError(t, err)
	assert.Equal(t, "user-godot", loaded.Godot)

	require.NoError(t, os.WriteFile(
		filepath.Join(root, Filename), []byte(`{"godot":"project-godot"}`), 0o600,
	))
	loaded, err = LoadWithUser(root, user)
	require.NoError(t, err)
	assert.Equal(t, "project-godot", loaded.Godot)
}
