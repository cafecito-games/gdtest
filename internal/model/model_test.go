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

func TestPlanLeakCheckRoundTripsBackendNeutralValues(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 3, ProtocolVersion)
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
