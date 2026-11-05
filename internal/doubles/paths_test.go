package doubles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/gdtest/internal/config"
)

const inventorySource = "extends RefCounted\n\nfunc value() -> int:\n\treturn 1\n"

func TestAnalyzeRejectsSourceSymlinkOutsideProject(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "inventory.gd"), []byte(inventorySource), 0o600))
	root := writeProject(t, map[string]string{"src/.keep": ""})
	require.NoError(t, os.Symlink(filepath.Join(outside, "inventory.gd"), filepath.Join(root, "src", "linked.gd")))

	index, indexDiagnostics := BuildIndex(root)
	require.Len(t, indexDiagnostics, 1)
	_, diagnostics := Analyze(index, entryFor("linked.gd"))
	assert.Contains(t, messagesOf(diagnostics), "source resolves outside the project root")
}

func TestAnalyzeSourceUnderSymlinkedDirectoryOutsideProjectIsRejected(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "inventory.gd"), []byte(inventorySource), 0o600))
	root := writeProject(t, map[string]string{"src/.keep": ""})
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linkeddir")))

	index, _ := BuildIndex(root)
	_, diagnostics := Analyze(index, config.TestDouble{
		Source: "linkeddir/inventory.gd", Output: "doubles/inventory_double.gd",
	})
	assert.Contains(t, messagesOf(diagnostics), "source resolves outside the project root")
}

func TestAnalyzeRefusesOutputThatIsASymlink(t *testing.T) {
	t.Parallel()

	root := writeProject(t, map[string]string{"src/source.gd": inventorySource, "doubles/.keep": ""})
	require.NoError(t, os.Symlink(filepath.Join(root, "src", "source.gd"), filepath.Join(root, "doubles", "source_double.gd")))

	index, _ := BuildIndex(root)
	_, diagnostics := Analyze(index, entryFor("source.gd"))
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "doubles/source_double.gd", diagnostics[0].Path)
	assert.Equal(t, "refusing to write through a symbolic link", diagnostics[0].Message)
}

func TestAnalyzeRefusesOutputThroughSymlinkedDirectory(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	root := writeProject(t, map[string]string{"src/source.gd": inventorySource})
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape")))

	index, _ := BuildIndex(root)
	_, diagnostics := Analyze(index, config.TestDouble{
		Source: "src/source.gd", Output: "escape/source_double.gd",
	})
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "output resolves outside the project root", diagnostics[0].Message)
}

func TestAnalyzeRefusesOutputThatEscapesLexically(t *testing.T) {
	t.Parallel()

	index := buildProject(t, map[string]string{"src/source.gd": inventorySource})
	_, diagnostics := Analyze(index, config.TestDouble{Source: "src/source.gd", Output: "../outside.gd"})
	assert.Contains(t, messagesOf(diagnostics), "output escapes the project root")
}

func TestAnalyzeRefusesExistingOutputThatIsNotARegularFile(t *testing.T) {
	t.Parallel()

	root := writeProject(t, map[string]string{"src/source.gd": inventorySource})
	require.NoError(t, os.MkdirAll(filepath.Join(root, "doubles", "source_double.gd"), 0o755))

	index, _ := BuildIndex(root)
	_, diagnostics := Analyze(index, entryFor("source.gd"))
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "output exists and is not a regular file", diagnostics[0].Message)
}

func TestAnalyzeAcceptsMissingOutputUnderMissingDirectories(t *testing.T) {
	t.Parallel()

	index := buildProject(t, map[string]string{"src/source.gd": inventorySource})
	model, diagnostics := Analyze(index, entryFor("source.gd"))
	assert.Empty(t, diagnostics)
	assert.Equal(t, "res://doubles/source_double.gd", model.OutputResource)
}
