extends GDTest

## A large two-suite fixture for intra-file partitioning parity. Every case
## asserts the lifecycle-hook state its own file instance must observe, so a
## sharded run passes exactly when suite hooks wrap the same cases as serial
## execution.

## Counts `before_all` invocations for this file instance.
var _before_all_runs: int = 0
## Counts `before_suite` invocations per suite name.
var _suite_entries: Dictionary[String, int] = {}


## Supplies a stable root suite name.
func get_suite_name() -> String:
	return "Partitioning"


## Registers two disjoint suites sized for the suite cut rule.
func suite() -> void:
	define(
			"Alpha",
			func() -> void:
				for index: int in range(30):
					test(
							"alpha case %02d" % index,
							func() -> void:
								expect_hook_state("Alpha")
					)
	)
	define(
			"Beta",
			func() -> void:
				for index: int in range(6):
					test(
							"beta case %02d" % index,
							func() -> void:
								expect_hook_state("Beta")
					)
	)


## Asserts the hook state one case must observe in its own instance: file
## setup ran once and the owning suite was entered exactly once. A shard may
## hold several suites in case mode, so sibling suites are not asserted.
func expect_hook_state(suite_name: String) -> void:
	expect_equal(1, _before_all_runs, "before_all must run exactly once per file instance")
	expect_equal(
			1,
			int(_suite_entries.get(suite_name, 0)),
			"the owning suite must be entered exactly once"
	)


## Counts one file setup per instance.
func before_all() -> void:
	_before_all_runs += 1


## Records suite entries for per-case hook assertions.
func before_suite(suite_info: GDTestSuiteInfo) -> void:
	_suite_entries[suite_info.name] = int(_suite_entries.get(suite_info.name, 0)) + 1
