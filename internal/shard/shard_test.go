package shard_test

import (
	"math/rand"
	"strconv"
	"testing"

	"github.com/cafecito-games/gdtest/internal/shard"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func suiteChain(ids ...string) []string {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func shardIDs(shards [][]string) []string {
	var joined []string
	for _, shardIDs := range shards {
		joined = append(joined, shardIDs...)
	}
	return joined
}

func TestPartitionNeverSplitsFileModeOrSmallFiles(t *testing.T) {
	t.Parallel()

	cases := make([]shard.Case, shard.MinCasesPerShard+10)
	ids := make([]string, len(cases))
	for index := range cases {
		cases[index] = shard.Case{ID: "case-" + strconv.Itoa(index)}
		ids[index] = cases[index].ID
	}

	assert.Equal(t, [][]string{ids}, shard.Partition(cases, shard.ModeFile, 8),
		"file mode never splits")
	assert.Equal(
		t, [][]string{ids[:shard.MinCasesPerShard]},
		shard.Partition(cases[:shard.MinCasesPerShard], shard.ModeCase, 8),
		"files at the minimum threshold stay whole",
	)
	assert.Equal(t, [][]string{ids}, shard.Partition(cases, shard.ModeCase, 1),
		"a single lane leaves the file whole")
	assert.Nil(t, shard.Partition(nil, shard.ModeCase, 8), "empty input produces no shards")
}

func TestPartitionSuiteModeCutsOnlyWhereNoSuiteSpans(t *testing.T) {
	t.Parallel()

	// One dominant suite has no legal cut and stays a single shard.
	dominant := make([]shard.Case, 0, shard.MinCasesPerShard+2)
	for index := range shard.MinCasesPerShard + 2 {
		dominant = append(dominant, shard.Case{
			ID: "case-" + strconv.Itoa(index), Suites: suiteChain("A"),
		})
	}
	assert.Len(t, shard.Partition(dominant, shard.ModeSuite, 4), 1,
		"a single dominant suite cannot be split in suite mode")

	// Suite A then suite B: exactly one legal cut, two shards under jobs=2.
	var mixed []shard.Case
	for index := range 30 {
		mixed = append(mixed, shard.Case{ID: "a-" + strconv.Itoa(index), Suites: suiteChain("A")})
	}
	for index := range 6 {
		mixed = append(mixed, shard.Case{ID: "b-" + strconv.Itoa(index), Suites: suiteChain("B")})
	}
	shards := shard.Partition(mixed, shard.ModeSuite, 2)
	require.Len(t, shards, 2, "two suites with jobs=2 produce one shard per suite")
	assert.Len(t, shards[0], 30)
	assert.Len(t, shards[1], 6)
	assert.Equal(t, "a-0", shards[0][0])
	assert.Equal(t, "b-5", shards[1][5])
}

func TestPartitionSuiteModeTreatsNestedSuitesAndRootCasesCorrectly(t *testing.T) {
	t.Parallel()

	// Two waves of the same suite shape with unique case IDs; A>B -> A>C
	// shares A (illegal cut), A>C -> root-only shares nothing (legal cut).
	base := []shard.Case{
		{Suites: suiteChain("A", "B")},
		{Suites: suiteChain("A", "B")},
		{Suites: suiteChain("A", "C")},
		{Suites: suiteChain("A", "C")},
		{}, // static method case: root suite only
		{}, // root-only to root-only is a legal cut
		{Suites: suiteChain("B")},
		{Suites: suiteChain("B")},
		{Suites: suiteChain("D")},
	}
	cases := make([]shard.Case, 0, len(base)*2)
	for wave := range 2 {
		for index, value := range base {
			value.ID = "case-" + strconv.Itoa(wave*len(base)+index)
			cases = append(cases, value)
		}
	}

	legal := func(left, right shard.Case) bool {
		for _, leftSuite := range left.Suites {
			for _, rightSuite := range right.Suites {
				if leftSuite == rightSuite {
					return false
				}
			}
		}
		return true
	}
	assert.False(t, legal(cases[1], cases[2]))
	assert.True(t, legal(cases[3], cases[4]))

	shards := shard.Partition(cases, shard.ModeSuite, 3)
	require.Greater(t, len(shards), 1)
	for index := 0; index+1 < len(shards); index++ {
		last := findCase(t, cases, shards[index][len(shards[index])-1])
		first := findCase(t, cases, shards[index+1][0])
		assert.True(t, legal(last, first),
			"no non-root suite may span the boundary between shards %d and %d", index, index+1)
	}
}

func findCase(t *testing.T, cases []shard.Case, id string) shard.Case {
	t.Helper()
	for _, value := range cases {
		if value.ID == id {
			return value
		}
	}
	t.Fatalf("unknown case ID %q", id)
	return shard.Case{}
}

func TestPartitionCaseModeSplitsAnywhereAndRespectsTargetSize(t *testing.T) {
	t.Parallel()

	total := 40
	cases := make([]shard.Case, total)
	for index := range cases {
		cases[index] = shard.Case{ID: "case-" + strconv.Itoa(index), Suites: suiteChain("A")}
	}
	shards := shard.Partition(cases, shard.ModeCase, 4)
	require.Len(t, shards, 4, "case mode packs to ceil(total/jobs)")
	for _, shardIDs := range shards {
		assert.Len(t, shardIDs, 10)
	}

	// A remainder smaller than the target closes the run as the last shard.
	shards = shard.Partition(cases[:total-1], shard.ModeCase, 4)
	require.Len(t, shards, 4)
	assert.Len(t, shards[3], 9)
}

func TestPartitionReturnsFileUnsplitWhenSafetyInvariantBreaks(t *testing.T) {
	t.Parallel()

	// Duplicate input IDs violate disjointness; rerunning the whole file
	// beats silently dropping a case.
	cases := []shard.Case{}
	for index := range shard.MinCasesPerShard + 6 {
		id := "case-" + strconv.Itoa(index/2)
		cases = append(cases, shard.Case{ID: id})
	}
	ids := make([]string, 0, len(cases))
	for _, value := range cases {
		ids = append(ids, value.ID)
	}
	assert.Equal(t, [][]string{ids}, shard.Partition(cases, shard.ModeCase, 4))
}

func TestPartitionPropertiesHoldAcrossDeterministicRandomInputs(t *testing.T) {
	t.Parallel()

	generator := rand.New(rand.NewSource(20261106))
	for iteration := range 200 {
		total := generator.Intn(shard.MinCasesPerShard*3) + 1
		suiteCount := generator.Intn(5) + 1
		cases := make([]shard.Case, total)
		for index := range cases {
			depth := generator.Intn(3)
			suites := make([]string, depth)
			for level := range suites {
				suites[level] = "S" + strconv.Itoa(generator.Intn(suiteCount))
			}
			cases[index] = shard.Case{ID: "case-" + strconv.Itoa(index), Suites: suites}
		}
		mode := []string{shard.ModeSuite, shard.ModeCase}[iteration%2]
		jobs := generator.Intn(6) + 1
		ids := idsFromCases(cases)

		first := shard.Partition(cases, mode, jobs)
		second := shard.Partition(cases, mode, jobs)
		assert.Equal(t, first, second, "determinism: identical input produces identical shards")

		require.NotEmpty(t, first)
		assert.Equal(t, ids, shardIDs(first), "shards must cover the input IDs in order")

		seen := make(map[string]int, total)
		for shardIndex, shardIDs := range first {
			require.NotEmpty(t, shardIDs, "shard %d is empty", shardIndex)
			for _, id := range shardIDs {
				if previous, duplicate := seen[id]; duplicate {
					t.Fatalf("case %q appears in shards %d and %d", id, previous, shardIndex)
				}
				seen[id] = shardIndex
			}
		}

		if mode == shard.ModeSuite {
			byID := make(map[string]shard.Case, total)
			for _, value := range cases {
				byID[value.ID] = value
			}
			for index := 0; index+1 < len(first); index++ {
				last := byID[first[index][len(first[index])-1]]
				next := byID[first[index+1][0]]
				for _, left := range last.Suites {
					for _, right := range next.Suites {
						assert.NotEqual(t, left, right,
							"suite %q spans the boundary between shards %d and %d", left, index, index+1)
					}
				}
			}
		}

		if len(first) > 1 {
			assert.LessOrEqual(t, len(first), jobs,
				"shard count must respect the jobs bound")
		}
	}
}

func idsFromCases(cases []shard.Case) []string {
	ids := make([]string, len(cases))
	for index, value := range cases {
		ids[index] = value.ID
	}
	return ids
}

func TestValidateModeAcceptsOnlyKnownModes(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{shard.ModeFile, shard.ModeSuite, shard.ModeCase} {
		assert.NoError(t, shard.ValidateMode(mode))
	}
	for _, mode := range []string{"", "files", "Suite", "auto"} {
		assert.Error(t, shard.ValidateMode(mode))
	}
}
