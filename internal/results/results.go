// Package results folds immutable events once into deterministic reports.
package results

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/cafecito-games/gdtest/internal/model"
)

// CaseResult is one terminal case result plus its streamed diagnostics.
type CaseResult struct {
	ID         string          `json:"id"`
	Name       string          `json:"name,omitempty"`
	Status     string          `json:"status"`
	File       string          `json:"file,omitempty"`
	Line       int             `json:"line,omitempty"`
	Message    string          `json:"message,omitempty"`
	Duration   time.Duration   `json:"duration_ns,omitempty"`
	Failures   []model.Event   `json:"failures,omitempty"`
	Warnings   []model.Event   `json:"warnings,omitempty"`
	Output     []model.Event   `json:"output,omitempty"`
	Assertions int             `json:"assertions,omitempty"`
	Messages   []string        `json:"messages,omitempty"`
	Outcomes   []model.Outcome `json:"outcomes,omitempty"`
}

// Summary is a deterministic folded run result.
type Summary struct {
	Cases          []CaseResult    `json:"cases"`
	Failures       []model.Event   `json:"failures,omitempty"`
	Warnings       []model.Event   `json:"warnings,omitempty"`
	Output         []model.Event   `json:"output,omitempty"`
	Errors         []model.Event   `json:"errors,omitempty"`
	Infrastructure []model.Event   `json:"infrastructure,omitempty"`
	Passed         int             `json:"passed"`
	Failed         int             `json:"failed"`
	Skipped        int             `json:"skipped"`
	Todo           int             `json:"todo"`
	Void           int             `json:"void"`
	Errored        int             `json:"errored"`
	Canceled       int             `json:"canceled"`
	Stale          bool            `json:"stale,omitempty"`
	PolicyFailures []PolicyFailure `json:"policy_failures,omitempty"`
}

// OutcomePolicy controls which otherwise non-failing terminal outcomes fail a run.
type OutcomePolicy struct {
	FailOnSkip bool
	FailOnTodo bool
	FailOnVoid bool
}

// PolicyFailure records one enabled strict outcome and its observed count.
type PolicyFailure struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// Collector safely folds an event stream without reserializing accumulated state.
type Collector struct {
	mu             sync.Mutex
	cases          map[string]*CaseResult
	order          []string
	stale          bool
	started        bool
	ended          bool
	failures       []model.Event
	warnings       []model.Event
	output         []model.Event
	errors         []model.Event
	infrastructure []model.Event
	runStatus      string
	preferred      map[string]int
}

// NewCollector creates an empty collector.
func NewCollector() *Collector { return &Collector{cases: make(map[string]*CaseResult)} }

// NewCollectorWithOrder creates a collector whose final cases follow the
// selected manifest order even when files execute concurrently.
func NewCollectorWithOrder(ids []string) *Collector {
	collector := NewCollector()
	collector.preferred = make(map[string]int, len(ids))
	for index, id := range ids {
		if _, exists := collector.preferred[id]; !exists {
			collector.preferred[id] = index
		}
	}
	return collector
}

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
		result := &CaseResult{ID: event.TestID, Name: event.Name}
		applyCaseLocation(result, event.Location)
		c.cases[event.TestID] = result
		c.order = append(c.order, event.TestID)
	case model.EventAssertionFail:
		if event.TestID == "" {
			c.failures = append(c.failures, event)
		} else {
			result := c.ensureCase(event.TestID)
			result.Failures = append(result.Failures, event)
		}
	case model.EventCaseWarning:
		if event.TestID == "" {
			c.warnings = append(c.warnings, event)
		} else {
			result := c.ensureCase(event.TestID)
			result.Warnings = append(result.Warnings, event)
		}
	case model.EventOutput:
		if event.TestID == "" {
			c.output = append(c.output, event)
		} else {
			result := c.ensureCase(event.TestID)
			result.Output = append(result.Output, event)
		}
	case model.EventCaseFinished:
		result := c.ensureCase(event.TestID)
		result.Status = event.Status
		result.Assertions = event.Assertions
		result.Duration = event.Duration
		if event.Message != "" {
			result.Message = event.Message
		}
		applyCaseLocation(result, event.Location)
		result.Messages = append([]string(nil), event.Messages...)
		result.Outcomes = append([]model.Outcome(nil), event.Outcomes...)
		if event.Name != "" {
			result.Name = event.Name
		}
	case model.EventRunFinished:
		if c.ended {
			return errors.New("duplicate run_finished event")
		}
		c.ended = true
		c.stale = event.Stale
		c.runStatus = event.Status
	case model.EventWorkerError:
		if isFrameworkError(event) {
			c.errors = append(c.errors, event)
		} else {
			c.infrastructure = append(c.infrastructure, event)
		}
	}
	return nil
}

func isFrameworkError(event model.Event) bool {
	if event.Name == "worker.framework" || event.Assertion == "framework" {
		return true
	}
	if event.Metadata != nil {
		if code, ok := event.Metadata["code"].(string); ok && code == "worker.framework" {
			return true
		}
	}
	return false
}

func applyCaseLocation(result *CaseResult, location *model.Location) {
	if location == nil {
		return
	}
	if result.File == "" && location.File != "" {
		result.File = location.File
	}
	if result.Line == 0 && location.Line > 0 {
		result.Line = location.Line
	}
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

// Summary returns cases in manifest/execution order.
func (c *Collector) Summary() Summary {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := append([]string(nil), c.order...)
	if len(c.preferred) > 0 {
		sort.SliceStable(ids, func(i, j int) bool {
			left, leftKnown := c.preferred[ids[i]]
			right, rightKnown := c.preferred[ids[j]]
			if leftKnown != rightKnown {
				return leftKnown
			}
			if leftKnown && left != right {
				return left < right
			}
			return ids[i] < ids[j]
		})
	}
	summary := Summary{
		Cases:          make([]CaseResult, 0, len(ids)),
		Failures:       append([]model.Event(nil), c.failures...),
		Warnings:       append([]model.Event(nil), c.warnings...),
		Output:         append([]model.Event(nil), c.output...),
		Errors:         append([]model.Event(nil), c.errors...),
		Failed:         len(c.failures),
		Stale:          c.stale,
		Infrastructure: append([]model.Event(nil), c.infrastructure...),
		Errored:        len(c.infrastructure) + len(c.errors),
	}
	for _, id := range ids {
		result := *c.cases[id]
		result.Failures = append([]model.Event(nil), result.Failures...)
		result.Warnings = append([]model.Event(nil), result.Warnings...)
		result.Output = append([]model.Event(nil), result.Output...)
		result.Messages = append([]string(nil), result.Messages...)
		result.Outcomes = append([]model.Outcome(nil), result.Outcomes...)
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
	if summary.Errored == 0 && c.runStatus == "error" {
		summary.Errored = 1
	}
	if summary.Failed == 0 && summary.Errored == 0 && c.runStatus == "fail" {
		summary.Failed = 1
	}
	if c.runStatus == "canceled" && summary.Canceled == 0 {
		summary.Canceled = 1
	}
	return summary
}

// ApplyOutcomePolicy records enabled strict outcomes without changing their
// underlying case statuses or double-counting them as assertion failures.
func (s *Summary) ApplyOutcomePolicy(policy OutcomePolicy) {
	s.PolicyFailures = s.PolicyFailures[:0]
	if policy.FailOnSkip && s.Skipped > 0 {
		s.PolicyFailures = append(s.PolicyFailures, PolicyFailure{Status: "skip", Count: s.Skipped})
	}
	if policy.FailOnTodo && s.Todo > 0 {
		s.PolicyFailures = append(s.PolicyFailures, PolicyFailure{Status: "todo", Count: s.Todo})
	}
	if policy.FailOnVoid && s.Void > 0 {
		s.PolicyFailures = append(s.PolicyFailures, PolicyFailure{Status: "void", Count: s.Void})
	}
}

// HasFailures reports whether the run has a failing case or enabled
// strict-policy status. Infrastructure failures are reported separately.
func (s Summary) HasFailures() bool {
	return s.Failed+s.Errored > 0 || len(s.PolicyFailures) > 0
}

// HasInfrastructureFailure reports whether discovery, protocol, or worker
// diagnostics made the run non-authoritative independently of case failures.
func (s Summary) HasInfrastructureFailure() bool {
	return len(s.Infrastructure) > 0
}
