package benchmark

import (
	"errors"
	"fmt"

	"github.com/cafecito-games/gdtest/internal/model"
)

type collected struct {
	plan         model.BenchmarkCasePlan
	samples      []int64
	warmups      int
	started      bool
	finished     bool
	sampleActive bool
	sampleWarmup bool
	sampleIndex  int
	status       string
}

// Collector folds one immutable benchmark event stream into authoritative results.
type Collector struct {
	plan       model.BenchmarkPlan
	benchmarks map[string]*collected
	order      []string
	started    bool
	finished   bool
	status     string
}

// NewCollector creates a collector bound to exactly one benchmark plan.
func NewCollector(plan model.BenchmarkPlan) *Collector {
	collector := &Collector{plan: plan, benchmarks: make(map[string]*collected)}
	for _, file := range plan.Files {
		for _, item := range file.Benchmarks {
			copy := item
			collector.benchmarks[item.ID] = &collected{plan: copy}
			collector.order = append(collector.order, item.ID)
		}
	}
	return collector
}

// EmitBenchmark validates and folds one event exactly once.
func (c *Collector) EmitBenchmark(event model.BenchmarkEvent) error {
	if event.RunID != c.plan.RunID {
		return fmt.Errorf("benchmark event run ID %q does not match %q", event.RunID, c.plan.RunID)
	}
	switch event.Type {
	case model.EventBenchmarkRunStarted:
		if c.started {
			return errors.New("duplicate benchmark_run_started event")
		}
		c.started = true
	case model.EventBenchmarkStarted:
		item, err := c.item(event.BenchmarkID)
		if err != nil {
			return err
		}
		if item.started {
			return fmt.Errorf("duplicate benchmark_started event for %s", event.BenchmarkID)
		}
		item.started = true
	case model.EventBenchmarkSampleStarted:
		item, err := c.item(event.BenchmarkID)
		if err != nil {
			return err
		}
		if !item.started || item.finished || item.sampleActive {
			return fmt.Errorf("sample %d started for inactive benchmark %s", event.SampleIndex, event.BenchmarkID)
		}
		if event.Iterations != c.plan.Iterations {
			return fmt.Errorf("benchmark %s sample iterations %d do not match plan %d", event.BenchmarkID, event.Iterations, c.plan.Iterations)
		}
		expectedIndex := len(item.samples)
		if event.Warmup {
			expectedIndex = item.warmups
		}
		if event.SampleIndex != expectedIndex {
			return fmt.Errorf("out-of-order sample start %d for %s, want %d", event.SampleIndex, event.BenchmarkID, expectedIndex)
		}
		item.sampleActive = true
		item.sampleWarmup = event.Warmup
		item.sampleIndex = event.SampleIndex
	case model.EventBenchmarkSample:
		item, err := c.item(event.BenchmarkID)
		if err != nil {
			return err
		}
		if !item.started || item.finished || !item.sampleActive {
			return fmt.Errorf("sample for inactive benchmark %s", event.BenchmarkID)
		}
		if event.Warmup != item.sampleWarmup || event.SampleIndex != item.sampleIndex {
			return fmt.Errorf("sample identity does not match its start for %s", event.BenchmarkID)
		}
		if event.Iterations != c.plan.Iterations {
			return fmt.Errorf("benchmark %s sample iterations %d do not match plan %d", event.BenchmarkID, event.Iterations, c.plan.Iterations)
		}
		item.sampleActive = false
		if event.DurationNS < 0 {
			return fmt.Errorf("benchmark %s sample duration is negative", event.BenchmarkID)
		}
		if event.Warmup {
			item.warmups++
			return nil
		}
		if event.SampleIndex != len(item.samples) {
			return fmt.Errorf("duplicate sample or out-of-order sample %d for %s", event.SampleIndex, event.BenchmarkID)
		}
		item.samples = append(item.samples, event.DurationNS)
	case model.EventBenchmarkFinished:
		item, err := c.item(event.BenchmarkID)
		if err != nil {
			return err
		}
		if item.finished || item.sampleActive {
			return fmt.Errorf("duplicate benchmark_finished event for %s", event.BenchmarkID)
		}
		item.finished = true
		item.status = event.Status
		if event.Status != "pass" {
			return fmt.Errorf("benchmark %s ended with status %s: %s", event.BenchmarkID, event.Status, event.Message)
		}
	case model.EventBenchmarkRunFinished:
		if c.finished {
			return errors.New("duplicate benchmark_run_finished event")
		}
		c.finished = true
		c.status = event.Status
	case model.EventBenchmarkWorkerError:
		return fmt.Errorf("benchmark worker error: %s", event.Message)
	default:
		return fmt.Errorf("unknown benchmark event type %q", event.Type)
	}
	return nil
}

// Results returns only complete successful distributions.
func (c *Collector) Results(environment Environment) (ResultSet, error) {
	if !c.started || !c.finished {
		return ResultSet{}, errors.New("benchmark run is incomplete")
	}
	if c.status != "pass" {
		return ResultSet{}, fmt.Errorf("benchmark run ended with status %s", c.status)
	}
	result := ResultSet{SchemaVersion: SchemaVersion, Environment: environment}
	for _, id := range c.order {
		item := c.benchmarks[id]
		if !item.started || !item.finished || item.status != "pass" || item.warmups != c.plan.Warmup || len(item.samples) != c.plan.Samples {
			return ResultSet{}, fmt.Errorf("benchmark %s has an incomplete sample set: got %d, want %d", id, len(item.samples), c.plan.Samples)
		}
		statistics, err := Summarize(item.samples)
		if err != nil {
			return ResultSet{}, fmt.Errorf("benchmark %s: %w", id, err)
		}
		filename := ""
		for _, file := range c.plan.Files {
			for _, planned := range file.Benchmarks {
				if planned.ID == id {
					filename = file.Path
				}
			}
		}
		result.Benchmarks = append(result.Benchmarks, Result{
			ID: id, File: filename, MethodName: item.plan.MethodName, Line: item.plan.Line,
			Generation: c.plan.Generation, ContentFingerprint: c.plan.ContentFingerprint,
			Warmup: c.plan.Warmup, SampleCount: c.plan.Samples, Iterations: c.plan.Iterations,
			Samples: statistics,
		})
	}
	return result, nil
}

func (c *Collector) item(id string) (*collected, error) {
	item, exists := c.benchmarks[id]
	if !exists {
		return nil, fmt.Errorf("event references unknown benchmark %q", id)
	}
	return item, nil
}
