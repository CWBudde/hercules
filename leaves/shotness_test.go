package leaves

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	ast_items "github.com/cwbudde/hercules/internal/plumbing/ast"
	"github.com/cwbudde/hercules/internal/render/readers"
	"github.com/cwbudde/hercules/internal/test"
)

const (
	testShotnessAnalysisName = "Shotness"
	testShotnessAlphaName    = "alpha"
	testShotnessAlphaRunName = "Alpha.Run"
	testShotnessBetaRunName  = "Beta.Run"
	testShotnessFunctionType = "ast:function_declaration"
	testShotnessMethodType   = "ast:method_declaration"
	testShotnessPeerName     = "Peer"
	testShotnessRunName      = "Run"
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
	err := sh.Initialize(test.Repository)
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	if got := sh.Name(); got != testShotnessAnalysisName {
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
	err := sh.Initialize(test.Repository)
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	err = sh.Configure(nil)
	if err != nil {
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

	insertDeps := map[string]any{
		core.DependencyCommit: &object.Commit{},
		items.DependencyTreeChanges: object.Changes{
			&object.Change{
				To: object.ChangeEntry{
					Name: testDemoPath,
					TreeEntry: object.TreeEntry{
						Hash: newHash,
					},
				},
			},
		},
		items.DependencyBlobCache: map[plumbing.Hash]*items.CachedBlob{
			newHash: {Data: []byte(oldText)},
		},
		items.DependencyFileDiff: map[string]items.FileDiffData{
			testDemoPath: fileDiff,
		},
	}
	_, err = sh.Consume(insertDeps)
	if err != nil {
		t.Fatalf("consume insert failed: %v", err)
	}

	modifyDeps := map[string]any{
		core.DependencyCommit: &object.Commit{},
		items.DependencyTreeChanges: object.Changes{
			&object.Change{
				From: object.ChangeEntry{
					Name: testDemoPath,
					TreeEntry: object.TreeEntry{
						Hash: oldHash,
					},
				},
				To: object.ChangeEntry{
					Name: testDemoPath,
					TreeEntry: object.TreeEntry{
						Hash: newHash,
					},
				},
			},
		},
		items.DependencyBlobCache: map[plumbing.Hash]*items.CachedBlob{
			oldHash: {Data: []byte(oldText)},
			newHash: {Data: []byte(newText)},
		},
		items.DependencyFileDiff: map[string]items.FileDiffData{
			testDemoPath: fileDiff,
		},
	}
	_, err = sh.Consume(modifyDeps)
	if err != nil {
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
	if !seen[testShotnessAlphaName] || !seen["beta"] {
		t.Fatalf("expected alpha and beta nodes, got: %+v", result.Nodes)
	}
}

func TestShotnessExtractionDoesNotOverwriteQualifiedSameNamedEntities(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		source string
		want   []string
	}{
		{
			name: "Go receivers",
			path: "receivers.go",
			source: `package demo
type Alpha struct{}
type Beta struct{}
func (a Alpha) Run() {}
func (b *Beta) Run() {}
`,
			want: []string{testShotnessAlphaRunName, testShotnessBetaRunName},
		},
		{
			name: "nested Python scopes",
			path: "nested.py",
			source: `def left():
    def run():
        return 1
def right():
    def run():
        return 2
`,
			want: []string{"left", "left.run", "right", "right.run"},
		},
	}

	for testIndex, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sh := &ShotnessAnalysis{}
			err := sh.Initialize(test.Repository)
			if err != nil {
				t.Fatalf("initialize failed: %v", err)
			}
			hash := makeHash(100 + testIndex)
			nodes, err := sh.extractNodes(
				tc.path,
				map[plumbing.Hash]*items.CachedBlob{hash: {Data: []byte(tc.source)}},
				hash,
			)
			if err != nil {
				t.Fatalf("extract nodes failed: %v", err)
			}
			if len(nodes) != len(tc.want) {
				t.Fatalf("got %d nodes, want %d: %+v", len(nodes), len(tc.want), nodes)
			}
			got := map[string]bool{}
			for _, node := range nodes {
				got[node.QualifiedName] = true
			}
			for _, want := range tc.want {
				if !got[want] {
					t.Fatalf("missing qualified entity %q in %+v", want, nodes)
				}
			}
		})
	}
}

func TestShotnessSerializeTreeSitter(t *testing.T) {
	sh := &ShotnessAnalysis{}
	result := ShotnessResult{
		Nodes: []NodeSummary{{
			Type: testShotnessFunctionType, Name: testShotnessAlphaName, File: testDemoPath,
		}},
		Counters: []map[int]int{
			{0: 1},
		},
	}
	text := &bytes.Buffer{}
	err := sh.Serialize(result, false, text)
	if err != nil {
		t.Fatalf("serialize text failed: %v", err)
	}
	if !strings.Contains(text.String(), testShotnessAlphaName) {
		t.Fatalf("expected serialized text to mention alpha, got %q", text.String())
	}

	binary := &bytes.Buffer{}
	err = sh.Serialize(result, true, binary)
	if err != nil {
		t.Fatalf("serialize binary failed: %v", err)
	}
	msg := &pb.ShotnessAnalysisResults{}
	err = proto.Unmarshal(binary.Bytes(), msg)
	if err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(msg.GetRecords()) != 1 || msg.GetRecords()[0].GetName() != testShotnessAlphaName {
		t.Fatalf("unexpected protobuf payload: %+v", msg.GetRecords())
	}
}

func TestShotnessStructuralMoveAndRenameIdentity(t *testing.T) {
	sh := &ShotnessAnalysis{}
	err := sh.Initialize(test.Repository)
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	original := ast_items.Node{
		Type:           testShotnessMethodType,
		Name:           testShotnessRunName,
		QualifiedName:  testShotnessAlphaRunName,
		SourceIdentity: testDemoPath,
		StartLine:      3,
		EndLine:        5,
	}
	sh.recordNode(map[string]bool{}, original, testDemoPath)

	// Moving a structurally identical method within the same file preserves
	// its identity because line/column coordinates are not identity fields.
	moved := original
	moved.StartLine, moved.EndLine = 30, 32
	if original.StableIdentity() != moved.StableIdentity() {
		t.Fatalf("within-file move changed stable identity")
	}
	sh.recordNode(map[string]bool{}, moved, testDemoPath)

	// A structural rename starts a new identity; it must not overwrite the
	// history of the old qualified name.
	renamed := moved
	renamed.Name = "Execute"
	renamed.QualifiedName = "Alpha.Execute"
	if original.StableIdentity() == renamed.StableIdentity() {
		t.Fatalf("structural rename retained old stable identity")
	}
	sh.recordNode(map[string]bool{}, renamed, testDemoPath)

	// Moving one entity to a different source file starts a new identity.
	// Whole-file renames are the explicit exception tested below: Shotness can
	// migrate every entity in that source without guessing entity matches.
	sourceMoved := moved
	sourceMoved.SourceIdentity = "other/demo.go"
	if original.StableIdentity() == sourceMoved.StableIdentity() {
		t.Fatalf("cross-source move retained old stable identity")
	}

	if len(sh.nodes) != 2 {
		t.Fatalf("move/rename produced %d identities, want 2", len(sh.nodes))
	}
	originalKey := (NodeSummary{
		Type: testShotnessMethodType, Name: testShotnessAlphaRunName, File: testDemoPath,
	}).String()
	renamedKey := (NodeSummary{
		Type: testShotnessMethodType, Name: "Alpha.Execute", File: testDemoPath,
	}).String()
	if sh.nodes[originalKey] == nil || sh.nodes[originalKey].Count != 2 {
		t.Fatalf("moved entity did not retain history: %+v", sh.nodes[originalKey])
	}
	if sh.nodes[renamedKey] == nil || sh.nodes[renamedKey].Count != 1 {
		t.Fatalf("renamed entity did not start new history: %+v", sh.nodes[renamedKey])
	}
}

func TestShotnessFileRenameRebasesIdentityAndPreservesCounters(t *testing.T) {
	sh := &ShotnessAnalysis{}
	err := sh.Initialize(test.Repository)
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	const oldPath = "old/demo.go"
	const newPath = "new/demo.go"
	node := ast_items.Node{
		Type:           testShotnessFunctionType,
		Name:           testShotnessRunName,
		QualifiedName:  testShotnessRunName,
		SourceIdentity: oldPath,
	}
	sh.recordNode(map[string]bool{}, node, oldPath)
	sh.recordNode(map[string]bool{}, node, oldPath)
	peer := ast_items.Node{
		Type:           testShotnessFunctionType,
		Name:           testShotnessPeerName,
		QualifiedName:  testShotnessPeerName,
		SourceIdentity: "peer.go",
	}
	sh.recordNode(map[string]bool{}, peer, "peer.go")
	oldKey := (NodeSummary{
		Type: testShotnessFunctionType, Name: testShotnessRunName, File: oldPath,
	}).String()
	peerKey := (NodeSummary{
		Type: testShotnessFunctionType, Name: testShotnessPeerName, File: "peer.go",
	}).String()
	sh.nodes[oldKey].Couples[peerKey] = 3
	sh.nodes[peerKey].Couples[oldKey] = 3

	sh.renameFile(oldPath, newPath)

	newKey := (NodeSummary{
		Type: testShotnessFunctionType, Name: testShotnessRunName, File: newPath,
	}).String()
	if sh.nodes[oldKey] != nil {
		t.Fatalf("old source identity survived file rename: %+v", sh.nodes[oldKey])
	}
	if sh.nodes[newKey] == nil || sh.nodes[newKey].Count != 2 {
		t.Fatalf("file rename lost entity counters: %+v", sh.nodes[newKey])
	}
	if sh.nodes[newKey].Couples[peerKey] != 3 ||
		sh.nodes[peerKey].Couples[newKey] != 3 ||
		sh.nodes[peerKey].Couples[oldKey] != 0 {
		t.Fatalf("file rename did not rebase coupling keys: %+v", sh.nodes)
	}
}

func TestShotnessQualifiedEntitiesRoundTripYAMLAndProtobuf(t *testing.T) {
	sh := &ShotnessAnalysis{}
	result := ShotnessResult{
		Nodes: []NodeSummary{
			{Type: testShotnessMethodType, Name: testShotnessAlphaRunName, File: testDemoPath},
			{Type: testShotnessMethodType, Name: testShotnessBetaRunName, File: testDemoPath},
		},
		Counters: []map[int]int{
			{0: 2, 1: 1},
			{0: 1, 1: 3},
		},
	}

	yamlBody := &bytes.Buffer{}
	err := sh.Serialize(result, false, yamlBody)
	if err != nil {
		t.Fatalf("serialize YAML failed: %v", err)
	}
	yamlReader := &readers.YamlReader{}
	err = yamlReader.Read(strings.NewReader("Shotness:\n" + yamlBody.String()))
	if err != nil {
		t.Fatalf("read YAML failed: %v", err)
	}

	pbBody := &bytes.Buffer{}
	err = sh.Serialize(result, true, pbBody)
	if err != nil {
		t.Fatalf("serialize protobuf failed: %v", err)
	}
	payload, err := proto.Marshal(&pb.AnalysisResults{
		Contents: map[string][]byte{testShotnessAnalysisName: pbBody.Bytes()},
	})
	if err != nil {
		t.Fatalf("marshal protobuf envelope failed: %v", err)
	}
	pbReader := &readers.ProtobufReader{}
	err = pbReader.Read(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("read protobuf failed: %v", err)
	}

	yamlRecords, err := yamlReader.GetShotnessRecords()
	if err != nil {
		t.Fatalf("get YAML records failed: %v", err)
	}
	pbRecords, err := pbReader.GetShotnessRecords()
	if err != nil {
		t.Fatalf("get protobuf records failed: %v", err)
	}
	if !reflect.DeepEqual(yamlRecords, pbRecords) {
		t.Fatalf("record round-trip mismatch:\nYAML: %+v\nPB:   %+v", yamlRecords, pbRecords)
	}
	if len(yamlRecords) != 2 ||
		yamlRecords[0].Name != testShotnessAlphaRunName ||
		yamlRecords[1].Name != testShotnessBetaRunName {
		t.Fatalf("qualified entities collapsed during round-trip: %+v", yamlRecords)
	}
	wantCounters := []map[int32]int32{
		{0: 2, 1: 1},
		{0: 1, 1: 3},
	}
	for i := range yamlRecords {
		if !reflect.DeepEqual(yamlRecords[i].Counters, wantCounters[i]) {
			t.Fatalf("entity %d counters changed during round-trip: %+v", i, yamlRecords[i].Counters)
		}
	}

	yamlNames, yamlCoupling, err := yamlReader.GetShotnessCooccurrence()
	if err != nil {
		t.Fatalf("get YAML coupling failed: %v", err)
	}
	pbNames, pbCoupling, err := pbReader.GetShotnessCooccurrence()
	if err != nil {
		t.Fatalf("get protobuf coupling failed: %v", err)
	}
	if !reflect.DeepEqual(yamlNames, pbNames) ||
		!reflect.DeepEqual(yamlCoupling, pbCoupling) {
		t.Fatalf(
			"coupling round-trip mismatch:\nYAML: %v %+v\nPB:   %v %+v",
			yamlNames, yamlCoupling, pbNames, pbCoupling,
		)
	}
	wantNames := []string{
		testDemoPath + ":" + testShotnessAlphaRunName,
		testDemoPath + ":" + testShotnessBetaRunName,
	}
	wantCoupling := [][]int{{5, 5}, {5, 10}}
	if !reflect.DeepEqual(yamlNames, wantNames) ||
		!reflect.DeepEqual(yamlCoupling, wantCoupling) {
		t.Fatalf("unexpected qualified coupling: %v %+v", yamlNames, yamlCoupling)
	}
}

func TestShotnessSerializeEmptyCountersProducesValidYAML(t *testing.T) {
	sh := &ShotnessAnalysis{}
	result := ShotnessResult{
		Nodes: []NodeSummary{{
			Type: testShotnessFunctionType, Name: testShotnessAlphaName, File: testDemoPath,
		}},
		Counters: []map[int]int{{}},
	}

	body := &bytes.Buffer{}
	err := sh.Serialize(result, false, body)
	if err != nil {
		t.Fatalf("serialize YAML failed: %v", err)
	}
	if !strings.Contains(body.String(), "counters: {}") {
		t.Fatalf("empty counters were not closed: %q", body.String())
	}

	reader := &readers.YamlReader{}
	err = reader.Read(strings.NewReader("Shotness:\n" + body.String()))
	if err != nil {
		t.Fatalf("serialized empty counters are invalid YAML: %v", err)
	}
	records, err := reader.GetShotnessRecords()
	if err != nil {
		t.Fatalf("get YAML records failed: %v", err)
	}
	if len(records) != 1 || len(records[0].Counters) != 0 {
		t.Fatalf("empty counters changed during YAML round trip: %+v", records)
	}
}
