// Package model defines backend-neutral test plans and immutable result events.
package model

import (
	"encoding/json"
	"time"
)

// ProtocolVersion is the serialized plan and event schema version.
const ProtocolVersion = 1

// TestPlan is independent of absolute worktree paths and concrete executors.
type TestPlan struct {
	ProtocolVersion    int           `json:"protocol_version"`
	RunID              string        `json:"run_id"`
	Generation         uint64        `json:"generation"`
	CaseTimeout        time.Duration `json:"case_timeout_ns,omitempty"`
	ContentFingerprint string        `json:"content_fingerprint,omitempty"`
	OnlyMode           string        `json:"only_mode,omitempty"`
	SelectionApplied   bool          `json:"selection_applied,omitempty"`
	SelectedIDs        []string      `json:"selected_ids,omitempty"`
	Files              []FilePlan    `json:"files"`
}

// FilePlan is one serial file-level scheduling unit.
type FilePlan struct {
	Path  string     `json:"path"`
	Cases []CasePlan `json:"cases"`
}

// MarshalJSON preserves an explicit empty static-case list so a backend can
// distinguish a dynamic-only file from a legacy plan that omitted discovery.
func (f FilePlan) MarshalJSON() ([]byte, error) {
	cases := f.Cases
	if cases == nil {
		cases = []CasePlan{}
	}
	return json.Marshal(struct {
		Path  string     `json:"path"`
		Cases []CasePlan `json:"cases"`
	}{Path: f.Path, Cases: cases})
}

// CasePlan identifies a statically discovered method test.
type CasePlan struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	MethodName string `json:"method_name"`
	Line       int    `json:"line"`
	IsOnly     bool   `json:"is_only,omitempty"`
}

// DiscoveryManifest is a deterministic, backend-neutral description of all
// cases registered by one plan. It contains no Godot objects or callables.
type DiscoveryManifest struct {
	ProtocolVersion    int              `json:"protocol_version"`
	Generation         uint64           `json:"generation"`
	ContentFingerprint string           `json:"content_fingerprint,omitempty"`
	Files              []DiscoveryFile  `json:"files"`
	Errors             []DiscoveryError `json:"errors,omitempty"`
}

// DiscoveryFile describes one root suite in plan order.
type DiscoveryFile struct {
	Path  string          `json:"path"`
	Name  string          `json:"name"`
	Line  int             `json:"line"`
	Cases []DiscoveryCase `json:"cases"`
}

// DiscoveryCase is one dynamically completed or statically parameterized
// case. Parameters use the backend-neutral wire-value representation.
type DiscoveryCase struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	File          string           `json:"file"`
	Line          int              `json:"line"`
	MethodName    string           `json:"method_name,omitempty"`
	Parameters    []WireValue      `json:"parameters,omitempty"`
	ParameterName string           `json:"parameter_name,omitempty"`
	IsOnly        bool             `json:"is_only,omitempty"`
	Suites        []DiscoverySuite `json:"suites,omitempty"`
}

// DiscoverySuite is immutable nested-suite metadata.
type DiscoverySuite struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	IsOnly bool   `json:"is_only,omitempty"`
}

// DiscoveryError is a source-located non-executable manifest finding.
type DiscoveryError struct {
	Code     string    `json:"code"`
	Message  string    `json:"message"`
	Location *Location `json:"location,omitempty"`
}

// EventType identifies one immutable execution event.
type EventType string

// Event types shared by every executor and reporter.
const (
	EventWorkerReady   EventType = "worker_ready"
	EventRunStarted    EventType = "run_started"
	EventSuiteStarted  EventType = "suite_started"
	EventCaseStarted   EventType = "case_started"
	EventAssertionFail EventType = "assertion_failed"
	EventOutput        EventType = "output"
	EventCaseFinished  EventType = "case_finished"
	EventSuiteFinished EventType = "suite_finished"
	EventRunFinished   EventType = "run_finished"
	EventWorkerTainted EventType = "worker_tainted"
	EventWorkerError   EventType = "worker_error"
)

// Location is a project-relative source location.
type Location struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column,omitempty"`
}

// WireValue preserves diagnostic values without binding Go to Godot Variant.
type WireValue struct {
	Type      string               `json:"type"`
	Value     any                  `json:"value,omitempty"`
	Display   string               `json:"display,omitempty"`
	Items     []WireValue          `json:"items,omitempty"`
	Entries   []WireDictionaryItem `json:"entries,omitempty"`
	Truncated bool                 `json:"truncated,omitempty"`
}

// WireDictionaryItem represents one dictionary entry without stringifying keys.
type WireDictionaryItem struct {
	Key   WireValue `json:"key"`
	Value WireValue `json:"value"`
}

// Outcome is structured data attached by ok, fail, skip, or todo.
type Outcome struct {
	Kind    string    `json:"kind"`
	Message string    `json:"message,omitempty"`
	Data    WireValue `json:"data"`
}

// Event is the stable, backend-neutral event envelope.
type Event struct {
	ProtocolVersion int            `json:"protocol_version"`
	Type            EventType      `json:"type"`
	RunID           string         `json:"run_id,omitempty"`
	TestID          string         `json:"test_id,omitempty"`
	Name            string         `json:"name,omitempty"`
	Assertion       string         `json:"assertion,omitempty"`
	Status          string         `json:"status,omitempty"`
	Message         string         `json:"message,omitempty"`
	Location        *Location      `json:"location,omitempty"`
	Actual          *WireValue     `json:"actual,omitempty"`
	Expected        *WireValue     `json:"expected,omitempty"`
	Assertions      int            `json:"assertions,omitempty"`
	Messages        []string       `json:"messages,omitempty"`
	Outcomes        []Outcome      `json:"outcomes,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Duration        time.Duration  `json:"duration_ns,omitempty"`
	Generation      uint64         `json:"generation,omitempty"`
	Stale           bool           `json:"stale,omitempty"`
}
