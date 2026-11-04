package selection

import (
	"testing"

	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAffectedSelectsDirectAndTransitiveDependenciesWithReasons(t *testing.T) {
	t.Parallel()
	manifest := affectedManifest()

	direct, err := Apply(manifest, Options{Affected: &AffectedOptions{Paths: []string{"tests/a.test.gd"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"tests/a.test.gd::test_a", "tests/dynamic.test.gd::test_dynamic"}, caseIDs(direct.Manifest))
	require.Len(t, direct.Reasons, 2)
	assert.Equal(t, ReasonDirectDependency, direct.Reasons[0].Kind)

	immediate, err := Apply(manifest, Options{Affected: &AffectedOptions{Paths: []string{"src/service.gd"}}})
	require.NoError(t, err)
	require.Len(t, immediate.Reasons, 2)
	assert.Equal(t, ReasonDirectDependency, immediate.Reasons[0].Kind)
	assert.Equal(t, []string{"tests/a.test.gd", "src/service.gd"}, immediate.Reasons[0].Chain)

	transitive, err := Apply(manifest, Options{Affected: &AffectedOptions{Paths: []string{"data/value.tres"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"tests/a.test.gd::test_a", "tests/dynamic.test.gd::test_dynamic"}, caseIDs(transitive.Manifest))
	require.Len(t, transitive.Reasons, 2)
	assert.Equal(t, ReasonTransitiveDependency, transitive.Reasons[0].Kind)
	assert.Equal(t, []string{"tests/a.test.gd", "src/service.gd", "data/value.tres"}, transitive.Reasons[0].Chain)
	assert.Contains(t, transitive.Reasons[0].Message, "tests/a.test.gd -> src/service.gd -> data/value.tres")
}

func TestAffectedWidensDynamicAndGlobalInputsConservatively(t *testing.T) {
	t.Parallel()
	manifest := affectedManifest()

	dynamic, err := Apply(manifest, Options{Affected: &AffectedOptions{Paths: []string{"docs/readme.md"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{"tests/dynamic.test.gd::test_dynamic"}, caseIDs(dynamic.Manifest))
	require.Len(t, dynamic.Reasons, 1)
	assert.Equal(t, ReasonDynamicDependency, dynamic.Reasons[0].Kind)

	for _, options := range []*AffectedOptions{
		{Paths: []string{"project.godot"}},
		{Paths: []string{"addons/gdtest/worker.gd"}},
		{Paths: []string{"native/runtime.gdextension"}},
		{Paths: []string{"unknown/runtime.json"}},
		{Paths: []string{"src/global.gd"}},
		{Paths: []string{"src/autoload.gd"}, GlobalInputs: []string{"src/autoload.gd"}},
	} {
		result, err := Apply(manifest, Options{Affected: options})
		require.NoError(t, err)
		assert.Equal(t, allAffectedIDs(), caseIDs(result.Manifest), options.Paths)
		assert.NotEmpty(t, result.Reasons)
		for _, reason := range result.Reasons {
			assert.Equal(t, ReasonGlobalInput, reason.Kind)
		}
	}
}

func TestAffectedCyclesTerminateAndDirectoryChangesMatch(t *testing.T) {
	t.Parallel()
	manifest := affectedManifest()
	result, err := Apply(manifest, Options{Affected: &AffectedOptions{Paths: []string{"src/cycle-a.gd"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"tests/cycle.test.gd::test_cycle",
		"tests/dynamic.test.gd::test_dynamic",
	}, caseIDs(result.Manifest))

	result, err = Apply(manifest, Options{Affected: &AffectedOptions{Paths: []string{"data"}}})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"tests/a.test.gd::test_a",
		"tests/dynamic.test.gd::test_dynamic",
	}, caseIDs(result.Manifest))
}

func TestAffectedEmptySelectionSucceedsWithMessage(t *testing.T) {
	t.Parallel()
	for _, paths := range [][]string{nil, {"docs/readme.md"}} {
		manifest := affectedManifest()
		if len(paths) > 0 {
			// Remove the dynamic test so a documentation-only change is provably irrelevant.
			manifest.Files = manifest.Files[:3]
			manifest.Scripts = manifest.Scripts[:6]
		}
		result, err := Apply(manifest, Options{Affected: &AffectedOptions{Paths: paths}})
		require.NoError(t, err)
		assert.Empty(t, result.Manifest.Files)
		assert.Equal(t, "no tests were affected", result.Message)
	}
}

func TestAffectedIntersectsOrdinarySelectorsAndNormalizesPaths(t *testing.T) {
	t.Parallel()
	result, err := Apply(affectedManifest(), Options{
		Selectors: []string{`tests\a.test.gd`},
		Affected:  &AffectedOptions{Paths: []string{`data\value.tres`}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"tests/a.test.gd::test_a"}, caseIDs(result.Manifest))
	assert.NotContains(t, result.Reasons[0].Changed, `\`)

	_, err = Apply(affectedManifest(), Options{Affected: &AffectedOptions{Paths: []string{"/absolute.gd"}}})
	var usage *UsageError
	require.ErrorAs(t, err, &usage)
}

func TestAffectedReasonsAreDeterministicAcrossInputOrderAndWorktrees(t *testing.T) {
	t.Parallel()
	one, err := Apply(affectedManifest(), Options{Affected: &AffectedOptions{
		Paths: []string{"z.txt", "data/value.tres"},
	}})
	require.NoError(t, err)
	two, err := Apply(affectedManifest(), Options{Affected: &AffectedOptions{
		Paths: []string{"data/value.tres", "z.txt"},
	}})
	require.NoError(t, err)
	assert.Equal(t, one.Reasons, two.Reasons)
	for _, reason := range one.Reasons {
		assert.NotContains(t, reason.Message, "/private/")
		assert.NotContains(t, reason.Message, `C:\`)
	}
}

func TestAffectedConservativeProjectMetadataSelectsAll(t *testing.T) {
	t.Parallel()
	manifest := affectedManifest()
	result, err := Apply(manifest, Options{Affected: &AffectedOptions{
		Paths:           []string{"README.md"},
		ConservativeAll: true,
	}})
	require.NoError(t, err)
	assert.Equal(t, allAffectedIDs(), caseIDs(result.Manifest))
	require.NotEmpty(t, result.Reasons)
	for _, reason := range result.Reasons {
		assert.Equal(t, ReasonGlobalInput, reason.Kind)
		assert.Contains(t, reason.Message, "unresolved global input")
	}
}

func affectedManifest() discovery.Manifest {
	return discovery.Manifest{
		Files: []model.FilePlan{
			file("tests/a.test.gd", "test_a"),
			file("tests/b.test.gd", "test_b"),
			file("tests/cycle.test.gd", "test_cycle"),
			file("tests/dynamic.test.gd", "test_dynamic"),
		},
		Scripts: []discovery.ScriptMetadata{
			{Path: "tests/a.test.gd", Dependencies: []string{"src/service.gd"}},
			{Path: "src/service.gd", Dependencies: []string{"data/value.tres"}},
			{Path: "tests/b.test.gd"},
			{Path: "tests/cycle.test.gd", Dependencies: []string{"src/cycle-a.gd"}},
			{Path: "src/cycle-a.gd", Dependencies: []string{"src/cycle-b.gd"}},
			{Path: "src/cycle-b.gd", Dependencies: []string{"src/cycle-a.gd"}},
			{Path: "tests/dynamic.test.gd", Dependencies: []string{"src/dynamic.gd"}},
			{Path: "src/dynamic.gd", HasDynamicDependency: true},
			{Path: "src/global.gd", ClassName: "GlobalValue"},
			{Path: "src/autoload.gd"},
		},
	}
}

func file(name, method string) model.FilePlan {
	return model.FilePlan{Path: name, Cases: []model.CasePlan{{
		ID: name + "::" + method, Name: method, MethodName: method,
	}}}
}

func allAffectedIDs() []string {
	return []string{
		"tests/a.test.gd::test_a",
		"tests/b.test.gd::test_b",
		"tests/cycle.test.gd::test_cycle",
		"tests/dynamic.test.gd::test_dynamic",
	}
}
