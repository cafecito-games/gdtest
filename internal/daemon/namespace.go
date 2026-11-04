// Package daemon hosts the user-scoped gdtest service and its local protocol.
package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/cafecito-games/gdtest/internal/version"
)

// ProtocolVersion is the client-to-daemon JSONL protocol version.
const ProtocolVersion = 1

var safeBuildID = regexp.MustCompile(`\A[a-zA-Z0-9._-]+\z`)

// Namespace isolates daemon state by protocol and exact executable build.
type Namespace struct {
	Root         string
	Protocol     int
	BuildID      string
	SocketPath   string
	MetadataPath string
	LockPath     string
	LogPath      string
}

// DefaultNamespace returns the namespace for the running executable.
func DefaultNamespace() (Namespace, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return Namespace{}, fmt.Errorf("user cache directory: %w", err)
	}
	return NewNamespace(cache, ProtocolVersion, version.BuildID())
}

// NewNamespace builds a namespace below cacheRoot without touching the filesystem.
func NewNamespace(cacheRoot string, protocolVersion int, buildID string) (Namespace, error) {
	if cacheRoot == "" || !filepath.IsAbs(cacheRoot) {
		return Namespace{}, errors.New("daemon cache root must be absolute")
	}
	if protocolVersion < 1 {
		return Namespace{}, errors.New("daemon protocol version must be positive")
	}
	if !safeBuildID.MatchString(buildID) {
		return Namespace{}, fmt.Errorf("invalid daemon build ID %q", buildID)
	}
	root := filepath.Join(cacheRoot, "gdtest", "daemons", fmt.Sprint(protocolVersion), buildID)
	return Namespace{
		Root:         root,
		Protocol:     protocolVersion,
		BuildID:      buildID,
		SocketPath:   filepath.Join(root, "daemon.sock"),
		MetadataPath: filepath.Join(root, "metadata.json"),
		LockPath:     filepath.Join(root, "daemon.lock"),
		LogPath:      filepath.Join(root, "daemon.jsonl"),
	}, nil
}

// Prepare creates the namespace with current-user-only permissions.
func (n Namespace) Prepare() error {
	if err := os.MkdirAll(n.Root, 0o700); err != nil {
		return fmt.Errorf("create daemon namespace: %w", err)
	}
	if err := os.Chmod(n.Root, 0o700); err != nil {
		return fmt.Errorf("secure daemon namespace: %w", err)
	}
	return nil
}

// Metadata is the permission-protected daemon rendezvous record.
type Metadata struct {
	Protocol   int       `json:"protocol"`
	BuildID    string    `json:"build_id"`
	PID        int       `json:"pid"`
	SocketPath string    `json:"socket_path"`
	StartedAt  time.Time `json:"started_at"`
}

// ReadMetadata validates the namespace's rendezvous record.
func (n Namespace) ReadMetadata() (Metadata, error) {
	data, err := os.ReadFile(n.MetadataPath)
	if err != nil {
		return Metadata{}, fmt.Errorf("read daemon metadata: %w", err)
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return Metadata{}, fmt.Errorf("parse daemon metadata: %w", err)
	}
	if metadata.Protocol != n.Protocol || metadata.BuildID != n.BuildID {
		return Metadata{}, errors.New("daemon metadata namespace mismatch")
	}
	if filepath.Clean(metadata.SocketPath) != filepath.Clean(n.SocketPath) {
		return Metadata{}, errors.New("daemon metadata socket mismatch")
	}
	return metadata, nil
}

func (n Namespace) writeMetadata(metadata Metadata) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode daemon metadata: %w", err)
	}
	temporary, err := os.CreateTemp(n.Root, ".metadata-*")
	if err != nil {
		return fmt.Errorf("create daemon metadata: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure daemon metadata: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write daemon metadata: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync daemon metadata: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close daemon metadata: %w", err)
	}
	if err := os.Rename(temporaryName, n.MetadataPath); err != nil {
		return fmt.Errorf("publish daemon metadata: %w", err)
	}
	return nil
}
