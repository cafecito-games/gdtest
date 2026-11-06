package app

import (
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/shard"
)

// automaticJobsBound mirrors the daemon's default global worker budget. When a
// project requests automatic concurrency, shards are sized for this many
// lanes; a daemon with a different budget merely queues the extra shards on
// its own lanes, which changes wall-clock interleaving but never semantics.
const automaticJobsBound = 4

// onlyPolicyError mirrors the worker's Protocol.only_policy_error so the app
// can report require/forbid violations exactly once, before any shard is
// dispatched. The message strings are a shared contract with
// addons/gdtest/protocol.gd and are pinned by tests on both sides.
func onlyPolicyError(onlyMode string, hasFocus bool) string {
	switch onlyMode {
	case "", "auto", "ignore":
		return ""
	case "require":
		if hasFocus {
			return ""
		}
		return "only mode requires at least one focused selected case"
	case "forbid":
		if hasFocus {
			return "focused tests are forbidden by only mode"
		}
		return ""
	default:
		return "unsupported only mode: " + onlyMode
	}
}

// partitionedPlan carries the plan inputs resolved app-side before dispatch:
// same-path file entries (with explicit case subsets for split files), the
// focus-resolved selection union, the plan-level focus stamp, and any
// only-mode policy violation to report exactly once.
type partitionedPlan struct {
	files       []model.FilePlan
	selectedIDs []string
	hasFocus    *bool
	policyError string
	sharded     map[string]struct{}
}

// resolvePartition resolves plan focus app-side, pre-flights the only-mode
// policy, and partitions each large selected file into same-path shards.
//
// staticFiles is the static plan's file entries (in plan order) and supplies
// each shard entry's static method cases; selectedFiles is the completed
// selection whose cases carry the suite chains the suite cut rule needs.
// Every completed-manifest path comes from a static plan entry, so iterating
// the static files preserves today's plan order and keeps entries for files
// with no selected cases byte-identical.
func resolvePartition(
	staticFiles []model.FilePlan,
	selectedFiles []model.DiscoveryFile,
	selectedIDs []string,
	onlyMode string,
	mode string,
	jobs int,
) partitionedPlan {
	hasFocus := false
	for _, file := range selectedFiles {
		for _, testCase := range file.Cases {
			if testCase.IsOnly {
				hasFocus = true
			}
		}
	}
	resolved := partitionedPlan{hasFocus: &hasFocus}
	resolved.policyError = onlyPolicyError(onlyMode, hasFocus)

	// Mirror the worker's focus filter: with focus present, only focused
	// cases execute unless the only policy ignores focus.
	focusFilter := hasFocus && onlyMode != "ignore"
	if focusFilter {
		focused := make(map[string]struct{})
		for _, file := range selectedFiles {
			for _, testCase := range file.Cases {
				if testCase.IsOnly {
					focused[testCase.ID] = struct{}{}
				}
			}
		}
		kept := make([]string, 0, len(focused))
		for _, id := range selectedIDs {
			if _, isSelected := focused[id]; isSelected {
				kept = append(kept, id)
			}
		}
		resolved.selectedIDs = kept
	} else {
		resolved.selectedIDs = append([]string(nil), selectedIDs...)
	}

	selectedByPath := make(map[string][]model.DiscoveryCase, len(selectedFiles))
	for _, file := range selectedFiles {
		selectedByPath[file.Path] = file.Cases
	}
	jobsBound := jobs
	if jobsBound <= 0 {
		jobsBound = automaticJobsBound
	}
	// Size shards against the whole selected run, not against every file in
	// isolation. File-level fan-out already fills the worker lanes in a large
	// suite; splitting every eligible file up to jobs multiplies registration
	// and hook overhead without exposing more useful concurrency. The global
	// target keeps approximately jobs case-count units across the run while a
	// single dominant file can still consume every otherwise-idle lane.
	inputs := make(map[string][]shard.Case, len(selectedFiles))
	totalCases := 0
	for path, fileCases := range selectedByPath {
		input := make([]shard.Case, 0, len(fileCases))
		for _, testCase := range fileCases {
			if focusFilter && !testCase.IsOnly {
				continue
			}
			suites := make([]string, len(testCase.Suites))
			for index, suite := range testCase.Suites {
				suites[index] = suite.ID
			}
			input = append(input, shard.Case{ID: testCase.ID, Suites: suites})
		}
		inputs[path] = input
		totalCases += len(input)
	}
	targetCases := totalCases
	if jobsBound > 0 && totalCases > 0 {
		targetCases = (totalCases + jobsBound - 1) / jobsBound
	}
	resolved.files = make([]model.FilePlan, 0, len(staticFiles))
	for _, staticEntry := range staticFiles {
		input, isSelected := inputs[staticEntry.Path]
		if !isSelected || len(input) == 0 {
			resolved.files = append(resolved.files, staticEntry)
			continue
		}
		shardBound := 1
		if targetCases > 0 {
			shardBound = min(jobsBound, (len(input)+targetCases-1)/targetCases)
		}
		shards := shard.Partition(input, mode, shardBound)
		if len(shards) <= 1 {
			resolved.files = append(resolved.files, staticEntry)
			continue
		}
		for _, shardIDs := range shards {
			entry := staticEntry
			entry.CaseIDs = shardIDs
			resolved.files = append(resolved.files, entry)
		}
		if resolved.sharded == nil {
			resolved.sharded = make(map[string]struct{})
		}
		resolved.sharded[staticEntry.Path] = struct{}{}
	}
	return resolved
}

// shardedFileEventSink drops worker_error diagnostics that repeat across
// shards of one file. Worker-side re-registration diagnostics are
// deterministic per file content, so every shard of a file can emit the same
// event; the first survives and later duplicates are dropped.
type shardedFileEventSink struct {
	inner   executor.EventSink
	sharded map[string]struct{}
	seen    map[string]struct{}
}

// newShardedFileEventSink wraps inner with per-file worker_error deduplication
// for the given sharded paths.
func newShardedFileEventSink(inner executor.EventSink, sharded map[string]struct{}) executor.EventSink {
	return &shardedFileEventSink{inner: inner, sharded: sharded, seen: make(map[string]struct{})}
}

func (s *shardedFileEventSink) Emit(event model.Event) error {
	if event.Type != model.EventWorkerError || event.Location == nil {
		return s.inner.Emit(event)
	}
	if _, isSharded := s.sharded[event.Location.File]; !isSharded {
		return s.inner.Emit(event)
	}
	key := event.Location.File + "\x00" + event.Name + "\x00" + event.Message
	if _, duplicate := s.seen[key]; duplicate {
		return nil
	}
	s.seen[key] = struct{}{}
	return s.inner.Emit(event)
}
