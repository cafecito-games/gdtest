// Package app implements the gdtest command surface and exit policy.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	"github.com/cafecito-games/gdtest/internal/index"
	"github.com/cafecito-games/gdtest/internal/migrate"
	"github.com/cafecito-games/gdtest/internal/model"
	projectidentity "github.com/cafecito-games/gdtest/internal/project"
	"github.com/cafecito-games/gdtest/internal/reporter"
	"github.com/cafecito-games/gdtest/internal/results"
	"github.com/cafecito-games/gdtest/internal/service"
	"github.com/cafecito-games/gdtest/internal/version"
	sourcewatch "github.com/cafecito-games/gdtest/internal/watch"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/spf13/cobra"
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
	if streams.In == nil {
		streams.In = strings.NewReader("")
	}
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
	exitCode := ExitOK
	root := newRootCommand(streams, &exitCode)
	if len(arguments) > 0 && arguments[0] == "-version" {
		arguments = append([]string{"--version"}, arguments[1:]...)
	}
	root.SetArgs(arguments)
	root.SetIn(streams.In)
	root.SetOut(streams.Out)
	root.SetErr(streams.Err)
	err := root.ExecuteContext(ctx)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	return exitCode
}

type runOptions struct {
	project     string
	json        bool
	allowEmpty  bool
	noDaemon    bool
	name        string
	onlyMode    string
	affected    []string
	affectedSet bool
	verbose     bool
	selectors   []string
}

type listOptions struct {
	project    string
	allowEmpty bool
	name       string
	onlyMode   string
	selectors  []string
}

type migrateOptions struct {
	project string
	check   bool
	write   bool
	paths   []string
}

func newRootCommand(streams IO, exitCode *int) *cobra.Command {
	var showVersion bool
	root := &cobra.Command{
		Use:           "gdtest",
		Short:         "Run isolated GDScript tests",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if showVersion {
				_, err := fmt.Fprintln(streams.Out, versionString())
				return err
			}
			return errors.New("a command is required; run gdtest --help for usage")
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.Flags().BoolVar(&showVersion, "version", false, "print version information")
	root.AddCommand(
		newInitCommand(streams),
		newRunCommand(streams, exitCode),
		newListCommand(streams),
		newWatchCommand(streams, exitCode),
		newWarmCommand(streams),
		newDoctorCommand(streams),
		newVersionCommand(streams),
		newMigrateCommand(streams),
		newDaemonCommand(streams),
	)
	return root
}

func newInitCommand(streams IO) *cobra.Command {
	var project string
	command := &cobra.Command{
		Use:   "init",
		Short: "Install the gdtest runtime into a Godot project",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return initialize(project, streams)
		},
	}
	addProjectFlag(command, &project)
	return command
}

func newRunCommand(streams IO, exitCode *int) *cobra.Command {
	options := runOptions{}
	command := &cobra.Command{
		Use:   "run [selectors...]",
		Short: "Run selected tests",
		Args:  cobra.ArbitraryArgs,
		Run: func(command *cobra.Command, selectors []string) {
			options.selectors = selectors
			options.affectedSet = command.Flags().Changed("affected")
			*exitCode = runTests(command.Context(), options, streams)
		},
	}
	addRunFlags(command, &options)
	return command
}

func newWatchCommand(streams IO, exitCode *int) *cobra.Command {
	options := runOptions{}
	command := &cobra.Command{
		Use:   "watch [selectors...]",
		Short: "Run tests when project sources change",
		Args:  cobra.ArbitraryArgs,
		Run: func(command *cobra.Command, selectors []string) {
			options.selectors = selectors
			options.affectedSet = command.Flags().Changed("affected")
			*exitCode = watchTests(command.Context(), options, streams)
		},
	}
	addRunFlags(command, &options)
	return command
}

func newListCommand(streams IO) *cobra.Command {
	options := listOptions{}
	command := &cobra.Command{
		Use:   "list [selectors...]",
		Short: "List discovered tests",
		Args:  cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, selectors []string) error {
			options.selectors = selectors
			return listTests(command.Context(), options, streams)
		},
	}
	addProjectFlag(command, &options.project)
	command.Flags().BoolVar(&options.allowEmpty, "allow-empty", false, "allow selectors that match no tests")
	command.Flags().StringVar(&options.name, "name", "", "select tests whose name contains this text")
	command.Flags().StringVar(&options.onlyMode, "only", "", "focus policy: auto, ignore, require, or forbid")
	return command
}

func newDoctorCommand(streams IO) *cobra.Command {
	var project string
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Verify the local gdtest installation",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return doctor(command.Context(), project, streams)
		},
	}
	addProjectFlag(command, &project)
	return command
}

func newWarmCommand(streams IO) *cobra.Command {
	var project string
	command := &cobra.Command{
		Use:   "warm",
		Short: "Start and warm the project worker",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return warm(command.Context(), project, streams)
		},
	}
	addProjectFlag(command, &project)
	return command
}

func newVersionCommand(streams IO) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(streams.Out, versionString())
			return err
		},
	}
}

func newMigrateCommand(streams IO) *cobra.Command {
	command := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate tests from another framework",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return errors.New("migrate requires the source framework name: gdtest migrate vest")
		},
	}
	options := migrateOptions{}
	vest := &cobra.Command{
		Use:   "vest [paths...]",
		Short: "Migrate supported Vest APIs",
		Args:  cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, paths []string) error {
			options.paths = paths
			return migrateVest(command.Context(), options, streams)
		},
	}
	addProjectFlag(vest, &options.project)
	vest.Flags().BoolVar(&options.check, "check", false, "report safe rewrites without changing files")
	vest.Flags().BoolVar(&options.write, "write", false, "apply safe rewrites atomically")
	vest.MarkFlagsMutuallyExclusive("check", "write")
	command.AddCommand(vest)
	return command
}

func addProjectFlag(command *cobra.Command, project *string) {
	command.Flags().StringVar(project, "project", "", "Godot project root")
}

func addRunFlags(command *cobra.Command, options *runOptions) {
	addProjectFlag(command, &options.project)
	command.Flags().BoolVar(&options.json, "json", false, "write JSON Lines events")
	command.Flags().BoolVar(&options.allowEmpty, "allow-empty", false, "allow selectors that match no tests")
	command.Flags().BoolVar(&options.noDaemon, "no-daemon", false, "run orchestration in the foreground")
	command.Flags().StringVar(&options.name, "name", "", "select tests whose name contains this text")
	command.Flags().StringVar(&options.onlyMode, "only", "", "focus policy: auto, ignore, require, or forbid")
	command.Flags().StringSliceVar(&options.affected, "affected", nil, "select tests affected by project-relative changed paths")
	command.Flags().BoolVarP(&options.verbose, "verbose", "v", false, "explain affected-test selection")
}

func migrateVest(ctx context.Context, options migrateOptions, streams IO) error {
	mode := migrate.Check
	if options.write {
		mode = migrate.Write
	}
	root, err := projectRoot(options.project)
	if err != nil {
		return err
	}
	paths := options.paths
	if len(paths) == 0 {
		paths, err = migrationPaths(ctx, root)
		if err != nil {
			return err
		}
	}
	report, err := migrate.Run(ctx, root, paths, mode)
	if err != nil {
		return err
	}
	for _, finding := range report.Findings {
		if _, err := fmt.Fprintf(
			streams.Out, "%s:%d:%d: %s %s: %s\n",
			finding.Path, finding.Line, finding.Column, finding.Severity, finding.Code, finding.Message,
		); err != nil {
			return err
		}
	}
	action := "would change"
	if mode == migrate.Write {
		action = "changed"
	}
	_, err = fmt.Fprintf(streams.Out, "%d file(s) %s; %d finding(s)\n", report.Changed, action, len(report.Findings))
	return err
}

func migrationPaths(ctx context.Context, root string) ([]string, error) {
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			switch relative {
			case ".git", ".godot", ".gdtest", "addons":
				return fs.SkipDir
			default:
				return nil
			}
		}
		if entry.Type().IsRegular() && strings.HasSuffix(strings.ToLower(relative), ".gd") {
			paths = append(paths, relative)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate GDScript migration inputs: %w", err)
	}
	return paths, nil
}

func watchTests(ctx context.Context, options runOptions, streams IO) int {
	root, err := projectRoot(options.project)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	reconciler := sourcewatch.New(root)
	current, err := reconciler.Reconcile(ctx, sourcewatch.Initial)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest: watch:", err)
		return ExitError
	}
	for {
		exitCode := runTests(ctx, options, streams)
		if exitCode == ExitError || exitCode == ExitCanceled {
			return exitCode
		}
		_, _ = fmt.Fprintf(streams.Err, "gdtest: watching generation %d\n", current.Update.Generation)
		ticker := time.NewTicker(250 * time.Millisecond)
		changed := false
		for !changed {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return ExitCanceled
			case <-ticker.C:
				result, reconcileErr := reconciler.Reconcile(ctx, sourcewatch.Periodic)
				if reconcileErr != nil {
					ticker.Stop()
					writeDiagnostic(streams.Err, "gdtest: watch:", reconcileErr)
					return ExitError
				}
				if result.Update.Generation > current.Update.Generation {
					current = result
					changed = true
				}
			}
		}
		ticker.Stop()
	}
}

func initialize(project string, streams IO) error {
	root, err := projectRoot(project)
	if err != nil {
		return err
	}
	if err := gdtest.InstallRuntime(root); err != nil {
		return err
	}
	_, err = fmt.Fprintf(streams.Out, "Installed gdtest runtime in %s\n", filepath.Join(root, "addons", "gdtest"))
	return err
}

func runTests(ctx context.Context, options runOptions, streams IO) int {
	root, err := projectRoot(options.project)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	projectConfig, err := config.Load(root)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	if options.onlyMode != "" {
		projectConfig.OnlyMode = options.onlyMode
	}
	if err := validateOnlyMode(projectConfig.OnlyMode); err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	staticManifest, err := index.Discover(ctx, root, discovery.Options{
		Patterns: projectConfig.Tests,
		Exclude:  projectConfig.Exclude,
	})
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	discoveryManifest, err := discoveryCandidates(staticManifest, options.selectors, options.allowEmpty)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	if options.affectedSet {
		affectedResult, affectedErr := affectedCandidates(discoveryManifest, root, options.affected)
		if affectedErr != nil {
			writeDiagnostic(streams.Err, "gdtest:", affectedErr)
			return ExitError
		}
		if options.verbose {
			for _, reason := range affectedResult.Reasons {
				_, _ = fmt.Fprintf(streams.Err, "gdtest: affected: %s\n", reason.Message)
			}
		}
		discoveryManifest = affectedResult.Manifest
		if len(discoveryManifest.Files) == 0 {
			if options.json {
				_, _ = fmt.Fprintln(streams.Out, `{"type":"summary","passed":0,"message":"no tests were affected"}`)
			} else {
				_, _ = fmt.Fprintln(streams.Out, "no tests were affected")
			}
			return ExitOK
		}
	}
	sessionID, err := resolveSessionID(ctx, root, projectConfig)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	completedManifest, err := completeManifest(
		ctx, root, projectConfig, discoveryManifest, sessionID, options.noDaemon, streams,
	)
	if err != nil {
		var findings *manifestErrors
		if errors.As(err, &findings) {
			if writeErr := findings.writeTo(streams.Out); writeErr != nil {
				writeDiagnostic(streams.Err, "gdtest: report:", writeErr)
			}
			return ExitError
		}
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	selected, err := selectCompletedManifest(
		completedManifest, staticManifest, root, options.selectors, options.name,
		options.allowEmpty, options.affectedSet, options.affected,
	)
	if err != nil {
		writeDiagnostic(streams.Err, "gdtest:", err)
		return ExitError
	}
	if len(selected.ids) == 0 {
		message := selected.message
		if message == "" {
			message = "No tests selected."
		}
		if options.json {
			encoded, _ := json.Marshal(map[string]any{"type": "summary", "passed": 0, "message": message})
			_, _ = fmt.Fprintln(streams.Out, string(encoded))
		} else {
			_, _ = fmt.Fprintln(streams.Out, message)
		}
		return ExitOK
	}
	plan := model.TestPlan{
		ProtocolVersion: model.ProtocolVersion, RunID: newID(),
		Generation: completedManifest.Generation, ContentFingerprint: staticManifest.ContentFingerprint,
		OnlyMode: projectConfig.OnlyMode, SelectionApplied: true,
		SelectedIDs: append([]string(nil), selected.ids...), Files: discoveryManifest.Files,
	}
	collector := results.NewCollector()
	var eventReporter executor.EventSink
	var finish func(results.Summary) error
	if options.json {
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
	if err := executePlan(
		ctx, sessionID, root, projectConfig.Godot, projectConfig.WorkerMaxRuns,
		plan, sink, options.noDaemon, streams,
	); err != nil {
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
	if summary.HasInfrastructureFailure() {
		return ExitError
	}
	if summary.HasFailures() {
		return ExitFailures
	}
	return ExitOK
}

func executePlan(
	ctx context.Context,
	sessionID string,
	root string,
	godot string,
	workerMaxRuns int,
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
		SessionID: sessionID, Project: root, Godot: godot,
		Plan: plan, WorkerMaxRuns: workerMaxRuns,
	}, func(data json.RawMessage) error {
		var event model.Event
		if err := json.Unmarshal(data, &event); err != nil {
			return fmt.Errorf("decode daemon event: %w", err)
		}
		return sink.Emit(event)
	})
	return err
}

func listTests(ctx context.Context, options listOptions, streams IO) error {
	root, err := projectRoot(options.project)
	if err != nil {
		return err
	}
	projectConfig, err := config.Load(root)
	if err != nil {
		return err
	}
	if options.onlyMode != "" {
		projectConfig.OnlyMode = options.onlyMode
	}
	if err := validateOnlyMode(projectConfig.OnlyMode); err != nil {
		return err
	}
	staticManifest, err := index.Discover(ctx, root, discovery.Options{Patterns: projectConfig.Tests, Exclude: projectConfig.Exclude})
	if err != nil {
		return err
	}
	discoveryManifest, err := discoveryCandidates(staticManifest, options.selectors, options.allowEmpty)
	if err != nil {
		return err
	}
	sessionID, err := resolveSessionID(ctx, root, projectConfig)
	if err != nil {
		return err
	}
	manifest, err := completeManifest(ctx, root, projectConfig, discoveryManifest, sessionID, false, streams)
	if err != nil {
		return err
	}
	selected, err := selectCompletedManifest(
		manifest, staticManifest, root, options.selectors, options.name, options.allowEmpty, false, nil,
	)
	if err != nil {
		return err
	}
	for _, file := range selected.manifest.Files {
		for _, testCase := range file.Cases {
			if _, err := fmt.Fprintln(streams.Out, testCase.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func doctor(ctx context.Context, project string, streams IO) error {
	root, err := projectRoot(project)
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
	manifest, err := index.Discover(ctx, root, discovery.Options{Patterns: projectConfig.Tests, Exclude: projectConfig.Exclude})
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
	sessionID, err := resolveSessionID(ctx, root, projectConfig)
	if err != nil {
		return err
	}
	response, err := daemonClient.Call(ctx, "session.warm", service.WarmRequest{
		SessionID: sessionID, Project: root, Godot: projectConfig.Godot, Generation: 1,
	}, nil)
	if err != nil {
		return fmt.Errorf("headless worker handshake: %w", err)
	}
	_, err = fmt.Fprintf(streams.Out, "runtime: %s\ndaemon_worker: %s\n", runtimePath, response)
	return err
}

func warm(ctx context.Context, project string, streams IO) error {
	root, err := projectRoot(project)
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
	sessionID, err := resolveSessionID(ctx, root, projectConfig)
	if err != nil {
		return err
	}
	response, err := daemonClient.Call(ctx, "session.warm", service.WarmRequest{
		SessionID: sessionID, Project: root, Godot: projectConfig.Godot, Generation: 1,
	}, nil)
	if err != nil {
		return err
	}
	return printJSON(streams.Out, response)
}

func newDaemonCommand(streams IO) *cobra.Command {
	command := &cobra.Command{
		Use:   "daemon",
		Short: "Manage the user-level gdtest daemon",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return errors.New("daemon requires status, sessions, stop, logs, or serve")
		},
	}
	var namespace string
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Serve the daemon protocol",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return serveDaemon(command.Context(), namespace, streams)
		},
	}
	serve.Flags().StringVar(&namespace, "namespace", "", "exact daemon namespace")
	command.AddCommand(
		serve,
		newDaemonRPCCommand("status", "Report daemon status", streams),
		newDaemonRPCCommand("sessions", "List daemon sessions", streams),
		newDaemonStopCommand(streams),
		newDaemonLogsCommand(streams),
	)
	return command
}

func newDaemonRPCCommand(name string, description string, streams IO) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: description,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			daemonClient, err := compatibleClient(false)
			if err != nil {
				return err
			}
			response, err := daemonClient.Call(command.Context(), "daemon."+name, struct{}{}, nil)
			if err != nil {
				return err
			}
			return printJSON(streams.Out, response)
		},
	}
}

func newDaemonStopCommand(streams IO) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			daemonClient, err := compatibleClient(false)
			if err != nil {
				return err
			}
			if err := daemonClient.Stop(command.Context()); err != nil {
				return err
			}
			_, err = fmt.Fprintln(streams.Out, "daemon stopping")
			return err
		},
	}
}

func newDaemonLogsCommand(streams IO) *cobra.Command {
	return &cobra.Command{
		Use:   "logs",
		Short: "Print the daemon log",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
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
		},
	}
}

func serveDaemon(ctx context.Context, requestedNamespace string, streams IO) error {
	namespace, err := daemon.DefaultNamespace()
	if err != nil {
		return err
	}
	if requestedNamespace != "" && filepath.Clean(requestedNamespace) != filepath.Clean(namespace.Root) {
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
	janitorCtx, stopJanitor := context.WithCancel(ctx)
	janitorDone := make(chan struct{})
	go func() {
		defer close(janitorDone)
		sourceTicker := time.NewTicker(time.Second)
		lifecycleTicker := time.NewTicker(5 * time.Second)
		defer sourceTicker.Stop()
		defer lifecycleTicker.Stop()
		for {
			select {
			case <-janitorCtx.Done():
				return
			case <-sourceTicker.C:
				updates, failures := sessions.ReconcileSources(janitorCtx)
				if len(updates) > 0 {
					_ = logger.Write(map[string]any{"event": "source_generations_advanced", "sessions": updates})
				}
				for sessionID, reconcileErr := range failures {
					_ = logger.Write(map[string]any{
						"event": "source_reconcile_error", "session": sessionID, "error": reconcileErr.Error(),
					})
				}
			case now := <-lifecycleTicker.C:
				removed, reconcileErr := sessions.Reconcile(now, 2*time.Second, 2*time.Minute)
				if reconcileErr != nil {
					_ = logger.Write(map[string]any{"event": "session_reconcile_error", "error": reconcileErr.Error()})
				} else if len(removed) > 0 {
					_ = logger.Write(map[string]any{"event": "sessions_evicted", "sessions": removed})
				}
			}
		}
	}()
	defer func() {
		stopJanitor()
		<-janitorDone
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

func resolveSessionID(ctx context.Context, root string, projectConfig config.Project) (string, error) {
	identity, err := projectidentity.Resolve(ctx, root)
	if err != nil {
		return "", err
	}
	godotExecutable, err := exec.LookPath(projectConfig.Godot)
	if err != nil {
		return "", fmt.Errorf("identify Godot executable: %w", err)
	}
	godotIdentity, err := projectidentity.ExecutableIdentity(godotExecutable)
	if err != nil {
		return "", fmt.Errorf("identify Godot executable: %w", err)
	}
	configuration, err := json.Marshal(projectConfig)
	if err != nil {
		return "", fmt.Errorf("identify project configuration: %w", err)
	}
	key, err := projectidentity.NewSessionKey(
		identity,
		godotIdentity,
		projectidentity.ContentIdentity(configuration),
		version.BuildID(),
	)
	if err != nil {
		return "", err
	}
	return key.ID(), nil
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
