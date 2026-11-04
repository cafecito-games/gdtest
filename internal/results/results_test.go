package results

import (
	"testing"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectorSortsByStableID(t *testing.T) {
	t.Parallel()

	collector := NewCollector()
	events := []model.Event{
		{Type: model.EventCaseStarted, TestID: "z::test"},
		{Type: model.EventCaseFinished, TestID: "z::test", Status: "pass"},
		{Type: model.EventCaseStarted, TestID: "a::test"},
		{Type: model.EventCaseFinished, TestID: "a::test", Status: "fail"},
	}
	for _, event := range events {
		require.NoError(t, collector.Emit(event))
	}
	summary := collector.Summary()
	require.Len(t, summary.Cases, 2)
	assert.Equal(t, "a::test", summary.Cases[0].ID)
	assert.Equal(t, 1, summary.Passed)
	assert.Equal(t, 1, summary.Failed)
}

func TestCollectorFoldsDiscoveryDiagnosticsAsInfrastructure(t *testing.T) {
	t.Parallel()
	collector := NewCollector()
	events := []model.Event{
		{Type: model.EventRunStarted, RunID: "run"},
		{Type: model.EventWorkerError, RunID: "run", Name: "worker.discovery", Message: "invalid provider"},
		{Type: model.EventRunFinished, RunID: "run", Status: "error"},
	}
	for _, event := range events {
		require.NoError(t, collector.Emit(event))
	}
	summary := collector.Summary()
	assert.Equal(t, 1, summary.Errored)
	assert.True(t, summary.HasInfrastructureFailure())
	assert.True(t, summary.HasFailures())
	assert.Len(t, summary.Infrastructure, 1)
}

func TestCollectorCountsRunErrorWithoutDiagnostic(t *testing.T) {
	t.Parallel()
	collector := NewCollector()
	require.NoError(t, collector.Emit(model.Event{Type: model.EventRunFinished, Status: "error"}))
	summary := collector.Summary()
	assert.Equal(t, 1, summary.Errored)
	assert.False(t, summary.HasInfrastructureFailure())
}
