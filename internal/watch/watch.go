// Package watch reconciles relevant project files into source generations.
//
// Reconciliation is the source of truth: callers may invoke it after a native
// notification, periodically, or after notification overflow. This keeps the
// correctness boundary independent of lossy platform watcher queues.
package watch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/cafecito-games/gdtest/internal/generation"
)

// Trigger records why a reconciliation was requested.
type Trigger string

const (
	Initial    Trigger = "initial"
	Filesystem Trigger = "filesystem"
	Periodic   Trigger = "periodic"
	Overflow   Trigger = "overflow"
)

var (
	// ErrProjectMissing means the worktree vanished. Its last generation stays
	// intact so in-flight work can still be labelled accurately.
	ErrProjectMissing = errors.New("project root disappeared")
	// ErrUnstableScan means files repeatedly disappeared between enumeration and
	// reading. A subsequent reconciliation is required.
	ErrUnstableScan = errors.New("project changed during reconciliation")
)

// Result is the proof produced by a complete reconciliation scan.
type Result struct {
	Trigger    Trigger           `json:"trigger"`
	FullRescan bool              `json:"full_rescan"`
	Paths      []string          `json:"paths"`
	Update     generation.Update `json:"update"`
}

// Reconciler owns the live generation state of exactly one project root.
type Reconciler struct {
	root     string
	tracker  generation.Tracker
	discover func(context.Context, string) ([]string, error)
	mu       sync.Mutex
}

// New creates a reconciler. It performs no I/O before Reconcile.
func New(root string) *Reconciler {
	return &Reconciler{root: root, discover: RelevantPaths}
}

// Reconcile performs a full authoritative scan for every trigger. Full scans
// make overflow recovery explicit and also heal lost/coalesced notifications.
func (r *Reconciler) Reconcile(ctx context.Context, trigger Trigger) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if trigger == "" {
		trigger = Periodic
	}
	for attempt := 0; attempt < 3; attempt++ {
		paths, err := r.discover(ctx, r.root)
		if err != nil {
			if errors.Is(err, ErrProjectMissing) {
				return Result{}, ErrProjectMissing
			}
			if errors.Is(err, os.ErrNotExist) {
				if _, statErr := os.Stat(r.root); errors.Is(statErr, os.ErrNotExist) {
					return Result{}, ErrProjectMissing
				}
				continue
			}
			return Result{}, err
		}
		snapshot, err := generation.Build(ctx, r.root, paths)
		if err == nil {
			return Result{
				Trigger: trigger, FullRescan: true,
				Paths: append([]string(nil), paths...), Update: r.tracker.Apply(snapshot),
			}, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			if _, statErr := os.Stat(r.root); errors.Is(statErr, os.ErrNotExist) {
				return Result{}, ErrProjectMissing
			}
			continue
		}
		return Result{}, err
	}
	return Result{}, ErrUnstableScan
}

// Generation returns the latest successfully reconciled generation.
func (r *Reconciler) Generation() uint64 { return r.tracker.Generation() }

// Stamp labels a result requested against an earlier project generation.
func (r *Reconciler) Stamp(requested uint64) generation.Stamp {
	return r.tracker.Stamp(requested)
}

// RelevantPaths returns a deterministic list of source/config/resource files.
func RelevantPaths(ctx context.Context, root string) ([]string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("absolute project root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrProjectMissing
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project root is not a directory")
	}

	paths := make([]string, 0)
	err = filepath.WalkDir(absolute, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if name != absolute && ignoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() && entry.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		relative, err := filepath.Rel(absolute, name)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relevant(relative) {
			paths = append(paths, relative)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, statErr := os.Stat(absolute); errors.Is(statErr, os.ErrNotExist) {
				return nil, ErrProjectMissing
			}
		}
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func relevant(name string) bool {
	normalized := strings.ToLower(filepath.ToSlash(name))
	if strings.HasPrefix(normalized, "addons/gdtest/") {
		return true
	}
	base := strings.ToLower(filepath.Base(name))
	if base == "project.godot" || base == "gdtest.json" || base == "addons.toml" || base == "addons.lock" {
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".gd", ".gdextension", ".gdshader", ".shader", ".tscn", ".tres", ".res",
		".cfg", ".json", ".uid", ".cs", ".csproj", ".sln", ".so", ".dylib", ".dll":
		return true
	default:
		return false
	}
}

func ignoredDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".godot", ".gdtest", ".gpm", "node_modules":
		return true
	default:
		return false
	}
}
