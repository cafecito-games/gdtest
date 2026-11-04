package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
	if err != nil {
		t.Fatalf("Resolve worktree A: %v", err)
	}
	identityB, err := Resolve(context.Background(), worktreeB)
	if err != nil {
		t.Fatalf("Resolve worktree B: %v", err)
	}
	if !identityA.Git || !identityB.Git {
		t.Fatalf("linked worktrees were not identified as Git: %#v %#v", identityA, identityB)
	}
	if identityA.Repository != identityB.Repository {
		t.Fatalf("common directories differ: %q != %q", identityA.Repository, identityB.Repository)
	}
	if identityA.WorktreeRoot == identityB.WorktreeRoot || identityA.WorktreeGit == identityB.WorktreeGit {
		t.Fatalf("worktree identity leaked: %#v %#v", identityA, identityB)
	}
	keyA := mustSessionKey(t, identityA, "build-a")
	keyB := mustSessionKey(t, identityB, "build-a")
	if keyA.ID() == keyB.ID() {
		t.Fatal("conflicting worktrees received the same session ID")
	}

	before := keyA.ID()
	mustWrite(t, filepath.Join(worktreeA, "untracked.gd"), "const DIRTY: bool = true\n")
	afterIdentity, err := Resolve(context.Background(), worktreeA)
	if err != nil {
		t.Fatalf("Resolve dirty worktree: %v", err)
	}
	if after := mustSessionKey(t, afterIdentity, "build-a").ID(); after != before {
		t.Fatalf("dirty content changed live identity: %s != %s", before, after)
	}

	buildB := mustSessionKey(t, identityA, "build-b")
	if buildB.ID() == keyA.ID() {
		t.Fatal("development build identities shared a session ID")
	}
	pathA, err := RelativePath(worktreeA, filepath.Join(worktreeA, "tests", "same.test.gd"))
	if err != nil {
		t.Fatalf("RelativePath A: %v", err)
	}
	pathB, err := RelativePath(worktreeB, filepath.Join(worktreeB, "tests", "same.test.gd"))
	if err != nil {
		t.Fatalf("RelativePath B: %v", err)
	}
	if pathA != "tests/same.test.gd" || pathA != pathB {
		t.Fatalf("relative identities differ: %q %q", pathA, pathB)
	}
}

func TestResolveNonGitProject(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity, err := Resolve(context.Background(), root)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if identity.Git || identity.ProjectRoot != identity.Repository || identity.ProjectRoot != identity.WorktreeRoot {
		t.Fatalf("unexpected non-Git identity: %#v", identity)
	}
	first := mustSessionKey(t, identity, "build").ID()
	second := mustSessionKey(t, identity, "build").ID()
	if first == "" || first != second {
		t.Fatalf("session ID is not stable: %q %q", first, second)
	}
}

func TestRelativePathRejectsEscape(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := RelativePath(root, filepath.Join(root, "..", "outside.gd")); err == nil {
		t.Fatal("RelativePath accepted an escaping path")
	}
}

func TestContentIdentity(t *testing.T) {
	t.Parallel()

	if ContentIdentity([]byte("a")) == ContentIdentity([]byte("b")) {
		t.Fatal("different content shared an identity")
	}
	first := ContentIdentity([]byte("a"))
	second := ContentIdentity(append([]byte(nil), 'a'))
	if first != second {
		t.Fatal("equal content did not share an identity")
	}
}

func mustSessionKey(t *testing.T, identity Identity, build string) SessionKey {
	t.Helper()
	key, err := NewSessionKey(identity, "godot-4.7.2", "default-config-v1", build)
	if err != nil {
		t.Fatalf("NewSessionKey: %v", err)
	}
	return key
}

func mustRun(t *testing.T, directory, name string, arguments ...string) {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, arguments, err, output)
	}
}

func mustWrite(t *testing.T, filename, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filename, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", filename, err)
	}
}
