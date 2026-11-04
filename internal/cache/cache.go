// Package cache provides corruption-resistant content-addressed storage.
package cache

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	formatVersion       = 1
	defaultMaxEntrySize = 64 << 20
)

var (
	ErrInvalidKey    = errors.New("invalid cache key")
	ErrEntryTooLarge = errors.New("cache entry exceeds size limit")
)

// Key derives a reusable cache identity from every semantic input. Worktree
// paths deliberately do not participate.
func Key(toolVersion, parserVersion string, configuration, content []byte) string {
	hash := sha256.New()
	for _, part := range [][]byte{
		[]byte("gdtest-cache-v1"), []byte(toolVersion), []byte(parserVersion),
		configuration, content,
	} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// Store is safe for concurrent goroutines and processes. Each Store coalesces
// same-key computation in process; atomic rename provides process isolation.
type Store struct {
	root     string
	maxBytes int64

	mu      sync.Mutex
	flights map[string]*flight
}

type flight struct {
	done chan struct{}
	data []byte
	err  error
}

type envelope struct {
	Format   int    `json:"format"`
	Key      string `json:"key"`
	Checksum string `json:"checksum"`
	Payload  []byte `json:"payload"`
}

// Open constructs a store rooted at an explicit absolute or relative path.
// Production callers should use Default or pass another user cache location,
// never a project worktree.
func Open(root string, maxEntryBytes int64) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("cache root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("absolute cache root: %w", err)
	}
	if maxEntryBytes <= 0 {
		maxEntryBytes = defaultMaxEntrySize
	}
	if maxEntryBytes > (1<<62)-4096 {
		return nil, errors.New("cache entry size limit is too large")
	}
	return &Store{
		root: filepath.Clean(absolute), maxBytes: maxEntryBytes,
		flights: make(map[string]*flight),
	}, nil
}

// Default opens gdtest's version-scoped user cache outside any worktree.
func Default(toolVersion string) (*Store, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("locate user cache: %w", err)
	}
	return Open(filepath.Join(root, "gdtest", safeVersion(toolVersion)), defaultMaxEntrySize)
}

// Root returns the absolute storage directory.
func (s *Store) Root() string { return s.root }

// Get returns a verified copy. Malformed, truncated, oversized, mismatched, or
// checksum-invalid entries are rejected and removed as cache misses.
func (s *Store) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	name := s.entryPath(key)
	file, err := os.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open cache entry: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, false, fmt.Errorf("inspect cache entry: %w", err)
	}
	// Base64 plus envelope fields stay below twice the payload bound.
	if info.Size() < 0 || info.Size() > s.maxBytes*2+4096 {
		_ = file.Close()
		return s.reject(name, info)
	}
	data, err := io.ReadAll(io.LimitReader(file, s.maxBytes*2+4097))
	if err != nil {
		_ = file.Close()
		return nil, false, fmt.Errorf("read cache entry: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, false, fmt.Errorf("close cache entry: %w", err)
	}
	var stored envelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&stored); err != nil {
		return s.reject(name, info)
	}
	if err := ensureEOF(decoder); err != nil {
		return s.reject(name, info)
	}
	if stored.Format != formatVersion || stored.Key != key || int64(len(stored.Payload)) > s.maxBytes ||
		stored.Checksum != checksum(stored.Payload) {
		return s.reject(name, info)
	}
	return append([]byte(nil), stored.Payload...), true, nil
}

// Put atomically publishes data. Readers see either the previous complete
// entry, the new complete entry, or a miss—never a partially written file.
func (s *Store) Put(ctx context.Context, key string, data []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if int64(len(data)) > s.maxBytes {
		return ErrEntryTooLarge
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stored := envelope{
		Format: formatVersion, Key: key, Checksum: checksum(data),
		Payload: append([]byte(nil), data...),
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return fmt.Errorf("encode cache entry: %w", err)
	}
	directory := filepath.Dir(s.entryPath(key))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".publish-*")
	if err != nil {
		return fmt.Errorf("create cache temporary: %w", err)
	}
	temporaryName := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect cache temporary: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write cache temporary: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync cache temporary: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close cache temporary: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	target := s.entryPath(key)
	if err := os.Rename(temporaryName, target); err != nil {
		// Windows cannot atomically replace an existing file. An existing valid
		// immutable entry already satisfies publication; Get also removes a
		// corrupt incumbent before the retry.
		if _, hit, getErr := s.Get(ctx, key); getErr != nil {
			return fmt.Errorf("inspect publication conflict: %w", getErr)
		} else if hit {
			return nil
		}
		if retryErr := os.Rename(temporaryName, target); retryErr != nil {
			if _, hit, getErr := s.Get(ctx, key); getErr == nil && hit {
				return nil
			}
			return fmt.Errorf("publish cache entry: %w", retryErr)
		}
	}
	removeTemporary = false
	return nil
}

// Delete removes one application-level cache entry. It is used when an
// envelope is valid but the owning package rejects the decoded payload schema.
// Missing entries are already the desired state and do not fail.
func (s *Store) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(s.entryPath(key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete cache entry: %w", err)
	}
	return nil
}

// GetOrCompute returns a valid entry or computes and atomically publishes it.
// Concurrent same-key callers in this process share one computation.
func (s *Store) GetOrCompute(
	ctx context.Context,
	key string,
	compute func(context.Context) ([]byte, error),
) ([]byte, error) {
	data, hit, err := s.Get(ctx, key)
	if err != nil || hit {
		return data, err
	}
	if compute == nil {
		return nil, errors.New("cache compute function is nil")
	}

	s.mu.Lock()
	if existing, ok := s.flights[key]; ok {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-existing.done:
			return append([]byte(nil), existing.data...), existing.err
		}
	}
	current := &flight{done: make(chan struct{})}
	current.err = errors.New("cache computation aborted")
	s.flights[key] = current
	s.mu.Unlock()

	func() {
		defer func() {
			s.mu.Lock()
			delete(s.flights, key)
			close(current.done)
			s.mu.Unlock()
		}()
		current.data, current.err = s.compute(ctx, key, compute)
	}()
	return append([]byte(nil), current.data...), current.err
}

func (s *Store) compute(
	ctx context.Context,
	key string,
	compute func(context.Context) ([]byte, error),
) ([]byte, error) {
	// A different Store or process may have published between the first lookup
	// and leadership acquisition.
	if data, hit, err := s.Get(ctx, key); err != nil || hit {
		return data, err
	}
	data, err := compute(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Put(ctx, key, data); err != nil {
		return nil, err
	}
	return append([]byte(nil), data...), nil
}

func (s *Store) reject(name string, opened os.FileInfo) ([]byte, bool, error) {
	current, err := os.Stat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect corrupt cache entry: %w", err)
	}
	// A concurrent publisher may have replaced the file after it was read. Do
	// not remove that new entry; report a miss and let the caller retry/compute.
	if !os.SameFile(opened, current) {
		return nil, false, nil
	}
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("remove corrupt cache entry: %w", err)
	}
	return nil, false, nil
}

func (s *Store) entryPath(key string) string {
	return filepath.Join(s.root, key[:2], key+".json")
}

func validateKey(key string) error {
	if len(key) != sha256.Size*2 || strings.ToLower(key) != key {
		return ErrInvalidKey
	}
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != sha256.Size {
		return ErrInvalidKey
	}
	return nil
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("cache entry has trailing JSON")
		}
		return err
	}
	return nil
}

func safeVersion(version string) string {
	if version == "" {
		return "dev"
	}
	for _, character := range version {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._-", character) {
			continue
		}
		return "version-" + checksum([]byte(version))[:16]
	}
	if version == "." || version == ".." {
		return "version-" + checksum([]byte(version))[:16]
	}
	return version
}
