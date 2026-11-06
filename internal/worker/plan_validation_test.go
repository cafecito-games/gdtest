package worker

import (
	"testing"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func shardedWorkerPlan() model.TestPlan {
	return model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "run-1",
		Generation:      1,
		Files: []model.FilePlan{
			{
				Path:    "tests/a.test.gd",
				CaseIDs: []string{"tests/a.test.gd::test_one", "tests/a.test.gd::dynamic@3:0"},
			},
			{
				Path:    "tests/a.test.gd",
				CaseIDs: []string{"tests/a.test.gd::test_rows[1]"},
			},
		},
	}
}

func TestValidateWorkerPlanAcceptsDisjointPerFileCaseSubsets(t *testing.T) {
	t.Parallel()

	assert.NoError(t, validateWorkerPlan(shardedWorkerPlan()))
	assert.NoError(t, validateWorkerPlan(model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           "run-1",
		Generation:      1,
		HasFocus:        func() *bool { value := false; return &value }(),
		Files:           []model.FilePlan{{Path: "tests/a.test.gd", CaseIDs: []string{}}},
	}))
}

func TestValidateWorkerPlanRejectsPreviousProtocolVersion(t *testing.T) {
	t.Parallel()

	plan := shardedWorkerPlan()
	plan.ProtocolVersion = model.ProtocolVersion - 1
	err := validateWorkerPlan(plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test plan protocol 3 does not match 4")
}

func TestValidateWorkerPlanRejectsInvalidPerFileCaseSubsets(t *testing.T) {
	t.Parallel()

	emptyEntry := shardedWorkerPlan()
	emptyEntry.Files[1].CaseIDs = []string{"tests/a.test.gd::test_rows[1]", ""}
	err := validateWorkerPlan(emptyEntry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `file "tests/a.test.gd" case_ids[1]: case ID must not be empty`)

	duplicateEntry := shardedWorkerPlan()
	duplicateEntry.Files[1].CaseIDs = []string{"tests/a.test.gd::test_one", "tests/a.test.gd::test_one"}
	err = validateWorkerPlan(duplicateEntry)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `file "tests/a.test.gd" case_ids[1]: "tests/a.test.gd::test_one" duplicates case_ids[0]`)
}
