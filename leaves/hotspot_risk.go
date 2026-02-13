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
	"github.com/meko-christian/hercules/internal/core"
	"github.com/meko-christian/hercules/internal/pb"
	items "github.com/meko-christian/hercules/internal/plumbing"
	"github.com/meko-christian/hercules/internal/plumbing/identity"
	"github.com/meko-christian/hercules/internal/yaml"
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
	fileMetrics  map[string]*fileRiskMetrics
	tickSize     int64 // Duration of one tick in seconds
	currentTick  int
	lastCommit   *object.Commit

	l core.Logger
}

// fileRiskMetrics tracks all metrics needed to calculate risk score for a file
type fileRiskMetrics struct {
	CurrentSize   int                  // Current number of lines
	ChurnInWindow int                  // Number of changes within time window
	ChurnByTick   map[int]int          // Changes per tick for window calculation
	CoupledFiles  map[string]bool      // Set of files that co-changed with this one
	AuthorLines   map[int]int          // Lines contributed by each author
}

// HotspotRiskResult is returned by Finalize()
type HotspotRiskResult struct {
	Files      []FileRisk // Top-N risky files, sorted by score descending
	WindowDays int        // Time window used for churn calculation
}

// FileRisk contains the risk assessment for a single file
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
	// ConfigHotspotRiskTopN sets the number of top risky files to report
	ConfigHotspotRiskTopN = "HotspotRisk.TopN"
	// ConfigHotspotRiskWindow sets the time window in days for churn calculation
	ConfigHotspotRiskWindow = "HotspotRisk.WindowDays"
	// ConfigHotspotRiskWeightSize sets the weight for size factor
	ConfigHotspotRiskWeightSize = "HotspotRisk.WeightSize"
	// ConfigHotspotRiskWeightChurn sets the weight for churn factor
	ConfigHotspotRiskWeightChurn = "HotspotRisk.WeightChurn"
	// ConfigHotspotRiskWeightCoupling sets the weight for coupling factor
	ConfigHotspotRiskWeightCoupling = "HotspotRisk.WeightCoupling"
	// ConfigHotspotRiskWeightOwnership sets the weight for ownership concentration factor
	ConfigHotspotRiskWeightOwnership = "HotspotRisk.WeightOwnership"

	// DefaultTopN is the default number of files to report
	DefaultTopN = 20
	// DefaultWindowDays is the default time window in days
	DefaultWindowDays = 90
	// DefaultWeight is the default weight for all factors
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
func (hra *HotspotRiskAnalysis) Configure(facts map[string]interface{}) error {
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

func (*HotspotRiskAnalysis) ConfigureUpstream(facts map[string]interface{}) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (hra *HotspotRiskAnalysis) Flag() string {
	return "hotspot-risk"
}

// Description returns the text which explains what the analysis is doing.
func (hra *HotspotRiskAnalysis) Description() string {
	return "Identifies high-risk files by combining size, churn rate, coupling degree, and ownership concentration metrics."
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
func (hra *HotspotRiskAnalysis) Consume(deps map[string]interface{}) (map[string]interface{}, error) {
	if !hra.ShouldConsumeCommit(deps) {
		return nil, nil
	}

	hra.lastCommit = deps[core.DependencyCommit].(*object.Commit)
	treeDiff := deps[items.DependencyTreeChanges].(object.Changes)
	lineStats := deps[items.DependencyLineStats].(map[object.ChangeEntry]items.LineStats)
	author := deps[identity.DependencyAuthor].(int)
	tick := deps[items.DependencyTick].(int)
	hra.currentTick = tick

	// Track which files changed in this commit for coupling
	changedFiles := make([]string, 0, len(treeDiff))

	for _, change := range treeDiff {
		action, err := change.Action()
		if err != nil {
			return nil, err
		}

		var fileName string
		switch action {
		case merkletrie.Insert:
			fileName = change.To.Name
		case merkletrie.Delete:
			fileName = change.From.Name
		case merkletrie.Modify:
			// Handle renames
			if change.From.Name != change.To.Name {
				// Transfer metrics from old name to new name
				if old, exists := hra.fileMetrics[change.From.Name]; exists {
					hra.fileMetrics[change.To.Name] = old
					delete(hra.fileMetrics, change.From.Name)
				}
			}
			fileName = change.To.Name
		}

		if fileName != "" {
			changedFiles = append(changedFiles, fileName)

			// Get or create metrics for this file
			metrics := hra.fileMetrics[fileName]
			if metrics == nil {
				metrics = &fileRiskMetrics{
					ChurnByTick:  make(map[int]int),
					CoupledFiles: make(map[string]bool),
					AuthorLines:  make(map[int]int),
				}
				hra.fileMetrics[fileName] = metrics
			}

			// Update churn
			metrics.ChurnByTick[tick]++

			// Update author lines
			if stats, exists := lineStats[object.ChangeEntry{Name: fileName}]; exists {
				// For line ownership, we accumulate net changes per author
				netChange := stats.Added - stats.Removed
				metrics.AuthorLines[author] += netChange
			}
		}
	}

	// Update coupling: all files in this commit are coupled to each other
	for _, file1 := range changedFiles {
		if metrics, exists := hra.fileMetrics[file1]; exists {
			for _, file2 := range changedFiles {
				if file1 != file2 {
					metrics.CoupledFiles[file2] = true
				}
			}
		}
	}

	return nil, nil
}

// Finalize returns the result of the analysis.
func (hra *HotspotRiskAnalysis) Finalize() interface{} {
	if hra.lastCommit == nil {
		return HotspotRiskResult{Files: []FileRisk{}, WindowDays: hra.WindowDays}
	}

	// Calculate window in ticks
	windowTicks := 0
	if hra.tickSize > 0 {
		windowTicks = (hra.WindowDays * 24 * 3600) / int(hra.tickSize)
	}
	startTick := hra.currentTick - windowTicks
	if startTick < 0 {
		startTick = 0
	}

	// Get current file sizes and calculate metrics for existing files
	var risks []FileRisk
	tree, err := hra.lastCommit.Tree()
	if err != nil {
		hra.l.Errorf("Failed to get tree: %v", err)
		return HotspotRiskResult{Files: []FileRisk{}, WindowDays: hra.WindowDays}
	}

	err = tree.Files().ForEach(func(file *object.File) error {
		fileName := file.Name
		metrics, exists := hra.fileMetrics[fileName]
		if !exists {
			// File exists but was never changed in our analysis - skip
			return nil
		}

		// Get current file size
		blob := items.CachedBlob{Blob: file.Blob}
		if err := blob.Cache(); err != nil {
			return nil // Skip binary/unreadable files
		}
		size, err := blob.CountLines()
		if err != nil {
			return nil // Skip binary files
		}
		metrics.CurrentSize = size

		// Calculate churn within window
		churnInWindow := 0
		for tick, count := range metrics.ChurnByTick {
			if tick >= startTick {
				churnInWindow += count
			}
		}

		// Calculate coupling degree
		couplingDegree := len(metrics.CoupledFiles)

		// Calculate ownership Gini coefficient
		gini := calculateGini(metrics.AuthorLines)

		risks = append(risks, FileRisk{
			Path:           fileName,
			Size:           size,
			Churn:          churnInWindow,
			CouplingDegree: couplingDegree,
			OwnershipGini:  gini,
		})

		return nil
	})

	if err != nil {
		hra.l.Errorf("Failed to iterate files: %v", err)
	}

	// Normalize and calculate risk scores
	hra.normalizeAndScore(risks)

	// Sort by risk score descending
	sort.Slice(risks, func(i, j int) bool {
		return risks[i].RiskScore > risks[j].RiskScore
	})

	// Take top N
	if len(risks) > hra.TopN {
		risks = risks[:hra.TopN]
	}

	return HotspotRiskResult{
		Files:      risks,
		WindowDays: hra.WindowDays,
	}
}

// normalizeAndScore normalizes all factors to [0,1] and calculates risk scores
func (hra *HotspotRiskAnalysis) normalizeAndScore(risks []FileRisk) {
	if len(risks) == 0 {
		return
	}

	// Find min/max for each factor
	var maxSize, maxChurn, maxCoupling float64 = 0, 0, 0

	for _, risk := range risks {
		if float64(risk.Size) > maxSize {
			maxSize = float64(risk.Size)
		}
		if float64(risk.Churn) > maxChurn {
			maxChurn = float64(risk.Churn)
		}
		if float64(risk.CouplingDegree) > maxCoupling {
			maxCoupling = float64(risk.CouplingDegree)
		}
	}

	// Normalize and calculate scores
	for i := range risks {
		// Size: use log scale, then normalize
		var sizeNorm float64
		if risks[i].Size > 0 && maxSize > 0 {
			logSize := math.Log(float64(risks[i].Size) + 1)
			logMaxSize := math.Log(maxSize + 1)
			sizeNorm = logSize / logMaxSize
		}

		// Churn: linear normalization
		var churnNorm float64
		if maxChurn > 0 {
			churnNorm = float64(risks[i].Churn) / maxChurn
		}

		// Coupling: linear normalization
		var couplingNorm float64
		if maxCoupling > 0 {
			couplingNorm = float64(risks[i].CouplingDegree) / maxCoupling
		}

		// Ownership: Gini is already in [0,1], higher = more concentrated
		ownershipNorm := risks[i].OwnershipGini

		// Store normalized values
		risks[i].SizeNormalized = sizeNorm
		risks[i].ChurnNormalized = churnNorm
		risks[i].CouplingNormalized = couplingNorm
		risks[i].OwnershipNormalized = ownershipNorm

		// Calculate composite score with weights
		score := 1.0
		score *= math.Pow(sizeNorm, float64(hra.WeightSize))
		score *= math.Pow(churnNorm, float64(hra.WeightChurn))
		score *= math.Pow(couplingNorm, float64(hra.WeightCoupling))
		score *= math.Pow(ownershipNorm, float64(hra.WeightOwnership))

		risks[i].RiskScore = score
	}
}

// calculateGini computes the Gini coefficient for line ownership distribution
// Returns value in [0,1] where 0 = perfectly equal, 1 = one person owns everything
func calculateGini(authorLines map[int]int) float64 {
	if len(authorLines) == 0 {
		return 0
	}
	if len(authorLines) == 1 {
		return 1.0 // Single owner = maximum concentration
	}

	// Get line counts, filtering out negative values (deleted lines)
	var values []int
	totalLines := 0
	for _, lines := range authorLines {
		if lines > 0 {
			values = append(values, lines)
			totalLines += lines
		}
	}

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
	n := len(values)
	var weightedSum int64
	for i, val := range values {
		weightedSum += int64(i+1) * int64(val)
	}

	gini := (2.0*float64(weightedSum))/(float64(n)*float64(totalLines)) - float64(n+1)/float64(n)

	// Clamp to [0, 1] to handle numerical issues
	if gini < 0 {
		gini = 0
	}
	if gini > 1 {
		gini = 1
	}

	return gini
}

// Fork clones this pipeline item.
func (hra *HotspotRiskAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkSamePipelineItem(hra, n)
}

// Serialize converts the analysis result to text or bytes.
func (hra *HotspotRiskAnalysis) Serialize(result interface{}, binary bool, writer io.Writer) error {
	riskResult := result.(HotspotRiskResult)
	if binary {
		return hra.serializeBinary(&riskResult, writer)
	}
	hra.serializeText(&riskResult, writer)
	return nil
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
	message := pb.HotspotRiskResults{
		WindowDays: int32(result.WindowDays),
		Files:      make([]*pb.FileRisk, len(result.Files)),
	}

	for i, file := range result.Files {
		message.Files[i] = &pb.FileRisk{
			Path:                file.Path,
			RiskScore:           file.RiskScore,
			Size_:               int32(file.Size),
			Churn:               int32(file.Churn),
			CouplingDegree:      int32(file.CouplingDegree),
			OwnershipGini:       file.OwnershipGini,
			SizeNormalized:      file.SizeNormalized,
			ChurnNormalized:     file.ChurnNormalized,
			CouplingNormalized:  file.CouplingNormalized,
			OwnershipNormalized: file.OwnershipNormalized,
		}
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return err
	}
	_, err = writer.Write(serialized)
	return err
}

// Deserialize converts protobuf bytes to HotspotRiskResult.
func (hra *HotspotRiskAnalysis) Deserialize(pbmessage []byte) (interface{}, error) {
	message := pb.HotspotRiskResults{}
	err := proto.Unmarshal(pbmessage, &message)
	if err != nil {
		return nil, err
	}

	result := HotspotRiskResult{
		WindowDays: int(message.WindowDays),
		Files:      make([]FileRisk, len(message.Files)),
	}

	for i, file := range message.Files {
		result.Files[i] = FileRisk{
			Path:                file.Path,
			RiskScore:           file.RiskScore,
			Size:                int(file.Size_),
			Churn:               int(file.Churn),
			CouplingDegree:      int(file.CouplingDegree),
			OwnershipGini:       file.OwnershipGini,
			SizeNormalized:      file.SizeNormalized,
			ChurnNormalized:     file.ChurnNormalized,
			CouplingNormalized:  file.CouplingNormalized,
			OwnershipNormalized: file.OwnershipNormalized,
		}
	}

	return result, nil
}

// MergeResults combines two HotspotRisk results (not really meaningful, but required by interface).
func (hra *HotspotRiskAnalysis) MergeResults(r1, r2 interface{}, c1, c2 *core.CommonAnalysisResult) interface{} {
	// Merging hotspot risk across repositories doesn't make semantic sense,
	// but we implement it by concatenating and re-sorting
	cr1 := r1.(HotspotRiskResult)
	cr2 := r2.(HotspotRiskResult)

	allFiles := append(cr1.Files, cr2.Files...)
	sort.Slice(allFiles, func(i, j int) bool {
		return allFiles[i].RiskScore > allFiles[j].RiskScore
	})

	if len(allFiles) > hra.TopN {
		allFiles = allFiles[:hra.TopN]
	}

	return HotspotRiskResult{
		Files:      allFiles,
		WindowDays: cr1.WindowDays,
	}
}

func init() {
	core.Registry.Register(&HotspotRiskAnalysis{})
}
