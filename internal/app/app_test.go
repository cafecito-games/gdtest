package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cancelingBuffer struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (b *cancelingBuffer) Write(data []byte) (int, error) {
	written, err := b.Buffer.Write(data)
	if strings.Contains(string(data), "watching generation 1") {
		b.cancel()
	}
	return written, err
}

func TestWatchWaitsAfterEmptyRunAndCancelsCleanly(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), []byte("[application]\n"), 0o600))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var stdout bytes.Buffer
	stderr := cancelingBuffer{cancel: cancel}
	exitCode := Run(ctx, []string{
		"watch", "--project", root, "--allow-empty", "--no-daemon",
	}, IO{Out: &stdout, Err: &stderr})
	require.Equal(t, ExitCanceled, exitCode, "stdout=%q stderr=%q", stdout.String(), stderr.String())
	assert.Contains(t, stdout.String(), "No tests selected.")
	assert.Contains(t, stderr.String(), "watching generation 1")
}
