// Package performance defines deterministic synthetic corpora used by gdtest benchmarks.
package performance

import (
	"fmt"
	"strings"

	"github.com/cafecito-games/gdtest/internal/model"
)

const (
	// LargeCount is the required assertion and case corpus size.
	LargeCount = 10_000
	// FileCount is the required many-file discovery corpus size.
	FileCount = 1_000
	// SessionCount exercises scheduling beyond the default global worker bound.
	SessionCount = 64
	// SuiteDepth exercises recursive dynamic-suite metadata without exceeding protocol limits.
	SuiteDepth = 32
)

// PassingAssertionsScript returns a valid case that performs 10,000 cheap passing assertions.
func PassingAssertionsScript() string {
	return "extends GDTest\n\n\n## Executes the passing-assertion corpus.\n" +
		"func test_passing_assertions() -> void:\n" +
		fmt.Sprintf("\tfor index: int in range(%d):\n", LargeCount) +
		"\t\texpect_true(index >= 0)\n"
}

// DynamicCasesScript returns a valid suite that registers 10,000 no-op cases.
func DynamicCasesScript() string {
	return "extends GDTest\n\n\n## Registers the dynamic-case corpus.\n" +
		"func suite() -> void:\n" +
		fmt.Sprintf("\tfor index: int in range(%d):\n", LargeCount) +
		"\t\ttest(\"case %d\" % index, func() -> void: pass)\n"
}

// ThousandFiles returns stable project-relative GDScript sources.
func ThousandFiles() map[string][]byte {
	files := make(map[string][]byte, FileCount)
	for index := range FileCount {
		name := fmt.Sprintf("tests/generated_%04d.test.gd", index)
		source := fmt.Sprintf(
			"extends GDTest\n\n\n## Generated discovery case.\nfunc test_%04d() -> void:\n\tpass\n",
			index,
		)
		files[name] = []byte(source)
	}
	return files
}

// TenThousandCases returns an immutable engine-completed manifest.
func TenThousandCases() model.DiscoveryManifest {
	return CaseManifest(LargeCount)
}

// CaseManifest returns an immutable engine-completed manifest of the requested size.
func CaseManifest(count int) model.DiscoveryManifest {
	if count < 0 {
		count = 0
	}
	cases := make([]model.DiscoveryCase, count)
	for index := range cases {
		name := fmt.Sprintf("case %d", index)
		cases[index] = model.DiscoveryCase{
			ID:   fmt.Sprintf("tests/cases.test.gd::dynamic@5:%d[%s]", index, strings.ReplaceAll(name, " ", "%20")),
			Name: name, File: "tests/cases.test.gd", Line: 5,
		}
	}
	return model.DiscoveryManifest{
		ProtocolVersion: model.ProtocolVersion, Generation: 1,
		ContentFingerprint: fmt.Sprintf("case-corpus-%d", count),
		Files:              []model.DiscoveryFile{{Path: "tests/cases.test.gd", Cases: cases}},
	}
}

// DeepSuiteManifest returns one case nested beneath the required suite depth.
func DeepSuiteManifest() model.DiscoveryManifest {
	suites := make([]model.DiscoverySuite, SuiteDepth)
	for index := range suites {
		suites[index] = model.DiscoverySuite{
			ID: fmt.Sprintf("suite-%02d", index), Name: fmt.Sprintf("suite %02d", index),
			File: "tests/deep.test.gd", Line: index + 1,
		}
	}
	return model.DiscoveryManifest{
		ProtocolVersion: model.ProtocolVersion, Generation: 1, ContentFingerprint: "deep-suites",
		Files: []model.DiscoveryFile{{
			Path: "tests/deep.test.gd",
			Cases: []model.DiscoveryCase{{
				ID: "tests/deep.test.gd::dynamic@40:0[leaf]", Name: "leaf",
				File: "tests/deep.test.gd", Line: 40, Suites: suites,
			}},
		}},
	}
}

// RichFailures returns failure events whose nested values exercise diagnostic serialization.
func RichFailures(count int) []model.Event {
	result := make([]model.Event, count)
	for index := range result {
		actual := model.WireValue{Type: "dictionary", Entries: []model.WireDictionaryItem{{
			Key:   model.WireValue{Type: "string", Value: "index"},
			Value: model.WireValue{Type: "array", Items: []model.WireValue{{Type: "int", Value: fmt.Sprint(index)}}},
		}}}
		expected := model.WireValue{Type: "null"}
		result[index] = model.Event{
			ProtocolVersion: model.ProtocolVersion, Type: model.EventAssertionFail,
			RunID: "failure-corpus", TestID: fmt.Sprintf("tests/failures.test.gd::test_%d", index),
			Message: "rich values differ", Actual: &actual, Expected: &expected,
		}
	}
	return result
}

// SessionPlans returns path-neutral plans for a many-session scheduler benchmark.
func SessionPlans() []model.TestPlan {
	result := make([]model.TestPlan, SessionCount)
	for index := range result {
		result[index] = model.TestPlan{
			ProtocolVersion: model.ProtocolVersion, RunID: fmt.Sprintf("session-%02d", index),
			Generation: 1,
			Files:      []model.FilePlan{{Path: fmt.Sprintf("tests/session_%02d.test.gd", index)}},
		}
	}
	return result
}
