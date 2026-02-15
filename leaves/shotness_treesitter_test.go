//go:build !babelfish
// +build !babelfish

package leaves

import (
	"bytes"
	"fmt"
	"strings"
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

func makeHash(i int) plumbing.Hash {
	return plumbing.NewHash(fmt.Sprintf("%040x", i))
}

func buildFileDiff(before, after string) items.FileDiffData {
	dmp := diffmatchpatch.New()
	dmp.DiffTimeout = time.Hour
	src, dst, _ := dmp.DiffLinesToRunes(before, after)
	return items.FileDiffData{
		OldLinesOfCode: len(src),
		NewLinesOfCode: len(dst),
		Diffs:          dmp.DiffMainRunes(src, dst, false),
	}
}

func TestShotnessMetaTreeSitter(t *testing.T) {
	sh := &ShotnessAnalysis{}
	if err := sh.Initialize(test.Repository); err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if got := sh.Name(); got != "Shotness" {
		t.Fatalf("unexpected name: %s", got)
	}
	if len(sh.Requires()) != 3 {
		t.Fatalf("unexpected requires length: %d", len(sh.Requires()))
	}
	if len(sh.Features()) != 0 {
		t.Fatalf("unexpected features: %v", sh.Features())
	}
}

func TestShotnessConsumeTreeSitter(t *testing.T) {
	sh := &ShotnessAnalysis{}
	if err := sh.Initialize(test.Repository); err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if err := sh.Configure(nil); err != nil {
		t.Fatalf("configure failed: %v", err)
	}

	oldText := `package demo

func alpha() int {
	return 1
}
`
	newText := `package demo

func alpha() int {
	return 1
}

func beta() int {
	return 2
}
`
	oldHash := makeHash(1)
	newHash := makeHash(2)
	fileDiff := buildFileDiff(oldText, newText)

	insertDeps := map[string]interface{}{
		core.DependencyCommit: &object.Commit{},
		items.DependencyTreeChanges: object.Changes{
			&object.Change{
				To: object.ChangeEntry{
					Name: "demo.go",
					TreeEntry: object.TreeEntry{
						Hash: newHash,
					},
				},
			},
		},
		items.DependencyBlobCache: map[plumbing.Hash]*items.CachedBlob{
			newHash: &items.CachedBlob{Data: []byte(oldText)},
		},
		items.DependencyFileDiff: map[string]items.FileDiffData{
			"demo.go": fileDiff,
		},
	}
	if _, err := sh.Consume(insertDeps); err != nil {
		t.Fatalf("consume insert failed: %v", err)
	}

	modifyDeps := map[string]interface{}{
		core.DependencyCommit: &object.Commit{},
		items.DependencyTreeChanges: object.Changes{
			&object.Change{
				From: object.ChangeEntry{
					Name: "demo.go",
					TreeEntry: object.TreeEntry{
						Hash: oldHash,
					},
				},
				To: object.ChangeEntry{
					Name: "demo.go",
					TreeEntry: object.TreeEntry{
						Hash: newHash,
					},
				},
			},
		},
		items.DependencyBlobCache: map[plumbing.Hash]*items.CachedBlob{
			oldHash: &items.CachedBlob{Data: []byte(oldText)},
			newHash: &items.CachedBlob{Data: []byte(newText)},
		},
		items.DependencyFileDiff: map[string]items.FileDiffData{
			"demo.go": fileDiff,
		},
	}
	if _, err := sh.Consume(modifyDeps); err != nil {
		t.Fatalf("consume modify failed: %v", err)
	}

	result := sh.Finalize().(ShotnessResult)
	if len(result.Nodes) == 0 {
		t.Fatal("expected non-empty result")
	}
	seen := map[string]bool{}
	for _, node := range result.Nodes {
		seen[node.Name] = true
	}
	if !seen["alpha"] || !seen["beta"] {
		t.Fatalf("expected alpha and beta nodes, got: %+v", result.Nodes)
	}
}

func TestShotnessSerializeTreeSitter(t *testing.T) {
	sh := &ShotnessAnalysis{}
	result := ShotnessResult{
		Nodes: []NodeSummary{{Type: "ast:function_declaration", Name: "alpha", File: "demo.go"}},
		Counters: []map[int]int{
			{0: 1},
		},
	}
	text := &bytes.Buffer{}
	if err := sh.Serialize(result, false, text); err != nil {
		t.Fatalf("serialize text failed: %v", err)
	}
	if !strings.Contains(text.String(), "alpha") {
		t.Fatalf("expected serialized text to mention alpha, got %q", text.String())
	}

	binary := &bytes.Buffer{}
	if err := sh.Serialize(result, true, binary); err != nil {
		t.Fatalf("serialize binary failed: %v", err)
	}
	msg := &pb.ShotnessAnalysisResults{}
	if err := proto.Unmarshal(binary.Bytes(), msg); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(msg.Records) != 1 || msg.Records[0].Name != "alpha" {
		t.Fatalf("unexpected protobuf payload: %+v", msg.Records)
	}
}
