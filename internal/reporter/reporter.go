// Package reporter renders backend-neutral event streams and summaries.
package reporter

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/results"
)

// JSONL writes exactly one event per line with no decorative output.
type JSONL struct{ encoder *json.Encoder }

// NewJSONL creates a machine reporter.
func NewJSONL(writer io.Writer) *JSONL {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return &JSONL{encoder: encoder}
}

// Emit writes an immutable event immediately.
func (r *JSONL) Emit(event model.Event) error { return r.encoder.Encode(event) }

// Summary writes the final aggregate as one JSONL object.
func (r *JSONL) Summary(summary results.Summary) error {
	return r.encoder.Encode(struct {
		Type    string          `json:"type"`
		Summary results.Summary `json:"summary"`
	}{Type: "summary", Summary: summary})
}

// Terminal prints failures as they arrive and one concise summary.
type Terminal struct {
	writer  io.Writer
	verbose bool
	output  map[string][]model.Event
}

// NewTerminal creates a terminal reporter.
func NewTerminal(writer io.Writer) *Terminal {
	return NewTerminalWithOptions(writer, false)
}

// NewTerminalWithOptions creates a terminal reporter with optional passing-case detail.
func NewTerminalWithOptions(writer io.Writer, verbose bool) *Terminal {
	return &Terminal{writer: writer, verbose: verbose, output: make(map[string][]model.Event)}
}

// Emit prints immediate failure and infrastructure diagnostics.
func (r *Terminal) Emit(event model.Event) error {
	switch event.Type {
	case model.EventAssertionFail:
		location := formatLocation(event.Location)
		details := diagnosticValues(event)
		_, err := fmt.Fprintf(r.writer, "FAIL %s%s: %s%s\n", event.TestID, location, event.Message, details)
		return err
	case model.EventOutput:
		if r.verbose {
			return r.writeOutput(event)
		}
		r.output[event.TestID] = append(r.output[event.TestID], event)
		return nil
	case model.EventCaseFinished:
		if r.verbose || event.Status == "fail" || event.Status == "error" {
			for _, text := range event.Messages {
				if text == "" && !r.verbose {
					continue
				}
				if _, err := fmt.Fprintf(r.writer, "MESSAGE %s: %s\n", event.TestID, text); err != nil {
					return err
				}
			}
			for _, output := range r.output[event.TestID] {
				if err := r.writeOutput(output); err != nil {
					return err
				}
			}
		}
		delete(r.output, event.TestID)
		return nil
	case model.EventWorkerError:
		location := formatLocation(event.Location)
		_, err := fmt.Fprintf(r.writer, "ERROR %s%s: %s\n", event.Name, location, event.Message)
		return err
	default:
		return nil
	}
}

func formatLocation(location *model.Location) string {
	if location == nil || location.File == "" {
		return ""
	}
	if location.Line > 0 {
		return fmt.Sprintf(" (%s:%d)", location.File, location.Line)
	}
	return fmt.Sprintf(" (%s)", location.File)
}

// Summary prints counts in deterministic order.
func (r *Terminal) Summary(summary results.Summary) error {
	if _, err := fmt.Fprintf(
		r.writer,
		"%d passed, %d failed, %d errors, %d skipped, %d todo, %d void, %d canceled\n",
		summary.Passed,
		summary.Failed,
		summary.Errored,
		summary.Skipped,
		summary.Todo,
		summary.Void,
		summary.Canceled,
	); err != nil {
		return err
	}
	if len(summary.PolicyFailures) == 0 {
		return nil
	}
	violations := make([]string, 0, len(summary.PolicyFailures))
	for _, failure := range summary.PolicyFailures {
		violations = append(violations, fmt.Sprintf("%d %s", failure.Count, failure.Status))
	}
	_, err := fmt.Fprintf(r.writer, "strict policy failed: %s\n", strings.Join(violations, ", "))
	return err
}

func (r *Terminal) writeOutput(event model.Event) error {
	text := event.Message
	if text == "" && event.Metadata != nil {
		if value, ok := event.Metadata["text"].(string); ok {
			text = value
		}
	}
	_, err := fmt.Fprintf(r.writer, "OUTPUT %s: %s\n", event.TestID, text)
	return err
}

func diagnosticValues(event model.Event) string {
	values := make([]string, 0, 2)
	if event.Expected != nil {
		values = append(values, "expected="+wireDisplay(*event.Expected))
	}
	if event.Actual != nil {
		values = append(values, "actual="+wireDisplay(*event.Actual))
	}
	if len(values) == 0 {
		return ""
	}
	return " (" + strings.Join(values, ", ") + ")"
}

func wireDisplay(value model.WireValue) string {
	if value.Display != "" {
		return value.Display
	}
	data, err := json.Marshal(value.Value)
	if err != nil {
		return value.Type
	}
	return string(data)
}
