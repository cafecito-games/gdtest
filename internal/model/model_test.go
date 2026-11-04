package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilePlanMarshalsDynamicOnlyCasesAsExplicitEmptyArray(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(FilePlan{Path: "tests/dynamic.test.gd"})

	require.NoError(t, err)
	assert.JSONEq(t, `{"path":"tests/dynamic.test.gd","cases":[]}`, string(encoded))
}
