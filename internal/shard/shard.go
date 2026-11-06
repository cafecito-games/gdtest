// Package shard partitions one file's ordered selected cases into
// deterministic same-path execution shards for intra-file parallelism.
//
// The partitioner is pure, performs no I/O, and speaks only backend-neutral
// types: it derives everything from the completed discovery manifest and
// maintains no second copy of case identity.
package shard

import (
	"fmt"
	"slices"
)

// Partition modes.
const (
	// ModeFile never splits a file; today's scheduling granularity.
	ModeFile = "file"
	// ModeSuite splits only between cases that share no non-root suite.
	ModeSuite = "suite"
	// ModeCase splits between any two consecutive cases.
	ModeCase = "case"
)

// MinCasesPerShard is the selected-case count at or below which a file is
// never split, keeping small files on a single worker.
const MinCasesPerShard = 16

// ValidateMode rejects unsupported partition mode names.
func ValidateMode(mode string) error {
	switch mode {
	case ModeFile, ModeSuite, ModeCase:
		return nil
	default:
		return fmt.Errorf("partition %q must be %s, %s, or %s", mode, ModeFile, ModeSuite, ModeCase)
	}
}

// Case is one selected case and its nested suite chain in nesting order,
// outermost first. Root-only cases such as static method tests carry no chain.
type Case struct {
	ID     string
	Suites []string
}

// Partition returns the case IDs of one file split into same-path shards.
//
// mode selects cut legality: ModeSuite cuts only between consecutive cases
// that share no non-root suite ID, ModeCase cuts anywhere, and ModeFile never
// cuts. jobs bounds the shard count; a non-positive bound leaves the file
// whole. Files at or below MinCasesPerShard are never split, and a file with
// no legal cut yields one shard without error.
//
// Packing is greedy in manifest order: a shard closes at the first legal cut
// once it reaches the target size ceil(total/shard_count), where shard_count
// is min(jobs, legal cuts + 1). The result is deterministic for identical
// input. Shards are pairwise disjoint and their concatenation equals the
// input IDs in order; when that invariant cannot be upheld — including
// duplicate input IDs — the file is returned unsplit so rerunning beats
// silently dropping a case.
func Partition(cases []Case, mode string, jobs int) [][]string {
	ids := make([]string, len(cases))
	for index, value := range cases {
		ids[index] = value.ID
	}
	unsplit := func() [][]string { return [][]string{ids} }
	if len(cases) == 0 {
		return nil
	}
	if mode == ModeFile || len(cases) <= MinCasesPerShard || len(cases) < 2 || jobs <= 1 {
		return unsplit()
	}
	legalCuts := make([]bool, len(cases)-1)
	available := 0
	for index := range legalCuts {
		if mode == ModeCase || !sharesSuite(cases[index], cases[index+1]) {
			legalCuts[index] = true
			available++
		}
	}
	shardCount := min(jobs, available+1)
	if shardCount <= 1 {
		return unsplit()
	}
	target := (len(cases) + shardCount - 1) / shardCount
	shards := make([][]string, 0, shardCount)
	current := make([]string, 0, target)
	for index, value := range cases {
		current = append(current, value.ID)
		if len(current) >= target && index < len(legalCuts) && legalCuts[index] {
			shards = append(shards, current)
			current = make([]string, 0, target)
		}
	}
	if len(current) > 0 {
		shards = append(shards, current)
	}
	if !coversInOrder(shards, ids) {
		return unsplit()
	}
	return shards
}

// sharesSuite reports whether two consecutive cases belong to a common
// non-root suite, which forbids a boundary between them in suite mode.
func sharesSuite(left, right Case) bool {
	for _, leftSuite := range left.Suites {
		for _, rightSuite := range right.Suites {
			if leftSuite == rightSuite {
				return true
			}
		}
	}
	return false
}

// coversInOrder proves the shards are pairwise disjoint and reproduce the
// input ID sequence exactly.
func coversInOrder(shards [][]string, ids []string) bool {
	joined := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, shardIDs := range shards {
		for _, id := range shardIDs {
			if _, duplicate := seen[id]; duplicate {
				return false
			}
			seen[id] = struct{}{}
			joined = append(joined, id)
		}
	}
	return slices.Equal(joined, ids)
}
