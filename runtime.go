// Package gdtest exposes the embedded standalone runtime installer.
package gdtest

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// runtimeFiles is the same committed addon tree used by packages.
//
//go:embed addons/gdtest/*
var runtimeFiles embed.FS

// InstallRuntime safely installs the embedded addon into a non-gpm project.
// Existing identical files make the operation idempotent; conflicts are never overwritten.
func InstallRuntime(project string) error {
	owned, err := gpmOwnsRuntime(project)
	if err != nil {
		return err
	}
	if owned {
		return errors.New("addons/gdtest is managed by gpm; run gpm install instead")
	}
	return fs.WalkDir(runtimeFiles, "addons/gdtest", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		target := filepath.Join(project, filepath.FromSlash(name))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := runtimeFiles.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", name, err)
		}
		current, err := os.ReadFile(target)
		if err == nil {
			if bytes.Equal(current, data) {
				return nil
			}
			return fmt.Errorf("refusing to overwrite modified %s", target)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect %s: %w", target, err)
		}
		mode := fs.FileMode(0o644)
		if filepath.Base(target) == "gdtest" {
			mode = 0o755
		}
		return atomicWrite(target, data, mode)
	})
}

func gpmOwnsRuntime(project string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(project, "addons.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read addons.toml: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "[addons.gdtest]" {
			return true, nil
		}
	}
	return false, nil
}

func atomicWrite(target string, data []byte, mode fs.FileMode) error {
	directory := filepath.Dir(target)
	file, err := os.CreateTemp(directory, ".gdtest-install-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", target, err)
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return fmt.Errorf("set mode on %s: %w", temporary, err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write %s: %w", temporary, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync %s: %w", temporary, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", temporary, err)
	}
	if err := os.Rename(temporary, target); err != nil {
		return fmt.Errorf("publish %s: %w", target, err)
	}
	return nil
}
