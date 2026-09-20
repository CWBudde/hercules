package corpus

import "testing"

// TestClassifyHead pins the rule that decides whether a committed number may be
// gated against a fresh measurement: the numbers describe one clone state, so a
// clone that moved since the baseline was seeded is not comparable at all.
func TestClassifyHead(t *testing.T) {
	const sha = "90c6e96ca33048b95ffa4b6ab6513385d88053f6"

	for _, testCase := range []struct {
		name             string
		recorded, actual string
		expected         headState
	}{
		{name: "same clone", recorded: sha, actual: sha, expected: headComparable},
		{
			name: "clone moved", recorded: sha,
			actual:   "41bced3c09140300c7efe4cb3ee996614560c26c",
			expected: headDrifted,
		},
		{name: "baseline predates pinning", recorded: "", actual: sha, expected: headUnpinned},
		{name: "head unreadable", recorded: sha, actual: "", expected: headUnpinned},
		{name: "neither known", recorded: "", actual: "", expected: headUnpinned},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actual := classifyHead(testCase.recorded, testCase.actual)
			if actual != testCase.expected {
				t.Errorf(
					"classifyHead(%q, %q) = %v, want %v",
					testCase.recorded, testCase.actual, actual, testCase.expected,
				)
			}
		})
	}
}
