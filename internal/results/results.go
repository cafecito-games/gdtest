// Package results folds immutable events once into deterministic reports.
package results

import (
	"errors"
	"sort"
	"sync"

	"github.com/cafecito-games/gdtest/internal/model"
)

// CaseResult is one terminal case result plus its streamed diagnostics.
type CaseResult struct {
	ID       string
	Name     string
	Status   string
	File     string
	Line     int
	Failures []model.Event
	Output   []model.Event
}

// Summary is a deterministic folded run result.
type Summary struct {
	Cases    []CaseResult
	Passed   int
	Failed   int
	Skipped  int
	Todo     int
	Void     int
	Errored  int
	Canceled int
	Stale    bool
}

// Collector safely folds an event stream without reserializing accumulated state.
type Collector struct {
	mu      sync.Mutex
	cases   map[string]*CaseResult
	order   []string
	stale   bool
	started bool
	ended   bool
}

// NewCollector creates an empty collector.
func NewCollector() *Collector { return &Collector{cases: make(map[string]*CaseResult)} }

// Emit folds one event.
func (c *Collector) Emit(event model.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch event.Type {
	case model.EventRunStarted:
		if c.started {
			return errors.New("duplicate run_started event")
		}
		c.started = true
	case model.EventCaseStarted:
		if _, exists := c.cases[event.TestID]; exists {
			return errors.New("duplicate case_started event for " + event.TestID)
		}
		c.cases[event.TestID] = &CaseResult{ID: event.TestID, Name: event.Name}
		c.order = append(c.order, event.TestID)
	case model.EventAssertionFail:
		result := c.ensureCase(event.TestID)
		result.Failures = append(result.Failures, event)
	case model.EventOutput:
		result := c.ensureCase(event.TestID)
		result.Output = append(result.Output, event)
	case model.EventCaseFinished:
		result := c.ensureCase(event.TestID)
		result.Status = event.Status
		if event.Name != "" {
			result.Name = event.Name
		}
	case model.EventRunFinished:
		if c.ended {
			return errors.New("duplicate run_finished event")
		}
		c.ended = true
		c.stale = event.Stale
	}
	return nil
}

func (c *Collector) ensureCase(id string) *CaseResult {
	if result, exists := c.cases[id]; exists {
		return result
	}
	result := &CaseResult{ID: id}
	c.cases[id] = result
	c.order = append(c.order, id)
	return result
}

// Summary returns cases sorted by stable ID, independent of execution order.
func (c *Collector) Summary() Summary {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := append([]string(nil), c.order...)
	sort.Strings(ids)
	summary := Summary{Cases: make([]CaseResult, 0, len(ids)), Stale: c.stale}
	for _, id := range ids {
		result := *c.cases[id]
		result.Failures = append([]model.Event(nil), result.Failures...)
		result.Output = append([]model.Event(nil), result.Output...)
		summary.Cases = append(summary.Cases, result)
		switch result.Status {
		case "pass":
			summary.Passed++
		case "fail":
			summary.Failed++
		case "skip":
			summary.Skipped++
		case "todo":
			summary.Todo++
		case "void":
			summary.Void++
		case "error":
			summary.Errored++
		case "canceled":
			summary.Canceled++
		}
	}
	return summary
}

// HasFailures reports whether the run has a failing or infrastructure status.
func (s Summary) HasFailures() bool { return s.Failed+s.Errored > 0 }
