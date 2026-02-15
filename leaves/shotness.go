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
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	ast_items "github.com/meko-christian/hercules/internal/plumbing/ast"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// ShotnessAnalysis contains the intermediate state which is mutated by Consume(). It should implement
// LeafPipelineItem.
type ShotnessAnalysis struct {
	core.NoopMerger
	core.OneShotMergeProcessor
	XpathStruct string
	XpathName   string

	nodes     map[string]*nodeShotness
	files     map[string]map[string]*nodeShotness
	extractor ast_items.Extractor

	l core.Logger
}

const (
	// ConfigShotnessXpathStruct is accepted for command-line compatibility and ignored.
	ConfigShotnessXpathStruct = "Shotness.XpathStruct"
	// ConfigShotnessXpathName is accepted for command-line compatibility and ignored.
	ConfigShotnessXpathName = "Shotness.XpathName"

	// DefaultShotnessXpathStruct is ignored in the default build.
	DefaultShotnessXpathStruct = "//uast:FunctionGroup"
	// DefaultShotnessXpathName is ignored in the default build.
	DefaultShotnessXpathName = "/Nodes/uast:Alias/Name"
)

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
	opts := [...]core.ConfigurationOption{
		{
			Name: ConfigShotnessXpathStruct,
			Description: "Legacy XPath filter (ignored by the tree-sitter " +
				"implementation).",
			Flag:    "shotness-xpath-struct",
			Type:    core.StringConfigurationOption,
			Default: DefaultShotnessXpathStruct,
		}, {
			Name: ConfigShotnessXpathName,
			Description: "Legacy XPath name selector (ignored by the tree-sitter " +
				"implementation).",
			Flag:    "shotness-xpath-name",
			Type:    core.StringConfigurationOption,
			Default: DefaultShotnessXpathName,
		},
	}
	return opts[:]
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
func (shotness *ShotnessAnalysis) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		shotness.l = l
	}
	if val, exists := facts[ConfigShotnessXpathStruct]; exists {
		shotness.XpathStruct = val.(string)
	} else {
		shotness.XpathStruct = DefaultShotnessXpathStruct
	}
	if val, exists := facts[ConfigShotnessXpathName]; exists {
		shotness.XpathName = val.(string)
	} else {
		shotness.XpathName = DefaultShotnessXpathName
	}
	return nil
}

func (*ShotnessAnalysis) ConfigureUpstream(facts map[string]interface{}) error {
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
func (shotness *ShotnessAnalysis) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if !shotness.ShouldConsumeCommit(deps) {
		return nil, nil
	}
	commit := deps[core.DependencyCommit].(*object.Commit)
	changes := deps[items.DependencyTreeChanges].(object.Changes)
	diffs := deps[items.DependencyFileDiff].(map[string]items.FileDiffData)
	cache := deps[items.DependencyBlobCache].(map[plumbing.Hash]*items.CachedBlob)
	allNodes := map[string]bool{}

	addNode := func(name string, node ast_items.Node, fileName string) {
		nodeSummary := NodeSummary{
			Type: node.Type,
			Name: name,
			File: fileName,
		}
		key := nodeSummary.String()
		exists := allNodes[key]
		allNodes[key] = true
		var count int
		if ns := shotness.nodes[key]; ns != nil {
			count = ns.Count
		}
		if count == 0 {
			shotness.nodes[key] = &nodeShotness{
				Summary: nodeSummary, Count: 1, Couples: map[string]int{},
			}
			fmap := shotness.files[nodeSummary.File]
			if fmap == nil {
				fmap = map[string]*nodeShotness{}
			}
			fmap[key] = shotness.nodes[key]
			shotness.files[nodeSummary.File] = fmap
		} else if !exists {
			shotness.nodes[key].Count = count + 1
		}
	}

	for _, change := range changes {
		action, err := change.Action()
		if err != nil {
			return nil, err
		}
		switch action {
		case merkletrie.Delete:
			fromName := change.From.Name
			for key, summary := range shotness.files[fromName] {
				for subkey := range summary.Couples {
					if shotness.nodes[subkey] != nil {
						delete(shotness.nodes[subkey].Couples, key)
					}
				}
			}
			for key := range shotness.files[fromName] {
				delete(shotness.nodes, key)
			}
			delete(shotness.files, fromName)
		case merkletrie.Insert:
			toName := change.To.Name
			nodes, err := shotness.extractNodes(toName, cache, change.To.TreeEntry.Hash)
			if err != nil {
				shotness.l.Warnf("Shotness: commit %s file %s failed to parse AST: %s\n",
					commit.Hash.String(), toName, err.Error())
				continue
			}
			for name, node := range nodes {
				addNode(name, node, toName)
			}
		case merkletrie.Modify:
			fromName := change.From.Name
			toName := change.To.Name
			if fromName != toName {
				oldFile := shotness.files[fromName]
				newFile := map[string]*nodeShotness{}
				shotness.files[toName] = newFile
				for oldKey, ns := range oldFile {
					ns.Summary.File = toName
					newKey := ns.Summary.String()
					newFile[newKey] = ns
					shotness.nodes[newKey] = ns
					for coupleKey, count := range ns.Couples {
						if shotness.nodes[coupleKey] == nil {
							continue
						}
						coupleCouples := shotness.nodes[coupleKey].Couples
						delete(coupleCouples, oldKey)
						coupleCouples[newKey] = count
					}
				}
				for key := range oldFile {
					delete(shotness.nodes, key)
				}
				delete(shotness.files, fromName)
			}

			nodesBefore, err := shotness.extractNodes(fromName, cache, change.From.TreeEntry.Hash)
			if err != nil {
				shotness.l.Warnf("Shotness: commit ^%s file %s failed to parse AST: %s\n",
					commit.Hash.String(), fromName, err.Error())
				continue
			}
			nodesAfter, err := shotness.extractNodes(toName, cache, change.To.TreeEntry.Hash)
			if err != nil {
				shotness.l.Warnf("Shotness: commit %s file %s failed to parse AST: %s\n",
					commit.Hash.String(), toName, err.Error())
				continue
			}
			diff, exists := diffs[toName]
			if !exists {
				for name, node := range nodesBefore {
					addNode(name, node, toName)
				}
				for name, node := range nodesAfter {
					addNode(name, node, toName)
				}
				continue
			}
			line2nodeBefore := genLine2Node(nodesBefore, diff.OldLinesOfCode)
			line2nodeAfter := genLine2Node(nodesAfter, diff.NewLinesOfCode)
			var lineNumBefore, lineNumAfter int
			for _, edit := range diff.Diffs {
				size := utf8.RuneCountInString(edit.Text)
				switch edit.Type {
				case diffmatchpatch.DiffDelete:
					for l := lineNumBefore; l < lineNumBefore+size && l < len(line2nodeBefore); l++ {
						for _, node := range line2nodeBefore[l] {
							addNode(node.Name, node, toName)
						}
					}
					lineNumBefore += size
				case diffmatchpatch.DiffInsert:
					for l := lineNumAfter; l < lineNumAfter+size && l < len(line2nodeAfter); l++ {
						for _, node := range line2nodeAfter[l] {
							addNode(node.Name, node, toName)
						}
					}
					lineNumAfter += size
				case diffmatchpatch.DiffEqual:
					lineNumBefore += size
					lineNumAfter += size
				}
			}
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
	return nil, nil
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

func (shotness *ShotnessAnalysis) extractNodes(
	path string,
	cache map[plumbing.Hash]*items.CachedBlob,
	hash plumbing.Hash,
) (map[string]ast_items.Node, error) {
	blob := cache[hash]
	if blob == nil {
		return map[string]ast_items.Node{}, nil
	}
	nodes, err := shotness.extractor.Extract(path, blob.Data)
	if err != nil {
		return nil, err
	}
	res := map[string]ast_items.Node{}
	for _, node := range nodes {
		res[node.Name] = node
	}
	return res, nil
}

// Fork clones this PipelineItem.
func (shotness *ShotnessAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(shotness, n)
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (shotness *ShotnessAnalysis) Finalize() interface{} {
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
	for i, key := range keys {
		reverseKeys[key] = i
	}
	for i, key := range keys {
		node := shotness.nodes[key]
		result.Nodes[i] = node.Summary
		counter := map[int]int{}
		result.Counters[i] = counter
		counter[i] = node.Count
		for ck, val := range node.Couples {
			counter[reverseKeys[ck]] = val
		}
	}
	return result
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (shotness *ShotnessAnalysis) Serialize(result interface{}, binary bool, writer io.Writer) error {
	shotnessResult := result.(ShotnessResult)
	if binary {
		return shotness.serializeBinary(&shotnessResult, writer)
	}
	shotness.serializeText(&shotnessResult, writer)
	return nil
}

func (shotness *ShotnessAnalysis) serializeText(result *ShotnessResult, writer io.Writer) {
	for i, summary := range result.Nodes {
		fmt.Fprintf(writer, "  - name: %s\n    file: %s\n    internal_role: %s\n    counters: {",
			summary.Name, summary.File, summary.Type)
		keys := make([]int, len(result.Counters[i]))
		j := 0
		for key := range result.Counters[i] {
			keys[j] = key
			j++
		}
		sort.Ints(keys)
		j = 0
		for _, key := range keys {
			val := result.Counters[i][key]
			if j < len(result.Counters[i])-1 {
				fmt.Fprintf(writer, "\"%d\":%d,", key, val)
			} else {
				fmt.Fprintf(writer, "\"%d\":%d}\n", key, val)
			}
			j++
		}
	}
}

func (shotness *ShotnessAnalysis) serializeBinary(result *ShotnessResult, writer io.Writer) error {
	message := pb.ShotnessAnalysisResults{
		Records: make([]*pb.ShotnessRecord, len(result.Nodes)),
	}
	for i, summary := range result.Nodes {
		record := &pb.ShotnessRecord{
			Name:     summary.Name,
			File:     summary.File,
			Type:     summary.Type,
			Counters: map[int32]int32{},
		}
		for key, val := range result.Counters[i] {
			record.Counters[int32(key)] = int32(val)
		}
		message.Records[i] = record
	}
	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}

func init() {
	core.Registry.Register(&ShotnessAnalysis{})
}
