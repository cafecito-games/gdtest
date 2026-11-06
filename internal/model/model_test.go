package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilePlanMarshalsDynamicOnlyCasesAsExplicitEmptyArray(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(FilePlan{Path: "tests/dynamic.test.gd"})

	require.NoError(t, err)
	assert.JSONEq(t, `{"path":"tests/dynamic.test.gd","cases":[]}`, string(encoded))
}

func TestFilePlanCaseIDsPreserveNilVersusEmptyShard(t *testing.T) {
	t.Parallel()

	unsharded, err := json.Marshal(FilePlan{Path: "tests/a.test.gd"})
	require.NoError(t, err)
	assert.Equal(t, `{"path":"tests/a.test.gd","cases":[]}`, string(unsharded))

	emptyShard, err := json.Marshal(FilePlan{Path: "tests/a.test.gd", CaseIDs: []string{}})
	require.NoError(t, err)
	assert.Equal(t, `{"path":"tests/a.test.gd","case_ids":[],"cases":[]}`, string(emptyShard))

	sharded, err := json.Marshal(FilePlan{
		Path:    "tests/a.test.gd",
		CaseIDs: []string{"tests/a.test.gd::test_one", "tests/a.test.gd::test_rows[1]"},
	})
	require.NoError(t, err)
	assert.Equal(
		t,
		`{"path":"tests/a.test.gd","case_ids":["tests/a.test.gd::test_one","tests/a.test.gd::test_rows[1]"],"cases":[]}`,
		string(sharded),
	)

	var absent FilePlan
	require.NoError(t, json.Unmarshal([]byte(`{"path":"tests/a.test.gd"}`), &absent))
	assert.Nil(t, absent.CaseIDs)

	var empty FilePlan
	require.NoError(t, json.Unmarshal([]byte(`{"path":"tests/a.test.gd","case_ids":[]}`), &empty))
	require.NotNil(t, empty.CaseIDs)
	assert.Empty(t, empty.CaseIDs)
}

func TestPlanSerializesWithoutShardFieldsExactlyAsUnsharded(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(TestPlan{
		ProtocolVersion:  ProtocolVersion,
		RunID:            "run-1",
		Generation:       1,
		SelectionApplied: true,
		SelectedIDs:      []string{"tests/a.test.gd::test_one"},
		Files:            []FilePlan{{Path: "tests/a.test.gd", Cases: []CasePlan{{ID: "tests/a.test.gd::test_one", Name: "One", MethodName: "test_one", Line: 3}}}},
	})
	require.NoError(t, err)
	assert.Equal(
		t,
		`{"protocol_version":4,"run_id":"run-1","generation":1,"selection_applied":true,`+
			`"selected_ids":["tests/a.test.gd::test_one"],`+
			`"files":[{"path":"tests/a.test.gd","cases":[{"id":"tests/a.test.gd::test_one","name":"One","method_name":"test_one","line":3}]}]}`,
		string(encoded),
	)
}

func TestPlanHasFocusRoundTripsStampedDecision(t *testing.T) {
	t.Parallel()

	unstamped, err := json.Marshal(TestPlan{ProtocolVersion: ProtocolVersion, RunID: "run-1", Generation: 1})
	require.NoError(t, err)
	assert.NotContains(t, string(unstamped), "has_focus")

	stampedTrue := boolPointer(true)
	encoded, err := json.Marshal(TestPlan{
		ProtocolVersion: ProtocolVersion, RunID: "run-1", Generation: 1, HasFocus: stampedTrue,
	})
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"has_focus":true`)

	stampedFalse := boolPointer(false)
	encoded, err = json.Marshal(TestPlan{
		ProtocolVersion: ProtocolVersion, RunID: "run-1", Generation: 1, HasFocus: stampedFalse,
	})
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"has_focus":false`)

	var decoded TestPlan
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.NotNil(t, decoded.HasFocus)
	assert.False(t, *decoded.HasFocus)

	absent, err := json.Marshal(TestPlan{ProtocolVersion: ProtocolVersion, RunID: "run-1", Generation: 1})
	require.NoError(t, err)
	var decodedAbsent TestPlan
	require.NoError(t, json.Unmarshal(absent, &decodedAbsent))
	assert.Nil(t, decodedAbsent.HasFocus)
}

func TestFilePlanValidateCaseIDsRejectsEmptyAndDuplicateEntries(t *testing.T) {
	t.Parallel()

	assert.NoError(t, (FilePlan{Path: "tests/a.test.gd"}).ValidateCaseIDs())
	assert.NoError(t, (FilePlan{Path: "tests/a.test.gd", CaseIDs: []string{}}).ValidateCaseIDs())
	assert.NoError(t, (FilePlan{
		Path:    "tests/a.test.gd",
		CaseIDs: []string{"tests/a.test.gd::test_one", "tests/a.test.gd::test_rows[1]"},
	}).ValidateCaseIDs())

	emptyEntry := FilePlan{Path: "tests/a.test.gd", CaseIDs: []string{"tests/a.test.gd::test_one", ""}}
	err := emptyEntry.ValidateCaseIDs()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `file "tests/a.test.gd" case_ids[1]: case ID must not be empty`)

	duplicate := FilePlan{
		Path:    "tests/a.test.gd",
		CaseIDs: []string{"tests/a.test.gd::test_one", "tests/a.test.gd::test_rows[1]", "tests/a.test.gd::test_one"},
	}
	err = duplicate.ValidateCaseIDs()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `file "tests/a.test.gd" case_ids[2]: "tests/a.test.gd::test_one" duplicates case_ids[0]`)
}

func boolPointer(value bool) *bool {
	return &value
}

func TestPlanLeakCheckRoundTripsBackendNeutralValues(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 4, ProtocolVersion)
	plan := TestPlan{
		ProtocolVersion: ProtocolVersion,
		RunID:           "run-1",
		Generation:      1,
		LeakCheck: LeakCheck{
			Policy: "warn",
			Ignore: []string{"res://ui/toast_layer.gd", "Timer"},
		},
		Files: []FilePlan{{Path: "tests/hud.test.gd"}},
	}
	require.NoError(t, plan.LeakCheck.Validate())

	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"leak_check":{"policy":"warn","ignore":["res://ui/toast_layer.gd","Timer"]}`)

	var decoded TestPlan
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, plan.LeakCheck, decoded.LeakCheck)
}

func TestLeakCheckValidateRejectsInvalidPoliciesAndEntries(t *testing.T) {
	t.Parallel()

	assert.NoError(t, (LeakCheck{}).Validate())
	assert.NoError(t, (LeakCheck{Policy: "fail", Ignore: []string{"res://a/b.gd", "Node2D", "_PrivateClass"}}).Validate())

	invalid := []LeakCheck{
		{Policy: "strict"},
		{Policy: "fail", Ignore: []string{""}},
		{Policy: "fail", Ignore: []string{"/abs/worktree/toast.gd"}},
		{Policy: "fail", Ignore: []string{"ui/toast.gd"}},
		{Policy: "fail", Ignore: []string{"res://ui/toast.tscn"}},
		{Policy: "fail", Ignore: []string{"res://ui/../toast.gd"}},
		{Policy: "fail", Ignore: []string{"res:///toast.gd"}},
		{Policy: "fail", Ignore: []string{"res://.gd"}},
		{Policy: "fail", Ignore: []string{`res://ui\toast.gd`}},
		{Policy: "fail", Ignore: []string{"123BadIdent"}},
		{Policy: "fail", Ignore: []string{"Timer", "Timer"}},
	}
	for _, candidate := range invalid {
		assert.Error(t, candidate.Validate(), "expected error for %+v", candidate)
	}
}

func TestEventCaseWarningRoundTripsLeakMetadata(t *testing.T) {
	t.Parallel()

	event := Event{
		ProtocolVersion: ProtocolVersion,
		Type:            EventCaseWarning,
		RunID:           "run-1",
		TestID:          "tests/hud.test.gd::test_toast",
		Assertion:       "leak",
		Message:         `leaked 1 node: orphan Node "Enemy"`,
		Location:        &Location{File: "tests/hud.test.gd", Line: 10},
		Metadata: map[string]any{
			"leaks": []any{
				map[string]any{
					"kind":        "orphan",
					"class":       "Node",
					"name":        "Enemy",
					"script":      "",
					"path":        "",
					"descendants": float64(0),
				},
			},
		},
	}
	encoded, err := json.Marshal(event)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"type":"case_warning"`)
	var decoded Event
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, EventCaseWarning, decoded.Type)
	assert.Equal(t, "leak", decoded.Assertion)
}
