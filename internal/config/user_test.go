package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadUserDefaultsAndStrictOverrides(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.json")
	defaults, err := loadUserFile(missing)
	require.NoError(t, err)
	assert.Equal(t, 4, defaults.MaxWorkers)
	assert.Equal(t, 30*time.Minute, time.Duration(defaults.WorkerMaxAge))

	filename := filepath.Join(t.TempDir(), UserFilename)
	require.NoError(t, os.WriteFile(filename, []byte(`{
  "max_workers": 2,
  "memory_budget_mb": 1024,
  "worker_max_age": "10m",
  "log_level": "debug",
  "godot": "godot-custom"
}`), 0o600))
	configured, err := loadUserFile(filename)
	require.NoError(t, err)
	assert.Equal(t, 2, configured.MaxWorkers)
	assert.Equal(t, 1024, configured.MemoryBudgetMB)
	assert.Equal(t, 10*time.Minute, time.Duration(configured.WorkerMaxAge))
	assert.Equal(t, "debug", configured.LogLevel)
	assert.Equal(t, "godot-custom", configured.Godot)

	require.NoError(t, os.WriteFile(filename, []byte(`{"unknown": true}`), 0o600))
	_, err = loadUserFile(filename)
	require.ErrorContains(t, err, "unknown field")
}
