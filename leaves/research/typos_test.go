package research

import (
	"bytes"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/test"
	"github.com/sergi/go-diff/diffmatchpatch"
)

func testDiff(before, after string) items.FileDiffData {
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = time.Hour
	src, dst, _ := dmp.DiffLinesToRunes(before, after)
	return items.FileDiffData{
		OldLinesOfCode: len(src),
		NewLinesOfCode: len(dst),
		Diffs:          dmp.DiffMainRunes(src, dst, false),
	}
}

func TestTyposTreeSitterMeta(t *testing.T) {
	tdb := &TyposDatasetBuilder{}
	if err := tdb.Initialize(test.Repository); err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if got := tdb.Name(); got != "TyposDataset" {
		t.Fatalf("unexpected name: %s", got)
	}
	if len(tdb.Requires()) != 3 {
		t.Fatalf("unexpected requires: %v", tdb.Requires())
	}
}

func TestTyposTreeSitterConsume(t *testing.T) {
	tdb := &TyposDatasetBuilder{}
	if err := tdb.Initialize(test.Repository); err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	before := "package demo\n\nfunc main() {\n\tvar cnt = 1\n\t_ = cnt\n}\n"
	after := "package demo\n\nfunc main() {\n\tvar count = 1\n\t_ = count\n}\n"
	beforeHash := plumbing.NewHash("1111111111111111111111111111111111111111")
	afterHash := plumbing.NewHash("2222222222222222222222222222222222222222")
	diff := testDiff(before, after)
	deps := map[string]interface{}{
		core.DependencyCommit: &object.Commit{Hash: plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")},
		items.DependencyTreeChanges: object.Changes{
			&object.Change{
				From: object.ChangeEntry{Name: "demo.go", TreeEntry: object.TreeEntry{Hash: beforeHash}},
				To:   object.ChangeEntry{Name: "demo.go", TreeEntry: object.TreeEntry{Hash: afterHash}},
			},
		},
		items.DependencyBlobCache: map[plumbing.Hash]*items.CachedBlob{
			beforeHash: {Data: []byte(before)},
			afterHash:  {Data: []byte(after)},
		},
		items.DependencyFileDiff: map[string]items.FileDiffData{
			"demo.go": diff,
		},
	}
	_, err := tdb.Consume(deps)
	if err != nil {
		t.Fatalf("consume failed: %v", err)
	}
	result := tdb.Finalize().(TyposResult)
	if len(result.Typos) == 0 {
		t.Fatal("expected at least one typo")
	}
	if result.Typos[0].Wrong != "cnt" || result.Typos[0].Correct != "count" {
		t.Fatalf("unexpected typo pair: %+v", result.Typos[0])
	}
}

func TestTyposTreeSitterSerialize(t *testing.T) {
	tdb := &TyposDatasetBuilder{}
	result := TyposResult{Typos: []Typo{{Wrong: "cnt", Correct: "count", Commit: plumbing.NewHash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), File: "demo.go", Line: 3}}}
	text := &bytes.Buffer{}
	if err := tdb.Serialize(result, false, text); err != nil {
		t.Fatalf("serialize text failed: %v", err)
	}
	if text.Len() == 0 {
		t.Fatal("expected text payload")
	}
	bin := &bytes.Buffer{}
	if err := tdb.Serialize(result, true, bin); err != nil {
		t.Fatalf("serialize binary failed: %v", err)
	}
	msg := &pb.TyposDataset{}
	if err := proto.Unmarshal(bin.Bytes(), msg); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(msg.Typos) != 1 || msg.Typos[0].Wrong != "cnt" {
		t.Fatalf("unexpected protobuf payload: %+v", msg.Typos)
	}
}
