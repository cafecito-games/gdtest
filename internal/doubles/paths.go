package doubles

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cafecito-games/gdtest/internal/config"
)

// checkEntryPaths verifies that the source and output of entry stay inside the
// canonical project root, including through symbolic links, and that an
// existing output is a regular file.
func (index *Index) checkEntryPaths(entry config.TestDouble) []Diagnostic {
	var diagnostics []Diagnostic
	if message := index.containmentProblem(entry.Source); message != "" {
		diagnostics = append(diagnostics, Diagnostic{Path: entry.Source, Message: "source " + message})
	}
	if message := index.containmentProblem(entry.Output); message != "" {
		diagnostics = append(diagnostics, Diagnostic{Path: entry.Output, Message: "output " + message})
		return diagnostics
	}
	info, err := os.Lstat(filepath.Join(index.root, filepath.FromSlash(entry.Output)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		diagnostics = append(diagnostics, Diagnostic{Path: entry.Output, Message: "inspect output: " + err.Error()})
	case info.Mode()&fs.ModeSymlink != 0:
		diagnostics = append(diagnostics, Diagnostic{Path: entry.Output, Message: "refusing to write through a symbolic link"})
	case !info.Mode().IsRegular():
		diagnostics = append(diagnostics, Diagnostic{Path: entry.Output, Message: "output exists and is not a regular file"})
	}
	return diagnostics
}

// containmentProblem resolves the deepest existing ancestor of relative and
// reports why that location is not inside the project root, or "" when it is.
func (index *Index) containmentProblem(relative string) string {
	target := filepath.Join(index.root, filepath.FromSlash(relative))
	if !within(index.root, target) {
		return "escapes the project root"
	}
	current := target
	for {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Sprintf("cannot be inspected: %v", err)
		}
		if current == index.root {
			return "is missing its project root"
		}
		current = filepath.Dir(current)
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return fmt.Sprintf("cannot be resolved: %v", err)
	}
	if !within(index.root, resolved) {
		return "resolves outside the project root"
	}
	return ""
}
