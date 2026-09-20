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

// TestGateDimension pins the per-dimension gate. Each dimension carries its own
// pin because a re-seed can accept one dimension and reject the other, so a
// drifted clone has to stop only the dimension whose number is stale.
func TestGateDimension(t *testing.T) {
	const (
		seeded = "90c6e96ca33048b95ffa4b6ab6513385d88053f6"
		moved  = "41bced3c09140300c7efe4cb3ee996614560c26c"
	)

	for _, testCase := range []struct {
		name             string
		recorded, actual string
		updating         bool
		expected         bool
	}{
		{name: "comparable clone is measured", recorded: seeded, actual: seeded, expected: true},
		{name: "drifted clone is not gated", recorded: seeded, actual: moved, expected: false},
		{
			name: "drifted clone is re-seeded", recorded: seeded, actual: moved,
			updating: true, expected: true,
		},
		{name: "unpinned baseline still gates", recorded: "", actual: seeded, expected: true},
		{name: "unreadable head still gates", recorded: seeded, actual: "", expected: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actual := gateDimension(
				t, "repository", "files", testCase.recorded, testCase.actual, testCase.updating,
			)
			if actual != testCase.expected {
				t.Errorf(
					"gateDimension(%q, %q, updating=%v) = %v, want %v",
					testCase.recorded, testCase.actual, testCase.updating, actual, testCase.expected,
				)
			}
		})
	}
}

// TestMergeHeads pins what a re-seed writes back. A dimension that produced no
// accepted measurement keeps its previous number, so it has to keep the pin that
// number was measured over — overwriting it would declare the stale number
// comparable to the new checkout, which is the false comparison pinning exists
// to prevent.
func TestMergeHeads(t *testing.T) {
	const (
		oldHead = "41bced3c09140300c7efe4cb3ee996614560c26c"
		newHead = "90c6e96ca33048b95ffa4b6ab6513385d88053f6"
	)

	committed := map[string]string{"measured": oldHead, "truncated": oldHead}
	merged := mergeHeads(committed, map[string]string{"measured": newHead, "unreadable": ""})

	if merged["measured"] != newHead {
		t.Errorf("re-measured repository pinned to %q, want %q", merged["measured"], newHead)
	}

	if merged["truncated"] != oldHead {
		t.Errorf(
			"repository that kept its old number pinned to %q, want %q",
			merged["truncated"], oldHead,
		)
	}

	if head, ok := merged["unreadable"]; ok {
		t.Errorf("unreadable head recorded as %q, want no entry", head)
	}

	if seeded := mergeHeads(nil, map[string]string{"first": newHead}); seeded["first"] != newHead {
		t.Errorf("seeding an absent map produced %q, want %q", seeded["first"], newHead)
	}
}
