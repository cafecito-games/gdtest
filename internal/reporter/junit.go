package reporter

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/results"
)

const defaultJUnitSuiteName = "gdtest"

// JUnit renders a folded run summary as deterministic JUnit XML.
type JUnit struct {
	writer io.Writer
}

// NewJUnit creates a JUnit XML reporter.
func NewJUnit(writer io.Writer) *JUnit {
	return &JUnit{writer: writer}
}

// Emit ignores incremental events because JUnit XML is rendered deterministically from Summary.
func (r *JUnit) Emit(model.Event) error {
	return nil
}

// Summary writes the complete JUnit XML document to the underlying writer.
func (r *JUnit) Summary(summary results.Summary) error {
	encoded, err := MarshalJUnit(summary)
	if err != nil {
		return err
	}
	_, err = r.writer.Write(encoded)
	return err
}

// MarshalJUnit encodes a folded run summary as UTF-8 JUnit XML 1.0.
func MarshalJUnit(summary results.Summary) ([]byte, error) {
	document := buildJUnitDocument(summary)
	body, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode JUnit XML: %w", err)
	}
	var buffer bytes.Buffer
	buffer.Grow(len(xml.Header) + len(body) + 1)
	buffer.WriteString(xml.Header)
	buffer.Write(body)
	buffer.WriteByte('\n')
	return buffer.Bytes(), nil
}

type junitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Name     string           `xml:"name,attr"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Errors   int              `xml:"errors,attr"`
	Skipped  int              `xml:"skipped,attr"`
	Time     string           `xml:"time,attr"`
	Suites   []junitTestSuite `xml:"testsuite,omitempty"`
}

type junitTestSuite struct {
	Name      string          `xml:"name,attr"`
	File      string          `xml:"file,attr,omitempty"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Errors    int             `xml:"errors,attr"`
	Skipped   int             `xml:"skipped,attr"`
	Time      string          `xml:"time,attr"`
	TestCases []junitTestCase `xml:"testcase,omitempty"`
	SystemOut string          `xml:"system-out,omitempty"`
	SystemErr string          `xml:"system-err,omitempty"`
}

type junitTestCase struct {
	Name       string        `xml:"name,attr"`
	Classname  string        `xml:"classname,attr"`
	File       string        `xml:"file,attr,omitempty"`
	Line       int           `xml:"line,attr,omitempty"`
	Time       string        `xml:"time,attr"`
	Assertions int           `xml:"assertions,attr,omitempty"`
	Skipped    *junitSkipped `xml:"skipped,omitempty"`
	Failure    *junitFailure `xml:"failure,omitempty"`
	Error      *junitError   `xml:"error,omitempty"`
	SystemOut  string        `xml:"system-out,omitempty"`
	SystemErr  string        `xml:"system-err,omitempty"`
}

type junitSkipped struct {
	Type    string `xml:"type,attr,omitempty"`
	Message string `xml:"message,attr,omitempty"`
	Body    string `xml:",chardata"`
}

type junitFailure struct {
	Type    string `xml:"type,attr,omitempty"`
	Message string `xml:"message,attr,omitempty"`
	Body    string `xml:",chardata"`
}

type junitError struct {
	Type    string `xml:"type,attr,omitempty"`
	Message string `xml:"message,attr,omitempty"`
	Body    string `xml:",chardata"`
}

type junitSuiteAccumulator struct {
	name     string
	file     string
	cases    []junitTestCase
	output   []string
	warnings []string
	duration time.Duration
}

func buildJUnitDocument(summary results.Summary) junitTestSuites {
	fileSuites := make(map[string]*junitSuiteAccumulator)
	manifestFiles := make([]string, 0)
	extraFiles := make([]string, 0)
	var globalSuite *junitSuiteAccumulator

	ensureSuite := func(suiteFile string) *junitSuiteAccumulator {
		if suiteFile == "" {
			if globalSuite == nil {
				globalSuite = &junitSuiteAccumulator{name: defaultJUnitSuiteName}
			}
			return globalSuite
		}
		if existing, exists := fileSuites[suiteFile]; exists {
			return existing
		}
		created := &junitSuiteAccumulator{name: suiteFile, file: suiteFile}
		fileSuites[suiteFile] = created
		extraFiles = append(extraFiles, suiteFile)
		return created
	}

	caseAndSuiteFailures := 0
	totalErrors := 0
	totalCanceledCases := 0

	for _, caseResult := range summary.Cases {
		suiteFile := resolveCaseSuiteFile(caseResult)
		var accumulator *junitSuiteAccumulator
		if suiteFile == "" {
			accumulator = ensureSuite("")
		} else {
			existing, exists := fileSuites[suiteFile]
			if !exists {
				existing = &junitSuiteAccumulator{name: suiteFile, file: suiteFile}
				fileSuites[suiteFile] = existing
				manifestFiles = append(manifestFiles, suiteFile)
			}
			accumulator = existing
		}
		testCase := buildJUnitTestCase(caseResult, accumulator.name, suiteFile)
		if testCase.Failure != nil {
			caseAndSuiteFailures++
		}
		if testCase.Error != nil {
			totalErrors++
		}
		if caseResult.Status == "canceled" {
			totalCanceledCases++
		}
		if caseResult.Duration > 0 {
			accumulator.duration += caseResult.Duration
		}
		accumulator.cases = append(accumulator.cases, testCase)
	}

	for _, event := range sortedEvents(summary.Failures) {
		suiteFile := cleanRelativeFile(locationFile(event.Location))
		accumulator := ensureSuite(suiteFile)
		accumulator.cases = append(accumulator.cases, buildSyntheticFailureCase(event, accumulator.name, suiteFile))
		caseAndSuiteFailures++
	}

	diagnosticErrors := make([]model.Event, 0, len(summary.Errors)+len(summary.Infrastructure))
	diagnosticErrors = append(diagnosticErrors, summary.Errors...)
	diagnosticErrors = append(diagnosticErrors, summary.Infrastructure...)
	for _, event := range sortedEvents(diagnosticErrors) {
		suiteFile := cleanRelativeFile(locationFile(event.Location))
		accumulator := ensureSuite(suiteFile)
		accumulator.cases = append(accumulator.cases, buildSyntheticErrorCase(event, accumulator.name, suiteFile))
		totalErrors++
	}

	for _, event := range sortedEvents(summary.Warnings) {
		suiteFile := cleanRelativeFile(locationFile(event.Location))
		accumulator := ensureSuite(suiteFile)
		accumulator.warnings = append(accumulator.warnings, formatWarningLine(event))
	}

	for _, event := range summary.Output {
		suiteFile := cleanRelativeFile(locationFile(event.Location))
		accumulator := ensureSuite(suiteFile)
		accumulator.output = append(accumulator.output, outputText(event))
	}

	if summary.Failed > 0 && caseAndSuiteFailures == 0 {
		accumulator := ensureSuite("")
		accumulator.cases = append(accumulator.cases, junitTestCase{
			Name:      "run",
			Classname: accumulator.name,
			Time:      formatSeconds(0),
			Failure: &junitFailure{
				Type:    "fail",
				Message: "test run failed",
				Body:    "test run failed",
			},
		})
	}

	if summary.Errored > 0 && totalErrors == 0 {
		accumulator := ensureSuite("")
		accumulator.cases = append(accumulator.cases, junitTestCase{
			Name:      "run",
			Classname: accumulator.name,
			Time:      formatSeconds(0),
			Error: &junitError{
				Type:    "error",
				Message: "test run failed with status error",
				Body:    "test run failed with status error",
			},
		})
	}

	if summary.Canceled > 0 && totalCanceledCases == 0 {
		accumulator := ensureSuite("")
		accumulator.cases = append(accumulator.cases, junitTestCase{
			Name:      "run",
			Classname: accumulator.name,
			Time:      formatSeconds(0),
			Error: &junitError{
				Type:    "canceled",
				Message: "test run canceled",
				Body:    "test run canceled",
			},
		})
	}

	if len(summary.PolicyFailures) > 0 {
		violations := make([]string, 0, len(summary.PolicyFailures))
		for _, failure := range summary.PolicyFailures {
			violations = append(violations, fmt.Sprintf("%d %s", failure.Count, failure.Status))
		}
		message := sanitizeXMLString("strict policy failed: " + strings.Join(violations, ", "))
		accumulator := ensureSuite("")
		accumulator.cases = append(accumulator.cases, junitTestCase{
			Name:      "strict_policy",
			Classname: accumulator.name,
			Time:      formatSeconds(0),
			Failure: &junitFailure{
				Type:    "strict_policy",
				Message: message,
				Body:    message,
			},
		})
	}

	sort.Strings(extraFiles)
	orderedAccumulators := make([]*junitSuiteAccumulator, 0, len(manifestFiles)+len(extraFiles)+1)
	for _, file := range manifestFiles {
		orderedAccumulators = append(orderedAccumulators, fileSuites[file])
	}
	for _, file := range extraFiles {
		orderedAccumulators = append(orderedAccumulators, fileSuites[file])
	}
	if globalSuite != nil {
		orderedAccumulators = append(orderedAccumulators, globalSuite)
	}

	document := junitTestSuites{
		Name:   defaultJUnitSuiteName,
		Suites: make([]junitTestSuite, 0, len(orderedAccumulators)),
	}
	var totalDuration time.Duration
	for _, accumulator := range orderedAccumulators {
		suite := finalizeSuite(accumulator)
		document.Tests += suite.Tests
		document.Failures += suite.Failures
		document.Errors += suite.Errors
		document.Skipped += suite.Skipped
		totalDuration += accumulator.duration
		document.Suites = append(document.Suites, suite)
	}
	document.Time = formatSeconds(totalDuration)
	return document
}

func finalizeSuite(accumulator *junitSuiteAccumulator) junitTestSuite {
	suite := junitTestSuite{
		Name:      sanitizeXMLString(accumulator.name),
		File:      sanitizeXMLString(accumulator.file),
		Tests:     len(accumulator.cases),
		Time:      formatSeconds(accumulator.duration),
		TestCases: accumulator.cases,
	}
	for _, testCase := range accumulator.cases {
		if testCase.Failure != nil {
			suite.Failures++
		}
		if testCase.Error != nil {
			suite.Errors++
		}
		if testCase.Skipped != nil {
			suite.Skipped++
		}
	}
	if len(accumulator.output) > 0 {
		suite.SystemOut = sanitizeXMLString(strings.Join(accumulator.output, "\n"))
	}
	if len(accumulator.warnings) > 0 {
		suite.SystemErr = sanitizeXMLString(strings.Join(accumulator.warnings, "\n"))
	}
	return suite
}

func buildJUnitTestCase(caseResult results.CaseResult, suiteName, suiteFile string) junitTestCase {
	line := caseResult.Line
	if line <= 0 && len(caseResult.Failures) > 0 && caseResult.Failures[0].Location != nil {
		firstLocation := caseResult.Failures[0].Location
		if firstLocation.Line > 0 && (suiteFile == "" || cleanRelativeFile(firstLocation.File) == suiteFile) {
			line = firstLocation.Line
		}
	}

	testCase := junitTestCase{
		Name:       sanitizeXMLString(resolveCaseDisplayName(caseResult)),
		Classname:  sanitizeXMLString(suiteName),
		File:       sanitizeXMLString(suiteFile),
		Line:       max(0, line),
		Time:       formatSeconds(caseResult.Duration),
		Assertions: max(0, caseResult.Assertions),
	}

	switch caseResult.Status {
	case "fail":
		testCase.Failure = buildCaseFailure(caseResult)
	case "error":
		testCase.Error = buildCaseError(caseResult)
	case "canceled":
		message := caseResult.Message
		if message == "" {
			message = "case canceled"
		}
		sanitized := sanitizeXMLString(message)
		testCase.Error = &junitError{
			Type:    "canceled",
			Message: sanitized,
			Body:    sanitized,
		}
	case "skip", "todo":
		testCase.Skipped = buildCaseSkipped(caseResult)
	}

	includeMessagesInSystemOut := caseResult.Status != "fail" && caseResult.Status != "error"
	systemOutLines := make([]string, 0, len(caseResult.Messages)+len(caseResult.Output))
	if includeMessagesInSystemOut {
		for _, text := range caseResult.Messages {
			if text == "" {
				continue
			}
			systemOutLines = append(systemOutLines, "MESSAGE: "+text)
		}
	}
	for _, event := range caseResult.Output {
		systemOutLines = append(systemOutLines, outputText(event))
	}
	if len(systemOutLines) > 0 {
		testCase.SystemOut = sanitizeXMLString(strings.Join(systemOutLines, "\n"))
	}

	if len(caseResult.Warnings) > 0 {
		warnings := make([]string, 0, len(caseResult.Warnings))
		for _, warning := range caseResult.Warnings {
			warnings = append(warnings, formatWarningLine(warning))
		}
		testCase.SystemErr = sanitizeXMLString(strings.Join(warnings, "\n"))
	}

	return testCase
}

func buildCaseFailure(caseResult results.CaseResult) *junitFailure {
	if len(caseResult.Failures) > 0 {
		first := caseResult.Failures[0]
		failureType := first.Assertion
		if failureType == "" {
			failureType = "assertion"
		}
		headline := strings.TrimSpace(first.Message + diagnosticValues(first))
		if headline == "" {
			headline = "assertion failed"
		}
		lines := make([]string, 0, len(caseResult.Failures)+len(caseResult.Messages))
		for _, failure := range caseResult.Failures {
			lines = append(lines, formatFailureDetailLine(failure))
		}
		for _, text := range caseResult.Messages {
			if text != "" {
				lines = append(lines, "message: "+text)
			}
		}
		return &junitFailure{
			Type:    sanitizeXMLString(failureType),
			Message: sanitizeXMLString(headline),
			Body:    sanitizeXMLString(strings.Join(lines, "\n")),
		}
	}

	headline := caseResult.Message
	if headline == "" {
		headline = outcomeMessage(caseResult.Outcomes, "fail")
	}
	if headline == "" {
		headline = "test failed"
	}
	lines := []string{headline}
	for _, text := range caseResult.Messages {
		if text != "" {
			lines = append(lines, "message: "+text)
		}
	}
	return &junitFailure{
		Type:    "fail",
		Message: sanitizeXMLString(headline),
		Body:    sanitizeXMLString(strings.Join(lines, "\n")),
	}
}

func buildCaseError(caseResult results.CaseResult) *junitError {
	errorType := "error"
	headline := caseResult.Message
	if len(caseResult.Failures) > 0 {
		if caseResult.Failures[0].Assertion != "" {
			errorType = caseResult.Failures[0].Assertion
		}
		if headline == "" {
			headline = strings.TrimSpace(caseResult.Failures[0].Message + diagnosticValues(caseResult.Failures[0]))
		}
	}
	if headline == "" {
		headline = "case error"
	}

	lines := make([]string, 0, 1+len(caseResult.Failures)+len(caseResult.Messages))
	if caseResult.File != "" && caseResult.Line > 0 {
		lines = append(lines, fmt.Sprintf("%s:%d: %s", caseResult.File, caseResult.Line, headline))
	} else if caseResult.File != "" {
		lines = append(lines, fmt.Sprintf("%s: %s", caseResult.File, headline))
	} else {
		lines = append(lines, headline)
	}
	for _, failure := range caseResult.Failures {
		lines = append(lines, formatFailureDetailLine(failure))
	}
	for _, text := range caseResult.Messages {
		if text != "" {
			lines = append(lines, "message: "+text)
		}
	}
	return &junitError{
		Type:    sanitizeXMLString(errorType),
		Message: sanitizeXMLString(headline),
		Body:    sanitizeXMLString(strings.Join(lines, "\n")),
	}
}

func buildCaseSkipped(caseResult results.CaseResult) *junitSkipped {
	message := outcomeMessage(caseResult.Outcomes, caseResult.Status)
	if message == "" {
		message = caseResult.Message
	}
	return &junitSkipped{
		Type:    sanitizeXMLString(caseResult.Status),
		Message: sanitizeXMLString(message),
	}
}

func buildSyntheticFailureCase(event model.Event, suiteName, suiteFile string) junitTestCase {
	name := event.Name
	if name == "" {
		name = event.Assertion
	}
	if name == "" {
		name = "suite_failure"
	}
	failureType := event.Assertion
	if failureType == "" {
		failureType = "assertion"
	}
	headline := strings.TrimSpace(event.Message + diagnosticValues(event))
	if headline == "" {
		headline = "suite failure"
	}
	line := 0
	if event.Location != nil && event.Location.Line > 0 {
		line = event.Location.Line
	}
	return junitTestCase{
		Name:      sanitizeXMLString(name),
		Classname: sanitizeXMLString(suiteName),
		File:      sanitizeXMLString(suiteFile),
		Line:      line,
		Time:      formatSeconds(0),
		Failure: &junitFailure{
			Type:    sanitizeXMLString(failureType),
			Message: sanitizeXMLString(headline),
			Body:    sanitizeXMLString(formatFailureDetailLine(event)),
		},
	}
}

func buildSyntheticErrorCase(event model.Event, suiteName, suiteFile string) junitTestCase {
	name := event.Name
	if name == "" {
		name = event.Assertion
	}
	if name == "" {
		name = "worker_error"
	}
	errorType := event.Name
	if errorType == "" {
		errorType = event.Assertion
	}
	if errorType == "" {
		errorType = "error"
	}
	headline := strings.TrimSpace(event.Message)
	if headline == "" {
		headline = name
	}
	line := 0
	if event.Location != nil && event.Location.Line > 0 {
		line = event.Location.Line
	}
	detail := headline
	if prefix := formatLocationPrefix(event.Location); prefix != "" {
		detail = prefix + ": " + detail
	}
	return junitTestCase{
		Name:      sanitizeXMLString(name),
		Classname: sanitizeXMLString(suiteName),
		File:      sanitizeXMLString(suiteFile),
		Line:      line,
		Time:      formatSeconds(0),
		Error: &junitError{
			Type:    sanitizeXMLString(errorType),
			Message: sanitizeXMLString(headline),
			Body:    sanitizeXMLString(detail),
		},
	}
}

func resolveCaseSuiteFile(caseResult results.CaseResult) string {
	if cleaned := cleanRelativeFile(caseResult.File); cleaned != "" {
		return cleaned
	}
	if prefix, _, found := strings.Cut(caseResult.ID, "::"); found {
		return cleanRelativeFile(prefix)
	}
	return ""
}

func resolveCaseDisplayName(caseResult results.CaseResult) string {
	if caseResult.Name != "" {
		return caseResult.Name
	}
	if _, suffix, found := strings.Cut(caseResult.ID, "::"); found && suffix != "" {
		return suffix
	}
	if caseResult.ID != "" {
		return caseResult.ID
	}
	return "case"
}

func cleanRelativeFile(filename string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(filename, `\`, "/"))
	trimmed = strings.TrimPrefix(trimmed, "res://")
	if trimmed == "" {
		return ""
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "/" {
		return ""
	}
	return strings.TrimPrefix(cleaned, "./")
}

func locationFile(location *model.Location) string {
	if location == nil {
		return ""
	}
	return location.File
}

func formatLocationPrefix(location *model.Location) string {
	if location == nil {
		return ""
	}
	file := cleanRelativeFile(location.File)
	if file == "" {
		return ""
	}
	if location.Line > 0 {
		return fmt.Sprintf("%s:%d", file, location.Line)
	}
	return file
}

func formatFailureDetailLine(event model.Event) string {
	prefix := formatLocationPrefix(event.Location)
	assertionTag := ""
	if event.Assertion != "" {
		assertionTag = "[" + event.Assertion + "] "
	}
	body := assertionTag + event.Message + diagnosticValues(event)
	if prefix != "" {
		return prefix + ": " + body
	}
	return body
}

func formatWarningLine(event model.Event) string {
	prefix := formatLocationPrefix(event.Location)
	if prefix != "" {
		return fmt.Sprintf("WARN (%s): %s%s", prefix, event.Message, diagnosticValues(event))
	}
	return fmt.Sprintf("WARN: %s%s", event.Message, diagnosticValues(event))
}

func outcomeMessage(outcomes []model.Outcome, kind string) string {
	messages := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.Kind == kind && outcome.Message != "" {
			messages = append(messages, outcome.Message)
		}
	}
	return strings.Join(messages, "; ")
}

func sortedEvents(events []model.Event) []model.Event {
	if len(events) <= 1 {
		return events
	}
	sorted := append([]model.Event(nil), events...)
	sort.SliceStable(sorted, func(leftIndex, rightIndex int) bool {
		left := sorted[leftIndex]
		right := sorted[rightIndex]
		leftFile := cleanRelativeFile(locationFile(left.Location))
		rightFile := cleanRelativeFile(locationFile(right.Location))
		if leftFile != rightFile {
			if leftFile == "" || rightFile == "" {
				return leftFile != ""
			}
			return leftFile < rightFile
		}
		leftLine := 0
		if left.Location != nil && left.Location.Line > 0 {
			leftLine = left.Location.Line
		}
		rightLine := 0
		if right.Location != nil && right.Location.Line > 0 {
			rightLine = right.Location.Line
		}
		if leftLine != rightLine {
			return leftLine < rightLine
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		if left.Assertion != right.Assertion {
			return left.Assertion < right.Assertion
		}
		return left.Message < right.Message
	})
	return sorted
}

func formatSeconds(duration time.Duration) string {
	if duration <= 0 {
		return "0.000000"
	}
	return fmt.Sprintf("%.6f", duration.Seconds())
}

// sanitizeXMLString replaces invalid UTF-8 sequences and characters disallowed by
// XML 1.0 (§2.2) with the Unicode replacement character so arbitrary test output
// never produces malformed XML.
func sanitizeXMLString(input string) string {
	if input == "" {
		return ""
	}
	needsSanitization := false
	for index := 0; index < len(input); {
		character, width := utf8.DecodeRuneInString(input[index:])
		if (character == utf8.RuneError && width == 1) || !isValidXML10Rune(character) {
			needsSanitization = true
			break
		}
		index += width
	}
	if !needsSanitization {
		return input
	}

	var builder strings.Builder
	builder.Grow(len(input))
	for index := 0; index < len(input); {
		character, width := utf8.DecodeRuneInString(input[index:])
		if character == utf8.RuneError && width == 1 {
			builder.WriteRune('\uFFFD')
			index++
			continue
		}
		if !isValidXML10Rune(character) {
			builder.WriteRune('\uFFFD')
		} else {
			builder.WriteRune(character)
		}
		index += width
	}
	return builder.String()
}

func isValidXML10Rune(character rune) bool {
	return character == 0x09 ||
		character == 0x0A ||
		character == 0x0D ||
		(character >= 0x20 && character <= 0xD7FF) ||
		(character >= 0xE000 && character <= 0xFFFD) ||
		(character >= 0x10000 && character <= 0x10FFFF)
}
