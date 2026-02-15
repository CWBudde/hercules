//go:build tensorflow && !babelfish
// +build tensorflow,!babelfish

package leaves

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	ast_items "github.com/meko-christian/hercules/internal/plumbing/ast"
	progress "gopkg.in/cheggaaa/pb.v1"
	sentiment "gopkg.in/vmarkovtsev/BiDiSentiment.v1"
)

// CommentSentimentAnalysis measures comment sentiment through time.
type CommentSentimentAnalysis struct {
	core.NoopMerger
	core.OneShotMergeProcessor
	MinCommentLength int
	Gap              float32

	commentsByTick map[int][]string
	commitsByTick  map[int][]plumbing.Hash
	extractor      *ast_items.TreeSitterExtractor

	l core.Logger
}

// CommentSentimentResult contains the sentiment values per tick, where 1 means very negative
// and 0 means very positive.
type CommentSentimentResult struct {
	EmotionsByTick map[int]float32
	CommentsByTick map[int][]string
	commitsByTick  map[int][]plumbing.Hash
}

const (
	ConfigCommentSentimentMinLength = "CommentSentiment.MinLength"
	ConfigCommentSentimentGap       = "CommentSentiment.Gap"

	DefaultCommentSentimentCommentMinLength = 20
	DefaultCommentSentimentGap              = float32(0.5)

	// CommentLettersRatio is the threshold to filter impure comments which contain code.
	CommentLettersRatio = 0.6
)

var (
	filteredFirstCharRE = regexp.MustCompile("[^a-zA-Z0-9]")
	filteredCharsRE     = regexp.MustCompile("[^-a-zA-Z0-9_:;,./?!#&%+*=\\n \\t()]+")
	charsRE             = regexp.MustCompile("[a-zA-Z]+")
	functionNameRE      = regexp.MustCompile("\\s*[a-zA-Z_][a-zA-Z_0-9]*\\(\\)")
	whitespaceRE        = regexp.MustCompile("\\s+")
	licenseRE           = regexp.MustCompile("(?i)[li[cs]en[cs][ei]|copyright|©")
)

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (sent *CommentSentimentAnalysis) Name() string {
	return "Sentiment"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (sent *CommentSentimentAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (sent *CommentSentimentAnalysis) Requires() []string {
	return []string{items.DependencyTreeChanges, items.DependencyBlobCache, items.DependencyTick}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (sent *CommentSentimentAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	options := [...]core.ConfigurationOption{
		{
			Name:        ConfigCommentSentimentMinLength,
			Description: "Minimum length of the comment to be analyzed.",
			Flag:        "min-comment-len",
			Type:        core.IntConfigurationOption,
			Default:     DefaultCommentSentimentCommentMinLength,
		}, {
			Name: ConfigCommentSentimentGap,
			Description: "Sentiment value threshold, values between 0.5 - X/2 and 0.5 + x/2 will not be " +
				"considered. Must be >= 0 and < 1. The purpose is to exclude neutral comments.",
			Flag:    "sentiment-gap",
			Type:    core.FloatConfigurationOption,
			Default: DefaultCommentSentimentGap,
		},
	}
	return options[:]
}

// Flag returns the command line switch which activates the analysis.
func (sent *CommentSentimentAnalysis) Flag() string {
	return "sentiment"
}

// Description returns the text which explains what the analysis is doing.
func (sent *CommentSentimentAnalysis) Description() string {
	return "Classifies each new or changed comment per commit as containing positive or " +
		"negative emotions. The classifier outputs a real number between 0 and 1," +
		"1 is the most positive and 0 is the most negative."
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (sent *CommentSentimentAnalysis) Configure(facts map[string]interface{}) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		sent.l = l
	}
	if val, exists := facts[ConfigCommentSentimentGap]; exists {
		sent.Gap = val.(float32)
	}
	if val, exists := facts[ConfigCommentSentimentMinLength]; exists {
		sent.MinCommentLength = val.(int)
	}
	sent.validate()
	sent.commitsByTick = facts[items.FactCommitsByTick].(map[int][]plumbing.Hash)
	return nil
}

func (*CommentSentimentAnalysis) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

func (sent *CommentSentimentAnalysis) validate() {
	if sent.Gap < 0 || sent.Gap >= 1 {
		sent.l.Warnf("Sentiment gap is too big: %f => reset to the default %f",
			sent.Gap, DefaultCommentSentimentGap)
		sent.Gap = DefaultCommentSentimentGap
	}
	if sent.MinCommentLength < 10 {
		sent.l.Warnf("Comment minimum length is too small: %d => reset to the default %d",
			sent.MinCommentLength, DefaultCommentSentimentCommentMinLength)
		sent.MinCommentLength = DefaultCommentSentimentCommentMinLength
	}
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (sent *CommentSentimentAnalysis) Initialize(repository *git.Repository) error {
	sent.l = core.NewLogger()
	sent.commentsByTick = map[int][]string{}
	sent.extractor = ast_items.NewTreeSitterExtractor()
	sent.validate()
	sent.OneShotMergeProcessor.Initialize()
	return nil
}

func diffComments(before, after []ast_items.Node) []ast_items.Node {
	have := map[string]int{}
	for _, node := range before {
		have[node.Text]++
	}
	added := make([]ast_items.Node, 0, len(after))
	for _, node := range after {
		if have[node.Text] > 0 {
			have[node.Text]--
			continue
		}
		added = append(added, node)
	}
	return added
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (sent *CommentSentimentAnalysis) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if !sent.ShouldConsumeCommit(deps) {
		return nil, nil
	}
	changes := deps[items.DependencyTreeChanges].(object.Changes)
	cache := deps[items.DependencyBlobCache].(map[plumbing.Hash]*items.CachedBlob)
	tick := deps[items.DependencyTick].(int)
	collected := []ast_items.Node{}
	for _, change := range changes {
		action, err := change.Action()
		if err != nil {
			return nil, err
		}
		switch action {
		case merkletrie.Delete:
			continue
		case merkletrie.Insert:
			after := cache[change.To.TreeEntry.Hash]
			if after == nil {
				continue
			}
			comments, err := sent.extractor.ExtractComments(change.To.Name, after.Data)
			if err != nil {
				sent.l.Warnf("Sentiment: failed to parse comments in %s: %v", change.To.Name, err)
				continue
			}
			collected = append(collected, comments...)
		case merkletrie.Modify:
			before := cache[change.From.TreeEntry.Hash]
			after := cache[change.To.TreeEntry.Hash]
			if before == nil || after == nil {
				continue
			}
			beforeComments, err := sent.extractor.ExtractComments(change.From.Name, before.Data)
			if err != nil {
				sent.l.Warnf("Sentiment: failed to parse comments in %s: %v", change.From.Name, err)
				continue
			}
			afterComments, err := sent.extractor.ExtractComments(change.To.Name, after.Data)
			if err != nil {
				sent.l.Warnf("Sentiment: failed to parse comments in %s: %v", change.To.Name, err)
				continue
			}
			collected = append(collected, diffComments(beforeComments, afterComments)...)
		}
	}
	comments := sent.mergeComments(collected)
	tickComments := sent.commentsByTick[tick]
	if tickComments == nil {
		tickComments = []string{}
	}
	tickComments = append(tickComments, comments...)
	sent.commentsByTick[tick] = tickComments
	return nil, nil
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.
func (sent *CommentSentimentAnalysis) Finalize() interface{} {
	result := CommentSentimentResult{
		EmotionsByTick: map[int]float32{},
		CommentsByTick: map[int][]string{},
		commitsByTick:  sent.commitsByTick,
	}
	ticks := make([]int, 0, len(sent.commentsByTick))
	for tick := range sent.commentsByTick {
		ticks = append(ticks, tick)
	}
	sort.Ints(ticks)
	var texts []string
	for _, key := range ticks {
		texts = append(texts, sent.commentsByTick[key]...)
	}
	session, err := sentiment.OpenSession()
	if err != nil {
		panic(err)
	}
	defer session.Close()
	var bar *progress.ProgressBar
	callback := func(pos int, total int) {
		if bar == nil {
			bar = progress.New(total)
			bar.Callback = func(msg string) {
				os.Stderr.WriteString("\r" + msg)
			}
			bar.NotPrint = true
			bar.ShowPercent = false
			bar.ShowSpeed = false
			bar.SetMaxWidth(80)
			bar.Start()
		}
		bar.Set(pos)
	}
	// we run the bulk evaluation in the end for efficiency
	weights, err := sentiment.EvaluateWithProgress(texts, session, callback)
	if bar != nil {
		bar.Finish()
	}
	if err != nil {
		panic(err)
	}
	pos := 0
	for _, key := range ticks {
		sum := float32(0)
		comments := make([]string, 0, len(sent.commentsByTick[key]))
		for _, comment := range sent.commentsByTick[key] {
			if weights[pos] < 0.5*(1-sent.Gap) || weights[pos] > 0.5*(1+sent.Gap) {
				sum += weights[pos]
				comments = append(comments, comment)
			}
			pos++
		}
		if len(comments) > 0 {
			result.EmotionsByTick[key] = sum / float32(len(comments))
			result.CommentsByTick[key] = comments
		}
	}
	return result
}

// Fork clones this PipelineItem.
func (sent *CommentSentimentAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(sent, n)
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (sent *CommentSentimentAnalysis) Serialize(result interface{}, binary bool, writer io.Writer) error {
	sentimentResult := result.(CommentSentimentResult)
	if binary {
		return sent.serializeBinary(&sentimentResult, writer)
	}
	sent.serializeText(&sentimentResult, writer)
	return nil
}

func (sent *CommentSentimentAnalysis) serializeText(result *CommentSentimentResult, writer io.Writer) {
	ticks := make([]int, 0, len(result.EmotionsByTick))
	for tick := range result.EmotionsByTick {
		ticks = append(ticks, tick)
	}
	sort.Ints(ticks)
	for _, tick := range ticks {
		commits := result.commitsByTick[tick]
		hashes := make([]string, len(commits))
		for i, hash := range commits {
			hashes[i] = hash.String()
		}
		fmt.Fprintf(writer, "  %d: [%.4f, [%s], \"%s\"]\n",
			tick, result.EmotionsByTick[tick], strings.Join(hashes, ","),
			strings.Join(result.CommentsByTick[tick], "|"))
	}
}

func (sent *CommentSentimentAnalysis) serializeBinary(
	result *CommentSentimentResult, writer io.Writer,
) error {
	message := pb.CommentSentimentResults{
		SentimentByTick: map[int32]*pb.Sentiment{},
	}
	for key, val := range result.EmotionsByTick {
		commits := make([]string, len(result.commitsByTick[key]))
		for i, commit := range result.commitsByTick[key] {
			commits[i] = commit.String()
		}
		message.SentimentByTick[int32(key)] = &pb.Sentiment{
			Value:    val,
			Comments: result.CommentsByTick[key],
			Commits:  commits,
		}
	}
	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	writer.Write(serialized)
	return nil
}

func (sent *CommentSentimentAnalysis) mergeComments(extracted []ast_items.Node) []string {
	var mergedComments []string
	lines := map[int][]ast_items.Node{}
	for _, node := range extracted {
		if node.StartLine <= 0 {
			continue
		}
		lines[node.StartLine] = append(lines[node.StartLine], node)
	}
	lineNums := make([]int, 0, len(lines))
	for line := range lines {
		lineNums = append(lineNums, line)
	}
	sort.Ints(lineNums)
	var buffer []string
	for i, line := range lineNums {
		lineNodes := lines[line]
		maxEnd := line
		for _, node := range lineNodes {
			if node.EndLine > maxEnd {
				maxEnd = node.EndLine
			}
			token := strings.TrimSpace(node.Text)
			if token != "" {
				buffer = append(buffer, token)
			}
		}
		if i < len(lineNums)-1 && lineNums[i+1] <= maxEnd+1 {
			continue
		}
		mergedComments = append(mergedComments, strings.Join(buffer, "\n"))
		buffer = make([]string, 0, len(buffer))
	}
	// We remove unneeded chars and filter too short comments
	filteredComments := make([]string, 0, len(mergedComments))
	for _, comment := range mergedComments {
		comment = strings.TrimSpace(comment)
		if comment == "" || filteredFirstCharRE.MatchString(comment[:1]) {
			// heuristic - we discard docstrings
			continue
		}
		// heuristic - remove function names
		comment = functionNameRE.ReplaceAllString(comment, "")
		comment = filteredCharsRE.ReplaceAllString(comment, "")
		if len(comment) < sent.MinCommentLength {
			continue
		}
		// collapse whitespace
		comment = whitespaceRE.ReplaceAllString(comment, " ")
		// heuristic - number of letters must be at least 60%
		charsCount := 0
		for _, match := range charsRE.FindAllStringIndex(comment, -1) {
			charsCount += match[1] - match[0]
		}
		if charsCount < int(float32(len(comment))*CommentLettersRatio) {
			continue
		}
		// heuristic - license
		if licenseRE.MatchString(comment) {
			continue
		}
		filteredComments = append(filteredComments, comment)
	}
	return filteredComments
}

func init() {
	core.Registry.Register(&CommentSentimentAnalysis{})
}
