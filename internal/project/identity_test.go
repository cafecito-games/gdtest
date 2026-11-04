package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveLinkedDirtyWorktrees(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	worktreeA := filepath.Join(root, "worktree-a")
	worktreeB := filepath.Join(root, "worktree-b")
	mustRun(t, root, "git", "init", "--initial-branch=main", repository)
	mustRun(t, repository, "git", "config", "user.name", "gdtest")
	mustRun(t, repository, "git", "config", "user.email", "gdtest@example.invalid")
	mustWrite(t, filepath.Join(repository, "project.godot"), "config_version=5\n")
	mustWrite(t, filepath.Join(repository, "value.gd"), "const VALUE: int = 0\n")
	mustRun(t, repository, "git", "add", ".")
	mustRun(t, repository, "git", "commit", "-m", "fixture")
	mustRun(t, repository, "git", "worktree", "add", "--detach", worktreeA, "HEAD")
	mustRun(t, repository, "git", "worktree", "add", "--detach", worktreeB, "HEAD")
	mustWrite(t, filepath.Join(worktreeA, "value.gd"), "const VALUE: int = 1\n")
	mustWrite(t, filepath.Join(worktreeB, "value.gd"), "const VALUE: int = 2\n")

	identityA, err := Resolve(context.Background(), worktreeA)
	require.NoError(t, err)
	identityB, err := Resolve(context.Background(), worktreeB)
	require.NoError(t, err)
	assert.True(t, identityA.Git)
	assert.True(t, identityB.Git)
	assert.Equal(t, identityA.Repository, identityB.Repository)
	assert.NotEqual(t, identityA.WorktreeRoot, identityB.WorktreeRoot)
	assert.NotEqual(t, identityA.WorktreeGit, identityB.WorktreeGit)
	keyA := mustSessionKey(t, identityA, "build-a")
	keyB := mustSessionKey(t, identityB, "build-a")
	assert.NotEqual(t, keyA.ID(), keyB.ID())

	before := keyA.ID()
	mustWrite(t, filepath.Join(worktreeA, "untracked.gd"), "const DIRTY: bool = true\n")
	afterIdentity, err := Resolve(context.Background(), worktreeA)
	require.NoError(t, err)
	assert.Equal(t, before, mustSessionKey(t, afterIdentity, "build-a").ID())

	buildB := mustSessionKey(t, identityA, "build-b")
	assert.NotEqual(t, keyA.ID(), buildB.ID())
	pathA, err := RelativePath(worktreeA, filepath.Join(worktreeA, "tests", "same.test.gd"))
	require.NoError(t, err)
	pathB, err := RelativePath(worktreeB, filepath.Join(worktreeB, "tests", "same.test.gd"))
	require.NoError(t, err)
	assert.Equal(t, "tests/same.test.gd", pathA)
	assert.Equal(t, pathA, pathB)
}

func TestResolveNonGitProject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity, err := Resolve(context.Background(), root)
	require.NoError(t, err)
	assert.False(t, identity.Git)
	assert.Equal(t, identity.ProjectRoot, identity.Repository)
	assert.Equal(t, identity.ProjectRoot, identity.WorktreeRoot)
	first := mustSessionKey(t, identity, "build").ID()
	second := mustSessionKey(t, identity, "build").ID()
	assert.NotEmpty(t, first)
	assert.Equal(t, first, second)
}

func TestRelativePathRejectsEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, err := RelativePath(root, filepath.Join(root, "..", "outside.gd"))
	require.Error(t, err)
}

func TestContentIdentity(t *testing.T) {
	t.Parallel()

	assert.NotEqual(t, ContentIdentity([]byte("a")), ContentIdentity([]byte("b")))
	first := ContentIdentity([]byte("a"))
	second := ContentIdentity(append([]byte(nil), 'a'))
	assert.Equal(t, first, second)
}

func mustSessionKey(t *testing.T, identity Identity, build string) SessionKey {
	t.Helper()
	key, err := NewSessionKey(identity, "godot-4.7.2", "default-config-v1", build)
	require.NoError(t, err)
	return key
}

func mustRun(t *testing.T, directory, name string, arguments ...string) {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s %v\n%s", name, arguments, output)
}

func mustWrite(t *testing.T, filename, contents string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	require.NoError(t, os.WriteFile(filename, []byte(contents), 0o644))
}
