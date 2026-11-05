extends GDTest

## Exercises engine-global state that a reusable worker can restore exactly.


## Proves each worker redirects user data away from the project's shared default.
func test_user_data_is_worker_scoped() -> void:
	var path := OS.get_user_data_dir().replace("\\", "/")
	expect_contains(path, "/gdtest/workers/")
	expect_contains(path, "/worker-")


## Changes engine-global audio state away from the project startup layout.
func test_mutates_audio_server_layout() -> void:
	AudioServer.set_bus_volume_db(0, -18.0)
	AudioServer.set_bus_mute(0, true)
	expect_equal(AudioServer.get_bus_volume_db(0), -18.0)
	expect_true(AudioServer.is_bus_mute(0))


## Proves reset restored the exact startup values before a subsequent run.
func test_audio_server_layout_matches_worker_startup() -> void:
	expect_equal(AudioServer.get_bus_volume_db(0), 0.0)
	expect_false(AudioServer.is_bus_mute(0))
