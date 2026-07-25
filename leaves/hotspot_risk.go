package leaves

import (
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/yaml"
)

// HotspotRiskAnalysis identifies high-risk files by combining multiple metrics:
// size, churn rate, coupling degree, and ownership concentration.
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
	fileMetrics map[string]*fileRiskMetrics
	tickSize    int64 // Duration of one tick in seconds
	currentTick int
	lastCommit  *object.Commit

	l core.Logger
}

// fileRiskMetrics tracks all metrics needed to calculate risk score for a file.
type fileRiskMetrics struct {
	CurrentSize   int             // Current number of lines
	ChurnInWindow int             // Number of changes within time window
	ChurnByTick   map[int]int     // Changes per tick for window calculation
	CoupledFiles  map[string]bool // Set of files that co-changed with this one
	AuthorLines   map[int]int     // Lines contributed by each author
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
		identity.DependencyAuthor,
		items.DependencyTick,
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

	if val, exists := facts[ConfigHotspotRiskTopN].(int); exists {
		hra.TopN = val
	}

	if val, exists := facts[ConfigHotspotRiskWindow].(int); exists {
		hra.WindowDays = val
	}

	if val, exists := facts[ConfigHotspotRiskWeightSize].(float32); exists {
		hra.WeightSize = val
	}

	if val, exists := facts[ConfigHotspotRiskWeightChurn].(float32); exists {
		hra.WeightChurn = val
	}

	if val, exists := facts[ConfigHotspotRiskWeightCoupling].(float32); exists {
		hra.WeightCoupling = val
	}

	if val, exists := facts[ConfigHotspotRiskWeightOwnership].(float32); exists {
		hra.WeightOwnership = val
	}

	if val, exists := facts[items.FactTickSize].(int64); exists {
		hra.tickSize = val
	}

	return nil
}

func (*HotspotRiskAnalysis) ConfigureUpstream(facts map[string]any) error {
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
	if hra.TopN == 0 {
		hra.TopN = DefaultTopN
	}

	if hra.WindowDays == 0 {
		hra.WindowDays = DefaultWindowDays
	}

	if hra.WeightSize == 0 {
		hra.WeightSize = DefaultWeight
	}

	if hra.WeightChurn == 0 {
		hra.WeightChurn = DefaultWeight
	}

	if hra.WeightCoupling == 0 {
		hra.WeightCoupling = DefaultWeight
	}

	if hra.WeightOwnership == 0 {
		hra.WeightOwnership = DefaultWeight
	}

	hra.fileMetrics = make(map[string]*fileRiskMetrics)
	hra.currentTick = 0
	hra.OneShotMergeProcessor.Initialize()

	return nil
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
	author := readFact[int](&reader, identity.DependencyAuthor)
	tick := readFact[int](&reader, items.DependencyTick)

	if reader.err != nil {
		return nil, reader.err
	}

	hra.lastCommit = commit
	hra.currentTick = tick

	// Track which files changed in this commit for coupling
	changedFiles := make([]string, 0, len(treeDiff))

	for _, change := range treeDiff {
		fileName, err := hra.updateFileRisk(change, lineStats, author, tick)
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

	windowTicks := 0
	if hra.tickSize > 0 {
		windowTicks = (hra.WindowDays * 24 * 3600) / int(hra.tickSize)
	}

	startTick := max(hra.currentTick-windowTicks, 0)

	tree, err := hra.lastCommit.Tree()
	if err != nil {
		hra.l.Errorf("Failed to get tree: %v", err)
		return HotspotRiskResult{Files: []FileRisk{}, WindowDays: hra.WindowDays}
	}

	var risks []FileRisk

	err = tree.Files().ForEach(func(file *object.File) error {
		if risk, ok := hra.fileRisk(file, startTick); ok {
			risks = append(risks, risk)
		}

		return nil
	})
	if err != nil {
		hra.l.Errorf("Failed to iterate files: %v", err)
	}

	hra.normalizeAndScore(risks)
	sortFileRisks(risks)

	if len(risks) > hra.TopN {
		risks = risks[:hra.TopN]
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

	err := proto.Unmarshal(pbmessage, &message)
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

// MergeResults combines two HotspotRisk results (not really meaningful, but required by interface).
func (hra *HotspotRiskAnalysis) MergeResults(
	firstResult, secondResult any, _, _ *core.CommonAnalysisResult,
) any {
	// Merging hotspot risk across repositories doesn't make semantic sense,
	// but we implement it by concatenating and re-sorting
	cr1, err := requiredResult[HotspotRiskResult](firstResult)
	if err != nil {
		return err
	}

	cr2, err := requiredResult[HotspotRiskResult](secondResult)
	if err != nil {
		return err
	}

	allFiles := append([]FileRisk(nil), cr1.Files...)
	allFiles = append(allFiles, cr2.Files...)
	sortFileRisks(allFiles)

	if len(allFiles) > hra.TopN {
		allFiles = allFiles[:hra.TopN]
	}

	return HotspotRiskResult{
		Files:      allFiles,
		WindowDays: cr1.WindowDays,
	}
}

func (hra *HotspotRiskAnalysis) updateFileRisk(
	change *object.Change,
	lineStats map[object.ChangeEntry]items.LineStats,
	author, tick int,
) (string, error) {
	action, err := change.Action()
	if err != nil {
		return "", fmt.Errorf("determine change action: %w", err)
	}

	var fileName string

	switch action {
	case merkletrie.Insert:
		fileName = change.To.Name
	case merkletrie.Delete:
		fileName = change.From.Name
	case merkletrie.Modify:
		hra.transferFileRisk(change.From.Name, change.To.Name)
		fileName = change.To.Name
	}

	if fileName == "" {
		return "", nil
	}

	metrics := hra.fileMetrics[fileName]
	if metrics == nil {
		metrics = &fileRiskMetrics{
			ChurnByTick: make(map[int]int), CoupledFiles: make(map[string]bool), AuthorLines: make(map[int]int),
		}
		hra.fileMetrics[fileName] = metrics
	}

	metrics.ChurnByTick[tick]++
	if stats, exists := lineStats[object.ChangeEntry{Name: fileName}]; exists {
		metrics.AuthorLines[author] += stats.Added - stats.Removed
	}

	return fileName, nil
}

func (hra *HotspotRiskAnalysis) transferFileRisk(sourceName, targetName string) {
	if sourceName == targetName {
		return
	}

	if old, exists := hra.fileMetrics[sourceName]; exists {
		hra.fileMetrics[targetName] = old
		delete(hra.fileMetrics, sourceName)
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

func (hra *HotspotRiskAnalysis) fileRisk(file *object.File, startTick int) (FileRisk, bool) {
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

	metrics.CurrentSize = size

	churn := 0

	for tick, count := range metrics.ChurnByTick {
		if tick >= startTick {
			churn += count
		}
	}

	return FileRisk{
		Path: file.Name, Size: size, Churn: churn, CouplingDegree: len(metrics.CoupledFiles),
		OwnershipGini: calculateGini(metrics.AuthorLines),
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

		// Calculate composite score with weights
		score := 1.0
		score *= math.Pow(sizeNorm, float64(hra.WeightSize))
		score *= math.Pow(churnNorm, float64(hra.WeightChurn))
		score *= math.Pow(couplingNorm, float64(hra.WeightCoupling))
		score *= math.Pow(ownershipNorm, float64(hra.WeightOwnership))

		risks[riskIndex].RiskScore = score
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
	fmt.Fprintln(writer, "  window_days:", result.WindowDays)
	fmt.Fprintln(writer, "  files:")

	for _, file := range result.Files {
		fmt.Fprintf(writer, "    - path: %s\n", yaml.SafeString(file.Path))
		fmt.Fprintf(writer, "      risk_score: %.6f\n", file.RiskScore)
		fmt.Fprintf(writer, "      size: %d\n", file.Size)
		fmt.Fprintf(writer, "      churn: %d\n", file.Churn)
		fmt.Fprintf(writer, "      coupling_degree: %d\n", file.CouplingDegree)
		fmt.Fprintf(writer, "      ownership_gini: %.6f\n", file.OwnershipGini)
		fmt.Fprintf(writer, "      normalized:\n")
		fmt.Fprintf(writer, "        size: %.6f\n", file.SizeNormalized)
		fmt.Fprintf(writer, "        churn: %.6f\n", file.ChurnNormalized)
		fmt.Fprintf(writer, "        coupling: %.6f\n", file.CouplingNormalized)
		fmt.Fprintf(writer, "        ownership: %.6f\n", file.OwnershipNormalized)
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
