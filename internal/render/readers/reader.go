package readers

import (
	"errors"
	"io"

	"github.com/cwbudde/hercules/internal/analysisio"
	"github.com/cwbudde/hercules/internal/render/burndown"
)

var (
	ErrAnalysisMissing            = errors.New("analysis missing")
	ErrAnalysisMalformed          = analysisio.ErrAnalysisMalformed
	ErrAnalysisTooLarge           = analysisio.ErrAnalysisTooLarge
	ErrAnalysisVersionUnsupported = analysisio.ErrAnalysisVersionUnsupported
)

type Reader interface {
	Read(file io.Reader) error
	GetName() string
	GetHeader() (int64, int64)
	GetProjectBurndown() (string, [][]int)
	// Python-compatible methods
	GetBurndownParameters() (burndown.BurndownParameters, error)
	GetProjectBurndownWithHeader() (burndown.BurndownHeader, string, [][]int, error)
	GetFilesBurndown() ([]FileBurndown, error)
	GetPeopleBurndown() ([]PeopleBurndown, error)
	GetOwnershipBurndown() ([]string, map[string][][]int, error)
	GetPeopleInteraction() ([]string, [][]int, error)
	GetFileCooccurrence() ([]string, SparseMatrix, error)
	GetPeopleCooccurrence() ([]string, SparseMatrix, error)
	// GetShotnessCooccurrence returns entity labels and their symmetric dot-product
	// sparse matrix over aligned entity-indexed co-occurrence profiles. Diagonal
	// cells are squared profile magnitudes.
	GetShotnessCooccurrence() ([]string, SparseMatrix, error)
	GetShotnessRecords() ([]ShotnessRecord, error)
	GetDeveloperStats() ([]DeveloperStat, error)
	GetLanguageStats() ([]LanguageStat, error)
	GetRuntimeStats() (map[string]float64, error)
	GetDeveloperTimeSeriesData() (*DeveloperTimeSeriesData, error)
}

type FileBurndown struct {
	Filename string
	Matrix   [][]int
}

type PeopleBurndown struct {
	Person string
	Matrix [][]int
}

type RepositoryBurndown struct {
	Repository string
	Matrix     [][]int
}

type DeveloperStat struct {
	Name          string
	Commits       int
	LinesAdded    int
	LinesRemoved  int
	LinesModified int
	FilesTouched  int
	Languages     map[string]int
}

type LanguageStat struct {
	Language string
	Lines    int
}

type LineStats struct {
	Added   int
	Removed int
	Changed int
}

type CommitFile struct {
	Name     string
	Language string
	Stats    LineStats
}

type Commit struct {
	Hash         string
	WhenUnixTime int64
	Author       int
	Files        []CommitFile
}

type CommitsData struct {
	Commits     []Commit
	AuthorIndex []string
}

type FileHistory struct {
	Commits            []string
	ChangesByDeveloper map[int]LineStats
}

type FileHistoryData struct {
	Files map[string]FileHistory
}

type ShotnessRecord struct {
	Type     string          // Type of structural unit (e.g., "function", "class")
	Name     string          // Name of the structural unit
	File     string          // File path containing the unit
	Counters map[int32]int32 // Time-based modification counters
}

// DevDay represents developer activity for a single day (Python-compatible).
type DevDay struct {
	Commits       int              // Number of commits
	LinesAdded    int              // Lines of code added
	LinesRemoved  int              // Lines of code removed
	LinesModified int              // Lines of code changed
	Languages     map[string][]int // Language-specific stats [added, removed, changed]
}

// DeveloperTimeSeriesData represents Python-compatible developer time series data.
type DeveloperTimeSeriesData struct {
	People []string               // List of developer names
	Days   map[int]map[int]DevDay // {day: {dev_index: DevDay}}
}

type SentimentTick struct {
	Value    float32
	Comments []string
	Commits  []string
}

type TemporalDimensionData struct {
	Commits []int
	Lines   []int
}

type TemporalDeveloperActivity struct {
	Weekdays TemporalDimensionData
	Hours    TemporalDimensionData
	Months   TemporalDimensionData
	Weeks    TemporalDimensionData
}

type TemporalActivityTick struct {
	Commits int
	Lines   int
	Weekday int
	Hour    int
	Month   int
	Week    int
}

type TemporalActivityData struct {
	Activities map[int]TemporalDeveloperActivity
	People     []string
	Ticks      map[int]map[int]TemporalActivityTick
	TickSize   int64
}

type BusFactorSnapshot struct {
	BusFactor   int
	TotalLines  int64
	AuthorLines map[int]int64
}

type BusFactorData struct {
	Snapshots          map[int]BusFactorSnapshot
	People             []string
	SubsystemBusFactor map[string]int
	Threshold          float32
	TickSize           int64
}

type OwnershipConcentrationSnapshot struct {
	Gini        float64
	HHI         float64
	TotalLines  int64
	AuthorLines map[int]int64
}

type OwnershipConcentrationData struct {
	Snapshots     map[int]OwnershipConcentrationSnapshot
	People        []string
	SubsystemGini map[string]float64
	SubsystemHHI  map[string]float64
	TickSize      int64
}

type KnowledgeDiffusionFile struct {
	UniqueEditors         int
	RecentEditors         int
	UniqueEditorsOverTime map[int]int
	Authors               []int
}

type KnowledgeDiffusionData struct {
	Files        map[string]KnowledgeDiffusionFile
	Distribution map[int]int
	People       []string
	WindowMonths int
	TickSize     int64
}

type HotspotRiskFile struct {
	Path                string
	RiskScore           float64
	Size                int
	Churn               int
	CouplingDegree      int
	OwnershipGini       float64
	SizeNormalized      float64
	ChurnNormalized     float64
	CouplingNormalized  float64
	OwnershipNormalized float64
}

type HotspotRiskData struct {
	Files      []HotspotRiskFile
	WindowDays int
}

type RefactoringProxyTick struct {
	Timestamp       int64
	RefactoringRate float32
	IsRefactoring   bool
	TotalChanges    int
}

type RefactoringProxyData struct {
	Ticks        []RefactoringProxyTick
	Threshold    float32
	TickSizeDays int64
	StartDate    int64
	EndDate      int64
}

// OnboardingSnapshotData is one author's cumulative activity over the window
// [first commit, first commit + DaysSinceJoin], inclusive at both ends.
type OnboardingSnapshotData struct {
	DaysSinceJoin     int
	TotalCommits      int
	TotalFiles        int
	TotalLines        int
	MeaningfulCommits int
	MeaningfulFiles   int
	MeaningfulLines   int
}

// OnboardingAverageSnapshotData is a cohort's per-author mean of
// OnboardingSnapshotData at one window.
type OnboardingAverageSnapshotData struct {
	DaysSinceJoin        int
	AvgTotalCommits      float64
	AvgTotalFiles        float64
	AvgTotalLines        float64
	AvgMeaningfulCommits float64
	AvgMeaningfulFiles   float64
	AvgMeaningfulLines   float64
}

// OnboardingAuthorData is one author's ramp-up, keyed by window in days.
// FirstCommitTick is tick-indexed against the analysed repository's own tick 0,
// so it is not comparable across repositories.
type OnboardingAuthorData struct {
	FirstCommitTick int
	// DaysToFirstMeaningfulCommit is the whole-day offset from the author's
	// first commit to their first commit at or above the meaningfulness
	// threshold. It is -1 when the retained activity trail holds no meaningful
	// commit at all, which also covers results stored before the trail existed;
	// charts render that as an explicit "no meaningful commit yet" bucket rather
	// than as day zero.
	DaysToFirstMeaningfulCommit int
	JoinCohort                  string
	Snapshots                   map[int]OnboardingSnapshotData
}

// OnboardingCohortData aggregates the authors whose first commit falls in the
// same calendar month.
type OnboardingCohortData struct {
	Cohort           string
	AuthorCount      int
	AverageSnapshots map[int]OnboardingAverageSnapshotData
}

type OnboardingData struct {
	Authors             map[int]OnboardingAuthorData
	Cohorts             map[string]OnboardingCohortData
	WindowDays          []int
	MeaningfulThreshold int
	People              []string
	TickSize            int64
}

type RepositoryBurndownReader interface {
	GetRepositoriesBurndown() ([]RepositoryBurndown, error)
	GetRepositoryNames() ([]string, error)
}

type SentimentReader interface {
	GetSentimentByTick() (map[int]SentimentTick, error)
}

type TemporalActivityReader interface {
	GetTemporalActivity() (*TemporalActivityData, error)
}

type BusFactorReader interface {
	GetBusFactor() (*BusFactorData, error)
}

type OwnershipConcentrationReader interface {
	GetOwnershipConcentration() (*OwnershipConcentrationData, error)
}

type KnowledgeDiffusionReader interface {
	GetKnowledgeDiffusion() (*KnowledgeDiffusionData, error)
}

type HotspotRiskReader interface {
	GetHotspotRisk() (*HotspotRiskData, error)
}

type RefactoringProxyReader interface {
	GetRefactoringProxy() (*RefactoringProxyData, error)
}

type OnboardingReader interface {
	GetOnboarding() (*OnboardingData, error)
}

type CommitsReader interface {
	GetCommits() (*CommitsData, error)
}

type FileHistoryReader interface {
	GetFileHistory() (*FileHistoryData, error)
}
