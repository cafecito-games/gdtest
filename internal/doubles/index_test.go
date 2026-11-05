package doubles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildIndexSkipsGodotAndGitDirectories(t *testing.T) {
	t.Parallel()

	root := writeProject(t, map[string]string{
		"src/a.gd":           "class_name A\n",
		".godot/cache.gd":    "this is not GDScript (\n",
		".git/hooks/x.gd":    "this is not GDScript (\n",
		"nested/.godot/b.gd": "this is not GDScript (\n",
	})
	index, diagnostics := BuildIndex(root)
	require.Empty(t, diagnostics)
	require.Contains(t, index.scripts, "src/a.gd")
	assert.Len(t, index.scripts, 1)
}

func TestBuildIndexReportsParseFailuresWithPosition(t *testing.T) {
	t.Parallel()

	_, diagnostics := BuildIndex(writeProject(t, map[string]string{
		"src/broken.gd": "extends RefCounted\n\nfunc (:\n",
	}))
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "src/broken.gd", diagnostics[0].Path)
	assert.Equal(t, 3, diagnostics[0].Line)
	assert.NotEmpty(t, diagnostics[0].Message)
}

func TestBuildIndexRecordsExactOwnershipMarkerOnly(t *testing.T) {
	t.Parallel()

	marker := "# gdtest:generated-double v1 source=res://src/a.gd\n"
	index := buildProject(t, map[string]string{
		"doubles/owned.gd":      marker + "class_name Owned\n",
		"doubles/spaced.gd":     "# gdtest:generated-double  v1 source=res://src/a.gd\n",
		"doubles/second.gd":     "\n" + marker,
		"doubles/no_source.gd":  "# gdtest:generated-double v1 source=src/a.gd\n",
		"doubles/extra_text.gd": "# gdtest:generated-double v1 source=res://src/a.gd trailing\n",
	})
	assert.Equal(t, []GeneratedScript{{Path: "doubles/owned.gd", Source: "src/a.gd"}}, index.GeneratedScripts())
}

func TestBuildIndexDiagnosesDuplicateClassName(t *testing.T) {
	t.Parallel()

	_, diagnostics := BuildIndex(writeProject(t, map[string]string{
		"src/a.gd": "class_name Shared\n",
		"src/b.gd": "class_name Shared\n",
	}))
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "src/b.gd", diagnostics[0].Path)
	assert.Contains(t, diagnostics[0].Message, "class_name Shared is also declared by src/a.gd")
}

func TestBuildIndexRejectsRootThatDoesNotExist(t *testing.T) {
	t.Parallel()

	index, diagnostics := BuildIndex(filepath.Join(t.TempDir(), "missing"))
	assert.Nil(t, index)
	require.Len(t, diagnostics, 1)
	assert.Contains(t, diagnostics[0].Message, "resolve project root")
}

func TestBuildIndexSymlinkedScriptOutsideRootIsDiagnosed(t *testing.T) {
	t.Parallel()

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "external.gd"), []byte("class_name External\n"), 0o600))
	root := writeProject(t, map[string]string{})
	require.NoError(t, os.Symlink(filepath.Join(outside, "external.gd"), filepath.Join(root, "linked.gd")))

	index, diagnostics := BuildIndex(root)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "linked.gd", diagnostics[0].Path)
	assert.Contains(t, diagnostics[0].Message, "symlink resolves outside the project root")
	assert.NotContains(t, index.classes, "External")
}
