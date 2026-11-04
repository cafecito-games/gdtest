package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	config, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.Godot != "godot" || len(config.Tests) != 1 {
		t.Fatalf("unexpected defaults: %#v", config)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, Filename), []byte(`{"mystery": true}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load error = %v, want unknown field", err)
	}
}

func TestLoadAppliesProjectValuesOverDefaults(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	data := []byte(`{"godot":"custom-godot","worker_idle_timeout":"3s","worker_max_runs":4}`)
	if err := os.WriteFile(filepath.Join(root, Filename), data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.Godot != "custom-godot" || time.Duration(config.WorkerIdleTimeout) != 3*time.Second {
		t.Fatalf("unexpected config: %#v", config)
	}
}
