package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cafecito-games/gdtest/internal/cache"
	"github.com/cafecito-games/gdtest/internal/config"
	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/dynamic"
	"github.com/cafecito-games/gdtest/internal/generation"
	"github.com/cafecito-games/gdtest/internal/model"
	projectidentity "github.com/cafecito-games/gdtest/internal/project"
	"github.com/cafecito-games/gdtest/internal/projectmeta"
	"github.com/cafecito-games/gdtest/internal/selection"
	"github.com/cafecito-games/gdtest/internal/service"
	"github.com/cafecito-games/gdtest/internal/version"
	sourcewatch "github.com/cafecito-games/gdtest/internal/watch"
	"github.com/cafecito-games/gdtest/internal/worker"
)

type completedSelection struct {
	manifest model.DiscoveryManifest
	ids      []string
	reasons  []selection.Reason
	message  string
}

func completeManifest(
	ctx context.Context,
	root string,
	projectConfig config.Project,
	static discovery.Manifest,
	sessionID string,
	noDaemon bool,
	streams IO,
) (model.DiscoveryManifest, error) {
	return completeManifestWithTimings(
		ctx, root, projectConfig, static, sessionID, noDaemon, streams, nil,
	)
}

func completeManifestWithTimings(
	ctx context.Context,
	root string,
	projectConfig config.Project,
	static discovery.Manifest,
	sessionID string,
	noDaemon bool,
	streams IO,
	timings map[string]time.Duration,
) (model.DiscoveryManifest, error) {
	if len(static.Files) == 0 {
		return model.DiscoveryManifest{
			ProtocolVersion: model.ProtocolVersion, Generation: 1,
			ContentFingerprint: static.ContentFingerprint,
		}, nil
	}
	onlyMode := projectConfig.OnlyMode
	plan := model.TestPlan{
		ProtocolVersion:    model.ProtocolVersion,
		RunID:              newID(),
		Generation:         1,
		ContentFingerprint: static.ContentFingerprint,
		OnlyMode:           onlyMode,
		Files:              static.Files,
	}
	configuration, err := json.Marshal(struct {
		Tests    []string         `json:"tests"`
		Exclude  []string         `json:"exclude"`
		OnlyMode string           `json:"only_mode"`
		Files    []model.FilePlan `json:"files"`
	}{
		Tests: projectConfig.Tests, Exclude: projectConfig.Exclude,
		OnlyMode: onlyMode, Files: static.Files,
	})
	if err != nil {
		return model.DiscoveryManifest{}, fmt.Errorf("encode dynamic discovery configuration: %w", err)
	}
	identity, err := godotIdentity(projectConfig.Godot)
	if err != nil {
		return model.DiscoveryManifest{}, err
	}
	var discoveryTimings map[string]time.Duration
	compute := func(computeCtx context.Context) (model.DiscoveryManifest, error) {
		var manifest model.DiscoveryManifest
		var computeTimings map[string]time.Duration
		var computeErr error
		if noDaemon {
			manifest, computeTimings, computeErr = discoverForeground(
				computeCtx, root, projectConfig.Godot, plan, streams,
			)
		} else {
			manifest, computeTimings, computeErr = discoverDaemon(
				computeCtx, sessionID, root, projectConfig.Godot,
				time.Duration(projectConfig.WorkerIdleTimeout), plan,
			)
		}
		discoveryTimings = computeTimings
		return manifest, computeErr
	}
	if !noDaemon {
		generation, generationErr := daemonGeneration(
			ctx, sessionID, root, projectConfig.Godot,
			time.Duration(projectConfig.WorkerIdleTimeout),
		)
		if generationErr != nil {
			return model.DiscoveryManifest{}, generationErr
		}
		plan.Generation = generation
	}
	relevantPaths, err := sourcewatch.RelevantPaths(ctx, root)
	if err != nil {
		return model.DiscoveryManifest{}, fmt.Errorf("enumerate dynamic discovery inputs: %w", err)
	}
	inputSnapshot, err := generation.BuildContent(ctx, root, relevantPaths)
	if err != nil {
		return model.DiscoveryManifest{}, fmt.Errorf("fingerprint dynamic discovery inputs: %w", err)
	}
	cacheIdentity := dynamic.Identity{
		ToolVersion: version.BuildID(), RuntimeVersion: version.Product,
		GodotIdentity: identity, Configuration: configuration,
		ContentFingerprint: static.ContentFingerprint,
		InputFingerprint:   inputSnapshot.ContentFingerprint(),
		Generation:         plan.Generation,
	}
	store, err := cache.Default(version.BuildID())
	if err != nil {
		manifest, computeErr := compute(ctx)
		mergeDiscoveryTimings(timings, discoveryTimings)
		return manifest, computeErr
	}
	manifest, hit, err := dynamic.LoadOrCompute(ctx, store, cacheIdentity, compute)
	if err != nil {
		return model.DiscoveryManifest{}, err
	}
	if !hit {
		mergeDiscoveryTimings(timings, discoveryTimings)
	}
	if len(manifest.Errors) > 0 {
		return model.DiscoveryManifest{}, &manifestErrors{errors: manifest.Errors}
	}
	return manifest, nil
}

func discoveryCandidates(
	manifest discovery.Manifest,
	selectors []string,
	allowEmpty bool,
) (discovery.Manifest, error) {
	fileSelectors := make([]string, 0, len(selectors))
	seen := make(map[string]struct{}, len(selectors))
	for _, selector := range selectors {
		candidate := selector
		if filename, _, ok := strings.Cut(strings.ReplaceAll(selector, `\`, "/"), "::"); ok {
			candidate = filename
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		fileSelectors = append(fileSelectors, candidate)
	}
	return discovery.Select(manifest, fileSelectors, allowEmpty)
}

func affectedCandidates(
	manifest discovery.Manifest,
	root string,
	paths []string,
) (selection.Result, error) {
	metadata, err := projectmeta.Load(root)
	if err != nil {
		return selection.Result{}, err
	}
	return selection.Apply(manifest, selection.Options{Affected: &selection.AffectedOptions{
		Paths: paths, GlobalInputs: metadata.GlobalInputs, ConservativeAll: metadata.ConservativeAll,
	}})
}

func discoverForeground(
	ctx context.Context,
	root string,
	godot string,
	plan model.TestPlan,
	streams IO,
) (model.DiscoveryManifest, map[string]time.Duration, error) {
	timings := make(map[string]time.Duration)
	workerStarted := time.Now()
	process, err := worker.Start(ctx, worker.ProcessOptions{
		Godot: godot, Project: root, SessionID: "foreground-discovery-" + newID(),
		Generation: plan.Generation, Stdout: streams.Err, Stderr: streams.Err,
	})
	timings["worker_acquisition_startup"] = time.Since(workerStarted)
	if err != nil {
		return model.DiscoveryManifest{}, timings, err
	}
	mergeNanosecondTimings(timings, process.TakeStartupTimings())
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = process.Shutdown(shutdownCtx)
	}()
	discoveryStarted := time.Now()
	manifest, err := process.Discover(ctx, plan)
	timings["engine_discovery"] = time.Since(discoveryStarted)
	return manifest, timings, err
}

func discoverDaemon(
	ctx context.Context,
	sessionID string,
	root string,
	godot string,
	workerIdleTimeout time.Duration,
	plan model.TestPlan,
) (model.DiscoveryManifest, map[string]time.Duration, error) {
	daemonClient, err := compatibleClient(true)
	if err != nil {
		return model.DiscoveryManifest{}, nil, err
	}
	response, err := daemonClient.Call(ctx, "session.discover", service.DiscoverRequest{
		SessionID: sessionID, Project: root, Godot: godot, Plan: plan,
		WorkerIdleNS: workerIdleTimeout.Nanoseconds(),
	}, nil)
	if err != nil {
		return model.DiscoveryManifest{}, nil, err
	}
	var discovered service.DiscoverResponse
	if err := json.Unmarshal(response, &discovered); err != nil {
		return model.DiscoveryManifest{}, nil, fmt.Errorf("decode daemon discovery response: %w", err)
	}
	timings := make(map[string]time.Duration, len(discovered.TimingsNS))
	mergeNanosecondTimings(timings, discovered.TimingsNS)
	return discovered.Manifest, timings, nil
}

func mergeDiscoveryTimings(destination, source map[string]time.Duration) {
	if destination == nil {
		return
	}
	for name, duration := range source {
		destination[name] = duration
	}
}

func mergeNanosecondTimings(destination map[string]time.Duration, source map[string]int64) {
	for name, nanoseconds := range source {
		if nanoseconds >= 0 {
			destination[name] = time.Duration(nanoseconds)
		}
	}
}

func daemonGeneration(
	ctx context.Context,
	sessionID string,
	root string,
	godot string,
	workerIdleTimeout time.Duration,
) (uint64, error) {
	daemonClient, err := compatibleClient(true)
	if err != nil {
		return 0, err
	}
	response, err := daemonClient.Call(ctx, "session.generation", service.GenerationRequest{
		SessionID: sessionID, Project: root, Godot: godot,
		WorkerIdleNS: workerIdleTimeout.Nanoseconds(),
	}, nil)
	if err != nil {
		return 0, err
	}
	var warmed struct {
		Generation uint64 `json:"generation"`
	}
	if err := json.Unmarshal(response, &warmed); err != nil {
		return 0, fmt.Errorf("decode daemon generation: %w", err)
	}
	if warmed.Generation == 0 {
		return 0, errors.New("daemon returned an invalid zero source generation")
	}
	return warmed.Generation, nil
}

func selectCompletedManifest(
	manifest model.DiscoveryManifest,
	static discovery.Manifest,
	root string,
	selectors []string,
	name string,
	allowEmpty bool,
	affectedSet bool,
	affected []string,
) (completedSelection, error) {
	groups := make([]selection.Group[model.DiscoveryCase], 0, len(manifest.Files))
	for _, file := range manifest.Files {
		groups = append(groups, selection.Group[model.DiscoveryCase]{Path: file.Path, Cases: file.Cases})
	}
	filtered, err := selection.FilterGroups(groups, selection.MatchOptions{
		Selectors: selectors, Name: name, AllowEmpty: allowEmpty,
	}, func(testCase model.DiscoveryCase) selection.CaseInfo {
		return selection.CaseInfo{ID: testCase.ID, Name: testCase.Name, MethodName: testCase.MethodName}
	})
	if err != nil {
		return completedSelection{}, err
	}
	result := completedSelection{manifest: manifest}
	allowed := map[string]struct{}(nil)
	if affectedSet {
		metadata, metadataErr := projectmeta.Load(root)
		if metadataErr != nil {
			return completedSelection{}, metadataErr
		}
		affectedResult, affectedErr := selection.Apply(static, selection.Options{Affected: &selection.AffectedOptions{
			Paths: affected, GlobalInputs: metadata.GlobalInputs, ConservativeAll: metadata.ConservativeAll,
		}})
		if affectedErr != nil {
			return completedSelection{}, affectedErr
		}
		result.reasons = affectedResult.Reasons
		result.message = affectedResult.Message
		allowed = make(map[string]struct{}, len(affectedResult.Manifest.Files))
		for _, file := range affectedResult.Manifest.Files {
			allowed[file.Path] = struct{}{}
		}
	}
	selectedFiles := make([]model.DiscoveryFile, 0, len(filtered))
	for _, group := range filtered {
		if allowed != nil {
			if _, ok := allowed[group.Path]; !ok {
				continue
			}
		}
		for _, sourceFile := range manifest.Files {
			if sourceFile.Path != group.Path {
				continue
			}
			selectedFile := sourceFile
			selectedFile.Cases = append([]model.DiscoveryCase(nil), group.Cases...)
			selectedFiles = append(selectedFiles, selectedFile)
			for _, testCase := range group.Cases {
				result.ids = append(result.ids, testCase.ID)
			}
			break
		}
	}
	result.manifest.Files = selectedFiles
	if affectedSet && len(result.ids) == 0 {
		result.message = "no tests were affected"
	}
	return result, nil
}

func godotIdentity(executable string) (string, error) {
	identity, err := projectidentity.ExecutableIdentity(executable)
	if err != nil {
		return "", fmt.Errorf("identify Godot executable: %w", err)
	}
	return identity, nil
}

func godotDescription(ctx context.Context, executable string) (string, string, error) {
	path, err := exec.LookPath(executable)
	if err != nil {
		return "", "", fmt.Errorf("godot executable %q was not found: %w", executable, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve Godot executable: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = resolved
	}
	versionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(versionCtx, path, "--version").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("identify Godot executable %q: %w: %s", path, err, strings.TrimSpace(string(output)))
	}
	return filepath.ToSlash(path), strings.TrimSpace(string(output)), nil
}

func validateOnlyMode(value string) error {
	switch value {
	case "auto", "ignore", "require", "forbid":
		return nil
	default:
		return fmt.Errorf("only policy %q must be auto, ignore, require, or forbid", value)
	}
}

type manifestErrors struct {
	errors []model.DiscoveryError
}

func (e *manifestErrors) writeTo(destination interface{ Write([]byte) (int, error) }) error {
	for _, finding := range e.errors {
		location := "discovery"
		if finding.Location != nil {
			location = fmt.Sprintf("%s:%d:%d", finding.Location.File, finding.Location.Line, finding.Location.Column)
		}
		if _, err := fmt.Fprintf(destination, "%s: %s: %s\n", location, finding.Code, finding.Message); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(destination, "%d errors\n", len(e.errors))
	return err
}

func (e *manifestErrors) Error() string {
	parts := make([]string, 0, len(e.errors))
	for _, finding := range e.errors {
		location := "discovery"
		if finding.Location != nil {
			location = fmt.Sprintf("%s:%d:%d", finding.Location.File, finding.Location.Line, finding.Location.Column)
		}
		parts = append(parts, fmt.Sprintf("%s: %s: %s", location, finding.Code, finding.Message))
	}
	return strings.Join(parts, "\n")
}
