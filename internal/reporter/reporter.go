// Package reporter renders backend-neutral event streams and summaries.
package reporter

import (
	"encoding/json"
	"fmt"
	"io"

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
type Terminal struct{ writer io.Writer }

// NewTerminal creates a terminal reporter.
func NewTerminal(writer io.Writer) *Terminal { return &Terminal{writer: writer} }

// Emit prints immediate failure and infrastructure diagnostics.
func (r *Terminal) Emit(event model.Event) error {
	switch event.Type {
	case model.EventAssertionFail:
		location := ""
		if event.Location != nil && event.Location.File != "" {
			location = fmt.Sprintf(" (%s:%d)", event.Location.File, event.Location.Line)
		}
		_, err := fmt.Fprintf(r.writer, "FAIL %s%s: %s\n", event.TestID, location, event.Message)
		return err
	case model.EventWorkerError:
		_, err := fmt.Fprintf(r.writer, "ERROR %s\n", event.Name)
		return err
	default:
		return nil
	}
}

// Summary prints counts in deterministic order.
func (r *Terminal) Summary(summary results.Summary) error {
	_, err := fmt.Fprintf(
		r.writer,
		"%d passed, %d failed, %d errors, %d skipped, %d todo, %d void\n",
		summary.Passed,
		summary.Failed,
		summary.Errored,
		summary.Skipped,
		summary.Todo,
		summary.Void,
	)
	return err
}
