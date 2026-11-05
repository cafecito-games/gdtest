package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gdtest "github.com/cafecito-games/gdtest"
	"github.com/cafecito-games/gdtest/internal/app"
	"github.com/cafecito-games/gdtest/internal/discovery"
	"github.com/cafecito-games/gdtest/internal/executor"
	"github.com/cafecito-games/gdtest/internal/model"
	"github.com/cafecito-games/gdtest/internal/results"
	"github.com/cafecito-games/gdtest/internal/worker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type decodedJSONEvent struct {
	Type      string           `json:"type"`
	TestID    string           `json:"test_id"`
	Name      string           `json:"name"`
	Status    string           `json:"status"`
	Assertion string           `json:"assertion"`
	Message   string           `json:"message"`
	Location  *model.Location  `json:"location"`
	Metadata  map[string]any   `json:"metadata"`
	Summary   *results.Summary `json:"summary"`
}

func setupTempGodotProject(
	t *testing.T,
	projectGodotExtra string,
	configurationJSON string,
	files map[string]string,
) string {
	t.Helper()
	if _, err := exec.LookPath("godot"); err != nil {
		t.Skip("Godot leak-check integration test skipped: godot is not on PATH")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	projectGodot := "config_version=5\n\n[application]\nconfig/name=\"gdtest leak fixture\"\n"
	if projectGodotExtra != "" {
		projectGodot += "\n" + projectGodotExtra + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte(projectGodot), 0o600))
	if configurationJSON != "" {
		require.NoError(t, os.WriteFile(filepath.Join(root, "gdtest.json"), []byte(configurationJSON), 0o600))
	}
	require.NoError(t, gdtest.InstallRuntime(root))
	for relative, content := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(relative))
		require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o750))
		require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o600))
	}
	return root
}

func runCLIJSON(t *testing.T, root string, extraArguments ...string) (int, []decodedJSONEvent, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var output, diagnostics bytes.Buffer
	arguments := append([]string{"run", "--no-daemon", "--json", "--project", root}, extraArguments...)
	code := app.Run(ctx, arguments, app.IO{Out: &output, Err: &diagnostics})
	var events []decodedJSONEvent
	trimmed := strings.TrimSpace(output.String())
	if trimmed != "" {
		for _, line := range strings.Split(trimmed, "\n") {
			var decoded decodedJSONEvent
			require.NoError(t, json.Unmarshal([]byte(line), &decoded), "line: %s", line)
			events = append(events, decoded)
		}
	}
	return code, events, diagnostics.String()
}

func runCLITerminal(t *testing.T, root string, extraArguments ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var output, diagnostics bytes.Buffer
	arguments := append([]string{"run", "--no-daemon", "--project", root}, extraArguments...)
	code := app.Run(ctx, arguments, app.IO{Out: &output, Err: &diagnostics})
	return code, output.String(), diagnostics.String()
}

func leakRecordsFromMetadata(t *testing.T, metadata map[string]any) []map[string]any {
	t.Helper()
	require.NotNil(t, metadata)
	rawLeaks, exists := metadata["leaks"]
	require.True(t, exists, "metadata missing leaks: %#v", metadata)
	items, ok := rawLeaks.([]any)
	require.True(t, ok, "metadata.leaks has unexpected type %T", rawLeaks)
	records := make([]map[string]any, 0, len(items))
	for _, item := range items {
		record, valid := item.(map[string]any)
		require.True(t, valid, "leak record has unexpected type %T", item)
		records = append(records, record)
	}
	return records
}

func TestGodotLeakDetectionOrphansStraysSubtreesAutoloadsAndQueueFreeDrain(t *testing.T) {
	root := setupTempGodotProject(
		t,
		"[autoload]\nGlobalHud=\"*res://global_hud.gd\"",
		"",
		map[string]string{
			"global_hud.gd":   "extends Node\n",
			"custom_panel.gd": "extends Control\n",
			"leaks.test.gd": `extends GDTest

const CustomPanel: GDScript = preload("res://custom_panel.gd")


func test_queue_free_drains_cleanly() -> void:
	var banner: Node = Node.new()
	banner.name = "QueuedBanner"
	get_tree().root.add_child(banner)
	var autoload_host: Node = get_tree().root.get_node("GlobalHud")
	var toast: Node = Node.new()
	toast.name = "QueuedToast"
	autoload_host.add_child(toast)
	banner.queue_free()
	toast.queue_free()
	expect_true(true)


func test_reports_collapsed_orphan_stray_and_autoload_leaks() -> void:
	var orphan_root: Control = CustomPanel.new()
	orphan_root.name = "DetachedPanel"
	var orphan_child: Node = Node.new()
	orphan_child.name = "DetachedLabel"
	var orphan_grandchild: Node = Node.new()
	orphan_grandchild.name = "DetachedIcon"
	orphan_child.add_child(orphan_grandchild)
	orphan_root.add_child(orphan_child)

	var stray_root: Node = Node.new()
	stray_root.name = "RootStray"
	var stray_child: Node = Node.new()
	stray_child.name = "RootStrayChild"
	stray_root.add_child(stray_child)
	get_tree().root.add_child(stray_root)

	var autoload_host: Node = get_tree().root.get_node("GlobalHud")
	var autoload_stray: Control = CustomPanel.new()
	autoload_stray.name = "HudBadge"
	autoload_host.add_child(autoload_stray)

	expect_true(true)
`,
		},
	)

	code, events, diagnostics := runCLIJSON(t, root)
	require.Equal(t, app.ExitFailures, code, "diagnostics:\n%s", diagnostics)
	assert.Empty(t, diagnostics)

	var leakFailures []decodedJSONEvent
	statuses := make(map[string]string)
	for _, event := range events {
		if event.Type == string(model.EventAssertionFail) && event.Assertion == "leak" {
			leakFailures = append(leakFailures, event)
		}
		if event.Type == string(model.EventCaseFinished) {
			statuses[event.TestID] = event.Status
		}
	}
	assert.Equal(t, "pass", statuses["leaks.test.gd::test_queue_free_drains_cleanly"])
	assert.Equal(t, "fail", statuses["leaks.test.gd::test_reports_collapsed_orphan_stray_and_autoload_leaks"])
	require.Len(t, leakFailures, 1, "case leak must not re-fire on enclosing suite or file scopes")

	leakEvent := leakFailures[0]
	assert.Equal(t, "leaks.test.gd::test_reports_collapsed_orphan_stray_and_autoload_leaks", leakEvent.TestID)
	assert.Contains(t, leakEvent.Message, "leaked 3 nodes:")
	_, hasOmitted := leakEvent.Metadata["omitted"]
	assert.False(t, hasOmitted)

	records := leakRecordsFromMetadata(t, leakEvent.Metadata)
	require.Len(t, records, 3)

	assert.Equal(t, map[string]any{
		"kind":        "stray",
		"class":       "Control",
		"name":        "HudBadge",
		"script":      "res://custom_panel.gd",
		"path":        "/root/GlobalHud/HudBadge",
		"descendants": float64(0),
	}, records[0])
	assert.Equal(t, map[string]any{
		"kind":        "stray",
		"class":       "Node",
		"name":        "RootStray",
		"script":      "",
		"path":        "/root/RootStray",
		"descendants": float64(1),
	}, records[1])
	assert.Equal(t, map[string]any{
		"kind":        "orphan",
		"class":       "Control",
		"name":        "DetachedPanel",
		"script":      "res://custom_panel.gd",
		"path":        "",
		"descendants": float64(2),
	}, records[2])
}

func TestGodotAutofreeScopesOrderingDeduplicationNarrowestScopeAndErrorPrecedence(t *testing.T) {
	markerDirectory := t.TempDir()
	orderLogPath := filepath.ToSlash(filepath.Join(markerDirectory, "release_order.txt"))

	root := setupTempGodotProject(
		t,
		"",
		"",
		map[string]string{
			"order_recorder.gd": fmt.Sprintf(`extends Node

var label: String = ""


func _notification(what: int) -> void:
	if what == NOTIFICATION_PREDELETE and not label.is_empty():
		var existing: String = ""
		if FileAccess.file_exists(%q):
			existing = FileAccess.get_file_as_string(%q)
		var handle: FileAccess = FileAccess.open(%q, FileAccess.WRITE)
		if handle != null:
			handle.store_string(existing + label + "\n")
			handle.close()
`, orderLogPath, orderLogPath, orderLogPath),
			"scoped_autofree.test.gd": `extends GDTest

const OrderRecorder: GDScript = preload("res://order_recorder.gd")

var _file_fixture: Node
var _suite_fixture: Node
var _narrowed_node: Node
var _before_case_node: Node


func suite() -> void:
	define(
			"alpha",
			func() -> void:
				test(
						"narrows suite registration and preserves reverse order",
						func() -> void:
							expect_true(is_instance_valid(_file_fixture))
							expect_true(is_instance_valid(_suite_fixture))
							expect_true(is_instance_valid(_narrowed_node))
							expect_true(is_instance_valid(_before_case_node))
							var returned: Node = autofree(_narrowed_node)
							expect_same(returned, _narrowed_node)

							var first: Node = OrderRecorder.new()
							first.set("label", "first")
							var second: Node = OrderRecorder.new()
							second.set("label", "second")
							autofree(first)
							autofree(second)
							autofree(first)

							var parent_first: Node = Node.new()
							var child_second: Node = Node.new()
							parent_first.add_child(child_second)
							autofree(parent_first)
							autofree(child_second)

							var parent_second: Node = Node.new()
							var child_first: Node = Node.new()
							parent_second.add_child(child_first)
							autofree(child_first)
							autofree(parent_second)
				)
				test(
						"sees narrowed and before_case nodes freed while suite fixture stays alive",
						func() -> void:
							expect_true(is_instance_valid(_file_fixture))
							expect_true(is_instance_valid(_suite_fixture))
							expect_false(is_instance_valid(_narrowed_node))
				)
	)
	define(
			"beta",
			func() -> void:
				test(
						"sees previous suite fixture freed while file fixture stays alive",
						func() -> void:
							expect_true(is_instance_valid(_file_fixture))
							expect_false(is_instance_valid(_suite_fixture))
				)
	)


func before_all() -> void:
	_file_fixture = autofree(Node.new())
	_file_fixture.name = "FileFixture"
	get_tree().root.add_child(_file_fixture)


func before_suite(suite_info: GDTestSuiteInfo) -> void:
	if suite_info.name == "alpha":
		_suite_fixture = autofree(Node.new())
		_suite_fixture.name = "AlphaSuiteFixture"
		get_tree().root.add_child(_suite_fixture)
		_narrowed_node = autofree(Node.new())
		_narrowed_node.name = "NarrowedFromSuite"
		get_tree().root.add_child(_narrowed_node)


func before_case(case_info: GDTestCaseInfo) -> void:
	if case_info.name == "narrows suite registration and preserves reverse order":
		_before_case_node = autofree(Node.new())
		_before_case_node.name = "BeforeCaseNode"


func after_case(_case_info: GDTestCaseInfo) -> void:
	var after_case_node: Node = autofree(Node.new())
	after_case_node.name = "AfterCaseNode"
`,
			"each_hooks.test.gd": `extends GDTest

var _each_node: Node


func before_each(_case_info: GDTestCaseInfo) -> void:
	_each_node = autofree(Node.new())


func after_each(_case_info: GDTestCaseInfo) -> void:
	var cleanup_node: Node = autofree(Node.new())
	cleanup_node.name = "AfterEachNode"


func test_first_each_case() -> void:
	expect_true(is_instance_valid(_each_node))


func test_second_each_case() -> void:
	expect_true(is_instance_valid(_each_node))
`,
		},
	)

	code, output, diagnostics := runCLITerminal(t, root)
	require.Equal(t, app.ExitOK, code, "output:\n%s\ndiagnostics:\n%s", output, diagnostics)
	assert.Contains(t, output, "5 passed")

	orderBytes, err := os.ReadFile(filepath.FromSlash(orderLogPath))
	require.NoError(t, err)
	assert.Equal(t, "second\nfirst\n", string(orderBytes))

	statusRoot := setupTempGodotProject(
		t,
		"",
		"",
		map[string]string{
			"error_and_fail_cleanup.test.gd": `extends GDTest

var _registered_in_failed: Node
var _registered_in_errored: Node


func test_failed_case_still_releases_autofree() -> void:
	_registered_in_failed = autofree(Node.new())
	expect_equal(1, 2)


func test_errored_case_releases_autofree_and_keeps_error_status_on_leak() -> void:
	_registered_in_errored = autofree(Node.new())
	var unregistered_leak: Node = Node.new()
	unregistered_leak.name = "ErrorCaseLeak"
	await sleep(-1.0)


func test_prior_failed_and_errored_autofree_nodes_were_freed() -> void:
	expect_false(is_instance_valid(_registered_in_failed))
	expect_false(is_instance_valid(_registered_in_errored))
`,
		},
	)

	statusCode, statusEvents, statusDiagnostics := runCLIJSON(t, statusRoot)
	require.Equal(t, app.ExitFailures, statusCode, "diagnostics:\n%s", statusDiagnostics)
	caseStatuses := make(map[string]string)
	leakAssertions := 0
	for _, event := range statusEvents {
		if event.Type == string(model.EventCaseFinished) {
			caseStatuses[event.TestID] = event.Status
		}
		if event.Type == string(model.EventAssertionFail) && event.Assertion == "leak" {
			leakAssertions++
			assert.Equal(
				t,
				"error_and_fail_cleanup.test.gd::test_errored_case_releases_autofree_and_keeps_error_status_on_leak",
				event.TestID,
			)
		}
	}
	assert.Equal(t, 1, leakAssertions)
	assert.Equal(t, "fail", caseStatuses["error_and_fail_cleanup.test.gd::test_failed_case_still_releases_autofree"])
	assert.Equal(
		t,
		"error",
		caseStatuses["error_and_fail_cleanup.test.gd::test_errored_case_releases_autofree_and_keeps_error_status_on_leak"],
	)
	assert.Equal(
		t,
		"pass",
		caseStatuses["error_and_fail_cleanup.test.gd::test_prior_failed_and_errored_autofree_nodes_were_freed"],
	)
}

func TestGodotLeakIgnoreAllowlistFormsPoliciesCapAndHookAttribution(t *testing.T) {
	t.Run("all four ignore forms exempt whole subtrees", func(t *testing.T) {
		root := setupTempGodotProject(
			t,
			"",
			`{
  "leak_check": {
    "policy": "fail",
    "ignore": [
      "res://ignored_direct.gd",
      "res://ignored_base.gd",
      "Timer",
      "AllowedGlobalOverlay"
    ]
  }
}`,
			map[string]string{
				"ignored_direct.gd": "extends Node\n",
				"ignored_base.gd":   "extends Node\n",
				"ignored_derived.gd": `extends "res://ignored_base.gd"
`,
				"custom_timer.gd": "extends Timer\n",
				"allowed_overlay.gd": `class_name AllowedGlobalOverlay
extends Node
`,
				"derived_overlay.gd": `extends AllowedGlobalOverlay
`,
				"allowlist.test.gd": `extends GDTest

const IgnoredDirect: GDScript = preload("res://ignored_direct.gd")
const IgnoredDerived: GDScript = preload("res://ignored_derived.gd")
const CustomTimer: GDScript = preload("res://custom_timer.gd")
const DerivedOverlay: GDScript = preload("res://derived_overlay.gd")


func test_exempts_matching_nodes_and_their_descendants() -> void:
	var direct_node: Node = IgnoredDirect.new()
	direct_node.add_child(Node.new())
	get_tree().root.add_child(direct_node)

	var base_derived_node: Node = IgnoredDerived.new()
	base_derived_node.add_child(Node.new())

	var native_subclass: Timer = CustomTimer.new()
	native_subclass.add_child(Node.new())
	get_tree().root.add_child(native_subclass)

	var global_derived: Node = DerivedOverlay.new()
	global_derived.add_child(Node.new())
	get_tree().root.add_child(global_derived)

	expect_true(true)
`,
			},
		)

		code, output, diagnostics := runCLITerminal(t, root)
		require.Equal(t, app.ExitOK, code, "output:\n%s\ndiagnostics:\n%s", output, diagnostics)
		assert.Contains(t, output, "1 passed")
	})

	t.Run("warn policy emits case_warning and exits zero", func(t *testing.T) {
		root := setupTempGodotProject(
			t,
			"",
			`{"leak_check": {"policy": "warn"}}`,
			map[string]string{
				"warn_policy.test.gd": `extends GDTest


func test_warns_without_failing() -> void:
	var leaked: Node = Node.new()
	leaked.name = "WarnedOrphan"
	expect_true(true)
`,
			},
		)

		terminalCode, terminalOutput, terminalDiagnostics := runCLITerminal(t, root)
		require.Equal(t, app.ExitOK, terminalCode, "output:\n%s\ndiagnostics:\n%s", terminalOutput, terminalDiagnostics)
		assert.Contains(t, terminalOutput, "WARN warn_policy.test.gd::test_warns_without_failing (warn_policy.test.gd:4):")
		assert.Contains(t, terminalOutput, `orphan Node "WarnedOrphan"`)

		jsonCode, jsonEvents, jsonDiagnostics := runCLIJSON(t, root)
		require.Equal(t, app.ExitOK, jsonCode, "diagnostics:\n%s", jsonDiagnostics)
		var warnings []decodedJSONEvent
		for _, event := range jsonEvents {
			if event.Type == string(model.EventCaseWarning) {
				warnings = append(warnings, event)
			}
			assert.NotEqual(t, string(model.EventAssertionFail), event.Type)
		}
		require.Len(t, warnings, 1)
		assert.Equal(t, "leak", warnings[0].Assertion)
		assert.Equal(t, "warn_policy.test.gd::test_warns_without_failing", warnings[0].TestID)
		require.Len(t, leakRecordsFromMetadata(t, warnings[0].Metadata), 1)
	})

	t.Run("off policy skips leak check while keeping autofree active", func(t *testing.T) {
		root := setupTempGodotProject(
			t,
			"",
			`{"leak_check": {"policy": "off"}}`,
			map[string]string{
				"off_policy.test.gd": `extends GDTest

var _autofreed: Node


func test_leaks_ignored_and_autofree_runs() -> void:
	_autofreed = autofree(Node.new())
	var leaked: Node = Node.new()
	leaked.name = "UncheckedLeak"
	expect_true(true)


func test_autofreed_node_was_still_freed() -> void:
	expect_false(is_instance_valid(_autofreed))
`,
			},
		)

		code, events, diagnostics := runCLIJSON(t, root)
		require.Equal(t, app.ExitOK, code, "diagnostics:\n%s", diagnostics)
		for _, event := range events {
			assert.NotEqual(t, string(model.EventAssertionFail), event.Type)
			assert.NotEqual(t, string(model.EventCaseWarning), event.Type)
		}
	})

	t.Run("suite and file hook leaks attribute to file without test_id and cap at 20 sorted roots", func(t *testing.T) {
		root := setupTempGodotProject(
			t,
			"",
			"",
			map[string]string{
				"hook_and_cap_leaks.test.gd": `extends GDTest


func suite() -> void:
	define(
			"Inventory",
			func() -> void:
				test(
						"caps and sorts twenty five leaks deterministically",
						func() -> void:
							var stray_b: Node = Node.new()
							stray_b.name = "StrayB"
							get_tree().root.add_child(stray_b)
							var stray_a: Node = Node.new()
							stray_a.name = "StrayA"
							get_tree().root.add_child(stray_a)

							var timer_orphan: Timer = Timer.new()
							timer_orphan.name = "AlphaTimer"

							for index: int in range(22):
								var orphan: Node = Node.new()
								orphan.name = "Orphan%02d" % (21 - index)
							expect_true(true)
				)
	)


func before_all() -> void:
	var file_leak: Node = Node.new()
	file_leak.name = "BeforeAllLeak"


func before_suite(suite_info: GDTestSuiteInfo) -> void:
	if suite_info.name == "Inventory":
		var suite_leak: Node = Node.new()
		suite_leak.name = "InventorySuiteLeak"
`,
			},
		)

		code, events, diagnostics := runCLIJSON(t, root)
		require.Equal(t, app.ExitFailures, code, "diagnostics:\n%s", diagnostics)

		var leakEvents []decodedJSONEvent
		var finalSummary *results.Summary
		for _, event := range events {
			if event.Type == string(model.EventAssertionFail) && event.Assertion == "leak" {
				leakEvents = append(leakEvents, event)
			}
			if event.Type == "summary" {
				finalSummary = event.Summary
			}
		}
		require.Len(t, leakEvents, 3)

		caseLeak := leakEvents[0]
		assert.NotEmpty(t, caseLeak.TestID)
		assert.Contains(t, caseLeak.Message, "leaked 25 nodes:")
		assert.True(t, strings.HasSuffix(caseLeak.Message, ", and 5 more"), "message: %s", caseLeak.Message)
		assert.Equal(t, float64(5), caseLeak.Metadata["omitted"])
		records := leakRecordsFromMetadata(t, caseLeak.Metadata)
		require.Len(t, records, 20)
		assert.Equal(t, "/root/StrayA", records[0]["path"])
		assert.Equal(t, "/root/StrayB", records[1]["path"])
		assert.Equal(t, "Node", records[2]["class"])
		assert.Equal(t, "Orphan00", records[2]["name"])
		assert.Equal(t, "Orphan01", records[3]["name"])
		assert.Equal(t, "Orphan17", records[19]["name"])

		suiteLeak := leakEvents[1]
		assert.Empty(t, suiteLeak.TestID)
		assert.Contains(t, suiteLeak.Message, "`before_suite`/`after_suite` of suite `Inventory` leaked 1 node:")
		assert.Contains(t, suiteLeak.Message, "InventorySuiteLeak")
		require.NotNil(t, suiteLeak.Location)
		assert.Equal(t, "hook_and_cap_leaks.test.gd", suiteLeak.Location.File)

		fileLeak := leakEvents[2]
		assert.Empty(t, fileLeak.TestID)
		assert.Contains(t, fileLeak.Message, "`before_all`/`after_all` leaked 1 node:")
		assert.Contains(t, fileLeak.Message, "BeforeAllLeak")
		require.NotNil(t, fileLeak.Location)
		assert.Equal(t, "hook_and_cap_leaks.test.gd", fileLeak.Location.File)

		require.NotNil(t, finalSummary)
		assert.Equal(t, 3, finalSummary.Failed)
	})
}

func TestGodotAutofreeMisuseTimeoutSkipAndPersistentWorkerIsolation(t *testing.T) {
	t.Run("autofree null and freed instances fail the case with source-located errors", func(t *testing.T) {
		root := setupTempGodotProject(
			t,
			"",
			"",
			map[string]string{
				"case_misuse.test.gd": `extends GDTest


func test_autofree_null_records_framework_error() -> void:
	var result: Node = autofree(null)
	expect_null(result)


func test_autofree_freed_node_records_runtime_error() -> void:
	var doomed: Node = Node.new()
	doomed.free()
	autofree(doomed)
`,
			},
		)

		nullLine := lineContaining(t, root, "case_misuse.test.gd", "var result: Node = autofree(null)")
		code, events, diagnostics := runCLIJSON(t, root)
		require.Equal(t, app.ExitFailures, code, "diagnostics:\n%s", diagnostics)

		statuses := make(map[string]decodedJSONEvent)
		for _, event := range events {
			if event.Type == string(model.EventCaseFinished) {
				statuses[event.TestID] = event
			}
		}
		nullCase := statuses["case_misuse.test.gd::test_autofree_null_records_framework_error"]
		assert.Equal(t, "error", nullCase.Status)
		assert.Contains(t, nullCase.Message, "autofree requires a valid Node instance")
		require.NotNil(t, nullCase.Location)
		assert.Equal(t, "case_misuse.test.gd", nullCase.Location.File)
		assert.Equal(t, nullLine, nullCase.Location.Line)

		freedCase := statuses["case_misuse.test.gd::test_autofree_freed_node_records_runtime_error"]
		assert.Equal(t, "error", freedCase.Status)
	})

	t.Run("autofree during discovery and registration reports source-located discovery errors", func(t *testing.T) {
		root := setupTempGodotProject(
			t,
			"",
			"",
			map[string]string{
				"discovery_misuse.test.gd": `extends GDTest

var _member_node: Node = autofree(Node.new())


func get_suite_name() -> String:
	autofree(Node.new())
	return "Misuse suite"


func suite() -> void:
	autofree(Node.new())
	define(
			"nested",
			func() -> void:
				autofree(Node.new())
				test("never runs", func() -> void: expect_true(true))
	)


func test_parameterized(value: int) -> void:
	expect_equal(value, 1)


func test_parameterized__cases() -> Array:
	autofree(Node.new())
	return [[1]]
`,
			},
		)

		code, output, diagnostics := runCLITerminal(t, root)
		require.Equal(t, app.ExitError, code, "output:\n%s\ndiagnostics:\n%s", output, diagnostics)
		combined := output + "\n" + diagnostics
		assert.Contains(t, combined, "autofree is only valid while a test file, suite, or case scope is executing")
		assert.Contains(t, combined, "discovery_misuse.test.gd:3")
	})

	t.Run("autofree in after_suite after_all and node predelete teardown reports framework errors and registers nothing", func(t *testing.T) {
		markerPath := filepath.ToSlash(filepath.Join(t.TempDir(), "unregistered_survivors.txt"))
		source := fmt.Sprintf(`extends GDTest


class PredeleteMisuseNode:
	extends Node

	var owner_test: GDTest
	var spawned_ids: Array[int]


	func _init(owner_value: GDTest, target_ids: Array[int]) -> void:
		owner_test = owner_value
		spawned_ids = target_ids


	func _notification(what: int) -> void:
		if what == NOTIFICATION_PREDELETE and owner_test != null:
			var late_node: Node = Node.new()
			spawned_ids.append(late_node.get_instance_id())
			owner_test.autofree(late_node)


var _spawned_ids: Array[int] = []
var _mid_case_ids: Array[int] = []


func suite() -> void:
	define(
			"teardown_suite",
			func() -> void:
				test(
						"mid-case node removal still registers in active case while end-of-scope release and drain reject",
						func() -> void:
							var mid_case_node: PredeleteMisuseNode = PredeleteMisuseNode.new(self, _mid_case_ids)
							mid_case_node.free()
							expect_equal(_mid_case_ids.size(), 1)
							expect_true(is_instance_id_valid(_mid_case_ids[0]))
							autofree(PredeleteMisuseNode.new(self, _spawned_ids))
							var queued_drain_node: PredeleteMisuseNode = PredeleteMisuseNode.new(self, _spawned_ids)
							get_tree().root.add_child(queued_drain_node)
							queued_drain_node.queue_free()
				)
	)


func before_suite(suite_info: GDTestSuiteInfo) -> void:
	if suite_info.name == "teardown_suite":
		autofree(PredeleteMisuseNode.new(self, _spawned_ids))


func after_suite(suite_info: GDTestSuiteInfo) -> void:
	if suite_info.name == "teardown_suite":
		var suite_closing_node: Node = Node.new()
		_spawned_ids.append(suite_closing_node.get_instance_id())
		autofree(suite_closing_node)


func after_all() -> void:
	var file_closing_node: Node = Node.new()
	_spawned_ids.append(file_closing_node.get_instance_id())
	autofree(file_closing_node)
	var mid_case_alive: int = 0
	for instance_id: int in _mid_case_ids:
		if is_instance_id_valid(instance_id):
			mid_case_alive += 1
	var alive_count: int = 0
	for instance_id: int in _spawned_ids:
		if is_instance_id_valid(instance_id):
			alive_count += 1
			var resolved: Object = instance_from_id(instance_id)
			if resolved != null:
				resolved.free()
	var handle: FileAccess = FileAccess.open(%q, FileAccess.WRITE)
	if handle != null:
		handle.store_string(str(mid_case_alive) + ":" + str(alive_count))
		handle.close()
`, markerPath)

		root := setupTempGodotProject(
			t,
			"",
			`{"leak_check": {"policy": "off"}}`,
			map[string]string{"teardown_misuse.test.gd": source},
		)
		predeleteLine := lineContaining(t, root, "teardown_misuse.test.gd", "owner_test.autofree(late_node)")
		afterSuiteLine := lineContaining(t, root, "teardown_misuse.test.gd", "autofree(suite_closing_node)")
		afterAllLine := lineContaining(t, root, "teardown_misuse.test.gd", "autofree(file_closing_node)")

		code, events, diagnostics := runCLIJSON(t, root)
		require.Equal(t, app.ExitFailures, code, "diagnostics:\n%s", diagnostics)

		var caseFinished decodedJSONEvent
		var frameworkErrors []decodedJSONEvent
		for _, event := range events {
			if event.Type == string(model.EventCaseFinished) {
				caseFinished = event
			}
			if event.Type == string(model.EventWorkerError) && event.Name == "worker.framework" {
				frameworkErrors = append(frameworkErrors, event)
			}
		}

		assert.Equal(t, "error", caseFinished.Status)
		assert.Contains(t, caseFinished.Message, "autofree is only valid while a test file, suite, or case scope is executing")
		require.NotNil(t, caseFinished.Location)
		assert.Equal(t, predeleteLine, caseFinished.Location.Line)

		require.Len(t, frameworkErrors, 3, "expected errors from after_suite, suite predelete, and after_all")
		errorLines := []int{
			frameworkErrors[0].Location.Line,
			frameworkErrors[1].Location.Line,
			frameworkErrors[2].Location.Line,
		}
		assert.Equal(t, []int{afterSuiteLine, predeleteLine, afterAllLine}, errorLines)

		aliveBytes, err := os.ReadFile(filepath.FromSlash(markerPath))
		require.NoError(t, err)
		assert.Equal(t, "0:5", string(aliveBytes), "mid-case autofree must release at case end while all 5 end-of-scope teardown autofree nodes remain unregistered")
	})

	t.Run("mid-case cancellation and teardown-drain cancellation preserve scope atomicity and taint worker", func(t *testing.T) {
		midCancelMarker := filepath.ToSlash(filepath.Join(t.TempDir(), "mid_cancel_freed.txt"))
		midCancelListener, err := net.Listen("tcp4", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = midCancelListener.Close() }()
		midCancelPort := midCancelListener.Addr().(*net.TCPAddr).Port

		midCancelSource := fmt.Sprintf(`extends GDTest


class MarkerNode:
	extends Node


	func _notification(what: int) -> void:
		if what == NOTIFICATION_PREDELETE:
			var handle: FileAccess = FileAccess.open(%q, FileAccess.WRITE)
			if handle != null:
				handle.store_string("freed")
				handle.close()


func test_canceled_while_suspended() -> void:
	autofree(MarkerNode.new())
	var leaked: Node = Node.new()
	leaked.name = "CanceledCaseLeak"
	var sync_peer: StreamPeerTCP = StreamPeerTCP.new()
	sync_peer.connect_to_host("127.0.0.1", %d)
	while sync_peer.get_status() == StreamPeerTCP.STATUS_CONNECTING:
		sync_peer.poll()
		await get_tree().process_frame
	await sleep(5.0)
`, midCancelMarker, midCancelPort)

		drainFileMarker := filepath.ToSlash(filepath.Join(t.TempDir(), "drain_file_freed.txt"))
		drainListener, err := net.Listen("tcp4", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = drainListener.Close() }()
		drainPort := drainListener.Addr().(*net.TCPAddr).Port

		drainSource := fmt.Sprintf(`extends GDTest


class FileMarkerNode:
	extends Node


	func _notification(what: int) -> void:
		if what == NOTIFICATION_PREDELETE:
			var handle: FileAccess = FileAccess.open(%q, FileAccess.WRITE)
			if handle != null:
				handle.store_string("freed")
				handle.close()


class DrainSyncNode:
	extends Node


	func _notification(what: int) -> void:
		if what == NOTIFICATION_PREDELETE:
			var sync_peer: StreamPeerTCP = StreamPeerTCP.new()
			sync_peer.connect_to_host("127.0.0.1", %d)
			var deadline_msec: int = Time.get_ticks_msec() + 2000
			while sync_peer.get_status() == StreamPeerTCP.STATUS_CONNECTING and Time.get_ticks_msec() < deadline_msec:
				sync_peer.poll()
				OS.delay_usec(1000)
			while sync_peer.get_status() == StreamPeerTCP.STATUS_CONNECTED and sync_peer.get_available_bytes() == 0 and Time.get_ticks_msec() < deadline_msec:
				sync_peer.poll()
				OS.delay_usec(1000)
			sync_peer.disconnect_from_host()


func before_all() -> void:
	autofree(FileMarkerNode.new())


func test_first_completes_before_cancel_in_drain() -> void:
	await get_tree().process_frame
	autofree(DrainSyncNode.new())
	var leaked: Node = Node.new()
	leaked.name = "CompletedCaseLeak"
	expect_true(true)


func test_second_never_runs() -> void:
	expect_true(true)
`, drainFileMarker, drainPort)

		root := setupTempGodotProject(t, "", "", map[string]string{
			"mid_cancel.test.gd":   midCancelSource,
			"drain_cancel.test.gd": drainSource,
		})
		midCancelPlan, err := discovery.Parse("mid_cancel.test.gd", []byte(midCancelSource))
		require.NoError(t, err)
		drainPlan, err := discovery.Parse("drain_cancel.test.gd", []byte(drainSource))
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		midProcess, err := worker.Start(ctx, worker.ProcessOptions{
			Godot:       "godot",
			Project:     root,
			SessionID:   "mid-cancel-leak-skip",
			Generation:  1,
			CancelGrace: 5 * time.Second,
		})
		require.NoError(t, err)
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer shutdownCancel()
			assert.NoError(t, midProcess.Shutdown(shutdownCtx))
		}()

		midRunCtx, midRunCancel := context.WithCancel(ctx)
		go func() {
			connection, acceptErr := midCancelListener.Accept()
			if acceptErr == nil {
				_ = connection.Close()
				midRunCancel()
			}
		}()

		var midTainted bool
		var midLeakEmitted bool
		var midCaseStatus string
		midErr := midProcess.Run(midRunCtx, model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "mid-cancel-run",
			Generation:      1,
			LeakCheck:       model.LeakCheck{Policy: "fail"},
			Files:           []model.FilePlan{midCancelPlan},
		}, executor.EventSinkFunc(func(event model.Event) error {
			if event.Type == model.EventWorkerTainted {
				midTainted = true
			}
			if (event.Type == model.EventAssertionFail || event.Type == model.EventCaseWarning) && event.Assertion == "leak" {
				midLeakEmitted = true
			}
			if event.Type == model.EventCaseFinished {
				midCaseStatus = event.Status
			}
			return nil
		}))
		require.ErrorIs(t, midErr, context.Canceled)
		assert.True(t, midTainted)
		assert.Equal(t, "canceled", midCaseStatus)
		assert.False(t, midLeakEmitted, "canceled case must skip leak check")
		_, midStatErr := os.Stat(filepath.FromSlash(midCancelMarker))
		assert.ErrorIs(t, midStatErr, os.ErrNotExist, "canceled case must skip autofree release")

		drainProcess, err := worker.Start(ctx, worker.ProcessOptions{
			Godot:       "godot",
			Project:     root,
			SessionID:   "drain-cancel-atomicity",
			Generation:  1,
			CancelGrace: 5 * time.Second,
		})
		require.NoError(t, err)
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer shutdownCancel()
			assert.NoError(t, drainProcess.Shutdown(shutdownCtx))
		}()

		go func() {
			connection, acceptErr := drainListener.Accept()
			if acceptErr == nil {
				_ = drainProcess.Cancel(context.Background(), "drain-cancel-run")
				_, _ = connection.Write([]byte("1"))
				_ = connection.Close()
			}
		}()

		var drainTainted bool
		drainLeakTests := make([]string, 0, 1)
		drainCaseStatuses := make(map[string]string)
		drainRunStatus := ""
		drainErr := drainProcess.Run(ctx, model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "drain-cancel-run",
			Generation:      1,
			LeakCheck:       model.LeakCheck{Policy: "fail"},
			Files:           []model.FilePlan{drainPlan},
		}, executor.EventSinkFunc(func(event model.Event) error {
			if event.Type == model.EventWorkerTainted {
				drainTainted = true
			}
			if event.Type == model.EventAssertionFail && event.Assertion == "leak" {
				drainLeakTests = append(drainLeakTests, event.TestID)
			}
			if event.Type == model.EventCaseFinished {
				drainCaseStatuses[event.TestID] = event.Status
			}
			if event.Type == model.EventRunFinished {
				drainRunStatus = event.Status
			}
			return nil
		}))
		require.NoError(t, drainErr)
		assert.Equal(
			t,
			"fail",
			drainCaseStatuses["drain_cancel.test.gd::test_first_completes_before_cancel_in_drain"],
			"completed case must keep its finalized status rather than turning into canceled during frame drain",
		)
		assert.NotContains(t, drainCaseStatuses, "drain_cancel.test.gd::test_second_never_runs")
		assert.Equal(t, []string{"drain_cancel.test.gd::test_first_completes_before_cancel_in_drain"}, drainLeakTests)
		assert.Equal(t, "canceled", drainRunStatus)
		assert.True(t, drainTainted, "worker must taint when cancellation aborts enclosing file cleanup")
		_, drainStatErr := os.Stat(filepath.FromSlash(drainFileMarker))
		assert.ErrorIs(t, drainStatErr, os.ErrNotExist, "enclosing file scope must skip autofree release after cancel")
	})

	t.Run("timed-out case skips autofree release and leak check", func(t *testing.T) {
		markerPath := filepath.ToSlash(filepath.Join(t.TempDir(), "timed_out_freed.txt"))
		source := fmt.Sprintf(`extends GDTest


class MarkerNode:
	extends Node


	func _notification(what: int) -> void:
		if what == NOTIFICATION_PREDELETE:
			var handle: FileAccess = FileAccess.open(%q, FileAccess.WRITE)
			if handle != null:
				handle.store_string("freed")
				handle.close()


func test_hangs_past_case_deadline() -> void:
	autofree(MarkerNode.new())
	var leaked: Node = Node.new()
	leaked.name = "TimeoutLeak"
	await sleep(2.0)
`, markerPath)

		root := setupTempGodotProject(t, "", "", map[string]string{"timeout_skip.test.gd": source})
		filePlan, err := discovery.Parse("timeout_skip.test.gd", []byte(source))
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		process, err := worker.Start(ctx, worker.ProcessOptions{
			Godot:      "godot",
			Project:    root,
			SessionID:  "timeout-leak-skip",
			Generation: 1,
		})
		require.NoError(t, err)
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer shutdownCancel()
			assert.NoError(t, process.Shutdown(shutdownCtx))
		}()

		var tainted bool
		var leakDiagnosticEmitted bool
		err = process.Run(ctx, model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "timeout-skip-run",
			Generation:      1,
			CaseTimeout:     60 * time.Millisecond,
			LeakCheck:       model.LeakCheck{Policy: "fail"},
			Files:           []model.FilePlan{filePlan},
		}, executor.EventSinkFunc(func(event model.Event) error {
			if event.Type == model.EventWorkerTainted {
				tainted = true
			}
			if (event.Type == model.EventAssertionFail || event.Type == model.EventCaseWarning) && event.Assertion == "leak" {
				leakDiagnosticEmitted = true
			}
			return nil
		}))
		require.NoError(t, err)
		assert.True(t, tainted)
		assert.False(t, leakDiagnosticEmitted, "timed-out case must skip leak check")
		_, statErr := os.Stat(filepath.FromSlash(markerPath))
		assert.ErrorIs(t, statErr, os.ErrNotExist, "timed-out case must skip autofree release before worker shutdown")
	})

	t.Run("persistent worker does not carry leak baselines or autofree state across runs", func(t *testing.T) {
		leakingSource := `extends GDTest


func test_intentionally_leaks_once() -> void:
	var leaked: Node = Node.new()
	leaked.name = "RunOneSurvivor"
	expect_true(true)
`
		cleanSource := `extends GDTest


func test_clean_follow_up_run() -> void:
	var cleaned: Node = autofree(Node.new())
	cleaned.name = "RunTwoAutofreed"
	get_tree().root.add_child(cleaned)
	expect_true(cleaned.is_inside_tree())
`
		root := setupTempGodotProject(t, "", "", map[string]string{
			"leaking_first.test.gd": leakingSource,
			"clean_second.test.gd":  cleanSource,
		})
		leakingPlan, err := discovery.Parse("leaking_first.test.gd", []byte(leakingSource))
		require.NoError(t, err)
		cleanPlan, err := discovery.Parse("clean_second.test.gd", []byte(cleanSource))
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		process, err := worker.Start(ctx, worker.ProcessOptions{
			Godot:      "godot",
			Project:    root,
			SessionID:  "persistent-leak-isolation",
			Generation: 1,
		})
		require.NoError(t, err)
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer shutdownCancel()
			assert.NoError(t, process.Shutdown(shutdownCtx))
		}()
		initialPID := process.PID()

		firstRunLeaks := 0
		require.NoError(t, process.Run(ctx, model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "persistent-leak-run-1",
			Generation:      1,
			LeakCheck:       model.LeakCheck{Policy: "fail"},
			Files:           []model.FilePlan{leakingPlan},
		}, executor.EventSinkFunc(func(event model.Event) error {
			if event.Type == model.EventAssertionFail && event.Assertion == "leak" {
				firstRunLeaks++
			}
			return nil
		})))
		assert.Equal(t, 1, firstRunLeaks)
		require.NoError(t, process.Reset(ctx))

		secondRunLeaks := 0
		secondRunPassed := 0
		require.NoError(t, process.Run(ctx, model.TestPlan{
			ProtocolVersion: model.ProtocolVersion,
			RunID:           "persistent-leak-run-2",
			Generation:      1,
			LeakCheck:       model.LeakCheck{Policy: "fail"},
			Files:           []model.FilePlan{cleanPlan},
		}, executor.EventSinkFunc(func(event model.Event) error {
			if event.Type == model.EventAssertionFail && event.Assertion == "leak" {
				secondRunLeaks++
			}
			if event.Type == model.EventCaseFinished && event.Status == "pass" {
				secondRunPassed++
			}
			return nil
		})))
		assert.Equal(t, initialPID, process.PID())
		assert.Equal(t, 0, secondRunLeaks, "survivor from prior run must not re-fire in subsequent run on same worker")
		assert.Equal(t, 1, secondRunPassed)
	})
}
