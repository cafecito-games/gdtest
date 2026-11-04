package results

import (
	"testing"

	"github.com/cafecito-games/gdtest/internal/model"
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
		if err := collector.Emit(event); err != nil {
			t.Fatalf("Emit: %v", err)
		}
	}
	summary := collector.Summary()
	if summary.Cases[0].ID != "a::test" || summary.Passed != 1 || summary.Failed != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}
