// Package model defines backend-neutral test plans and immutable result events.
package model

import "time"

// ProtocolVersion is the serialized plan and event schema version.
const ProtocolVersion = 1

// TestPlan is independent of absolute worktree paths and concrete executors.
type TestPlan struct {
	ProtocolVersion int        `json:"protocol_version"`
	RunID           string     `json:"run_id"`
	Generation      uint64     `json:"generation"`
	Files           []FilePlan `json:"files"`
}

// FilePlan is one serial file-level scheduling unit.
type FilePlan struct {
	Path  string     `json:"path"`
	Cases []CasePlan `json:"cases,omitempty"`
}

// CasePlan identifies a statically discovered method test.
type CasePlan struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	MethodName string `json:"method_name"`
	Line       int    `json:"line"`
	IsOnly     bool   `json:"is_only,omitempty"`
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
	Type    string               `json:"type"`
	Value   any                  `json:"value,omitempty"`
	Display string               `json:"display,omitempty"`
	Items   []WireValue          `json:"items,omitempty"`
	Entries []WireDictionaryItem `json:"entries,omitempty"`
}

// WireDictionaryItem represents one dictionary entry without stringifying keys.
type WireDictionaryItem struct {
	Key   WireValue `json:"key"`
	Value WireValue `json:"value"`
}

// Event is the stable, backend-neutral event envelope.
type Event struct {
	ProtocolVersion int            `json:"protocol_version"`
	Type            EventType      `json:"type"`
	RunID           string         `json:"run_id,omitempty"`
	TestID          string         `json:"test_id,omitempty"`
	Name            string         `json:"name,omitempty"`
	Status          string         `json:"status,omitempty"`
	Message         string         `json:"message,omitempty"`
	Location        *Location      `json:"location,omitempty"`
	Actual          *WireValue     `json:"actual,omitempty"`
	Expected        *WireValue     `json:"expected,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	Duration        time.Duration  `json:"duration_ns,omitempty"`
	Generation      uint64         `json:"generation,omitempty"`
	Stale           bool           `json:"stale,omitempty"`
}
