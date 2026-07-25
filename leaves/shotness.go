package leaves

import (
	"fmt"
	"io"
	"sort"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"
	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	ast_items "github.com/cwbudde/hercules/internal/plumbing/ast"
)

// ShotnessAnalysis contains the intermediate state which is mutated by Consume(). It should implement
// LeafPipelineItem.
type ShotnessAnalysis struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	nodes     map[string]*nodeShotness
	files     map[string]map[string]*nodeShotness
	extractor ast_items.Extractor

	l core.Logger
}

type nodeShotness struct {
	Count   int
	Summary NodeSummary
	Couples map[string]int
}

// NodeSummary carries the node attributes which annotate the "shotness" analysis' counters.
// These attributes are supposed to uniquely identify each node.
type NodeSummary struct {
	Type string
	Name string
	File string
}

// ShotnessResult is returned by ShotnessAnalysis.Finalize() and represents the analysis result.
type ShotnessResult struct {
	Nodes    []NodeSummary
	Counters []map[int]int
}

func (node NodeSummary) String() string {
	return node.Type + "_" + node.Name + "_" + node.File
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (shotness *ShotnessAnalysis) Name() string {
	return "Shotness"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (shotness *ShotnessAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (shotness *ShotnessAnalysis) Requires() []string {
	return []string{items.DependencyFileDiff, items.DependencyTreeChanges, items.DependencyBlobCache}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (shotness *ShotnessAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	return []core.ConfigurationOption{}
}

// Flag returns the command line switch which activates the analysis.
func (shotness *ShotnessAnalysis) Flag() string {
	return "shotness"
}

// Features returns the Hercules features required to deploy this leaf.
func (shotness *ShotnessAnalysis) Features() []string {
	return []string{}
}

// Description returns the text which explains what the analysis is doing.
func (shotness *ShotnessAnalysis) Description() string {
	return "Structural hotness - a fine-grained alternative to --couples. " +
		"Given tree-sitter function-level structure we build the square co-occurrence matrix. " +
		"The value in each cell equals to the number of times the pair of selected structural " +
		"units appeared in the same commit."
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (shotness *ShotnessAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		shotness.l = l
	}

	return nil
}

func (*ShotnessAnalysis) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (shotness *ShotnessAnalysis) Initialize(repository *git.Repository) error {
	shotness.l = core.NewLogger()
	shotness.nodes = map[string]*nodeShotness{}
	shotness.files = map[string]map[string]*nodeShotness{}
	shotness.extractor = ast_items.NewTreeSitterExtractor()
	shotness.OneShotMergeProcessor.Initialize()

	return nil
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (shotness *ShotnessAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	if !shotness.ShouldConsumeCommit(deps) {
		return noDependencies(), nil
	}

	commit := deps[core.DependencyCommit].(*object.Commit)
	changes := deps[items.DependencyTreeChanges].(object.Changes)
	diffs := deps[items.DependencyFileDiff].(map[string]items.FileDiffData)
	cache := deps[items.DependencyBlobCache].(map[plumbing.Hash]*items.CachedBlob)
	allNodes := map[string]bool{}

	for _, change := range changes {
		err := shotness.consumeChange(commit, change, diffs, cache, allNodes)
		if err != nil {
			return nil, err
		}
	}

	for keyi := range allNodes {
		for keyj := range allNodes {
			if keyi == keyj {
				continue
			}

			shotness.nodes[keyi].Couples[keyj]++
		}
	}

	return noDependencies(), nil
}

func genLine2Node(nodes map[string]ast_items.Node, linesNum int) [][]ast_items.Node {
	if linesNum <= 0 {
		return nil
	}

	res := make([][]ast_items.Node, linesNum)

	for _, node := range nodes {
		startLine := node.StartLine
		endLine := node.EndLine

		if startLine < 1 {
			startLine = 1
		}

		if endLine < startLine {
			endLine = startLine
		}

		if startLine > linesNum {
			continue
		}

		if endLine > linesNum {
			endLine = linesNum
		}

		for l := startLine; l <= endLine; l++ {
			res[l-1] = append(res[l-1], node)
		}
	}

	return res
}

// Fork clones this PipelineItem.
func (shotness *ShotnessAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(shotness, n)
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (shotness *ShotnessAnalysis) Finalize() any {
	result := ShotnessResult{
		Nodes:    make([]NodeSummary, len(shotness.nodes)),
		Counters: make([]map[int]int, len(shotness.nodes)),
	}
	keys := make([]string, len(shotness.nodes))

	i := 0
	for key := range shotness.nodes {
		keys[i] = key
		i++
	}

	sort.Strings(keys)

	reverseKeys := map[string]int{}
	for keyIndex, key := range keys {
		reverseKeys[key] = keyIndex
	}

	for keyIndex, key := range keys {
		node := shotness.nodes[key]
		result.Nodes[keyIndex] = node.Summary
		counter := map[int]int{}
		result.Counters[keyIndex] = counter

		counter[keyIndex] = node.Count
		for coupledKey, value := range node.Couples {
			counter[reverseKeys[coupledKey]] = value
		}
	}

	return result
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (shotness *ShotnessAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	shotnessResult := result.(ShotnessResult)
	if binary {
		return shotness.serializeBinary(&shotnessResult, writer)
	}

	shotness.serializeText(&shotnessResult, writer)

	return nil
}

func (shotness *ShotnessAnalysis) recordNode(
	allNodes map[string]bool, name string, node ast_items.Node, fileName string,
) {
	summary := NodeSummary{Type: node.Type, Name: name, File: fileName}
	key := summary.String()
	seen := allNodes[key]
	allNodes[key] = true

	current := shotness.nodes[key]
	if current == nil {
		current = &nodeShotness{Summary: summary, Count: 1, Couples: map[string]int{}}

		shotness.nodes[key] = current
		if shotness.files[fileName] == nil {
			shotness.files[fileName] = map[string]*nodeShotness{}
		}

		shotness.files[fileName][key] = current
	} else if !seen {
		current.Count++
	}
}

func (shotness *ShotnessAnalysis) consumeChange(
	commit *object.Commit,
	change *object.Change,
	diffs map[string]items.FileDiffData,
	cache map[plumbing.Hash]*items.CachedBlob,
	allNodes map[string]bool,
) error {
	action, err := change.Action()
	if err != nil {
		return fmt.Errorf("determine change action: %w", err)
	}

	switch action {
	case merkletrie.Delete:
		shotness.deleteFile(change.From.Name)
	case merkletrie.Insert:
		shotness.insertFile(commit, change.To.Name, change.To.TreeEntry.Hash, cache, allNodes)
	case merkletrie.Modify:
		shotness.modifyFile(commit, change, diffs, cache, allNodes)
	}

	return nil
}

func (shotness *ShotnessAnalysis) deleteFile(name string) {
	for key, summary := range shotness.files[name] {
		for coupled := range summary.Couples {
			if shotness.nodes[coupled] != nil {
				delete(shotness.nodes[coupled].Couples, key)
			}
		}
	}

	for key := range shotness.files[name] {
		delete(shotness.nodes, key)
	}

	delete(shotness.files, name)
}

func (shotness *ShotnessAnalysis) insertFile(
	commit *object.Commit, name string, hash plumbing.Hash,
	cache map[plumbing.Hash]*items.CachedBlob, allNodes map[string]bool,
) {
	nodes, err := shotness.extractNodes(name, cache, hash)
	if err != nil {
		shotness.warnParse(commit, name, false, err)
		return
	}

	for nodeName, node := range nodes {
		shotness.recordNode(allNodes, nodeName, node, name)
	}
}

func (shotness *ShotnessAnalysis) modifyFile(
	commit *object.Commit, change *object.Change,
	diffs map[string]items.FileDiffData, cache map[plumbing.Hash]*items.CachedBlob,
	allNodes map[string]bool,
) {
	fromName, toName := change.From.Name, change.To.Name
	if fromName != toName {
		shotness.renameFile(fromName, toName)
	}

	before, err := shotness.extractNodes(fromName, cache, change.From.TreeEntry.Hash)
	if err != nil {
		shotness.warnParse(commit, fromName, true, err)
		return
	}

	after, err := shotness.extractNodes(toName, cache, change.To.TreeEntry.Hash)
	if err != nil {
		shotness.warnParse(commit, toName, false, err)
		return
	}

	diff, exists := diffs[toName]
	if !exists {
		for name, node := range before {
			shotness.recordNode(allNodes, name, node, toName)
		}

		for name, node := range after {
			shotness.recordNode(allNodes, name, node, toName)
		}

		return
	}

	shotness.recordChangedNodes(allNodes, toName, before, after, diff)
}

func (shotness *ShotnessAnalysis) renameFile(sourceName, targetName string) {
	oldFile := shotness.files[sourceName]
	newFile := map[string]*nodeShotness{}

	shotness.files[targetName] = newFile
	for oldKey, node := range oldFile {
		node.Summary.File = targetName
		newKey := node.Summary.String()

		newFile[newKey], shotness.nodes[newKey] = node, node
		for coupledKey, count := range node.Couples {
			if shotness.nodes[coupledKey] != nil {
				delete(shotness.nodes[coupledKey].Couples, oldKey)
				shotness.nodes[coupledKey].Couples[newKey] = count
			}
		}
	}

	for key := range oldFile {
		delete(shotness.nodes, key)
	}

	delete(shotness.files, sourceName)
}

func (shotness *ShotnessAnalysis) warnParse(
	commit *object.Commit, name string, before bool, err error,
) {
	prefix := ""
	if before {
		prefix = "^"
	}

	shotness.l.Warnf(
		"Shotness: commit %s%s file %s failed to parse AST: %s\n",
		prefix, commit.Hash.String(), name, err.Error(),
	)
}

func (shotness *ShotnessAnalysis) recordChangedNodes(
	allNodes map[string]bool, fileName string,
	before, after map[string]ast_items.Node, diff items.FileDiffData,
) {
	beforeLines := genLine2Node(before, diff.OldLinesOfCode)
	afterLines := genLine2Node(after, diff.NewLinesOfCode)
	var beforeLine, afterLine int

	for _, edit := range diff.Diffs {
		size := utf8.RuneCountInString(edit.Text)
		switch edit.Type {
		case diffmatchpatch.DiffDelete:
			shotness.recordNodesOnLines(allNodes, fileName, beforeLines, beforeLine, size)
			beforeLine += size
		case diffmatchpatch.DiffInsert:
			shotness.recordNodesOnLines(allNodes, fileName, afterLines, afterLine, size)
			afterLine += size
		case diffmatchpatch.DiffEqual:
			beforeLine += size
			afterLine += size
		}
	}
}

func (shotness *ShotnessAnalysis) recordNodesOnLines(
	allNodes map[string]bool, fileName string, lines [][]ast_items.Node, start, count int,
) {
	for line := start; line < start+count && line < len(lines); line++ {
		for _, node := range lines[line] {
			shotness.recordNode(allNodes, node.Name, node, fileName)
		}
	}
}

func (shotness *ShotnessAnalysis) extractNodes(
	path string,
	cache map[plumbing.Hash]*items.CachedBlob,
	hash plumbing.Hash,
) (map[string]ast_items.Node, error) {
	blob := cache[hash]
	if blob == nil {
		return map[string]ast_items.Node(nil), nil
	}

	nodes, err := shotness.extractor.Extract(path, blob.Data)
	if err != nil {
		return nil, fmt.Errorf("extract nodes: %w", err)
	}

	res := map[string]ast_items.Node{}
	for _, node := range nodes {
		res[node.Name] = node
	}

	return res, nil
}

func (shotness *ShotnessAnalysis) serializeText(result *ShotnessResult, writer io.Writer) {
	for nodeIndex, summary := range result.Nodes {
		fmt.Fprintf(writer, "  - name: %s\n    file: %s\n    internal_role: %s\n    counters: {",
			summary.Name, summary.File, summary.Type)

		keys := make([]int, len(result.Counters[nodeIndex]))

		counterIndex := 0
		for key := range result.Counters[nodeIndex] {
			keys[counterIndex] = key
			counterIndex++
		}

		sort.Ints(keys)

		counterIndex = 0

		for _, key := range keys {
			val := result.Counters[nodeIndex][key]
			if counterIndex < len(result.Counters[nodeIndex])-1 {
				fmt.Fprintf(writer, "\"%d\":%d,", key, val)
			} else {
				fmt.Fprintf(writer, "\"%d\":%d}\n", key, val)
			}

			counterIndex++
		}
	}
}

func (shotness *ShotnessAnalysis) serializeBinary(result *ShotnessResult, writer io.Writer) error {
	message := pb.ShotnessAnalysisResults{
		Records: make([]*pb.ShotnessRecord, len(result.Nodes)),
	}
	for nodeIndex, summary := range result.Nodes {
		record := &pb.ShotnessRecord{
			Name:     summary.Name,
			File:     summary.File,
			Type:     summary.Type,
			Counters: map[int32]int32{},
		}
		for key, val := range result.Counters[nodeIndex] {
			counterID, err := intToProtoInt32(key, "shotness counter index")
			if err != nil {
				return err
			}

			counterValue, err := intToProtoInt32(val, "shotness counter value")
			if err != nil {
				return err
			}

			record.Counters[counterID] = counterValue
		}

		message.Records[nodeIndex] = record
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal shotness result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write shotness result: %w", err)
	}

	return nil
}

var _ = core.RegisterPipelineItem(&ShotnessAnalysis{})
