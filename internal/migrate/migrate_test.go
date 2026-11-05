package migrate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommonVestFixtureMigratesToGoldenAndIsIdempotent(t *testing.T) {
	t.Parallel()
	input := fixture(t, "common.input.gd")
	want := fixture(t, "common.expected.gd")
	result, err := Transform("tests/common.test.gd", input)
	require.NoError(t, err)
	assert.True(t, result.Changed)
	assert.Equal(t, string(want), string(result.Output))
	assert.Equal(t, []string{
		"rewrite.base-class",
		"rewrite.utility",
		"rewrite.hook-type",
		"rewrite.hook-type", "rewrite.utility",
		"rewrite.parameter-provider",
		"rewrite.utility",
		"rewrite.fluent-assertion",
		"rewrite.inherited-name-conflict",
	}, findingCodes(result.Findings))
	plan, err := discovery.Parse("tests/common.test.gd", result.Output)
	require.NoError(t, err, "migrated output must satisfy gdtest's static provider contract")
	assert.Len(t, plan.Cases, 2)

	again, err := Transform("tests/common.test.gd", result.Output)
	require.NoError(t, err)
	assert.False(t, again.Changed)
	assert.Equal(t, result.Output, again.Output)
	assert.Empty(t, again.Findings)
}

func TestVestMessageIsOnlyRewrittenInActiveCaseContexts(t *testing.T) {
	t.Parallel()

	source := []byte(`extends VestTest

func suite() -> void:
	Vest.message("discovery")

func before_suite(_info: VestDefs.Suite) -> void:
	Vest.message("suite")

func before_case(_info: VestDefs.Case) -> void:
	Vest.message("case hook")

func test_message() -> void:
	Vest.message("case")
`)
	result, err := Transform("tests/messages.test.gd", source)
	require.NoError(t, err)
	assert.Contains(t, string(result.Output), `Vest.message("discovery")`)
	assert.Contains(t, string(result.Output), `Vest.message("suite")`)
	assert.Contains(t, string(result.Output), `message("case hook")`)
	assert.Contains(t, string(result.Output), `message("case")`)
	assert.Equal(t, []string{
		"rewrite.base-class",
		"unsupported.message-context",
		"rewrite.hook-type", "unsupported.message-context",
		"rewrite.hook-type", "rewrite.utility",
		"rewrite.utility",
	}, findingCodes(result.Findings))
}

func TestMigrationReportsNamesThatConflictWithInheritedGDTestAPI(t *testing.T) {
	t.Parallel()

	source := []byte(`extends VestTest

func helper(message: String) -> void:
	var time: float = 0.0
	var harmless: String = message

func test_conflict() -> void:
	var fail: bool = false
	var callback := func(skip: bool) -> void: pass
	for message: String in [] as Array[String]:
		pass
`)
	result, err := Transform("tests/names.test.gd", source)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"rewrite.base-class",
		"rewrite.inherited-name-conflict",
		"rewrite.inherited-name-conflict",
		"rewrite.inherited-name-conflict",
		"rewrite.inherited-name-conflict",
		"rewrite.inherited-name-conflict",
	}, findingCodes(result.Findings))
	assert.Equal(t, 5, strings.Count(string(result.Output), `@warning_ignore("shadowed_variable_base_class")`))
	for _, finding := range result.Findings[1:] {
		assert.Equal(t, Info, finding.Severity)
		assert.True(t, finding.Planned)
		assert.Contains(t, finding.Message, "suppress inherited gdtest API shadowing")
	}
}

func TestMigrationLeavesActualInheritedMethodAndMemberOverridesForReview(t *testing.T) {
	t.Parallel()

	source := []byte(`extends VestTest

var message: String = "member"

func reference() -> bool:
	return false
`)
	result, err := Transform("tests/overrides.test.gd", source)
	require.NoError(t, err)

	assert.NotContains(t, string(result.Output), `@warning_ignore("shadowed_variable_base_class")`)
	assert.Equal(t, []string{
		"rewrite.base-class",
		"unsupported.inherited-name-conflict",
		"unsupported.inherited-name-conflict",
	}, findingCodes(result.Findings))
	assert.Equal(t, Warning, result.Findings[1].Severity)
	assert.Equal(t, Warning, result.Findings[2].Severity)
}

func TestMigrationIgnoresInheritedNamesInsideNestedClasses(t *testing.T) {
	t.Parallel()

	source := []byte(`extends VestTest

class Spy extends RefCounted:
	func emit_failed(message: String) -> void:
		var reference: Variant = message

func test_conflict() -> void:
	var message := "root"
`)
	result, err := Transform("tests/nested-names.test.gd", source)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"rewrite.base-class",
		"rewrite.inherited-name-conflict",
	}, findingCodes(result.Findings))
	assert.Equal(t, 8, result.Findings[1].Line)
}

func TestVestTreeAccessInNestedClassesUsesTheEngineSceneTree(t *testing.T) {
	t.Parallel()

	source := []byte(`extends VestTest

class Rig extends RefCounted:
	func attach() -> void:
		Vest.get_tree().root.add_child(Node.new())

func helper() -> void:
	await Vest.sleep(0.0)
`)
	result, err := Transform("tests/nested.test.gd", source)
	require.NoError(t, err)

	assert.Contains(t, string(result.Output), "(Engine.get_main_loop() as SceneTree).root.add_child")
	assert.Contains(t, string(result.Output), "await sleep(0.0)")
	assert.Equal(t, []string{
		"rewrite.base-class",
		"rewrite.utility",
		"rewrite.utility",
	}, findingCodes(result.Findings))
}

func TestContextualFluentAssertionsProduceManualFindingsWithoutRewrites(t *testing.T) {
	t.Parallel()

	source := []byte(`extends VestTest

func consume(_value: Variant) -> void:
	pass

func helper() -> Variant:
	return assert_that(1).is_equal_to(1)

func test_contexts() -> void:
	var saved: Variant = assert_that(2).is_not_null()
	consume(assert_that(3).is_true())
`)
	result, err := Transform("tests/contextual.test.gd", source)
	require.NoError(t, err)
	assert.Equal(t, strings.Replace(string(source), "extends VestTest", "extends GDTest", 1), string(result.Output))
	assert.Equal(t, []string{
		"rewrite.base-class",
		"unsupported.fluent-assertion-context",
		"unsupported.fluent-assertion-context",
		"unsupported.fluent-assertion-context",
	}, findingCodes(result.Findings))
	for _, finding := range result.Findings[1:] {
		assert.Equal(t, Warning, finding.Severity)
		assert.Positive(t, finding.Line)
		assert.Positive(t, finding.Column)
	}
}

func TestParameterProviderWrappersSatisfyDiscoveryReturnContract(t *testing.T) {
	t.Parallel()
	source := []byte(`extends VestTest

func rows():
	return [[1]]

func mapped_rows() -> Dictionary:
	return {"one": [1]}

func test_first(value: int, _params: String = "rows") -> void:
	pass

func test_second(value: int, _params: String = "mapped_rows") -> void:
	pass
`)
	result, err := Transform("tests/providers.test.gd", source)
	require.NoError(t, err)
	assert.Contains(t, string(result.Output), "func test_first__cases():")
	assert.NotContains(t, string(result.Output), "func test_first__cases() -> Variant")
	assert.Contains(t, string(result.Output), "func test_second__cases() -> Dictionary:")

	plan, err := discovery.Parse("tests/providers.test.gd", result.Output)
	require.NoError(t, err, "all generated providers must satisfy static discovery")
	assert.Len(t, plan.Cases, 2)
}

func TestVestProviderInFirstDefaultArgumentKeepsTheDataParameter(t *testing.T) {
	t.Parallel()
	source := []byte(`extends VestTest

func _slug_forms() -> Array[Variant]:
	return [["dire-wolf", "Dire Wolf"], ["", ""]]

func test_template_id_name(template_id: String = "_slug_forms", expected: String = "") -> void:
	expect_equal(template_id.capitalize(), expected)
`)
	result, err := Transform("tests/first-default-provider.test.gd", source)
	require.NoError(t, err)

	assert.Contains(t, string(result.Output), `func test_template_id_name(template_id: String, expected: String = "") -> void:`)
	assert.Contains(t, string(result.Output), "func test_template_id_name__cases() -> Array[Variant]:")
	assert.Contains(t, string(result.Output), "\treturn _slug_forms()")
	assert.NotContains(t, string(result.Output), `template_id: String = "_slug_forms"`)
	assert.Equal(t, []string{
		"rewrite.base-class",
		"rewrite.parameter-provider",
	}, findingCodes(result.Findings))

	plan, err := discovery.Parse("tests/first-default-provider.test.gd", result.Output)
	require.NoError(t, err)
	assert.Len(t, plan.Cases, 1)
}

func TestMigrationDoesNotInterpretGDTestDefaultsAsVestProviders(t *testing.T) {
	t.Parallel()
	source := []byte(`extends GDTest

func rows() -> Array[Variant]:
	return [["rows"]]

func test_label(value: String = "rows") -> void:
	expect_equal(value, "rows")
`)
	result, err := Transform("tests/already-gdtest.test.gd", source)
	require.NoError(t, err)

	assert.False(t, result.Changed)
	assert.Equal(t, source, result.Output)
	assert.Empty(t, result.Findings)
}

func TestUnsupportedVestFeaturesRemainIntactWithExactFindings(t *testing.T) {
	t.Parallel()
	input := fixture(t, "unsupported.input.gd")
	want := fixture(t, "unsupported.expected.gd")
	result, err := Transform("tests/unsupported.test.gd", input)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(result.Output))
	assert.Equal(t, []string{
		"rewrite.base-class",
		"unsupported.lifecycle-signal",
		"unsupported.runtime-mock",
		"unsupported.runtime-mock",
		"unsupported.runtime-mock",
		"unsupported.benchmark",
		"unsupported.signal-capture", "unsupported.lifecycle-signal",
		"unsupported.signal-capture", "unsupported.lifecycle-signal",
		"unsupported.timeout",
		"unsupported.fluent-assertion",
	}, findingCodes(result.Findings))
	for _, finding := range result.Findings[1:] {
		assert.Equal(t, Warning, finding.Severity)
		assert.Positive(t, finding.Line)
		assert.Positive(t, finding.Column)
		assert.False(t, finding.Planned)
	}
	var golden []Finding
	require.NoError(t, json.Unmarshal(fixture(t, "unsupported.findings.json"), &golden))
	assert.Equal(t, golden, result.Findings)
	encoded, err := json.Marshal(result.Findings)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), t.TempDir(), "findings must not expose absolute worktree paths")
}

func TestDeferredVestDefinitionsAndHelpersProducePreciseFindingsWithoutEdits(t *testing.T) {
	t.Parallel()
	source := []byte(`extends VestTestMixin

var suite: VestDefs.Suite

func test_helper() -> void:
	Vest.get_tests_root()
	VestDefs.Case.new()
`)
	result, err := Transform("tests/deferred.test.gd", source)
	require.NoError(t, err)
	assert.False(t, result.Changed)
	assert.Equal(t, source, result.Output)
	assert.Equal(t, []string{
		"unsupported.mixin",
		"unsupported.mutable-definition",
		"unsupported.vest-singleton",
		"unsupported.mutable-definition",
	}, findingCodes(result.Findings))
	for _, finding := range result.Findings {
		assert.Equal(t, Warning, finding.Severity)
		assert.Positive(t, finding.Line)
		assert.Positive(t, finding.Column)
		assert.False(t, finding.Planned)
	}
}

func TestRunCheckDoesNotMutateAndWriteIsAtomicAndIdempotent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	name := "tests/common.test.gd"
	filename := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	input := fixture(t, "common.input.gd")
	want := fixture(t, "common.expected.gd")
	require.NoError(t, os.WriteFile(filename, input, 0o640))

	checked, err := Run(context.Background(), root, []string{name}, Check)
	require.NoError(t, err)
	assert.Equal(t, 1, checked.Changed)
	assert.Equal(t, input, mustRead(t, filename))

	written, err := Run(context.Background(), root, []string{name}, Write)
	require.NoError(t, err)
	assert.Equal(t, 1, written.Changed)
	assert.Equal(t, want, mustRead(t, filename))
	info, err := os.Stat(filename)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())

	idempotent, err := Run(context.Background(), root, []string{name}, Write)
	require.NoError(t, err)
	assert.Zero(t, idempotent.Changed)
	assert.Equal(t, want, mustRead(t, filename))
}

func TestRunPreparesEveryFileBeforeWriting(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	good := filepath.Join(root, "good.gd")
	bad := filepath.Join(root, "bad.gd")
	input := []byte("extends VestTest\n")
	require.NoError(t, os.WriteFile(good, input, 0o600))
	require.NoError(t, os.WriteFile(bad, []byte("func broken(:\n"), 0o600))
	_, err := Run(context.Background(), root, []string{"good.gd", "bad.gd"}, Write)
	require.Error(t, err)
	assert.Equal(t, input, mustRead(t, good), "no file is published when preparation fails")
}

func TestRunRejectsEscapingPathsAndSymlinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, err := Run(context.Background(), root, []string{"../outside.gd"}, Check)
	require.Error(t, err)

	outside := filepath.Join(t.TempDir(), "outside.gd")
	require.NoError(t, os.WriteFile(outside, []byte("extends VestTest\n"), 0o600))
	link := filepath.Join(root, "linked.gd")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err = Run(context.Background(), root, []string{"linked.gd"}, Check)
	require.Error(t, err)
}

func findingCodes(findings []Finding) []string {
	result := make([]string, len(findings))
	for index, finding := range findings {
		result[index] = finding.Code
	}
	return result
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	return mustRead(t, filepath.Join("..", "..", "fixtures", "migration", name))
}

func mustRead(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	require.NoError(t, err)
	return data
}

func TestSignalCapturesConvertOnlyProvenBindingsAndPreserveAmbiguousUses(t *testing.T) {
	t.Parallel()
	input := fixture(t, "signal_capture.input.gd")
	want := fixture(t, "signal_capture.expected.gd")
	result, err := Transform("tests/signal_capture.test.gd", input)
	require.NoError(t, err)
	assert.True(t, result.Changed)
	assert.Equal(t, string(want), string(result.Output))
	assert.Equal(t, []string{
		"rewrite.base-class",
		"rewrite.signal-capture",
		"rewrite.signal-read", "rewrite.signal-read", "rewrite.signal-read",
		"rewrite.signal-capture",
		"rewrite.signal-capture",
		"rewrite.signal-read",
		"unsupported.signal-capture", "unsupported.signal-capture",
		"unsupported.signal-capture", "unsupported.signal-capture",
		"unsupported.signal-capture", "unsupported.signal-capture",
		"unsupported.signal-capture", "unsupported.signal-capture",
		"rewrite.signal-capture", "rewrite.signal-read",
		"rewrite.signal-capture", "rewrite.signal-read",
		"rewrite.signal-capture", "rewrite.signal-read", "rewrite.signal-read",
		"unsupported.signal-capture",
	}, findingCodes(result.Findings))
	plan, err := discovery.Parse("tests/signal_capture.test.gd", result.Output)
	require.NoError(t, err, "migrated output must satisfy gdtest's static discovery contract")
	assert.Len(t, plan.Cases, 10)

	again, err := Transform("tests/signal_capture.test.gd", result.Output)
	require.NoError(t, err)
	assert.False(t, again.Changed)
	assert.Equal(t, result.Output, again.Output)
}

func TestSignalCapturesPreserveSourceWhenReadsCannotBeAppliedWhole(t *testing.T) {
	t.Parallel()
	source := []byte(`extends VestTest

func test_fluent() -> void:
	capture_signal(source.changed, 1)
	assert_that(get_signal_emissions(source.changed).size()).is_equal_to(0)

func test_helper_read() -> void:
	capture_signal(_source.changed, 1)
	_check()

func test_alias_helper_read() -> void:
	var source := _source
	capture_signal(source.changed, 1)
	_check()

func test_initializer_capture() -> void:
	var result = capture_signal(_source.other, 1)
	expect_equal(get_signal_emissions(_source.other).size(), 0)

func _check() -> void:
	expect_equal(get_signal_emissions(_source.changed).size(), 0)
`)
	result, err := Transform("tests/blocked.test.gd", source)
	require.NoError(t, err)
	assert.Contains(t, string(result.Output), "capture_signal(source.changed, 1)")
	assert.Contains(t, string(result.Output), "capture_signal(_source.changed, 1)")
	assert.Contains(t, string(result.Output), "capture_signal(_source.other, 1)")
	assert.NotContains(t, string(result.Output), "watch_signal(")
	for _, finding := range result.Findings {
		assert.NotEqual(t, "rewrite.signal-capture", finding.Code)
	}
}

func TestSignalCapturesDoNotShadowUserDeclaredProbeHelper(t *testing.T) {
	t.Parallel()
	source := []byte(`extends VestTest

func watch_signal(_what: Signal) -> Variant:
	return null

func test_user_helper() -> void:
	capture_signal(source.changed, 1)
	expect_equal(get_signal_emissions(source.changed).size(), 0)
`)
	result, err := Transform("tests/shadow.test.gd", source)
	require.NoError(t, err)
	assert.Contains(t, string(result.Output), "capture_signal(source.changed, 1)")
	assert.NotContains(t, string(result.Output), "var changed_probe")
	assert.Contains(t, findingCodes(result.Findings), "unsupported.signal-capture")
}

func TestSignalCapturesPreserveWhenAnUnmatchedLocalAliasIsRead(t *testing.T) {
	t.Parallel()
	source := []byte(`extends VestTest

func test_local_alias() -> void:
	var changed_signal := source.changed
	capture_signal(source.changed, 1)
	expect_equal(get_signal_emissions(changed_signal).size(), 1)
`)
	result, err := Transform("tests/alias.test.gd", source)
	require.NoError(t, err)
	assert.Contains(t, string(result.Output), "capture_signal(source.changed, 1)")
	assert.Contains(t, string(result.Output), "get_signal_emissions(changed_signal)")
	assert.NotContains(t, string(result.Output), "watch_signal(")
}

func TestSignalProbeNamesAvoidLambdaParametersAndRootSignalsReportConflicts(t *testing.T) {
	t.Parallel()
	source := []byte(`extends VestTest

signal watch_signal

func test_lambda_parameter() -> void:
	capture_signal(source.changed, 1)
	var check := func(changed_probe: Variant) -> void:
		expect_equal(get_signal_emissions(source.changed).size(), 1)
	check.call(null)
`)
	result, err := Transform("tests/lambda.test.gd", source)
	require.NoError(t, err)
	assert.Contains(t, string(result.Output), "capture_signal(source.changed, 1)")
	assert.NotContains(t, string(result.Output), "var changed_probe :=")
	assert.Contains(t, findingCodes(result.Findings), "unsupported.inherited-name-conflict")
}

func TestSignalCapturesPreserveShadowedOwnersAndNegativeIndexes(t *testing.T) {
	t.Parallel()
	source := []byte(`extends VestTest

func test_lambda_shadows_owner() -> void:
	var source := Source.new()
	capture_signal(source.changed, 1)
	var check := func(source: Variant) -> void:
		expect_equal(get_signal_emissions(source.changed).size(), 1)
	check.call(null)

func test_negative_index() -> void:
	var source := Source.new()
	capture_signal(source.changed, 1)
	expect_equal(get_signal_emissions(source.changed)[-1][0], 1)
`)
	result, err := Transform("tests/shadow_index.test.gd", source)
	require.NoError(t, err)
	assert.Contains(t, string(result.Output), "capture_signal(source.changed, 1)")
	assert.Contains(t, string(result.Output), "get_signal_emissions(source.changed)[-1][0]")
	assert.NotContains(t, string(result.Output), "watch_signal(")
}

func TestSignalMigrationIsScopedToVestFilesAndRejectsMutatingReads(t *testing.T) {
	t.Parallel()
	plain := []byte(`extends Node

func capture_signal(_value: Variant, _count: int) -> void:
	pass

func test_local_helper() -> void:
	capture_signal(source.changed, 1)
`)
	result, err := Transform("app/helper.gd", plain)
	require.NoError(t, err)
	assert.False(t, result.Changed)
	assert.Empty(t, result.Findings)

	mutated := []byte(`extends VestTest

func test_indexed_write() -> void:
	var source := Source.new()
	capture_signal(source.changed, 1)
	get_signal_emissions(source.changed)[0][1] = 5
	get_signal_emissions(source.changed)[0].append(6)
`)
	mutatedResult, err := Transform("tests/mutated.test.gd", mutated)
	require.NoError(t, err)
	assert.Contains(t, string(mutatedResult.Output), "capture_signal(source.changed, 1)")
	assert.NotContains(t, string(mutatedResult.Output), "watch_signal(")

	matchShadow := []byte(`extends VestTest

var _source := Source.new()

func test_match_binding() -> void:
	capture_signal(_source.changed, 1)
	match Source.new():
		var _source:
			expect_equal(get_signal_emissions(_source.changed).size(), 1)
`)
	matchResult, err := Transform("tests/match.test.gd", matchShadow)
	require.NoError(t, err)
	assert.Contains(t, string(matchResult.Output), "capture_signal(_source.changed, 1)")
	assert.NotContains(t, string(matchResult.Output), "watch_signal(")
}
