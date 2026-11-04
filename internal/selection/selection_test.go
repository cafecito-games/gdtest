package selection

import (
	"errors"
	"testing"

	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplySelectorsPreservesOrderAndSupportsEverySelectorKind(t *testing.T) {
	t.Parallel()
	manifest := selectorManifest()

	tests := []struct {
		name      string
		selectors []string
		want      []string
	}{
		{name: "none", want: []string{"tests/a.test.gd::test_alpha", "tests/a.test.gd::test_beta", "tests/nested/b.test.gd::test_gamma"}},
		{name: "file", selectors: []string{"tests/a.test.gd"}, want: []string{"tests/a.test.gd::test_alpha", "tests/a.test.gd::test_beta"}},
		{name: "directory windows spelling", selectors: []string{`tests\nested`}, want: []string{"tests/nested/b.test.gd::test_gamma"}},
		{name: "double star glob", selectors: []string{"tests/**/*.test.gd"}, want: []string{"tests/a.test.gd::test_alpha", "tests/a.test.gd::test_beta", "tests/nested/b.test.gd::test_gamma"}},
		{name: "stable ID", selectors: []string{"res://tests/a.test.gd::test_beta"}, want: []string{"tests/a.test.gd::test_beta"}},
		{name: "union remains source ordered", selectors: []string{"tests/nested", "tests/a.test.gd::test_beta"}, want: []string{"tests/a.test.gd::test_beta", "tests/nested/b.test.gd::test_gamma"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := Apply(manifest, Options{Selectors: test.selectors})
			require.NoError(t, err)
			assert.Equal(t, test.want, caseIDs(result.Manifest))
			assert.Equal(t, manifest.ContentFingerprint, result.Manifest.ContentFingerprint)
		})
	}
}

func TestApplyExplicitNameFilterIntersectsPathSelection(t *testing.T) {
	t.Parallel()
	result, err := Apply(selectorManifest(), Options{
		Selectors: []string{"tests"},
		Name:      "BeTa",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"tests/a.test.gd::test_beta"}, caseIDs(result.Manifest))

	result, err = Apply(selectorManifest(), Options{Name: "test_gamma"})
	require.NoError(t, err)
	assert.Equal(t, []string{"tests/nested/b.test.gd::test_gamma"}, caseIDs(result.Manifest))
}

func TestApplySelectorUnionDeduplicatesAndCanIgnoreUnknownsExplicitly(t *testing.T) {
	t.Parallel()
	result, err := Apply(selectorManifest(), Options{
		Selectors: []string{
			"tests/a.test.gd::test_alpha",
			"tests/a.test.gd::test_alpha",
			"missing.test.gd",
		},
		AllowEmpty: true,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"tests/a.test.gd::test_alpha"}, caseIDs(result.Manifest))
}

func TestApplyRejectsUnknownUnsafeAndInvalidSelectors(t *testing.T) {
	t.Parallel()
	for _, selector := range []string{"missing.test.gd", "../outside.gd", "/absolute.gd", `C:\absolute.gd`, "tests/[.gd", "tests/a.test.gd::"} {
		_, err := Apply(selectorManifest(), Options{Selectors: []string{selector}})
		var usage *UsageError
		require.ErrorAs(t, err, &usage, selector)
	}

	result, err := Apply(selectorManifest(), Options{
		Selectors:  []string{"missing.test.gd"},
		AllowEmpty: true,
	})
	require.NoError(t, err)
	assert.Empty(t, result.Manifest.Files)

	_, err = Apply(selectorManifest(), Options{Name: "does not exist"})
	assert.ErrorContains(t, err, "matched no tests")
}

func TestFilterGroupsAppliesSameRulesToDynamicCases(t *testing.T) {
	t.Parallel()
	type dynamicCase struct {
		stableID string
		label    string
	}
	groups := []Group[dynamicCase]{
		{Path: "tests/generated.test.gd", Cases: []dynamicCase{
			{stableID: "tests/generated.test.gd::dynamic@4:0[first]", label: "First generated"},
			{stableID: "tests/generated.test.gd::dynamic@5:0[second]", label: "Second generated"},
		}},
	}
	describe := func(value dynamicCase) CaseInfo { return CaseInfo{ID: value.stableID, Name: value.label} }

	filtered, err := FilterGroups(groups, MatchOptions{
		Selectors: []string{"tests/generated.test.gd::dynamic@5:0[second]"},
	}, describe)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Len(t, filtered[0].Cases, 1)
	assert.Equal(t, "Second generated", filtered[0].Cases[0].label)

	filtered, err = FilterGroups(groups, MatchOptions{Name: "FIRST"}, describe)
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	assert.Equal(t, "First generated", filtered[0].Cases[0].label)
}

func TestApplyDoesNotAliasCaseSlices(t *testing.T) {
	t.Parallel()
	manifest := selectorManifest()
	result, err := Apply(manifest, Options{})
	require.NoError(t, err)
	result.Manifest.Files[0].Cases[0].Name = "mutated"
	assert.Equal(t, "Alpha behavior", manifest.Files[0].Cases[0].Name)
}

func selectorManifest() discovery.Manifest {
	return discovery.Manifest{
		Files: []model.FilePlan{
			{Path: "tests/a.test.gd", Cases: []model.CasePlan{
				{ID: "tests/a.test.gd::test_alpha", Name: "Alpha behavior", MethodName: "test_alpha"},
				{ID: "tests/a.test.gd::test_beta", Name: "Beta behavior", MethodName: "test_beta"},
			}},
			{Path: "tests/nested/b.test.gd", Cases: []model.CasePlan{
				{ID: "tests/nested/b.test.gd::test_gamma", Name: "Gamma behavior", MethodName: "test_gamma"},
			}},
		},
		StructuralFingerprint: "structural",
		ContentFingerprint:    "content",
	}
}

func caseIDs(manifest discovery.Manifest) []string {
	result := make([]string, 0)
	for _, file := range manifest.Files {
		for _, testCase := range file.Cases {
			result = append(result, testCase.ID)
		}
	}
	return result
}

func TestUsageErrorSupportsErrorsAs(t *testing.T) {
	t.Parallel()
	var usage *UsageError
	assert.True(t, errors.As(&UsageError{Message: "usage"}, &usage))
}
