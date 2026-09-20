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

func TestSafeString(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected string
	}{
		"plain":           {input: "alice", expected: `"alice"`},
		"empty":           {input: "", expected: `""`},
		"backslash":       {input: `a\b`, expected: `"a\\b"`},
		"double quote":    {input: `say "hi"`, expected: `"say \"hi\""`},
		"newline":         {input: "a\nb", expected: `"a\nb"`},
		"tab":             {input: "a\tb", expected: `"a\tb"`},
		"carriage return": {input: "a\rb", expected: `"a\rb"`},
		"nul":             {input: "a\x00b", expected: `"a\0b"`},
		"control byte":    {input: "a\x01b\x1fc", expected: `"a\x01b\x1fc"`},
		"delete":          {input: "a\x7fb", expected: `"a\x7fb"`},
		"mixed":           {input: "path: \"x\"\n\\", expected: `"path: \"x\"\n\\"`},
		"unicode kept":    {input: "Zoë Müller", expected: `"Zoë Müller"`},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, testCase.expected, SafeString(testCase.input))
		})
	}
}
