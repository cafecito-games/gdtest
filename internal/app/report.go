package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/reporter"
	"github.com/cafecito-games/gdtest/internal/results"
)

type reportConfig struct {
	stdoutFormat string
	fileFormat   string
	filePath     string
	verbose      bool
}

func resolveReportConfig(options runOptions) (reportConfig, error) {
	if options.json && options.reportFormat != "" {
		return reportConfig{}, errors.New("--json and --report-format are mutually exclusive")
	}
	requestedFormat := options.reportFormat
	if options.json {
		requestedFormat = reporter.FormatJSONL
	}
	if requestedFormat != "" {
		if err := reporter.ValidateFormat(requestedFormat); err != nil {
			return reportConfig{}, err
		}
	}
	if options.reportFile != "" {
		if requestedFormat == "" {
			return reportConfig{}, errors.New("--report-file requires --report-format")
		}
		if info, err := os.Stat(options.reportFile); err == nil && info.IsDir() {
			return reportConfig{}, fmt.Errorf("report file %q is a directory", options.reportFile)
		}
		return reportConfig{
			stdoutFormat: reporter.FormatTerminal,
			fileFormat:   requestedFormat,
			filePath:     options.reportFile,
			verbose:      options.verbose,
		}, nil
	}
	stdoutFormat := requestedFormat
	if stdoutFormat == "" {
		stdoutFormat = reporter.FormatTerminal
	}
	return reportConfig{
		stdoutFormat: stdoutFormat,
		verbose:      options.verbose,
	}, nil
}

func (c reportConfig) writeEmptySelection(out io.Writer, message string) error {
	if err := renderEmptySelection(out, c.stdoutFormat, message); err != nil {
		return err
	}
	if c.filePath == "" {
		return nil
	}
	var buffer bytes.Buffer
	if err := renderEmptySelection(&buffer, c.fileFormat, message); err != nil {
		return err
	}
	return writeReportFileAtomic(c.filePath, buffer.Bytes())
}

func renderEmptySelection(writer io.Writer, format string, message string) error {
	switch format {
	case reporter.FormatJSONL:
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(struct {
			Type    string `json:"type"`
			Passed  int    `json:"passed"`
			Message string `json:"message"`
		}{
			Type:    "summary",
			Passed:  0,
			Message: message,
		})
	case reporter.FormatJUnit:
		return reporter.NewJUnit(writer).Summary(results.Summary{})
	default:
		_, err := fmt.Fprintln(writer, message)
		return err
	}
}

func (c reportConfig) writeManifestErrors(out io.Writer, findings *manifestErrors) error {
	events := make([]model.Event, 0, len(findings.errors))
	for _, finding := range findings.errors {
		events = append(events, model.Event{
			ProtocolVersion: model.ProtocolVersion,
			Type:            model.EventWorkerError,
			Name:            finding.Code,
			Message:         finding.Message,
			Location:        finding.Location,
		})
	}
	summary := results.Summary{
		Cases:          []results.CaseResult{},
		Infrastructure: events,
		Errored:        len(events),
	}
	if err := renderManifestErrors(out, c.stdoutFormat, findings, events, summary); err != nil {
		return err
	}
	if c.filePath == "" {
		return nil
	}
	var buffer bytes.Buffer
	if err := renderManifestErrors(&buffer, c.fileFormat, findings, events, summary); err != nil {
		return err
	}
	return writeReportFileAtomic(c.filePath, buffer.Bytes())
}

func renderManifestErrors(
	writer io.Writer,
	format string,
	findings *manifestErrors,
	events []model.Event,
	summary results.Summary,
) error {
	switch format {
	case reporter.FormatJSONL:
		machine := reporter.NewJSONL(writer)
		for _, event := range events {
			if err := machine.Emit(event); err != nil {
				return err
			}
		}
		return machine.Summary(summary)
	case reporter.FormatJUnit:
		return reporter.NewJUnit(writer).Summary(summary)
	default:
		return findings.writeTo(writer)
	}
}

type summaryReporter interface {
	executor.EventSink
	Summary(results.Summary) error
}

type reportSession struct {
	config         reportConfig
	stdoutReporter summaryReporter
	fileBuffer     bytes.Buffer
	fileReporter   summaryReporter
}

func (c reportConfig) newSession(out io.Writer) *reportSession {
	session := &reportSession{
		config:         c,
		stdoutReporter: buildReporter(out, c.stdoutFormat, c.verbose),
	}
	if c.filePath != "" {
		session.fileReporter = buildReporter(&session.fileBuffer, c.fileFormat, c.verbose)
	}
	return session
}

func buildReporter(writer io.Writer, format string, verbose bool) summaryReporter {
	switch format {
	case reporter.FormatJSONL:
		return reporter.NewJSONL(writer)
	case reporter.FormatJUnit:
		return reporter.NewJUnit(writer)
	default:
		return reporter.NewTerminalWithOptions(writer, verbose)
	}
}

func (s *reportSession) Emit(event model.Event) error {
	if err := s.stdoutReporter.Emit(event); err != nil {
		return err
	}
	if s.fileReporter != nil {
		return s.fileReporter.Emit(event)
	}
	return nil
}

func (s *reportSession) Summary(summary results.Summary) error {
	if err := s.stdoutReporter.Summary(summary); err != nil {
		return err
	}
	if s.fileReporter != nil {
		return s.fileReporter.Summary(summary)
	}
	return nil
}

func (s *reportSession) CompleteTimingsAndPublish(streams IO, timings map[string]time.Duration) error {
	if s.config.stdoutFormat == reporter.FormatJSONL {
		if err := writeTimings(streams, timings, true); err != nil {
			return fmt.Errorf("timings: %w", err)
		}
	} else if s.config.verbose {
		if err := writeTimings(streams, timings, false); err != nil {
			return fmt.Errorf("timings: %w", err)
		}
	}
	if s.config.filePath == "" {
		return nil
	}
	if s.config.fileFormat == reporter.FormatJSONL {
		if err := writeTimings(IO{Out: &s.fileBuffer}, timings, true); err != nil {
			return fmt.Errorf("timings: %w", err)
		}
	}
	return writeReportFileAtomic(s.config.filePath, s.fileBuffer.Bytes())
}

func writeTimings(streams IO, timings map[string]time.Duration, machine bool) error {
	if machine {
		return encodeJSONTimings(streams.Out, timings)
	}
	return writeHumanTimings(streams.Err, timings)
}

func sortedTimingNames(timings map[string]time.Duration) []string {
	names := make([]string, 0, len(timings))
	for name := range timings {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func encodeJSONTimings(writer io.Writer, timings map[string]time.Duration) error {
	names := sortedTimingNames(timings)
	values := make(map[string]int64, len(timings))
	for _, name := range names {
		values[name] = timings[name].Nanoseconds()
	}
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(map[string]any{"type": "timings", "timings_ns": values})
}

func writeHumanTimings(writer io.Writer, timings map[string]time.Duration) error {
	for _, name := range sortedTimingNames(timings) {
		if _, err := fmt.Fprintf(writer, "gdtest: timing %s=%s\n", name, timings[name]); err != nil {
			return err
		}
	}
	return nil
}

func writeReportFileAtomic(filename string, content []byte) error {
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".gdtest-report-*")
	if err != nil {
		return fmt.Errorf("create temporary report file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set report file permissions: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write report file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync report file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close report file: %w", err)
	}
	if err := os.Rename(temporaryName, filename); err != nil {
		return fmt.Errorf("publish report file: %w", err)
	}
	return nil
}
