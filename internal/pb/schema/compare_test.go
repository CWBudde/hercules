package schema

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, proto string) Snapshot {
	t.Helper()
	snapshot, err := ParseProto([]byte(proto))
	if err != nil {
		t.Fatalf("ParseProto: %v", err)
	}
	return snapshot
}

func breakingCount(changes []Change) int {
	n := 0
	for _, c := range changes {
		if c.Breaking {
			n++
		}
	}
	return n
}

func requireChange(t *testing.T, changes []Change, breaking bool, substr string) {
	t.Helper()
	for _, c := range changes {
		if c.Breaking == breaking && strings.Contains(c.Description, substr) {
			return
		}
	}
	t.Fatalf("no change (breaking=%v) containing %q in %+v", breaking, substr, changes)
}

func TestCompareNoChanges(t *testing.T) {
	s := mustParse(t, `message A {
    string x = 1;
}`)
	if changes := Compare(s, s); len(changes) != 0 {
		t.Fatalf("expected no changes, got %+v", changes)
	}
}

func TestCompareCompatibleAdditions(t *testing.T) {
	old := mustParse(t, `message A {
    string x = 1;
}`)
	updated := mustParse(t, `
message A {
    string x = 1;
    int32 y = 2;
}
message B {
    string z = 1;
}
`)
	changes := Compare(old, updated)
	if breakingCount(changes) != 0 {
		t.Fatalf("expected only compatible changes, got %+v", changes)
	}
	requireChange(t, changes, false, "field A.y (2) added")
	requireChange(t, changes, false, "message B added")
}

func TestCompareBreakingChanges(t *testing.T) {
	old := mustParse(t, `
message A {
    string x = 1;
    int32 y = 2;
    int64 z = 3;
    repeated string w = 4;
}
message Gone {
    string a = 1;
}
`)
	updated := mustParse(t, `
message A {
    string x = 1;
    string y = 2;
    int64 renamed = 3;
    string w = 4;
}
`)
	changes := Compare(old, updated)
	requireChange(t, changes, true, "message Gone removed")
	requireChange(t, changes, true, "field A.y (2) changed type")
	requireChange(t, changes, true, "field A.z (3) renamed")
	requireChange(t, changes, true, "field A.w (4) changed label")
}

func TestCompareFieldRemoval(t *testing.T) {
	old := mustParse(t, `message A {
    string x = 1;
    string y = 2;
}`)

	// Removal with proper reservation: breaking, but no reservation complaint.
	reserved := mustParse(t, `message A {
    reserved 2;
    reserved "y";
    string x = 1;
}`)
	changes := Compare(old, reserved)
	requireChange(t, changes, true, "field A.y (2) removed")
	for _, c := range changes {
		if strings.Contains(c.Description, "without reserving") {
			t.Fatalf("unexpected reservation complaint: %+v", changes)
		}
	}

	// Removal without reservation: additional violation.
	unreserved := mustParse(t, `message A {
    string x = 1;
}`)
	changes = Compare(old, unreserved)
	requireChange(t, changes, true, "field A.y (2) removed without reserving")
}

func TestCompareReservedReuse(t *testing.T) {
	old := mustParse(t, `message A {
    reserved 2;
    reserved "y";
    string x = 1;
}`)

	reuseNumber := mustParse(t, `message A {
    reserved "y";
    string x = 1;
    int32 fresh = 2;
}`)
	changes := Compare(old, reuseNumber)
	requireChange(t, changes, true, "field A.fresh (2) reuses reserved number")
	// Dropping "reserved 2" is itself flagged.
	requireChange(t, changes, true, "un-reserved number 2")

	reuseName := mustParse(t, `message A {
    reserved 2;
    string x = 1;
    int32 y = 3;
}`)
	changes = Compare(old, reuseName)
	requireChange(t, changes, true, "field A.y (3) reuses reserved name")
}

func TestCompareReservedAdditionIsCompatible(t *testing.T) {
	old := mustParse(t, `message A {
    string x = 1;
}`)
	updated := mustParse(t, `message A {
    reserved 5;
    string x = 1;
}`)
	changes := Compare(old, updated)
	if breakingCount(changes) != 0 {
		t.Fatalf("expected compatible, got %+v", changes)
	}
	requireChange(t, changes, false, "reserved number 5 added")
}

func TestEvaluate(t *testing.T) {
	base := mustParse(t, `message A {
    string x = 1;
}`)
	base.Version = 2

	compatible := mustParse(t, `message A {
    string x = 1;
    int32 y = 2;
}`)
	compatible.Version = 2

	breaking := mustParse(t, `message A {
    int64 x = 1;
}`)
	breaking.Version = 2

	breakingBumped := breaking
	breakingBumped.Version = 3

	cases := []struct {
		name             string
		old, new         Snapshot
		changelogUpdated bool
		wantErrs         []string
	}{
		{"no change", base, base, false, nil},
		{"compatible with changelog", base, compatible, true, nil},
		{
			"compatible without changelog", base, compatible, false,
			[]string{"docs/SCHEMA_CHANGELOG.md"},
		},
		{
			"breaking without bump", base, breaking, true,
			[]string{"version bump"},
		},
		{"breaking with bump", base, breakingBumped, true, nil},
		{
			"breaking with bump, no changelog", base, breakingBumped, false,
			[]string{"docs/SCHEMA_CHANGELOG.md"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := Evaluate(c.old, c.new, c.changelogUpdated)
			if len(result.Errors) != len(c.wantErrs) {
				t.Fatalf("errors: %v, want %d matching %v", result.Errors, len(c.wantErrs), c.wantErrs)
			}
			for i, want := range c.wantErrs {
				if !strings.Contains(result.Errors[i], want) {
					t.Fatalf("error %q does not contain %q", result.Errors[i], want)
				}
			}
		})
	}
}
