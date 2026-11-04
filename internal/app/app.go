// Package app implements the gdtest command surface and exit policy.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	gdtest "github.com/cafecito-games/gdtest"
	"github.com/cafecito-games/gdtest/internal/config"
	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/reporter"
	"github.com/cafecito-games/gdtest/internal/results"
	"github.com/cafecito-games/gdtest/internal/version"
	"github.com/cafecito-games/gdtest/internal/worker"
)

// Exit codes are stable CLI behavior.
const (
	ExitOK       = 0
	ExitFailures = 1
	ExitError    = 2
	ExitCanceled = 130
)

// IO holds command streams for tests and embedding.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Run executes one command and returns its process exit code.
func Run(ctx context.Context, arguments []string, streams IO) int {
	if streams.Out == nil {
		streams.Out = io.Discard
	}
	if streams.Err == nil {
		streams.Err = io.Discard
	}
	if len(arguments) == 0 {
		usage(streams.Err)
		return ExitError
	}
	var err error
	switch arguments[0] {
	case "run":
		return runTests(ctx, arguments[1:], streams)
	case "list":
		err = listTests(arguments[1:], streams)
	case "doctor":
		err = doctor(arguments[1:], streams)
	case "version", "--version", "-version":
		_, err = fmt.Fprintln(streams.Out, versionString())
	case "help", "--help", "-h":
		usage(streams.Out)
		return ExitOK
	case "init":
		err = initialize(arguments[1:], streams)
	case "watch", "warm", "migrate", "daemon":
		err = fmt.Errorf("%s is not available in this build milestone", arguments[0])
	default:
		err = fmt.Errorf("unknown command %q", arguments[0])
	}
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	return ExitOK
}

func initialize(arguments []string, streams IO) error {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	projectFlag := flags.String("project", "", "Godot project root")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	root, err := projectRoot(*projectFlag)
	if err != nil {
		return err
	}
	if err := gdtest.InstallRuntime(root); err != nil {
		return err
	}
	_, err = fmt.Fprintf(streams.Out, "Installed gdtest runtime in %s\n", filepath.Join(root, "addons", "gdtest"))
	return err
}

func runTests(ctx context.Context, arguments []string, streams IO) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	jsonOutput := flags.Bool("json", false, "write JSON Lines events")
	allowEmpty := flags.Bool("allow-empty", false, "allow selectors that match no tests")
	noDaemon := flags.Bool("no-daemon", false, "run orchestration in the foreground")
	projectFlag := flags.String("project", "", "Godot project root")
	if err := flags.Parse(arguments); err != nil {
		return ExitError
	}
	_ = noDaemon // Foreground is the only implementation until the daemon milestone.
	root, err := projectRoot(*projectFlag)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	projectConfig, err := config.Load(root)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	manifest, err := discovery.Discover(root, discovery.Options{
		Patterns: projectConfig.Tests,
		Exclude:  projectConfig.Exclude,
	})
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	manifest, err = discovery.Select(manifest, flags.Args(), *allowEmpty)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	if len(manifest.Files) == 0 {
		if *jsonOutput {
			_, _ = fmt.Fprintln(streams.Out, `{"type":"summary","passed":0,"message":"no tests selected"}`)
		} else {
			_, _ = fmt.Fprintln(streams.Out, "No tests selected.")
		}
		return ExitOK
	}
	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion,
		RunID:           newID(),
		Generation:      1,
		Files:           manifest.Files,
	}
	collector := results.NewCollector()
	var eventReporter executor.EventSink
	var finish func(results.Summary) error
	if *jsonOutput {
		machine := reporter.NewJSONL(streams.Out)
		eventReporter = machine
		finish = machine.Summary
	} else {
		terminal := reporter.NewTerminal(streams.Out)
		eventReporter = terminal
		finish = terminal.Summary
	}
	sink := executor.EventSinkFunc(func(event model.Event) error {
		if err := collector.Emit(event); err != nil {
			return err
		}
		return eventReporter.Emit(event)
	})
	godotExecutor := &worker.GodotExecutor{
		Godot:      projectConfig.Godot,
		Project:    root,
		SessionID:  "foreground-" + newID(),
		Generation: plan.Generation,
		Stdout:     streams.Err,
		Stderr:     streams.Err,
	}
	if err := godotExecutor.Run(ctx, plan, sink); err != nil {
		if errors.Is(err, context.Canceled) {
			return ExitCanceled
		}
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	summary := collector.Summary()
	if err := finish(summary); err != nil {
		writeDiagnostic(streams.Err, "gdtest: report:", err)
		return ExitError
	}
	if summary.HasFailures() {
		return ExitFailures
	}
	return ExitOK
}

func listTests(arguments []string, streams IO) error {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	projectFlag := flags.String("project", "", "Godot project root")
	allowEmpty := flags.Bool("allow-empty", false, "allow selectors that match no tests")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	root, err := projectRoot(*projectFlag)
	if err != nil {
		return err
	}
	projectConfig, err := config.Load(root)
	if err != nil {
		return err
	}
	manifest, err := discovery.Discover(root, discovery.Options{Patterns: projectConfig.Tests, Exclude: projectConfig.Exclude})
	if err != nil {
		return err
	}
	manifest, err = discovery.Select(manifest, flags.Args(), *allowEmpty)
	if err != nil {
		return err
	}
	for _, file := range manifest.Files {
		for _, testCase := range file.Cases {
			if _, err := fmt.Fprintln(streams.Out, testCase.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func doctor(arguments []string, streams IO) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	projectFlag := flags.String("project", "", "Godot project root")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	root, err := projectRoot(*projectFlag)
	if err != nil {
		return err
	}
	projectConfig, err := config.Load(root)
	if err != nil {
		return err
	}
	executable, err := exec.LookPath(projectConfig.Godot)
	if err != nil {
		return fmt.Errorf("godot executable %q was not found: %w", projectConfig.Godot, err)
	}
	manifest, err := discovery.Discover(root, discovery.Options{Patterns: projectConfig.Tests, Exclude: projectConfig.Exclude})
	if err != nil {
		return err
	}
	count := 0
	for _, file := range manifest.Files {
		count += len(file.Cases)
	}
	_, err = fmt.Fprintf(streams.Out, "project: %s\ngodot: %s\ntests: %d\n", root, executable, count)
	return err
}

func projectRoot(explicit string) (string, error) {
	if explicit != "" {
		return canonicalProject(explicit)
	}
	directory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("current directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "project.godot")); err == nil {
			return canonicalProject(directory)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("no project.godot found; use --project")
		}
		directory = parent
	}
}

func canonicalProject(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("project path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("project path: %w", err)
	}
	if _, err := os.Stat(filepath.Join(resolved, "project.godot")); err != nil {
		return "", fmt.Errorf("%s is not a Godot project: %w", resolved, err)
	}
	return resolved, nil
}

func newID() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return strings.Repeat("0", 24)
	}
	return hex.EncodeToString(data)
}

func versionString() string {
	return fmt.Sprintf("gdtest %s (%s)", version.Product, version.BuildID())
}

func usage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "usage: gdtest <init|run|list|watch|warm|doctor|migrate|daemon> [options]")
}

func writeDiagnostic(writer io.Writer, prefix string, err error) {
	_, _ = fmt.Fprintln(writer, prefix, err)
}
