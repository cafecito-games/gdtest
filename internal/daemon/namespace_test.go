package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNamespaceIncludesProtocolAndBuild(t *testing.T) {
	t.Parallel()

	namespace, err := NewNamespace(t.TempDir(), 3, "build-a")
	if err != nil {
		t.Fatalf("NewNamespace: %v", err)
	}
	want := filepath.Join("gdtest", "daemons", "3", "build-a")
	if filepath.Base(namespace.Root) != "build-a" || !containsPath(namespace.Root, want) {
		t.Fatalf("namespace root = %q, want suffix %q", namespace.Root, want)
	}
	if err := namespace.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	info, err := os.Stat(namespace.Root)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("namespace mode = %o, want 700", info.Mode().Perm())
	}
}

func TestNamespaceRejectsUnsafeBuildID(t *testing.T) {
	t.Parallel()

	if _, err := NewNamespace(t.TempDir(), 1, "../other"); err == nil {
		t.Fatal("NewNamespace accepted unsafe build ID")
	}
}

func TestMetadataRoundTripAndMismatch(t *testing.T) {
	t.Parallel()

	namespace, err := NewNamespace(t.TempDir(), 1, "build-a")
	if err != nil {
		t.Fatalf("NewNamespace: %v", err)
	}
	if err := namespace.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	want := Metadata{Protocol: 1, BuildID: "build-a", PID: 42, SocketPath: namespace.SocketPath, StartedAt: time.Now().UTC()}
	if err := namespace.writeMetadata(want); err != nil {
		t.Fatalf("writeMetadata: %v", err)
	}
	got, err := namespace.ReadMetadata()
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if got.PID != want.PID || got.BuildID != want.BuildID {
		t.Fatalf("metadata = %#v, want %#v", got, want)
	}
	other := namespace
	other.BuildID = "build-b"
	if _, err := other.ReadMetadata(); err == nil {
		t.Fatal("ReadMetadata accepted build mismatch")
	}
}

func TestLockIsExclusiveAndReusable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "daemon.lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if _, err := AcquireLock(path); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second lock error = %v, want ErrAlreadyRunning", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first lock: %v", err)
	}
	second, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("reacquire lock: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second lock: %v", err)
	}
}

func containsPath(value, suffix string) bool {
	valueParts := filepath.Clean(value)
	suffixParts := filepath.Clean(suffix)
	return len(valueParts) >= len(suffixParts) && valueParts[len(valueParts)-len(suffixParts):] == suffixParts
}
