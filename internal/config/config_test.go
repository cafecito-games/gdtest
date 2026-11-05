package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	config, err := Load(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, "godot", config.Godot)
	assert.Len(t, config.Tests, 1)
	assert.Equal(t, []string{"**/*.bench.gd"}, config.Benchmarks)
	assert.Equal(t, LeakCheck{Policy: "fail"}, config.LeakCheck)
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, Filename), []byte(`{"mystery": true}`), 0o600))
	_, err := Load(root)
	require.Error(t, err)
	assert.ErrorContains(t, err, "unknown field")
}

func TestLoadAppliesProjectValuesOverDefaults(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	data := []byte(`{"godot":"custom-godot","worker_idle_timeout":"3s","worker_max_runs":4}`)
	require.NoError(t, os.WriteFile(filepath.Join(root, Filename), data, 0o600))
	config, err := Load(root)
	require.NoError(t, err)
	assert.Equal(t, "custom-godot", config.Godot)
	assert.Equal(t, 3*time.Second, time.Duration(config.WorkerIdleTimeout))
}

func TestLoadWithUserAppliesUserGodotUnlessProjectOverridesIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	user := UserDefaults()
	user.Godot = "user-godot"
	loaded, err := LoadWithUser(root, user)
	require.NoError(t, err)
	assert.Equal(t, "user-godot", loaded.Godot)

	require.NoError(t, os.WriteFile(
		filepath.Join(root, Filename), []byte(`{"godot":"project-godot"}`), 0o600,
	))
	loaded, err = LoadWithUser(root, user)
	require.NoError(t, err)
	assert.Equal(t, "project-godot", loaded.Godot)
}

func TestLoadLeakCheckPoliciesAndAllowlistEntries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		document string
		expected LeakCheck
	}{
		{
			name:     "partial leak_check defaults policy to fail",
			document: `{"leak_check":{"ignore":["res://ui/toast_layer.gd","Timer"]}}`,
			expected: LeakCheck{Policy: "fail", Ignore: []string{"res://ui/toast_layer.gd", "Timer"}},
		},
		{
			name:     "empty leak_check object defaults policy to fail",
			document: `{"leak_check":{}}`,
			expected: LeakCheck{Policy: "fail"},
		},
		{
			name:     "explicit fail policy with nested script path and global class identifier",
			document: `{"leak_check":{"policy":"fail","ignore":["res://features/chat/chat_hud.gd","ChatHud","_InternalNode2"]}}`,
			expected: LeakCheck{Policy: "fail", Ignore: []string{"res://features/chat/chat_hud.gd", "ChatHud", "_InternalNode2"}},
		},
		{
			name:     "warn policy",
			document: `{"leak_check":{"policy":"warn","ignore":[]}}`,
			expected: LeakCheck{Policy: "warn", Ignore: []string{}},
		},
		{
			name:     "off policy",
			document: `{"leak_check":{"policy":"off"}}`,
			expected: LeakCheck{Policy: "off"},
		},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, Filename), []byte(testCase.document), 0o600))
			loaded, err := Load(root)
			require.NoError(t, err)
			assert.Equal(t, testCase.expected, loaded.LeakCheck)
		})
	}
}

func TestLoadRejectsInvalidLeakCheckConfiguration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		document string
		fragment string
	}{
		{
			name:     "unknown key in leak_check",
			document: `{"leak_check":{"policy":"fail","allow_all":true}}`,
			fragment: "unknown field",
		},
		{
			name:     "null leak_check",
			document: `{"leak_check":null}`,
			fragment: "leak_check must be an object",
		},
		{
			name:     "string leak_check",
			document: `{"leak_check":"off"}`,
			fragment: "leak_check must be an object",
		},
		{
			name:     "null policy",
			document: `{"leak_check":{"policy":null}}`,
			fragment: "leak_check.policy must be a string",
		},
		{
			name:     "numeric policy",
			document: `{"leak_check":{"policy":1}}`,
			fragment: "leak_check.policy must be a string",
		},
		{
			name:     "empty policy",
			document: `{"leak_check":{"policy":""}}`,
			fragment: "leak_check.policy",
		},
		{
			name:     "unsupported policy",
			document: `{"leak_check":{"policy":"error"}}`,
			fragment: "leak_check.policy",
		},
		{
			name:     "null ignore",
			document: `{"leak_check":{"ignore":null}}`,
			fragment: "leak_check.ignore must be an array",
		},
		{
			name:     "non-array ignore",
			document: `{"leak_check":{"ignore":"Timer"}}`,
			fragment: "leak_check.ignore must be an array",
		},
		{
			name:     "null ignore element",
			document: `{"leak_check":{"ignore":[null]}}`,
			fragment: "leak_check.ignore[0]",
		},
		{
			name:     "empty ignore entry",
			document: `{"leak_check":{"ignore":[""]}}`,
			fragment: "leak_check.ignore[0]",
		},
		{
			name:     "relative path without res scheme",
			document: `{"leak_check":{"ignore":["ui/toast.gd"]}}`,
			fragment: "leak_check.ignore[0]",
		},
		{
			name:     "res path not ending in gd",
			document: `{"leak_check":{"ignore":["res://ui/toast.tscn"]}}`,
			fragment: "leak_check.ignore[0]",
		},
		{
			name:     "res path with parent traversal",
			document: `{"leak_check":{"ignore":["res://ui/../toast.gd"]}}`,
			fragment: "leak_check.ignore[0]",
		},
		{
			name:     "res path with bare extension",
			document: `{"leak_check":{"ignore":["res://.gd"]}}`,
			fragment: "leak_check.ignore[0]",
		},
		{
			name:     "invalid identifier",
			document: `{"leak_check":{"ignore":["9BadClass"]}}`,
			fragment: "leak_check.ignore[0]",
		},
		{
			name:     "duplicate ignore entry",
			document: `{"leak_check":{"ignore":["Timer","res://ui/toast.gd","Timer"]}}`,
			fragment: "duplicates leak_check.ignore[0]",
		},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, Filename), []byte(testCase.document), 0o600))
			_, err := Load(root)
			require.Error(t, err)
			assert.ErrorContains(t, err, testCase.fragment)
		})
	}
}
