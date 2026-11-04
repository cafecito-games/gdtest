// Package project identifies live Godot projects, Git worktrees, and daemon sessions.
package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Identity describes one canonical Godot project inside one worktree.
// Repository and worktree identity never depend on branch names or commits.
type Identity struct {
	ProjectRoot  string `json:"project_root"`
	Repository   string `json:"repository"`
	WorktreeRoot string `json:"worktree_root"`
	WorktreeGit  string `json:"worktree_git,omitempty"`
	Git          bool   `json:"git"`
}

// Resolve identifies projectRoot through Git when it belongs to a worktree.
// A directory outside Git receives a canonical path identity instead.
func Resolve(ctx context.Context, projectRoot string) (Identity, error) {
	root, err := canonicalDirectory(projectRoot)
	if err != nil {
		return Identity{}, fmt.Errorf("canonical project root: %w", err)
	}
	top, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return Identity{
				ProjectRoot: root, Repository: root, WorktreeRoot: root,
			}, nil
		}
		return Identity{}, fmt.Errorf("query Git worktree: %w", err)
	}
	worktreeRoot, err := canonicalDirectory(resolveGitPath(root, top))
	if err != nil {
		return Identity{}, fmt.Errorf("canonical Git worktree root: %w", err)
	}
	common, err := gitPath(ctx, root, "--git-common-dir")
	if err != nil {
		return Identity{}, fmt.Errorf("query Git common directory: %w", err)
	}
	gitDir, err := gitPath(ctx, root, "--git-dir")
	if err != nil {
		return Identity{}, fmt.Errorf("query Git worktree directory: %w", err)
	}
	return Identity{
		ProjectRoot:  root,
		Repository:   common,
		WorktreeRoot: worktreeRoot,
		WorktreeGit:  gitDir,
		Git:          true,
	}, nil
}

// SessionKey includes every identity input that can make a worker lease incompatible.
type SessionKey struct {
	Project        Identity `json:"project"`
	GodotIdentity  string   `json:"godot_identity"`
	ConfigIdentity string   `json:"config_identity"`
	BuildIdentity  string   `json:"build_identity"`
}

// NewSessionKey validates the live identity inputs.
func NewSessionKey(identity Identity, godotIdentity, configIdentity, buildIdentity string) (SessionKey, error) {
	if identity.ProjectRoot == "" || identity.Repository == "" || identity.WorktreeRoot == "" {
		return SessionKey{}, errors.New("project identity is incomplete")
	}
	if godotIdentity == "" {
		return SessionKey{}, errors.New("godot identity is required")
	}
	if configIdentity == "" {
		return SessionKey{}, errors.New("configuration identity is required")
	}
	if buildIdentity == "" {
		return SessionKey{}, errors.New("gdtest build identity is required")
	}
	return SessionKey{
		Project: identity, GodotIdentity: godotIdentity,
		ConfigIdentity: configIdentity, BuildIdentity: buildIdentity,
	}, nil
}

// ID returns a stable content digest suitable for logs and in-memory maps.
func (k SessionKey) ID() string {
	digest := sha256.New()
	for _, value := range []string{
		k.Project.Repository,
		k.Project.WorktreeRoot,
		k.Project.WorktreeGit,
		k.Project.ProjectRoot,
		k.GodotIdentity,
		k.ConfigIdentity,
		k.BuildIdentity,
	} {
		_, _ = digest.Write([]byte(strconv.Itoa(len(value))))
		_, _ = digest.Write([]byte{':'})
		_, _ = digest.Write([]byte(value))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// ContentIdentity hashes configuration or other semantic bytes.
func ContentIdentity(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// ExecutableIdentity identifies an executable by its canonical path and file metadata.
// The caller may append engine version/build information returned by the executable.
func ExecutableIdentity(path string) (string, error) {
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("locate executable %q: %w", path, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("absolute executable path: %w", err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("executable %s is not a regular file", resolved)
	}
	payload := strings.Join([]string{
		filepath.Clean(resolved), strconv.FormatInt(info.Size(), 10),
		strconv.FormatInt(info.ModTime().UnixNano(), 10),
	}, "\x00")
	return ContentIdentity([]byte(payload)), nil
}

// RelativePath returns a normalized, project-relative user-visible path.
func RelativePath(projectRoot, filename string) (string, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", fmt.Errorf("absolute project root: %w", err)
	}
	absolute := filename
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(root, filename)
	}
	absolute, err = filepath.Abs(absolute)
	if err != nil {
		return "", fmt.Errorf("absolute source path: %w", err)
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil {
		return "", fmt.Errorf("relative source path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("source path %s escapes project root %s", filename, projectRoot)
	}
	return filepath.ToSlash(filepath.Clean(relative)), nil
}

func gitPath(ctx context.Context, root, name string) (string, error) {
	value, err := gitOutput(ctx, root, "rev-parse", "--path-format=absolute", name)
	if err != nil {
		value, err = gitOutput(ctx, root, "rev-parse", name)
		if err != nil {
			return "", err
		}
	}
	value = resolveGitPath(root, value)
	return canonicalDirectory(value)
}

func resolveGitPath(root, value string) string {
	value = strings.TrimSpace(value)
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(root, filepath.FromSlash(value))
}

func gitOutput(ctx context.Context, root string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, arguments...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", errors.New("git returned an empty path")
	}
	return value, nil
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", resolved)
	}
	return filepath.Clean(resolved), nil
}
