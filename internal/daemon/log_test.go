package daemon

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoggerWritesJSONAndRotates(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "daemon.jsonl")
	logger, err := OpenLogger(path, 120)
	require.NoError(t, err)
	for index := range 4 {
		require.NoError(t, logger.Write(map[string]any{"event": "test", "index": index}))
	}
	require.NoError(t, logger.Close())
	require.FileExists(t, path+".1")
	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &record))
		assert.NotNil(t, record["timestamp"])
	}
	require.NoError(t, scanner.Err())
}

func TestLoggerHonorsConfiguredMinimumLevel(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "daemon.jsonl")
	logger, err := OpenLoggerWithLevel(path, 1<<20, "warn")
	require.NoError(t, err)
	for _, level := range []string{"debug", "info", "warn", "error"} {
		require.NoError(t, logger.Write(map[string]any{"event": level, "level": level}))
	}
	require.NoError(t, logger.Close())

	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	var records []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &record))
		records = append(records, record)
	}
	require.NoError(t, scanner.Err())
	require.Len(t, records, 2)
	assert.Equal(t, "warn", records[0]["level"])
	assert.Equal(t, "error", records[1]["level"])

	_, err = OpenLoggerWithLevel(filepath.Join(t.TempDir(), "invalid.jsonl"), 1<<20, "verbose")
	require.ErrorContains(t, err, "invalid daemon log level")
}
