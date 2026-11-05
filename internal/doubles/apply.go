package doubles

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cafecito-games/gdtest/internal/config"
)

// uidSuffix is the extension of the sidecar that holds a script's uid.
const uidSuffix = ".uid"

// tempPattern names the same-directory staging files that Write creates.
const tempPattern = ".gdtest-double-*"

// Plan is the difference between the desired generated doubles and the project
// on disk. A Plan is only meaningful when Prepare returned no diagnostics.
type Plan struct {
	// Files maps each desired project-relative path, both .gd and .gd.uid, to its
	// exact content.
	Files map[string][]byte
	// Changed lists the desired paths whose files are missing or differ, sorted.
	// Changed is the set Write touches.
	Changed []string
	// Missing lists the desired paths that do not exist, sorted.
	Missing []string
	// Stale lists the desired paths that exist with different content, sorted.
	Stale []string
	// Unexpected lists owned generated scripts and their existing .gd.uid
	// sidecars that no configured entry desires, sorted.
	Unexpected []string
}

// Findings returns one "missing: path", "stale: path", or "unexpected: path"
// line for each difference, sorted by path and then kind.
func (plan Plan) Findings() []string {
	type finding struct {
		kind string
		path string
	}
	var findings []finding
	for _, path := range plan.Missing {
		findings = append(findings, finding{kind: "missing", path: path})
	}
	for _, path := range plan.Stale {
		findings = append(findings, finding{kind: "stale", path: path})
	}
	for _, path := range plan.Unexpected {
		findings = append(findings, finding{kind: "unexpected", path: path})
	}
	sort.Slice(findings, func(left, right int) bool {
		if findings[left].path != findings[right].path {
			return findings[left].path < findings[right].path
		}
		return findings[left].kind < findings[right].kind
	})
	lines := make([]string, 0, len(findings))
	for _, current := range findings {
		lines = append(lines, current.kind+": "+current.path)
	}
	return lines
}

// Prepare computes the plan for the project at root. It indexes every script,
// analyzes and renders each configured double, and runs every ownership and
// path-safety check. Any returned diagnostic means the plan must not be written,
// and the returned plan is then empty.
func Prepare(root string, project config.Project, productVersion string) (Plan, []Diagnostic) {
	index, diagnostics := BuildIndex(root, project.Exclude)
	if index == nil {
		return Plan{}, diagnostics
	}
	files := make(map[string][]byte, 2*len(project.TestDoubles))
	for _, entry := range project.TestDoubles {
		diagnostics = append(diagnostics, index.checkOwnership(entry)...)
		model, entryDiagnostics := Analyze(index, entry)
		diagnostics = append(diagnostics, entryDiagnostics...)
		if len(entryDiagnostics) > 0 {
			continue
		}
		rendered, err := Render(model, productVersion)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Path: entry.Source, Message: err.Error()})
			continue
		}
		files[entry.Output] = rendered
		files[entry.Output+uidSuffix] = []byte(UIDFor(model.OutputResource))
	}

	unexpected, unexpectedDiagnostics := index.unexpectedOwned(files)
	diagnostics = append(diagnostics, unexpectedDiagnostics...)
	SortDiagnostics(diagnostics)
	if len(diagnostics) > 0 {
		return Plan{}, diagnostics
	}

	plan := Plan{Files: files, Unexpected: unexpected}
	for _, relative := range sortedKeys(files) {
		existing, exists, err := inspectOutput(filepath.Join(index.root, filepath.FromSlash(relative)))
		if err != nil {
			return Plan{}, []Diagnostic{{Path: relative, Message: err.Error()}}
		}
		switch {
		case !exists:
			plan.Missing = append(plan.Missing, relative)
			plan.Changed = append(plan.Changed, relative)
		case !bytes.Equal(existing, files[relative]):
			plan.Stale = append(plan.Stale, relative)
			plan.Changed = append(plan.Changed, relative)
		}
	}
	return plan, nil
}

// checkOwnership refuses to overwrite files at the configured output that the
// generator does not own. Ownership is the exact first-line marker, so an
// existing .gd.uid without its .gd cannot be proven owned either. Link and
// file-type problems at the output are left to Analyze, which reports them.
func (index *Index) checkOwnership(entry config.TestDouble) []Diagnostic {
	if containmentProblem(index.root, entry.Output) != "" {
		return nil
	}
	outputPath := filepath.Join(index.root, filepath.FromSlash(entry.Output))
	output, outputExists, outputErr := inspectOutput(outputPath)
	if outputErr != nil {
		return nil
	}
	_, uidExists, uidErr := inspectOutput(outputPath + uidSuffix)
	if uidErr != nil {
		return []Diagnostic{{Path: entry.Output + uidSuffix, Message: uidErr.Error()}}
	}
	switch {
	case outputExists && generatedSourceOf(output) == "":
		return []Diagnostic{refusal(entry.Output)}
	case uidExists && !outputExists:
		return []Diagnostic{refusal(entry.Output + uidSuffix)}
	}
	return nil
}

// inspectOutput reports whether a regular file exists at path and returns its
// content. A symbolic link or other non-regular file is an error, because
// outputs are never followed or replaced through links.
func inspectOutput(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("inspect output: %w", err)
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, false, errors.New("refusing to write through a symbolic link")
	case !info.Mode().IsRegular():
		return nil, false, errors.New("output exists and is not a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("read output: %w", err)
	}
	return content, true, nil
}

// aliasesDesired reports whether owned names the same file as a desired path
// that differs from it only in case, as happens on case-insensitive
// filesystems. Removing such a file would delete the freshly written output.
func (index *Index) aliasesDesired(owned string, desired map[string][]byte) bool {
	ownedInfo, err := os.Lstat(filepath.Join(index.root, filepath.FromSlash(owned)))
	if err != nil {
		return false
	}
	for relative := range desired {
		if relative == owned || !strings.EqualFold(relative, owned) {
			continue
		}
		desiredInfo, err := os.Lstat(filepath.Join(index.root, filepath.FromSlash(relative)))
		if err == nil && os.SameFile(ownedInfo, desiredInfo) {
			return true
		}
	}
	return false
}

func refusal(relative string) Diagnostic {
	return Diagnostic{Path: relative, Message: "refusing to overwrite " + relative + ": missing gdtest generated-double marker"}
}

// unexpectedOwned returns the owned generated scripts that desired does not
// name, together with each one's existing .gd.uid sidecar.
func (index *Index) unexpectedOwned(desired map[string][]byte) ([]string, []Diagnostic) {
	var unexpected []string
	var diagnostics []Diagnostic
	for _, owned := range index.GeneratedScripts() {
		if _, wanted := desired[owned.Path]; wanted {
			continue
		}
		if index.aliasesDesired(owned.Path, desired) {
			continue
		}
		for _, relative := range []string{owned.Path, owned.Path + uidSuffix} {
			info, err := os.Lstat(filepath.Join(index.root, filepath.FromSlash(relative)))
			switch {
			case errors.Is(err, fs.ErrNotExist):
				continue
			case err != nil:
				diagnostics = append(diagnostics, Diagnostic{Path: relative, Message: "inspect owned file: " + err.Error()})
			case info.Mode()&fs.ModeSymlink != 0:
				diagnostics = append(diagnostics, Diagnostic{Path: relative, Message: "refusing to remove a symbolic link"})
			case !info.Mode().IsRegular():
				diagnostics = append(diagnostics, Diagnostic{Path: relative, Message: "owned file is not a regular file"})
			default:
				unexpected = append(unexpected, relative)
			}
		}
	}
	sort.Strings(unexpected)
	return unexpected, diagnostics
}

func sortedKeys(files map[string][]byte) []string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// fileSystem is the set of filesystem operations that Write performs. Tests
// substitute an implementation to force failures at a chosen operation.
type fileSystem interface {
	makeDirectories(directory string) error
	stage(directory string, content []byte) (string, error)
	rename(from string, to string) error
	remove(path string) error
}

// osFileSystem performs Write's operations on the real filesystem.
type osFileSystem struct{}

func (osFileSystem) makeDirectories(directory string) error {
	return os.MkdirAll(directory, 0o755)
}

// stage writes content to a new same-directory temporary file with mode 0644
// and returns its path. A failed stage removes its own temporary file.
func (osFileSystem) stage(directory string, content []byte) (string, error) {
	file, err := os.CreateTemp(directory, tempPattern)
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := stageContent(file, content); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func stageContent(file *os.File, content []byte) error {
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Chmod(0o644); err != nil {
		return err
	}
	return file.Sync()
}

func (osFileSystem) rename(from string, to string) error {
	return os.Rename(from, to)
}

func (osFileSystem) remove(path string) error {
	return os.Remove(path)
}

// Write applies plan to the project at root. Before any target changes, Write
// proves every precondition it can: each path is contained, each changed file is
// staged as a temporary beside its target, and each owned file to remove still
// exists as a regular file in a directory where a temporary can be created and
// removed. If any step fails, every temporary and probe is removed and no target
// changes. Otherwise each changed .gd and then its .gd.uid is renamed into place,
// and unexpected sidecars are removed before the scripts they belong to. An I/O
// failure during the rename or remove phase can leave a partial state, and the
// next run converges from it. It returns the number of files written and removed.
func Write(root string, plan Plan) (int, int, error) {
	return write(root, plan, osFileSystem{})
}

func write(root string, plan Plan, files fileSystem) (int, int, error) {
	canonical, err := canonicalRoot(root)
	if err != nil {
		return 0, 0, err
	}
	for _, relative := range plan.Changed {
		if message := containmentProblem(canonical, relative); message != "" {
			return 0, 0, fmt.Errorf("refusing to write %s: path %s", relative, message)
		}
		if _, exists := plan.Files[relative]; !exists {
			return 0, 0, fmt.Errorf("plan has no content for %s", relative)
		}
	}

	staged := make([]string, 0, len(plan.Changed))
	for _, relative := range plan.Changed {
		directory := filepath.Dir(filepath.Join(canonical, filepath.FromSlash(relative)))
		temporary, err := stageFile(files, directory, plan.Files[relative])
		if err != nil {
			removeAll(files, staged)
			return 0, 0, fmt.Errorf("stage %s: %w", relative, err)
		}
		staged = append(staged, temporary)
	}

	if err := proveRemovals(canonical, plan.Unexpected, files); err != nil {
		removeAll(files, staged)
		return 0, 0, err
	}

	written := 0
	for index, relative := range plan.Changed {
		target := filepath.Join(canonical, filepath.FromSlash(relative))
		if err := files.rename(staged[index], target); err != nil {
			removeAll(files, staged[index:])
			return written, 0, fmt.Errorf("replace %s: %w", relative, err)
		}
		written++
	}

	removed := 0
	for _, relative := range removalOrder(plan.Unexpected) {
		if err := files.remove(filepath.Join(canonical, filepath.FromSlash(relative))); err != nil {
			return written, removed, fmt.Errorf("remove %s: %w", relative, err)
		}
		removed++
	}
	return written, removed, nil
}

// stageFile creates the parent directories of a target and stages its content.
func stageFile(files fileSystem, directory string, content []byte) (string, error) {
	if err := files.makeDirectories(directory); err != nil {
		return "", err
	}
	return files.stage(directory, content)
}

// removeAll deletes staged temporaries on a best-effort basis. Their names are
// known, so a failure here leaves at most the temporaries that the caller
// already reports.
func removeAll(files fileSystem, temporaries []string) {
	for _, temporary := range temporaries {
		_ = files.remove(temporary)
	}
}

// proveRemovals confirms that every unexpected file is still a regular file and
// that each directory holding one accepts a temporary which can then be removed.
// The probe goes through the staging seam, so a directory that cannot be written
// or cleaned is refused before the first rename. A probe that is created and then
// fails to remove is reported with the directory it lives in.
func proveRemovals(canonical string, unexpected []string, files fileSystem) error {
	probed := make(map[string]bool)
	for _, relative := range unexpected {
		target := filepath.Join(canonical, filepath.FromSlash(relative))
		info, err := os.Lstat(target)
		if err != nil {
			return fmt.Errorf("prepare removal of %s: %w", relative, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("prepare removal of %s: not a regular file", relative)
		}
		directory := filepath.Dir(target)
		if probed[directory] {
			continue
		}
		probed[directory] = true
		probe, err := files.stage(directory, nil)
		if err != nil {
			return fmt.Errorf("prepare removal in %s: %w", path.Dir(relative), err)
		}
		if err := files.remove(probe); err != nil {
			return fmt.Errorf("prepare removal in %s: %w", path.Dir(relative), err)
		}
	}
	return nil
}

// removalOrder places every .gd.uid before the script that owns it.
func removalOrder(unexpected []string) []string {
	ordered := make([]string, 0, len(unexpected))
	for _, relative := range unexpected {
		if filepath.Ext(relative) == uidSuffix {
			ordered = append(ordered, relative)
		}
	}
	for _, relative := range unexpected {
		if filepath.Ext(relative) != uidSuffix {
			ordered = append(ordered, relative)
		}
	}
	return ordered
}

// canonicalRoot resolves root to its absolute, symlink-free form.
func canonicalRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve project root: %w", err)
	}
	return canonical, nil
}
