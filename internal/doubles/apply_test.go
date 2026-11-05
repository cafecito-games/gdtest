package doubles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/gdtest/internal/config"
)

const applySource = "## Holds items.\nclass_name Inventory\nextends RefCounted\n\nfunc count() -> int:\n\treturn 0\n"

func applyProject(entries ...config.TestDouble) config.Project {
	project := config.Defaults()
	project.TestDoubles = entries
	return project
}

func inventoryEntry() config.TestDouble {
	return config.TestDouble{Source: "src/inventory.gd", Output: "doubles/inventory_double.gd", ClassName: "InventoryDouble"}
}

// failingStage forwards every staging call to the real filesystem except the
// failAt-th one, which fails.
type failingStage struct {
	osFileSystem
	failAt int
	calls  int
}

func (failing *failingStage) stage(directory string, content []byte) (string, error) {
	failing.calls++
	if failing.calls == failing.failAt {
		return "", errors.New("injected staging failure")
	}
	return failing.osFileSystem.stage(directory, content)
}

func TestPrepareThenWriteThenCheckIsClean(t *testing.T) {
	t.Parallel()

	root := writeProject(t, map[string]string{"src/inventory.gd": applySource})
	plan, diagnostics := Prepare(root, applyProject(inventoryEntry()), "0.0.0-test")
	require.Empty(t, diagnostics)
	assert.Equal(t, []string{"doubles/inventory_double.gd", "doubles/inventory_double.gd.uid"}, plan.Missing)
	assert.Equal(t, plan.Missing, plan.Changed)
	assert.Empty(t, plan.Stale)
	assert.Empty(t, plan.Unexpected)

	written, removed, err := Write(root, plan)
	require.NoError(t, err)
	assert.Equal(t, 2, written)
	assert.Equal(t, 0, removed)

	again, diagnostics := Prepare(root, applyProject(inventoryEntry()), "0.0.0-test")
	require.Empty(t, diagnostics)
	assert.Empty(t, again.Findings())
}

func TestWriteTwiceLeavesModificationTimesUnchanged(t *testing.T) {
	t.Parallel()

	root := writeProject(t, map[string]string{"src/inventory.gd": applySource})
	plan, diagnostics := Prepare(root, applyProject(inventoryEntry()), "0.0.0-test")
	require.Empty(t, diagnostics)
	_, _, err := Write(root, plan)
	require.NoError(t, err)
	before := modificationTimes(t, root)

	second, diagnostics := Prepare(root, applyProject(inventoryEntry()), "0.0.0-test")
	require.Empty(t, diagnostics)
	written, removed, err := Write(root, second)
	require.NoError(t, err)
	assert.Equal(t, 0, written)
	assert.Equal(t, 0, removed)
	assert.Equal(t, before, modificationTimes(t, root))
}

func TestFindingsReportMissingStaleAndUnexpectedSorted(t *testing.T) {
	t.Parallel()

	plan := Plan{
		Missing:    []string{"doubles/b.gd", "doubles/b.gd.uid"},
		Stale:      []string{"doubles/a.gd"},
		Unexpected: []string{"doubles/c.gd"},
	}
	assert.Equal(t, []string{
		"stale: doubles/a.gd",
		"missing: doubles/b.gd",
		"missing: doubles/b.gd.uid",
		"unexpected: doubles/c.gd",
	}, plan.Findings())
	assert.Empty(t, Plan{}.Findings())
}

func TestPrepareOutputIsIdenticalAcrossRootsAndPaths(t *testing.T) {
	t.Parallel()

	first, firstDiagnostics := Prepare(writeProject(t, map[string]string{"src/inventory.gd": applySource}), applyProject(inventoryEntry()), "0.0.0-test")
	second, secondDiagnostics := Prepare(writeProject(t, map[string]string{"src/inventory.gd": applySource}), applyProject(inventoryEntry()), "0.0.0-test")
	require.Empty(t, firstDiagnostics)
	require.Empty(t, secondDiagnostics)
	assert.Equal(t, first.Files, second.Files)
	assert.Contains(t, string(first.Files["doubles/inventory_double.gd"]), "source=res://src/inventory.gd\n")
}

func TestPrepareExcludedBrokenScriptDoesNotBlockGeneration(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"src/inventory.gd":        applySource,
		"vendor/broken/bad.gd":    "func (:\n",
		"vendor/broken/other.txt": "not script",
	}
	project := applyProject(inventoryEntry())
	project.Exclude = []string{".godot/**", "vendor/**"}
	_, diagnostics := Prepare(writeProject(t, files), project, "0.0.0-test")
	assert.Empty(t, diagnostics)

	project.Exclude = []string{".godot/**"}
	_, diagnostics = Prepare(writeProject(t, files), project, "0.0.0-test")
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "vendor/broken/bad.gd", diagnostics[0].Path)
}

func TestPrepareExcludedFileNameIsSkipped(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"src/inventory.gd": applySource,
		"src/broken.gd":    "func (:\n",
	}
	project := applyProject(inventoryEntry())
	project.Exclude = []string{"**/broken.gd"}
	_, diagnostics := Prepare(writeProject(t, files), project, "0.0.0-test")
	assert.Empty(t, diagnostics)
}

func TestPrepareRefusesOutputWithoutMarkerAndSidecarWithoutScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		files   map[string]string
		message string
	}{
		{
			name:    "unmarked output",
			files:   map[string]string{"doubles/inventory_double.gd": "extends RefCounted\n"},
			message: "refusing to overwrite doubles/inventory_double.gd: missing gdtest generated-double marker",
		},
		{
			name:    "sidecar without script",
			files:   map[string]string{"doubles/inventory_double.gd.uid": "uid://abc\n"},
			message: "refusing to overwrite doubles/inventory_double.gd.uid: missing gdtest generated-double marker",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			files := map[string]string{"src/inventory.gd": applySource}
			for name, content := range test.files {
				files[name] = content
			}
			_, diagnostics := Prepare(writeProject(t, files), applyProject(inventoryEntry()), "0.0.0-test")
			require.Len(t, diagnostics, 1)
			assert.Equal(t, test.message, diagnostics[0].Message)
		})
	}
}

func TestPrepareUnexpectedOwnedFilesIncludeSidecars(t *testing.T) {
	t.Parallel()

	marker := "# gdtest:generated-double v1 source=res://src/gone.gd\n"
	root := writeProject(t, map[string]string{
		"src/inventory.gd":            applySource,
		"doubles/gone_double.gd":      marker + "extends RefCounted\n",
		"doubles/gone_double.gd.uid":  "uid://gone\n",
		"doubles/unowned_double.gd":   "extends RefCounted\n",
		"doubles/unowned_double.gd.x": "kept\n",
	})
	plan, diagnostics := Prepare(root, applyProject(inventoryEntry()), "0.0.0-test")
	require.Empty(t, diagnostics)
	assert.Equal(t, []string{"doubles/gone_double.gd", "doubles/gone_double.gd.uid"}, plan.Unexpected)
	assert.Contains(t, plan.Findings(), "unexpected: doubles/gone_double.gd.uid")
}

func TestWriteRemovesSidecarBeforeScriptAndKeepsUnownedFiles(t *testing.T) {
	t.Parallel()

	marker := "# gdtest:generated-double v1 source=res://src/gone.gd\n"
	root := writeProject(t, map[string]string{
		"src/inventory.gd":           applySource,
		"doubles/gone_double.gd":     marker + "extends RefCounted\n",
		"doubles/gone_double.gd.uid": "uid://gone\n",
		"doubles/unowned_double.gd":  "extends RefCounted\n",
	})
	plan, diagnostics := Prepare(root, applyProject(), "0.0.0-test")
	require.Empty(t, diagnostics)
	written, removed, err := Write(root, plan)
	require.NoError(t, err)
	assert.Equal(t, 0, written)
	assert.Equal(t, 2, removed)
	assert.NoFileExists(t, filepath.Join(root, "doubles", "gone_double.gd"))
	assert.NoFileExists(t, filepath.Join(root, "doubles", "gone_double.gd.uid"))
	assert.FileExists(t, filepath.Join(root, "doubles", "unowned_double.gd"))
}

func TestWriteStagingFailureLeavesEveryTargetUnchanged(t *testing.T) {
	t.Parallel()

	entries := []config.TestDouble{
		inventoryEntry(),
		{Source: "src/ledger.gd", Output: "doubles/ledger_double.gd", ClassName: "LedgerDouble"},
	}
	marker := "# gdtest:generated-double v1 source=res://src/inventory.gd\n"
	root := writeProject(t, map[string]string{
		"src/inventory.gd":            applySource,
		"src/ledger.gd":               strings.Replace(applySource, "class_name Inventory", "class_name Ledger", 1),
		"doubles/inventory_double.gd": marker + "extends RefCounted\n",
	})
	before := snapshotFiles(t, root)
	plan, diagnostics := Prepare(root, applyProject(entries...), "0.0.0-test")
	require.Empty(t, diagnostics)
	require.NotEmpty(t, plan.Changed)

	for failAt := 1; failAt <= len(plan.Changed); failAt++ {
		written, removed, err := write(root, plan, &failingStage{failAt: failAt})
		require.Error(t, err)
		assert.Zero(t, written)
		assert.Zero(t, removed)
		assert.Equal(t, before, snapshotFiles(t, root), "failure at staging operation %d changed a target", failAt)
		assert.Empty(t, temporaryFiles(t, root), "failure at staging operation %d left temporaries", failAt)
	}
}

func TestWriteStagesEveryFileWithSameDirectoryTemporaries(t *testing.T) {
	t.Parallel()

	root := writeProject(t, map[string]string{"src/inventory.gd": applySource})
	plan, diagnostics := Prepare(root, applyProject(inventoryEntry()), "0.0.0-test")
	require.Empty(t, diagnostics)
	written, _, err := Write(root, plan)
	require.NoError(t, err)
	assert.Equal(t, 2, written)
	info, err := os.Stat(filepath.Join(root, "doubles", "inventory_double.gd"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	assert.Empty(t, temporaryFiles(t, root))
}

func TestPrepareRejectsSymlinkedOutputAndSidecar(t *testing.T) {
	t.Parallel()

	root := writeProject(t, map[string]string{
		"src/inventory.gd": applySource,
		"doubles/real.gd":  "# gdtest:generated-double v1 source=res://src/inventory.gd\n",
	})
	require.NoError(t, os.Symlink(filepath.Join(root, "doubles", "real.gd"), filepath.Join(root, "doubles", "inventory_double.gd")))
	_, diagnostics := Prepare(root, applyProject(inventoryEntry()), "0.0.0-test")
	assert.Contains(t, messagesOf(diagnostics), "refusing to write through a symbolic link")

	other := writeProject(t, map[string]string{"src/inventory.gd": applySource})
	require.NoError(t, os.MkdirAll(filepath.Join(other, "doubles"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(other, "src", "inventory.gd"), filepath.Join(other, "doubles", "inventory_double.gd.uid")))
	_, diagnostics = Prepare(other, applyProject(inventoryEntry()), "0.0.0-test")
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "doubles/inventory_double.gd.uid", diagnostics[0].Path)
}

// failingProbe forwards every staging call to the real filesystem except the
// zero-length probe that proveRemovals creates in directory.
type failingProbe struct {
	osFileSystem
	directory string
}

func (failing failingProbe) stage(directory string, content []byte) (string, error) {
	if directory == failing.directory && len(content) == 0 {
		return "", errors.New("injected probe failure")
	}
	return failing.osFileSystem.stage(directory, content)
}

func TestWriteRemovalProbeFailureChangesNoTarget(t *testing.T) {
	t.Parallel()

	marker := "# gdtest:generated-double v1 source=res://src/gone.gd\n"
	root := writeProject(t, map[string]string{
		"src/inventory.gd":           applySource,
		"doubles/gone_double.gd":     marker + "extends RefCounted\n",
		"doubles/gone_double.gd.uid": "uid://gone\n",
	})
	plan, diagnostics := Prepare(root, applyProject(inventoryEntry()), "0.0.0-test")
	require.Empty(t, diagnostics)
	require.NotEmpty(t, plan.Changed)
	require.Equal(t, []string{"doubles/gone_double.gd", "doubles/gone_double.gd.uid"}, plan.Unexpected)
	before := snapshotFiles(t, root)

	written, removed, err := write(root, plan, failingProbe{directory: filepath.Join(root, "doubles")})

	require.ErrorContains(t, err, "prepare removal in doubles")
	assert.Zero(t, written)
	assert.Zero(t, removed)
	assert.Equal(t, before, snapshotFiles(t, root))
	assert.Empty(t, temporaryFiles(t, root))
}

func TestWriteRefusesUnexpectedFileRemovedSincePrepare(t *testing.T) {
	t.Parallel()

	marker := "# gdtest:generated-double v1 source=res://src/gone.gd\n"
	root := writeProject(t, map[string]string{
		"src/inventory.gd":       applySource,
		"doubles/gone_double.gd": marker + "extends RefCounted\n",
	})
	plan, diagnostics := Prepare(root, applyProject(inventoryEntry()), "0.0.0-test")
	require.Empty(t, diagnostics)
	require.NoError(t, os.Remove(filepath.Join(root, "doubles", "gone_double.gd")))
	before := snapshotFiles(t, root)

	written, removed, err := Write(root, plan)

	require.ErrorContains(t, err, "prepare removal of doubles/gone_double.gd")
	assert.Zero(t, written)
	assert.Zero(t, removed)
	assert.Equal(t, before, snapshotFiles(t, root))
	assert.Empty(t, temporaryFiles(t, root))
}

func modificationTimes(t *testing.T, root string) map[string]int64 {
	t.Helper()
	times := make(map[string]int64)
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		times[filepath.ToSlash(relative)] = info.ModTime().UnixNano()
		return nil
	})
	require.NoError(t, err)
	return times
}

func snapshotFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = string(content)
		return nil
	})
	require.NoError(t, err)
	return files
}

func temporaryFiles(t *testing.T, root string) []string {
	t.Helper()
	var temporaries []string
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), ".gdtest-double-") {
			temporaries = append(temporaries, current)
		}
		return nil
	})
	require.NoError(t, err)
	return temporaries
}
