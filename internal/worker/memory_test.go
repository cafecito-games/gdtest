package worker

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResidentMemoryBytesReadsCurrentProcess(t *testing.T) {
	t.Parallel()

	resident, err := ResidentMemoryBytes(os.Getpid())

	require.NoError(t, err)
	assert.Positive(t, resident)
}

func TestResidentMemoryBytesRejectsInvalidPID(t *testing.T) {
	t.Parallel()

	_, err := ResidentMemoryBytes(0)

	require.EqualError(t, err, "invalid worker PID 0")
}
