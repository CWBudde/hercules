package leaves

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/yaml"
)

// HotspotRiskAnalysis identifies high-risk files by combining multiple metrics:
// size, recent text-line churn, lifetime change coupling, and current line
// ownership concentration. Each factor is normalized to [0,1], multiplied by
// its configured weight, and divided by the sum of enabled weights.
type HotspotRiskAnalysis struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	// Configuration
	TopN            int     // Number of top risky files to report
	WindowDays      int     // Time window for churn calculation (in days)
	WeightSize      float32 // Weight for size factor
	WeightChurn     float32 // Weight for churn factor
	WeightCoupling  float32 // Weight for coupling factor
	WeightOwnership float32 // Weight for ownership concentration factor

	// Runtime state
	fileMetrics         map[string]*fileRiskMetrics
	lineHistoryResolver core.FileIdResolver
	tickSize            time.Duration
	windowDuration      time.Duration
	currentTick         int
	lastCommit          *object.Commit
	weightsConfigured   [4]bool

	l core.Logger
}

// fileRiskMetrics tracks all metrics needed to calculate risk score for a file.
type fileRiskMetrics struct {
	ChurnByTick  map[int]int     // Text-line edits per tick for window calculation
	CoupledFiles map[string]bool // Set of files that co-changed with this one
}

// HotspotRiskResult is returned by Finalize().
type HotspotRiskResult struct {
	Files      []FileRisk // Top-N risky files, sorted by score descending
	WindowDays int        // Time window used for churn calculation
}

// FileRisk contains the risk assessment for a single file.
type FileRisk struct {
	Path                string  // File path
	RiskScore           float64 // Composite risk score
	Size                int     // Number of lines
	Churn               int     // Changes in window
	CouplingDegree      int     // Number of coupled files
	OwnershipGini       float64 // Gini coefficient for ownership concentration
	SizeNormalized      float64 // Normalized size factor
	ChurnNormalized     float64 // Normalized churn factor
	CouplingNormalized  float64 // Normalized coupling factor
	OwnershipNormalized float64 // Normalized ownership factor
}

const (
	// ConfigHotspotRiskTopN sets the number of top risky files to report.
	ConfigHotspotRiskTopN = "HotspotRisk.TopN"
	// ConfigHotspotRiskWindow sets the time window in days for churn calculation.
	ConfigHotspotRiskWindow = "HotspotRisk.WindowDays"
	// ConfigHotspotRiskWeightSize sets the weight for size factor.
	ConfigHotspotRiskWeightSize = "HotspotRisk.WeightSize"
	// ConfigHotspotRiskWeightChurn sets the weight for churn factor.
	ConfigHotspotRiskWeightChurn = "HotspotRisk.WeightChurn"
	// ConfigHotspotRiskWeightCoupling sets the weight for coupling factor.
	ConfigHotspotRiskWeightCoupling = "HotspotRisk.WeightCoupling"
	// ConfigHotspotRiskWeightOwnership sets the weight for ownership concentration factor.
	ConfigHotspotRiskWeightOwnership = "HotspotRisk.WeightOwnership"

	// DefaultTopN is the default number of files to report.
	DefaultTopN = 20
	// DefaultWindowDays is the default time window in days.
	DefaultWindowDays = 90
	// DefaultWeight is the default weight for all factors.
	DefaultWeight = float32(1.0)
)

var (
	errHotspotRiskTickSize       = errors.New("hotspot risk tick size must be positive")
	errHotspotRiskWeight         = errors.New("hotspot risk weight must be finite and non-negative")
	errHotspotRiskWindow         = errors.New("hotspot risk window must be positive")
	errHotspotRiskWindowTooLarge = errors.New("hotspot risk window is too large")
	errHotspotRiskWindowMismatch = errors.New("mismatching hotspot risk windows")
)

// Name of this PipelineItem.
func (hra *HotspotRiskAnalysis) Name() string {
	return "HotspotRisk"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
func (hra *HotspotRiskAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
func (hra *HotspotRiskAnalysis) Requires() []string {
	return []string{
		items.DependencyTreeChanges,
		items.DependencyLineStats,
		items.DependencyTick,
		linehistory.DependencyLineHistory,
	}
}

// ListConfigurationOptions returns the list of changeable public properties.
func (hra *HotspotRiskAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	return []core.ConfigurationOption{
		{
			Name:        ConfigHotspotRiskTopN,
			Description: "Number of top risky files to report.",
			Flag:        "hotspot-risk-top",
			Type:        core.IntConfigurationOption,
			Default:     DefaultTopN,
		},
		{
			Name:        ConfigHotspotRiskWindow,
			Description: "Time window in days for churn calculation.",
			Flag:        "hotspot-risk-window",
			Type:        core.IntConfigurationOption,
			Default:     DefaultWindowDays,
		},
		{
			Name:        ConfigHotspotRiskWeightSize,
			Description: "Weight for size factor (0.0 to disable).",
			Flag:        "hotspot-risk-weight-size",
			Type:        core.FloatConfigurationOption,
			Default:     DefaultWeight,
		},
		{
			Name:        ConfigHotspotRiskWeightChurn,
			Description: "Weight for churn factor (0.0 to disable).",
			Flag:        "hotspot-risk-weight-churn",
			Type:        core.FloatConfigurationOption,
			Default:     DefaultWeight,
		},
		{
			Name:        ConfigHotspotRiskWeightCoupling,
			Description: "Weight for coupling factor (0.0 to disable).",
			Flag:        "hotspot-risk-weight-coupling",
			Type:        core.FloatConfigurationOption,
			Default:     DefaultWeight,
		},
		{
			Name:        ConfigHotspotRiskWeightOwnership,
			Description: "Weight for ownership concentration factor (0.0 to disable).",
			Flag:        "hotspot-risk-weight-ownership",
			Type:        core.FloatConfigurationOption,
			Default:     DefaultWeight,
		},
	}
}

// Configure sets the properties.
func (hra *HotspotRiskAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		hra.l = l
	}

	err := hra.configureHotspotLimits(facts)
	if err != nil {
		return err
	}

	err = hra.configureHotspotWeights(facts)
	if err != nil {
		return err
	}

	return hra.configureHotspotTickSize(facts)
}

func (hra *HotspotRiskAnalysis) configureHotspotLimits(facts map[string]any) error {
	if val, exists := facts[ConfigHotspotRiskTopN].(int); exists {
		hra.TopN = val
	}

	if val, exists := facts[ConfigHotspotRiskWindow].(int); exists {
		if _, err := hotspotRiskWindowDuration(val); err != nil {
			return err
		}

		hra.WindowDays = val
	}

	return nil
}

func (hra *HotspotRiskAnalysis) configureHotspotWeights(facts map[string]any) error {
	if val, exists := facts[ConfigHotspotRiskWeightSize].(float32); exists {
		err := hra.configureWeight(0, ConfigHotspotRiskWeightSize, val)
		if err != nil {
			return err
		}
	}

	if val, exists := facts[ConfigHotspotRiskWeightChurn].(float32); exists {
		err := hra.configureWeight(1, ConfigHotspotRiskWeightChurn, val)
		if err != nil {
			return err
		}
	}

	if val, exists := facts[ConfigHotspotRiskWeightCoupling].(float32); exists {
		err := hra.configureWeight(2, ConfigHotspotRiskWeightCoupling, val)
		if err != nil {
			return err
		}
	}

	if val, exists := facts[ConfigHotspotRiskWeightOwnership].(float32); exists {
		err := hra.configureWeight(3, ConfigHotspotRiskWeightOwnership, val)
		if err != nil {
			return err
		}
	}

	return nil
}

func (hra *HotspotRiskAnalysis) configureHotspotTickSize(facts map[string]any) error {
	if val, exists := facts[items.FactTickSize].(time.Duration); exists {
		if val <= 0 {
			return fmt.Errorf("%w: %s got %s", errHotspotRiskTickSize, items.FactTickSize, val)
		}

		hra.tickSize = val
	}

	return nil
}

func (hra *HotspotRiskAnalysis) configureWeight(index int, name string, weight float32) error {
	err := validateHotspotRiskWeight(name, weight)
	if err != nil {
		return err
	}

	weights := []*float32{
		&hra.WeightSize,
		&hra.WeightChurn,
		&hra.WeightCoupling,
		&hra.WeightOwnership,
	}
	*weights[index] = weight
	hra.weightsConfigured[index] = true

	return nil
}

func validateHotspotRiskWeight(name string, weight float32) error {
	if weight < 0 || math.IsNaN(float64(weight)) || math.IsInf(float64(weight), 0) {
		return fmt.Errorf("%w: %s got %v", errHotspotRiskWeight, name, weight)
	}

	return nil
}

func hotspotRiskWindowDuration(days int) (time.Duration, error) {
	if days <= 0 {
		return 0, fmt.Errorf("%w: %s got %d", errHotspotRiskWindow, ConfigHotspotRiskWindow, days)
	}

	const day = 24 * time.Hour
	if int64(days) > int64(time.Duration(1<<63-1)/day) {
		return 0, fmt.Errorf(
			"%w: %s got %d", errHotspotRiskWindowTooLarge,
			ConfigHotspotRiskWindow, days,
		)
	}

	return time.Duration(days) * day, nil
}

func (*HotspotRiskAnalysis) ConfigureUpstream(_ map[string]any) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (hra *HotspotRiskAnalysis) Flag() string {
	return "hotspot-risk"
}

// Description returns the text which explains what the analysis is doing.
func (hra *HotspotRiskAnalysis) Description() string {
	return "Identifies high-risk files by combining size, churn rate, coupling degree, " +
		"and ownership concentration metrics."
}

// Initialize prepares the analysis.
func (hra *HotspotRiskAnalysis) Initialize(repository *git.Repository) error {
	hra.l = core.NewLogger()
	if hra.tickSize <= 0 {
		return fmt.Errorf(
			"%w: %s got %s", errHotspotRiskTickSize,
			items.FactTickSize, hra.tickSize,
		)
	}

	hra.TopN = hra.effectiveTopN()

	if hra.WindowDays == 0 {
		hra.WindowDays = DefaultWindowDays
	}

	windowDuration, err := hotspotRiskWindowDuration(hra.WindowDays)
	if err != nil {
		return err
	}

	hra.windowDuration = windowDuration

	hra.applyDefaultWeights()

	hra.fileMetrics = make(map[string]*fileRiskMetrics)
	hra.lineHistoryResolver = nil
	hra.currentTick = 0
	hra.lastCommit = nil
	hra.OneShotMergeProcessor.Initialize()

	return nil
}

// effectiveTopN returns the number of files to report. It must be used everywhere the
// report is truncated, because `hercules combine` summons this item through
// core.Registry.Summon (reflect.New) and calls neither Configure nor Initialize: the
// struct is zero-valued there, so a bare hra.TopN would truncate every merged result
// to the empty list.
func (hra *HotspotRiskAnalysis) effectiveTopN() int {
	if hra.TopN <= 0 {
		return DefaultTopN
	}

	return hra.TopN
}

func (hra *HotspotRiskAnalysis) applyDefaultWeights() {
	weights := []*float32{
		&hra.WeightSize,
		&hra.WeightChurn,
		&hra.WeightCoupling,
		&hra.WeightOwnership,
	}
	for index, weight := range weights {
		if !hra.weightsConfigured[index] && *weight == 0 {
			*weight = DefaultWeight
		}
	}
}

// Consume processes the next commit.
func (hra *HotspotRiskAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	if !hra.ShouldConsumeCommit(deps) {
		return noDependencies(), nil
	}

	reader := factReader{facts: deps}
	commit := readFact[*object.Commit](&reader, core.DependencyCommit)
	treeDiff := readFact[object.Changes](&reader, items.DependencyTreeChanges)
	lineStats := readFact[map[object.ChangeEntry]items.LineStats](&reader, items.DependencyLineStats)
	tick := readFact[int](&reader, items.DependencyTick)
	lineHistoryChanges := readFact[core.LineHistoryChanges](&reader, linehistory.DependencyLineHistory)

	if reader.err != nil {
		return nil, reader.err
	}

	hra.lastCommit = commit
	hra.currentTick = tick
	hra.lineHistoryResolver = lineHistoryChanges.Resolver

	// Track which files changed in this commit for coupling
	changedFiles := make([]string, 0, len(treeDiff))

	for _, change := range treeDiff {
		fileName, err := hra.updateFileRisk(change, lineStats, tick)
		if err != nil {
			return nil, err
		}

		if fileName != "" {
			changedFiles = append(changedFiles, fileName)
		}
	}

	hra.updateFileCoupling(changedFiles)

	return noDependencies(), nil
}

// Finalize returns the result of the analysis.

func (hra *HotspotRiskAnalysis) Finalize() any {
	if hra.lastCommit == nil {
		return HotspotRiskResult{Files: []FileRisk{}, WindowDays: hra.WindowDays}
	}

	tree, err := hra.lastCommit.Tree()
	if err != nil {
		hra.l.Errorf("Failed to get tree: %v", err)
		return HotspotRiskResult{Files: []FileRisk{}, WindowDays: hra.WindowDays}
	}

	var risks []FileRisk
	currentOwnership := currentOwnershipByPath(hra.lineHistoryResolver)

	err = tree.Files().ForEach(func(file *object.File) error {
		if risk, ok := hra.fileRisk(file, currentOwnership[file.Name]); ok {
			risks = append(risks, risk)
		}

		return nil
	})
	if err != nil {
		hra.l.Errorf("Failed to iterate files: %v", err)
	}

	hra.normalizeAndScore(risks)
	sortFileRisks(risks)

	topN := hra.effectiveTopN()
	if len(risks) > topN {
		risks = risks[:topN]
	}

	return HotspotRiskResult{
		Files:      risks,
		WindowDays: hra.WindowDays,
	}
}

func sortFileRisks(risks []FileRisk) {
	sort.Slice(risks, func(i, j int) bool {
		if risks[i].RiskScore == risks[j].RiskScore {
			return risks[i].Path < risks[j].Path
		}

		return risks[i].RiskScore > risks[j].RiskScore
	})
}

// calculateGini computes the Gini coefficient for line ownership distribution
// Returns value in [0,1] where 0 = perfectly equal, 1 = one person owns everything.
func calculateGini(authorLines map[int]int) float64 {
	if len(authorLines) == 0 {
		return 0
	}

	if len(authorLines) == 1 {
		return 1.0 // Single owner = maximum concentration
	}

	values, totalLines := positiveLineCounts(authorLines)

	if len(values) == 0 || totalLines == 0 {
		return 0
	}

	if len(values) == 1 {
		return 1.0
	}

	// Sort values
	sort.Ints(values)

	// Calculate Gini coefficient using formula:
	// G = (2 * sum(i * values[i])) / (n * sum(values)) - (n + 1) / n
	valueCount := len(values)

	var weightedSum int64
	for i, val := range values {
		weightedSum += int64(i+1) * int64(val)
	}

	gini := (2.0*float64(weightedSum))/(float64(valueCount)*float64(totalLines)) -
		float64(valueCount+1)/float64(valueCount)

	return max(0, min(gini, 1))
}

func positiveLineCounts(authorLines map[int]int) ([]int, int) {
	values := make([]int, 0, len(authorLines))
	totalLines := 0

	for _, lines := range authorLines {
		if lines <= 0 {
			continue
		}

		values = append(values, lines)
		totalLines += lines
	}

	return values, totalLines
}

// Fork clones this pipeline item.
func (hra *HotspotRiskAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(hra, n)
}

// Serialize converts the analysis result to text or bytes.
func (hra *HotspotRiskAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	riskResult, err := requiredResult[HotspotRiskResult](result)
	if err != nil {
		return err
	}

	if binary {
		return hra.serializeBinary(&riskResult, writer)
	}

	hra.serializeText(&riskResult, writer)

	return nil
}

// Deserialize converts protobuf bytes to HotspotRiskResult.
func (hra *HotspotRiskAnalysis) Deserialize(pbmessage []byte) (any, error) {
	message := pb.HotspotRiskResults{}

	err := unmarshalAnalysis(pbmessage, &message)
	if err != nil {
		return nil, fmt.Errorf("unmarshal hotspot risk result: %w", err)
	}

	result := HotspotRiskResult{
		WindowDays: int(message.GetWindowDays()),
		Files:      make([]FileRisk, len(message.GetFiles())),
	}

	for i, file := range message.GetFiles() {
		result.Files[i] = FileRisk{
			Path:                file.GetPath(),
			RiskScore:           file.GetRiskScore(),
			Size:                int(file.GetSize_()),
			Churn:               int(file.GetChurn()),
			CouplingDegree:      int(file.GetCouplingDegree()),
			OwnershipGini:       file.GetOwnershipGini(),
			SizeNormalized:      file.GetSizeNormalized(),
			ChurnNormalized:     file.GetChurnNormalized(),
			CouplingNormalized:  file.GetCouplingNormalized(),
			OwnershipNormalized: file.GetOwnershipNormalized(),
		}
	}

	return result, nil
}

// MergeResults combines two HotspotRisk results by concatenating the two file lists and
// re-ranking them.
//
// Two properties are deliberately NOT implemented here:
//
//   - No deduplication by Path. The same path in two different repositories denotes two
//     different files, and FileRisk carries no repository qualifier, so collapsing equal
//     paths would silently fuse unrelated files.
//   - No cross-run rescaling. normalizeAndScore normalizes each factor against the maxima
//     of its own run, so scores from two runs are not on a common scale and the merged
//     ranking only approximates a global one. Fixing that requires carrying the raw
//     normalization bounds in the FileRisk schema and is a separate change.
//
// Runs with different churn windows are rejected outright, since their Churn numbers are
// not comparable at all.
func (hra *HotspotRiskAnalysis) MergeResults(
	firstResult, secondResult any, _, _ *core.CommonAnalysisResult,
) any {
	cr1, err := requiredResult[HotspotRiskResult](firstResult)
	if err != nil {
		return err
	}

	cr2, err := requiredResult[HotspotRiskResult](secondResult)
	if err != nil {
		return err
	}

	if cr1.WindowDays != cr2.WindowDays {
		return fmt.Errorf("%w (r1: %d, r2: %d) received",
			errHotspotRiskWindowMismatch, cr1.WindowDays, cr2.WindowDays)
	}

	allFiles := append([]FileRisk(nil), cr1.Files...)
	allFiles = append(allFiles, cr2.Files...)
	sortFileRisks(allFiles)

	topN := hra.effectiveTopN()
	if len(allFiles) > topN {
		allFiles = allFiles[:topN]
	}

	return HotspotRiskResult{
		Files:      allFiles,
		WindowDays: cr1.WindowDays,
	}
}

func (hra *HotspotRiskAnalysis) updateFileRisk(
	change *object.Change,
	lineStats map[object.ChangeEntry]items.LineStats,
	tick int,
) (string, error) {
	action, err := change.Action()
	if err != nil {
		return "", fmt.Errorf("determine change action: %w", err)
	}

	var fileName string
	var statsEntry object.ChangeEntry

	switch action {
	case merkletrie.Insert:
		fileName = change.To.Name
		statsEntry = change.To
	case merkletrie.Delete:
		fileName = change.From.Name
		statsEntry = change.From
	case merkletrie.Modify:
		hra.transferFileRisk(change.From.Name, change.To.Name)
		fileName = change.To.Name
		statsEntry = change.To
	}

	if fileName == "" {
		return "", nil
	}

	stats, isText := lineStats[statsEntry]
	if !isText {
		// LinesStatsCalculator deliberately omits binary changes. They do not
		// have meaningful line size, churn, or ownership factors and therefore
		// must not affect text-file coupling either.
		return "", nil
	}

	metrics := hra.fileMetrics[fileName]
	if metrics == nil {
		metrics = &fileRiskMetrics{
			ChurnByTick: make(map[int]int), CoupledFiles: make(map[string]bool),
		}
		hra.fileMetrics[fileName] = metrics
	}

	metrics.ChurnByTick[tick] += stats.Added + stats.Removed + stats.Changed

	return fileName, nil
}

func (hra *HotspotRiskAnalysis) transferFileRisk(sourceName, targetName string) {
	if sourceName == targetName {
		return
	}

	if old, exists := hra.fileMetrics[sourceName]; exists {
		if target := hra.fileMetrics[targetName]; target != nil && target != old {
			mergeFileRiskMetrics(old, target)
		}

		hra.fileMetrics[targetName] = old
		delete(hra.fileMetrics, sourceName)
	}

	// Coupling is lifetime co-change history between logical files. A rename
	// therefore moves the history to the new path and rewrites references to
	// that logical file in every other file's coupling set.
	for path, metrics := range hra.fileMetrics {
		if metrics.CoupledFiles[sourceName] {
			delete(metrics.CoupledFiles, sourceName)

			if path != targetName {
				metrics.CoupledFiles[targetName] = true
			}
		}

		delete(metrics.CoupledFiles, path)
	}
}

func mergeFileRiskMetrics(destination, source *fileRiskMetrics) {
	for tick, churn := range source.ChurnByTick {
		destination.ChurnByTick[tick] += churn
	}

	for coupled := range source.CoupledFiles {
		destination.CoupledFiles[coupled] = true
	}
}

func (hra *HotspotRiskAnalysis) updateFileCoupling(changedFiles []string) {
	for _, file1 := range changedFiles {
		if metrics, exists := hra.fileMetrics[file1]; exists {
			for _, file2 := range changedFiles {
				if file1 != file2 {
					metrics.CoupledFiles[file2] = true
				}
			}
		}
	}
}

func currentOwnershipByPath(resolver core.FileIdResolver) map[string]map[int]int {
	ownership := map[string]map[int]int{}
	if resolver == nil {
		return ownership
	}

	resolver.ForEachFile(func(fileID core.FileId, path string) {
		authorLines := map[int]int{}
		previousLine := 0
		previousAuthor := int(core.AuthorMissing)

		if !resolver.ScanFile(
			fileID,
			func(line int, _ core.TickNumber, author core.AuthorId) {
				length := line - previousLine
				if length > 0 && previousAuthor >= 0 &&
					previousAuthor != int(core.AuthorMissing) {
					authorLines[previousAuthor] += length
				}

				previousLine = line

				if author >= core.AuthorMissing {
					previousAuthor = int(core.AuthorMissing)
				} else {
					previousAuthor = int(author)
				}
			},
		) {
			return
		}

		ownership[path] = authorLines
	})

	return ownership
}

func (hra *HotspotRiskAnalysis) fileRisk(file *object.File, authorLines map[int]int) (FileRisk, bool) {
	metrics, exists := hra.fileMetrics[file.Name]
	if !exists {
		return FileRisk{}, false
	}

	blob := items.CachedBlob{Blob: file.Blob}

	err := blob.Cache()
	if err != nil {
		return FileRisk{}, false
	}

	size, err := blob.CountLines()
	if err != nil {
		return FileRisk{}, false
	}

	churn := 0

	for tick, count := range metrics.ChurnByTick {
		if tick <= hra.currentTick &&
			time.Duration(hra.currentTick-tick)*hra.tickSize <= hra.windowDuration {
			churn += count
		}
	}

	return FileRisk{
		Path: file.Name, Size: size, Churn: churn, CouplingDegree: len(metrics.CoupledFiles),
		OwnershipGini: calculateGini(authorLines),
	}, true
}

// normalizeAndScore normalizes all factors to [0,1] and calculates risk scores.
func (hra *HotspotRiskAnalysis) normalizeAndScore(risks []FileRisk) {
	if len(risks) == 0 {
		return
	}

	maxSize, maxChurn, maxCoupling := riskFactorMaximums(risks)

	// Normalize and calculate scores
	for riskIndex := range risks {
		sizeNorm := normalizedLogFactor(risks[riskIndex].Size, maxSize)
		churnNorm := normalizedLinearFactor(risks[riskIndex].Churn, maxChurn)
		couplingNorm := normalizedLinearFactor(risks[riskIndex].CouplingDegree, maxCoupling)

		// Ownership: Gini is already in [0,1], higher = more concentrated
		ownershipNorm := risks[riskIndex].OwnershipGini

		// Store normalized values
		risks[riskIndex].SizeNormalized = sizeNorm
		risks[riskIndex].ChurnNormalized = churnNorm
		risks[riskIndex].CouplingNormalized = couplingNorm
		risks[riskIndex].OwnershipNormalized = ownershipNorm

		// A zero-valued factor contributes zero without collapsing the other
		// factors. A zero weight removes the factor from both numerator and
		// denominator.
		factors := [...]float64{sizeNorm, churnNorm, couplingNorm, ownershipNorm}
		weights := [...]float64{
			float64(hra.WeightSize),
			float64(hra.WeightChurn),
			float64(hra.WeightCoupling),
			float64(hra.WeightOwnership),
		}

		var weightedScore, enabledWeight float64
		for index, factor := range factors {
			weightedScore += weights[index] * factor
			enabledWeight += weights[index]
		}

		risks[riskIndex].RiskScore = 0
		if enabledWeight > 0 {
			risks[riskIndex].RiskScore = weightedScore / enabledWeight
		}
	}
}

func riskFactorMaximums(risks []FileRisk) (float64, float64, float64) {
	var maxSize, maxChurn, maxCoupling float64

	for _, risk := range risks {
		maxSize = max(maxSize, float64(risk.Size))
		maxChurn = max(maxChurn, float64(risk.Churn))
		maxCoupling = max(maxCoupling, float64(risk.CouplingDegree))
	}

	return maxSize, maxChurn, maxCoupling
}

func normalizedLogFactor(value int, maximum float64) float64 {
	if value <= 0 || maximum <= 0 {
		return 0
	}

	return math.Log(float64(value)+1) / math.Log(maximum+1)
}

func normalizedLinearFactor(value int, maximum float64) float64 {
	if maximum <= 0 {
		return 0
	}

	return float64(value) / maximum
}

func (hra *HotspotRiskAnalysis) serializeText(result *HotspotRiskResult, writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "  window_days:", result.WindowDays)
	_, _ = fmt.Fprintln(writer, "  files:")

	for _, file := range result.Files {
		_, _ = fmt.Fprintf(writer, "    - path: %s\n", yaml.SafeString(file.Path))
		_, _ = fmt.Fprintf(writer, "      risk_score: %.6f\n", file.RiskScore)
		_, _ = fmt.Fprintf(writer, "      size: %d\n", file.Size)
		_, _ = fmt.Fprintf(writer, "      churn: %d\n", file.Churn)
		_, _ = fmt.Fprintf(writer, "      coupling_degree: %d\n", file.CouplingDegree)
		_, _ = fmt.Fprintf(writer, "      ownership_gini: %.6f\n", file.OwnershipGini)
		_, _ = fmt.Fprintf(writer, "      normalized:\n")
		_, _ = fmt.Fprintf(writer, "        size: %.6f\n", file.SizeNormalized)
		_, _ = fmt.Fprintf(writer, "        churn: %.6f\n", file.ChurnNormalized)
		_, _ = fmt.Fprintf(writer, "        coupling: %.6f\n", file.CouplingNormalized)
		_, _ = fmt.Fprintf(writer, "        ownership: %.6f\n", file.OwnershipNormalized)
	}
}

func (hra *HotspotRiskAnalysis) serializeBinary(result *HotspotRiskResult, writer io.Writer) error {
	windowDays, err := intToProtoInt32(result.WindowDays, "hotspot-risk window days")
	if err != nil {
		return err
	}

	message := pb.HotspotRiskResults{
		WindowDays: windowDays,
		Files:      make([]*pb.FileRisk, len(result.Files)),
	}

	for fileIndex, file := range result.Files {
		size, err := intToProtoInt32(file.Size, "hotspot-risk file size")
		if err != nil {
			return err
		}

		churn, err := intToProtoInt32(file.Churn, "hotspot-risk file churn")
		if err != nil {
			return err
		}

		couplingDegree, err := intToProtoInt32(file.CouplingDegree, "hotspot-risk coupling degree")
		if err != nil {
			return err
		}

		message.Files[fileIndex] = &pb.FileRisk{
			Path:                file.Path,
			RiskScore:           file.RiskScore,
			Size_:               size,
			Churn:               churn,
			CouplingDegree:      couplingDegree,
			OwnershipGini:       file.OwnershipGini,
			SizeNormalized:      file.SizeNormalized,
			ChurnNormalized:     file.ChurnNormalized,
			CouplingNormalized:  file.CouplingNormalized,
			OwnershipNormalized: file.OwnershipNormalized,
		}
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal hotspot risk result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write hotspot risk result: %w", err)
	}

	return nil
}

var _ = core.RegisterPipelineItem(&HotspotRiskAnalysis{})
