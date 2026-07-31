package modes

import "testing"

func TestTitleRepositoryName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{
			// Per-repository charts and the Python-parity goldens must not move.
			name:  "single repository passes through",
			input: "../meko-etl-tool",
			want:  "../meko-etl-tool",
		},
		{
			name:  "empty passes through",
			input: "",
			want:  "",
		},
		{
			name:  "two repositories",
			input: "../ewws-auth & ../ewws-sync",
			want:  "2 combined repositories",
		},
		{
			name:  "many repositories",
			input: "../a & ../b & ../c & ../d",
			want:  "4 combined repositories",
		},
		{
			// " & " is the joiner; a bare ampersand inside a name is not a split.
			name:  "ampersand without spaces is not a separator",
			input: "../read&write",
			want:  "../read&write",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := titleRepositoryName(tc.input); got != tc.want {
				t.Fatalf("titleRepositoryName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
