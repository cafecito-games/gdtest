package gdtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallRuntimeIdempotentAndConflictSafe(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := InstallRuntime(root); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := InstallRuntime(root); err != nil {
		t.Fatalf("idempotent install: %v", err)
	}
	path := filepath.Join(root, "addons", "gdtest", "gdtest.gd")
	if err := os.WriteFile(path, []byte("modified\n"), 0o644); err != nil {
		t.Fatalf("modify runtime: %v", err)
	}
	if err := InstallRuntime(root); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("conflicting install error = %v", err)
	}
}

func TestInstallRuntimeRefusesGPMOwnership(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	manifest := []byte("[addons.gdtest]\nversion = \"v1.0.0\"\n")
	if err := os.WriteFile(filepath.Join(root, "addons.toml"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := InstallRuntime(root); err == nil || !strings.Contains(err.Error(), "gpm install") {
		t.Fatalf("gpm-owned install error = %v", err)
	}
}
