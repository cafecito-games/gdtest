package performance

import (
	"errors"
	"sort"
	"time"
)

// Distribution is a stable summary of benchmark samples. Durations serialize
// as integer nanoseconds through Go's standard JSON encoder.
type Distribution struct {
	Samples int           `json:"samples"`
	Min     time.Duration `json:"min_ns"`
	Median  time.Duration `json:"median_ns"`
	P95     time.Duration `json:"p95_ns"`
	Max     time.Duration `json:"max_ns"`
}

// Summarize returns nearest-rank median and p95 values without mutating input.
func Summarize(samples []time.Duration) (Distribution, error) {
	if len(samples) == 0 {
		return Distribution{}, errors.New("performance distribution requires at least one sample")
	}
	ordered := append([]time.Duration(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return Distribution{
		Samples: len(ordered),
		Min:     ordered[0],
		Median:  ordered[(len(ordered)-1)/2],
		P95:     ordered[nearestRankIndex(len(ordered), 95)],
		Max:     ordered[len(ordered)-1],
	}, nil
}

func nearestRankIndex(length, percentile int) int {
	// ceil(length*percentile/100)-1, using integer arithmetic.
	index := (length*percentile+99)/100 - 1
	if index < 0 {
		return 0
	}
	if index >= length {
		return length - 1
	}
	return index
}
