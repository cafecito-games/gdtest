package daemon

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoggerWritesJSONAndRotates(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "daemon.jsonl")
	logger, err := OpenLogger(path, 120)
	if err != nil {
		t.Fatalf("OpenLogger: %v", err)
	}
	for index := range 4 {
		if err := logger.Write(map[string]any{"event": "test", "index": index}); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated log: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open current log: %v", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid JSON log: %v", err)
		}
		if record["timestamp"] == nil {
			t.Fatal("log record has no timestamp")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log: %v", err)
	}
}
