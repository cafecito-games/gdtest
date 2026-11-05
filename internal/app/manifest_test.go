package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGodotIdentityDoesNotExecuteBinaryAndDescriptionDoes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	executable := filepath.Join(root, "godot-test")
	marker := filepath.Join(root, "executed")
	script := "#!/bin/sh\nprintf executed > \"" + marker + "\"\nprintf '4.7.test\\n'\n"
	require.NoError(t, os.WriteFile(executable, []byte(script), 0o700))

	first, err := godotIdentity(executable)
	require.NoError(t, err)
	assert.NotEmpty(t, first)
	_, err = os.Stat(marker)
	require.ErrorIs(t, err, os.ErrNotExist)

	path, engineVersion, err := godotDescription(context.Background(), executable)
	require.NoError(t, err)
	canonicalExecutable, err := filepath.EvalSymlinks(executable)
	require.NoError(t, err)
	assert.Equal(t, filepath.ToSlash(canonicalExecutable), path)
	assert.Equal(t, "4.7.test", engineVersion)
	require.FileExists(t, marker)

	require.NoError(t, os.WriteFile(executable, []byte(script+"# changed\n"), 0o700))
	second, err := godotIdentity(executable)
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
}

func TestDiscoveryCandidatesResolveDynamicStableIDToOwningFile(t *testing.T) {
	t.Parallel()
	manifest := discovery.Manifest{Files: []model.FilePlan{
		{Path: "tests/alpha.test.gd", Cases: []model.CasePlan{{ID: "tests/alpha.test.gd::test_static"}}},
		{Path: "tests/beta.test.gd", Cases: []model.CasePlan{{ID: "tests/beta.test.gd::test_static"}}},
	}}

	selected, err := discoveryCandidates(
		manifest,
		[]string{"tests/alpha.test.gd::dynamic@12:0[nested/case]"},
		false,
	)
	require.NoError(t, err)
	require.Len(t, selected.Files, 1)
	assert.Equal(t, "tests/alpha.test.gd", selected.Files[0].Path)
}

func TestDiscoveryCandidatesRetainDynamicOnlyOwnerFile(t *testing.T) {
	t.Parallel()
	manifest := discovery.Manifest{Files: []model.FilePlan{
		{Path: "tests/dynamic_only.test.gd"},
		{Path: "tests/static.test.gd", Cases: []model.CasePlan{{ID: "tests/static.test.gd::test_static"}}},
	}}

	selected, err := discoveryCandidates(
		manifest,
		[]string{"tests/dynamic_only.test.gd::dynamic@3:0[generated]"},
		false,
	)

	require.NoError(t, err)
	require.Len(t, selected.Files, 1)
	assert.Equal(t, "tests/dynamic_only.test.gd", selected.Files[0].Path)
	assert.Empty(t, selected.Files[0].Cases)
}

func TestSelectCompletedManifestSupportsDynamicIDsNamesAndAffectedIntersection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "project.godot"),
		[]byte("config_version=5\n\n[application]\nconfig/name=\"selection\"\n"),
		0o600,
	))
	static := discovery.Manifest{
		Files: []model.FilePlan{
			{Path: "tests/alpha.test.gd", Cases: []model.CasePlan{{ID: "tests/alpha.test.gd::test_static"}}},
			{Path: "tests/beta.test.gd", Cases: []model.CasePlan{{ID: "tests/beta.test.gd::test_static"}}},
		},
		Scripts: []discovery.ScriptMetadata{
			{Path: "tests/alpha.test.gd", Dependencies: []string{"src/value.gd"}},
			{Path: "tests/beta.test.gd"},
			{Path: "src/value.gd"},
		},
	}
	dynamicID := "tests/alpha.test.gd::dynamic@12:0[nested/generated%20case]"
	manifest := model.DiscoveryManifest{
		ProtocolVersion: model.ProtocolVersion,
		Generation:      2,
		Files: []model.DiscoveryFile{
			{Path: "tests/alpha.test.gd", Cases: []model.DiscoveryCase{
				{ID: dynamicID, Name: "generated case", File: "tests/alpha.test.gd", Line: 12},
				{ID: "tests/alpha.test.gd::test_static", Name: "static", File: "tests/alpha.test.gd", Line: 4},
			}},
			{Path: "tests/beta.test.gd", Cases: []model.DiscoveryCase{
				{ID: "tests/beta.test.gd::test_static", Name: "static", File: "tests/beta.test.gd", Line: 4},
			}},
		},
	}

	selected, err := selectCompletedManifest(
		manifest, static, root, nil, "generated", false, true, []string{"src/value.gd"},
	)
	require.NoError(t, err)
	assert.Equal(t, []string{dynamicID}, selected.ids)
	require.NotEmpty(t, selected.reasons)
	assert.Equal(t, "tests/alpha.test.gd", selected.reasons[0].File)

	exact, err := selectCompletedManifest(
		manifest, static, root, []string{dynamicID}, "", false, false, nil,
	)
	require.NoError(t, err)
	assert.Equal(t, []string{dynamicID}, exact.ids)
}

func TestAffectedCandidatesReturnsExactEmptyMessageWithoutStartingGodot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	manifest := discovery.Manifest{
		Files:   []model.FilePlan{{Path: "tests/alpha.test.gd", Cases: []model.CasePlan{{ID: "tests/alpha.test.gd::test_a"}}}},
		Scripts: []discovery.ScriptMetadata{{Path: "tests/alpha.test.gd"}},
	}

	result, err := affectedCandidates(manifest, root, []string{"README.md"})
	require.NoError(t, err)
	assert.Empty(t, result.Manifest.Files)
	assert.Equal(t, "no tests were affected", result.Message)
}
