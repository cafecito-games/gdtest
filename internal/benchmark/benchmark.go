// Package benchmark validates, folds, compares, and persists benchmark results.
package benchmark

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// SchemaVersion is the canonical benchmark result and baseline schema version.
const SchemaVersion = 1

// Environment identifies the execution environment without revealing a worktree path.
type Environment struct {
	GDTestVersion         string `json:"gdtest_version,omitempty"`
	RuntimeVersion        string `json:"runtime_version,omitempty"`
	ModelProtocolVersion  int    `json:"model_protocol_version,omitempty"`
	DaemonProtocolVersion int    `json:"daemon_protocol_version,omitempty"`
	WorkerProtocolVersion int    `json:"worker_protocol_version,omitempty"`
	GodotVersion          string `json:"godot_version,omitempty"`
	GodotHash             string `json:"godot_hash,omitempty"`
	OS                    string `json:"os,omitempty"`
	Architecture          string `json:"architecture,omitempty"`
	CPUModel              string `json:"cpu_model,omitempty"`
	LogicalCPUCount       int    `json:"logical_cpu_count"`
	DirtyWorktree         bool   `json:"dirty_worktree"`
}

// Statistics is the complete normalized sample distribution for one benchmark.
type Statistics struct {
	MinNS        int64   `json:"min_ns"`
	MedianNS     int64   `json:"median_ns"`
	P95NS        int64   `json:"p95_ns"`
	MaxNS        int64   `json:"max_ns"`
	RawSamplesNS []int64 `json:"raw_samples_ns"`
}

// Result is the single authoritative representation consumed by every reporter.
type Result struct {
	ID                 string     `json:"id"`
	File               string     `json:"file,omitempty"`
	MethodName         string     `json:"method_name,omitempty"`
	Line               int        `json:"line,omitempty"`
	Generation         uint64     `json:"generation,omitempty"`
	ContentFingerprint string     `json:"content_fingerprint,omitempty"`
	Warmup             int        `json:"warmup,omitempty"`
	SampleCount        int        `json:"sample_count"`
	Iterations         int        `json:"iterations"`
	Samples            Statistics `json:"samples"`
}

// ResultSet is one canonical, versioned benchmark artifact.
type ResultSet struct {
	SchemaVersion int         `json:"schema_version"`
	Environment   Environment `json:"environment"`
	Benchmarks    []Result    `json:"benchmarks"`
}

// Delta compares one matching stable benchmark ID by median duration.
type Delta struct {
	ID           string  `json:"id"`
	BaselineNS   int64   `json:"baseline_ns"`
	CurrentNS    int64   `json:"current_ns"`
	DeltaNS      int64   `json:"delta_ns"`
	DeltaPercent float64 `json:"delta_percent"`
}

// Comparison reports stable-ID matches and explicit additions/removals.
type Comparison struct {
	Matched []Delta  `json:"matched"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// Summarize derives deterministic statistics without reordering the raw samples.
func Summarize(samples []int64) (Statistics, error) {
	if len(samples) == 0 {
		return Statistics{}, errors.New("benchmark sample set is empty")
	}
	ordered := slices.Clone(samples)
	for _, sample := range ordered {
		if sample < 0 {
			return Statistics{}, errors.New("benchmark sample duration is negative")
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	middle := len(ordered) / 2
	median := ordered[middle]
	if len(ordered)%2 == 0 {
		median = ordered[middle-1] + (ordered[middle]-ordered[middle-1])/2
	}
	p95Index := int(math.Ceil(float64(len(ordered))*0.95)) - 1
	return Statistics{
		MinNS: ordered[0], MedianNS: median, P95NS: ordered[p95Index], MaxNS: ordered[len(ordered)-1],
		RawSamplesNS: slices.Clone(samples),
	}, nil
}

// MarshalCanonical validates and emits stable compact JSON with benchmarks sorted by ID.
func MarshalCanonical(result ResultSet) ([]byte, error) {
	result = cloneSorted(result)
	if err := Validate(result); err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode benchmark results: %w", err)
	}
	return append(data, '\n'), nil
}

// Decode strictly validates an untrusted canonical baseline artifact.
func Decode(data []byte) (ResultSet, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result ResultSet
	if err := decoder.Decode(&result); err != nil {
		return ResultSet{}, fmt.Errorf("decode benchmark baseline: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ResultSet{}, errors.New("decode benchmark baseline: unexpected trailing content")
	}
	result = cloneSorted(result)
	if err := Validate(result); err != nil {
		return ResultSet{}, err
	}
	return result, nil
}

// Validate enforces the fail-closed result and baseline contract.
func Validate(result ResultSet) error {
	if result.SchemaVersion != SchemaVersion {
		return fmt.Errorf("benchmark schema version %d does not match %d", result.SchemaVersion, SchemaVersion)
	}
	if result.Environment.LogicalCPUCount < 0 {
		return errors.New("benchmark environment logical CPU count is negative")
	}
	seen := make(map[string]struct{}, len(result.Benchmarks))
	for _, item := range result.Benchmarks {
		if item.ID == "" {
			return errors.New("benchmark result has an empty ID")
		}
		if item.Generation == 0 || item.ContentFingerprint == "" {
			return fmt.Errorf("benchmark %q needs a positive generation and content fingerprint", item.ID)
		}
		if _, exists := seen[item.ID]; exists {
			return fmt.Errorf("benchmark baseline contains duplicate ID %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		if item.SampleCount <= 0 || item.Iterations <= 0 {
			return fmt.Errorf("benchmark %q sample_count and iterations must be positive", item.ID)
		}
		if len(item.Samples.RawSamplesNS) != item.SampleCount {
			return fmt.Errorf("benchmark %q sample set is incomplete", item.ID)
		}
		statistics, err := Summarize(item.Samples.RawSamplesNS)
		if err != nil {
			return fmt.Errorf("benchmark %q: %w", item.ID, err)
		}
		if statistics.MinNS != item.Samples.MinNS || statistics.MedianNS != item.Samples.MedianNS ||
			statistics.P95NS != item.Samples.P95NS || statistics.MaxNS != item.Samples.MaxNS {
			return fmt.Errorf("benchmark %q statistics do not match raw samples or contain a negative duration", item.ID)
		}
	}
	return nil
}

// WriteAtomic preserves an existing baseline unless a complete validated artifact is ready.
func WriteAtomic(filename string, result ResultSet) error {
	data, err := MarshalCanonical(result)
	if err != nil {
		return err
	}
	directory := filepath.Dir(filename)
	temporary, err := os.CreateTemp(directory, ".gdtest-benchmark-*")
	if err != nil {
		return fmt.Errorf("create temporary benchmark baseline: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set benchmark baseline permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write benchmark baseline: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync benchmark baseline: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close benchmark baseline: %w", err)
	}
	if err := os.Rename(temporaryName, filename); err != nil {
		return fmt.Errorf("publish benchmark baseline: %w", err)
	}
	return nil
}

// Compare pairs only exact stable IDs and never assigns a pass/fail outcome.
func Compare(current, baseline ResultSet) Comparison {
	currentByID := byID(current.Benchmarks)
	baselineByID := byID(baseline.Benchmarks)
	comparison := Comparison{}
	for id, currentResult := range currentByID {
		baselineResult, exists := baselineByID[id]
		if !exists {
			comparison.Added = append(comparison.Added, id)
			continue
		}
		delta := currentResult.Samples.MedianNS - baselineResult.Samples.MedianNS
		percent := 0.0
		if baselineResult.Samples.MedianNS != 0 {
			percent = float64(delta) * 100 / float64(baselineResult.Samples.MedianNS)
		}
		comparison.Matched = append(comparison.Matched, Delta{
			ID: id, BaselineNS: baselineResult.Samples.MedianNS,
			CurrentNS: currentResult.Samples.MedianNS, DeltaNS: delta, DeltaPercent: percent,
		})
	}
	for id := range baselineByID {
		if _, exists := currentByID[id]; !exists {
			comparison.Removed = append(comparison.Removed, id)
		}
	}
	sort.Slice(comparison.Matched, func(i, j int) bool { return comparison.Matched[i].ID < comparison.Matched[j].ID })
	sort.Strings(comparison.Added)
	sort.Strings(comparison.Removed)
	return comparison
}

func cloneSorted(result ResultSet) ResultSet {
	result.Benchmarks = slices.Clone(result.Benchmarks)
	for index := range result.Benchmarks {
		result.Benchmarks[index].Samples.RawSamplesNS = slices.Clone(result.Benchmarks[index].Samples.RawSamplesNS)
	}
	sort.Slice(result.Benchmarks, func(i, j int) bool { return result.Benchmarks[i].ID < result.Benchmarks[j].ID })
	return result
}

func byID(results []Result) map[string]Result {
	indexed := make(map[string]Result, len(results))
	for _, result := range results {
		indexed[result.ID] = result
	}
	return indexed
}
