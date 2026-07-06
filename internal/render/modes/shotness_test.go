package modes

import (
	"testing"

	"github.com/cwbudde/hercules/internal/render/readers"
)

func TestProcessShotnessRecordsSortsPythonStyleTies(t *testing.T) {
	records := []readers.ShotnessRecord{
		{Type: "ast:method_declaration", Name: "Requires", File: "burndown.go", Counters: map[int32]int32{1: 10}},
		{Type: "ast:method_declaration", Name: "LoadPeopleDict", File: "identity.go", Counters: map[int32]int32{1: 10}},
		{Type: "ast:method_declaration", Name: "Consume", File: "tree_diff.go", Counters: map[int32]int32{1: 5}},
		{Type: "ast:method_declaration", Name: "Provides", File: "blob_cache.go", Counters: map[int32]int32{1: 5}},
	}

	results := processShotnessRecords(records)
	got := []string{
		results[0].File + ":" + results[0].Name,
		results[1].File + ":" + results[1].Name,
		results[2].File + ":" + results[2].Name,
		results[3].File + ":" + results[3].Name,
	}
	want := []string{
		"identity.go:LoadPeopleDict",
		"burndown.go:Requires",
		"tree_diff.go:Consume",
		"blob_cache.go:Provides",
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted result %d = %q, want %q (full order: %v)", i, got[i], want[i], got)
		}
	}
}
