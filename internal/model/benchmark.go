package model

// BenchmarkPlan is a backend-neutral, serializable benchmark execution plan.
type BenchmarkPlan struct {
	ProtocolVersion    int                 `json:"protocol_version"`
	RunID              string              `json:"run_id"`
	Generation         uint64              `json:"generation"`
	ContentFingerprint string              `json:"content_fingerprint"`
	Warmup             int                 `json:"warmup"`
	Samples            int                 `json:"samples"`
	Iterations         int                 `json:"iterations"`
	Files              []BenchmarkFilePlan `json:"files"`
}

// BenchmarkFilePlan is one project-relative serial benchmark scheduling unit.
type BenchmarkFilePlan struct {
	Path       string              `json:"path"`
	Benchmarks []BenchmarkCasePlan `json:"benchmarks"`
}

// BenchmarkCasePlan identifies one statically discovered benchmark method.
type BenchmarkCasePlan struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	MethodName string `json:"method_name"`
	Line       int    `json:"line"`
}

// BenchmarkEventType identifies one immutable benchmark execution event.
type BenchmarkEventType string

// Benchmark event types are shared by every benchmark executor and reporter.
const (
	EventBenchmarkRunStarted    BenchmarkEventType = "benchmark_run_started"
	EventBenchmarkStarted       BenchmarkEventType = "benchmark_started"
	EventBenchmarkSampleStarted BenchmarkEventType = "benchmark_sample_started"
	EventBenchmarkSample        BenchmarkEventType = "benchmark_sample"
	EventBenchmarkFinished      BenchmarkEventType = "benchmark_finished"
	EventBenchmarkRunFinished   BenchmarkEventType = "benchmark_run_finished"
	EventBenchmarkWorkerError   BenchmarkEventType = "benchmark_worker_error"
)

// BenchmarkEvent is the stable backend-neutral benchmark event envelope.
type BenchmarkEvent struct {
	ProtocolVersion int                `json:"protocol_version"`
	Type            BenchmarkEventType `json:"type"`
	RunID           string             `json:"run_id"`
	BenchmarkID     string             `json:"benchmark_id,omitempty"`
	Name            string             `json:"name,omitempty"`
	Status          string             `json:"status,omitempty"`
	Message         string             `json:"message,omitempty"`
	Location        *Location          `json:"location,omitempty"`
	SampleIndex     int                `json:"sample_index,omitempty"`
	Warmup          bool               `json:"warmup,omitempty"`
	DurationNS      int64              `json:"duration_ns,omitempty"`
	Iterations      int                `json:"iterations,omitempty"`
	Generation      uint64             `json:"generation,omitempty"`
	Stale           bool               `json:"stale,omitempty"`
	Metadata        map[string]any     `json:"metadata,omitempty"`
}
