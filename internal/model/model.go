// Package model defines backend-neutral test plans and immutable result events.
package model

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ProtocolVersion is the serialized plan and event schema version.
const ProtocolVersion = 4

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LeakCheck carries backend-neutral post-scope node leak policy and exemptions.
type LeakCheck struct {
	Policy string   `json:"policy,omitempty"`
	Ignore []string `json:"ignore,omitempty"`
}

// Validate rejects unsupported leak policies and non-neutral or duplicate ignore entries.
func (l LeakCheck) Validate() error {
	switch l.Policy {
	case "", "fail", "warn", "off":
	default:
		return fmt.Errorf("leak_check.policy %q must be fail, warn, or off", l.Policy)
	}
	seen := make(map[string]int, len(l.Ignore))
	for index, entry := range l.Ignore {
		if err := ValidateLeakIgnoreEntry(index, entry); err != nil {
			return err
		}
		if first, exists := seen[entry]; exists {
			return fmt.Errorf("leak_check.ignore[%d]: %q duplicates leak_check.ignore[%d]", index, entry, first)
		}
		seen[entry] = index
	}
	return nil
}

// ValidateLeakIgnoreEntry checks that one allowlist entry is a clean res://.gd
// path or a valid GDScript identifier, never an absolute or worktree path.
func ValidateLeakIgnoreEntry(index int, entry string) error {
	location := fmt.Sprintf("leak_check.ignore[%d]", index)
	if entry == "" {
		return fmt.Errorf("%s: must not be empty", location)
	}
	if strings.HasPrefix(entry, "res://") {
		if strings.TrimSpace(entry) != entry {
			return fmt.Errorf("%s: %q must not begin or end with whitespace", location, entry)
		}
		if strings.ContainsFunc(entry, func(character rune) bool {
			return character == '"' || character < 0x20 || character == 0x7f
		}) {
			return fmt.Errorf("%s: %q must not contain a double quote or control character", location, entry)
		}
		if strings.Contains(entry, `\`) {
			return fmt.Errorf("%s: %q must use forward slashes", location, entry)
		}
		relative := strings.TrimPrefix(entry, "res://")
		if relative == "" || strings.HasPrefix(relative, "/") {
			return fmt.Errorf("%s: %q must be a clean res:// script path", location, entry)
		}
		if path.Clean(relative) != relative || relative == "." || slices.Contains(strings.Split(relative, "/"), "..") {
			return fmt.Errorf("%s: %q must be a clean res:// script path without parent segments", location, entry)
		}
		if !strings.HasSuffix(relative, ".gd") || path.Base(relative) == ".gd" {
			return fmt.Errorf("%s: %q must name a .gd script", location, entry)
		}
		return nil
	}
	if !identifierPattern.MatchString(entry) {
		return fmt.Errorf("%s: %q must be a res://.gd path or a valid GDScript identifier", location, entry)
	}
	return nil
}

// TestPlan is independent of absolute worktree paths and concrete executors.
type TestPlan struct {
	ProtocolVersion    int           `json:"protocol_version"`
	RunID              string        `json:"run_id"`
	Generation         uint64        `json:"generation"`
	CaseTimeout        time.Duration `json:"case_timeout_ns,omitempty"`
	ContentFingerprint string        `json:"content_fingerprint,omitempty"`
	OnlyMode           string        `json:"only_mode,omitempty"`
	LeakCheck          LeakCheck     `json:"leak_check,omitzero"`
	SelectionApplied   bool          `json:"selection_applied,omitempty"`
	SelectedIDs        []string      `json:"selected_ids,omitempty"`
	HasFocus           *bool         `json:"has_focus,omitempty"`
	Files              []FilePlan    `json:"files"`
}

// FilePlan is one serial file-level scheduling unit.
type FilePlan struct {
	Path    string     `json:"path"`
	CaseIDs []string   `json:"case_ids,omitempty"`
	Cases   []CasePlan `json:"cases"`
}

// MarshalJSON preserves an explicit empty static-case list so a backend can
// distinguish a dynamic-only file from a legacy plan that omitted discovery,
// and preserves an explicit empty case_ids subset as a legal empty shard.
func (f FilePlan) MarshalJSON() ([]byte, error) {
	cases := f.Cases
	if cases == nil {
		cases = []CasePlan{}
	}
	var caseIDs *[]string
	if f.CaseIDs != nil {
		caseIDs = &f.CaseIDs
	}
	return json.Marshal(struct {
		Path    string     `json:"path"`
		CaseIDs *[]string  `json:"case_ids,omitempty"`
		Cases   []CasePlan `json:"cases"`
	}{Path: f.Path, CaseIDs: caseIDs, Cases: cases})
}

// ValidateCaseIDs rejects empty entries and duplicates within one file
// entry's explicit case subset; both would make shard execution ambiguous.
func (f FilePlan) ValidateCaseIDs() error {
	seen := make(map[string]int, len(f.CaseIDs))
	for index, id := range f.CaseIDs {
		if id == "" {
			return fmt.Errorf("file %q case_ids[%d]: case ID must not be empty", f.Path, index)
		}
		if first, exists := seen[id]; exists {
			return fmt.Errorf("file %q case_ids[%d]: %q duplicates case_ids[%d]", f.Path, index, id, first)
		}
		seen[id] = index
	}
	return nil
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
	EventCaseWarning   EventType = "case_warning"
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
