// Package app implements the gdtest command surface and exit policy.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	gdtest "github.com/cafecito-games/gdtest"
	"github.com/cafecito-games/gdtest/internal/client"
	"github.com/cafecito-games/gdtest/internal/config"
	"github.com/cafecito-games/gdtest/internal/daemon"
	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/reporter"
	"github.com/cafecito-games/gdtest/internal/results"
	"github.com/cafecito-games/gdtest/internal/service"
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
		err = doctor(ctx, arguments[1:], streams)
	case "version", "--version", "-version":
		_, err = fmt.Fprintln(streams.Out, versionString())
	case "help", "--help", "-h":
		usage(streams.Out)
		return ExitOK
	case "init":
		err = initialize(arguments[1:], streams)
	case "warm":
		err = warm(ctx, arguments[1:], streams)
	case "daemon":
		err = daemonCommand(ctx, arguments[1:], streams)
	case "watch", "migrate":
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
	if err := executePlan(ctx, root, projectConfig.Godot, plan, sink, *noDaemon, streams); err != nil {
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

func executePlan(
	ctx context.Context,
	root string,
	godot string,
	plan model.TestPlan,
	sink executor.EventSink,
	noDaemon bool,
	streams IO,
) error {
	if noDaemon {
		godotExecutor := &worker.GodotExecutor{
			Godot: godot, Project: root, SessionID: "foreground-" + newID(),
			Generation: plan.Generation, Stdout: streams.Err, Stderr: streams.Err,
		}
		return godotExecutor.Run(ctx, plan, sink)
	}
	daemonClient, err := compatibleClient(true)
	if err != nil {
		return err
	}
	_, err = daemonClient.Call(ctx, "session.run", service.RunRequest{
		Project: root, Godot: godot, Plan: plan,
	}, func(data json.RawMessage) error {
		var event model.Event
		if err := json.Unmarshal(data, &event); err != nil {
			return fmt.Errorf("decode daemon event: %w", err)
		}
		return sink.Emit(event)
	})
	return err
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

func doctor(ctx context.Context, arguments []string, streams IO) error {
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
	if _, err = fmt.Fprintf(streams.Out, "project: %s\ngodot: %s\ntests: %d\n", root, executable, count); err != nil {
		return err
	}
	runtimePath := filepath.Join(root, "addons", "gdtest", "version.gd")
	if _, err := os.Stat(runtimePath); err != nil {
		return fmt.Errorf("gdtest runtime is not ready at %s: %w", runtimePath, err)
	}
	daemonClient, err := compatibleClient(true)
	if err != nil {
		return err
	}
	response, err := daemonClient.Call(ctx, "session.warm", service.WarmRequest{
		Project: root, Godot: projectConfig.Godot, Generation: 1,
	}, nil)
	if err != nil {
		return fmt.Errorf("headless worker handshake: %w", err)
	}
	_, err = fmt.Fprintf(streams.Out, "runtime: %s\ndaemon_worker: %s\n", runtimePath, response)
	return err
}

func warm(ctx context.Context, arguments []string, streams IO) error {
	flags := flag.NewFlagSet("warm", flag.ContinueOnError)
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
	daemonClient, err := compatibleClient(true)
	if err != nil {
		return err
	}
	response, err := daemonClient.Call(ctx, "session.warm", service.WarmRequest{
		Project: root, Godot: projectConfig.Godot, Generation: 1,
	}, nil)
	if err != nil {
		return err
	}
	return printJSON(streams.Out, response)
}

func daemonCommand(ctx context.Context, arguments []string, streams IO) error {
	if len(arguments) == 0 {
		return errors.New("daemon requires status, sessions, stop, logs, or serve")
	}
	switch arguments[0] {
	case "serve":
		return serveDaemon(ctx, arguments[1:], streams)
	case "logs":
		namespace, err := daemon.DefaultNamespace()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(namespace.LogPath)
		if err != nil {
			return fmt.Errorf("read daemon log: %w", err)
		}
		_, err = streams.Out.Write(data)
		return err
	case "status", "sessions", "stop":
		daemonClient, err := compatibleClient(false)
		if err != nil {
			return err
		}
		if arguments[0] == "stop" {
			if err := daemonClient.Stop(ctx); err != nil {
				return err
			}
			_, err := fmt.Fprintln(streams.Out, "daemon stopping")
			return err
		}
		method := "daemon." + arguments[0]
		response, err := daemonClient.Call(ctx, method, struct{}{}, nil)
		if err != nil {
			return err
		}
		return printJSON(streams.Out, response)
	default:
		return fmt.Errorf("unknown daemon command %q", arguments[0])
	}
}

func serveDaemon(ctx context.Context, arguments []string, streams IO) error {
	flags := flag.NewFlagSet("daemon serve", flag.ContinueOnError)
	flags.SetOutput(streams.Err)
	namespaceFlag := flags.String("namespace", "", "exact daemon namespace")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	namespace, err := daemon.DefaultNamespace()
	if err != nil {
		return err
	}
	if *namespaceFlag != "" && filepath.Clean(*namespaceFlag) != filepath.Clean(namespace.Root) {
		return errors.New("requested daemon namespace does not match this executable build")
	}
	if err := namespace.Prepare(); err != nil {
		return err
	}
	logger, err := daemon.OpenLogger(namespace.LogPath, 0)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Close() }()
	sessions := service.NewSessionService()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = sessions.Close(shutdownCtx)
		cancel()
	}()
	server := &daemon.Server{Namespace: namespace, Handler: sessions, Logger: logger}
	return server.Serve(ctx)
}

func compatibleClient(autoStart bool) (client.Client, error) {
	namespace, err := daemon.DefaultNamespace()
	if err != nil {
		return client.Client{}, err
	}
	result := client.Client{Namespace: namespace}
	if autoStart {
		result.Starter = client.ExecutableStarter("")
	}
	return result, nil
}

func printJSON(writer io.Writer, data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode daemon response: %w", err)
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(writer, string(pretty))
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
