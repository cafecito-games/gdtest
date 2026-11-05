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

	"github.com/cafecito-games/gdtest/internal/version"
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
		data, err = runtimeFileContents(name, data, version.Product)
		if err != nil {
			return err
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

const runtimeVersionPrefix = `const PRODUCT_VERSION: String = "`

func runtimeFileContents(name string, data []byte, product string) ([]byte, error) {
	if filepath.ToSlash(name) != "addons/gdtest/version.gd" {
		return data, nil
	}
	if !validProductVersion(product) {
		return nil, fmt.Errorf("invalid embedded gdtest product version %q", product)
	}
	content := string(data)
	if strings.Count(content, runtimeVersionPrefix) != 1 {
		return nil, errors.New("embedded addons/gdtest/version.gd must declare PRODUCT_VERSION exactly once")
	}
	valueStart := strings.Index(content, runtimeVersionPrefix) + len(runtimeVersionPrefix)
	valueEnd := strings.IndexByte(content[valueStart:], '"')
	if valueEnd < 0 {
		return nil, errors.New("embedded addons/gdtest/version.gd has an unterminated PRODUCT_VERSION")
	}
	valueEnd += valueStart
	return []byte(content[:valueStart] + product + content[valueEnd:]), nil
}

func validProductVersion(product string) bool {
	if product == "" {
		return false
	}
	for _, character := range product {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune(".+-", character) {
			continue
		}
		return false
	}
	return true
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
