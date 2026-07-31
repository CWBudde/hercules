package yaml

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintMatrixEmpty(t *testing.T) {
	tests := map[string]struct {
		matrix   [][]int64
		indent   int
		name     string
		expected string
	}{
		"nil/named": {
			matrix:   nil,
			indent:   2,
			name:     "people_interaction",
			expected: "  \"people_interaction\": |-\n",
		},
		"nil/unnamed": {
			matrix:   nil,
			indent:   4,
			name:     "",
			expected: "",
		},
		"empty/named": {
			matrix:   [][]int64{},
			indent:   4,
			name:     "project",
			expected: "    \"project\": |-\n",
		},
		"empty/unnamed": {
			matrix:   [][]int64{},
			indent:   4,
			name:     "",
			expected: "",
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			buffer := &bytes.Buffer{}

			require.NotPanics(t, func() {
				PrintMatrix(buffer, testCase.matrix, testCase.indent, testCase.name, false)
			})
			assert.Equal(t, testCase.expected, buffer.String())
		})
	}
}

func TestPrintMatrixNonEmptyUnaffected(t *testing.T) {
	buffer := &bytes.Buffer{}
	PrintMatrix(buffer, [][]int64{{1145, 0}, {464, 369}}, 2, "project", false)
	assert.Equal(t, "  \"project\": |-\n    1145    0\n     464  369\n", buffer.String())
}
