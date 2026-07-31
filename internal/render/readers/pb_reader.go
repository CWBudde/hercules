package readers

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"sort"

	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/analysisio"
	"github.com/cwbudde/hercules/internal/pb"
	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/progress"
)

type ProtobufReader struct {
	data   *pb.AnalysisResults
	Limits analysisio.Limits
	Quiet  bool
}

// Read loads the Protobuf data into the ProtobufReader structure.
func (r *ProtobufReader) Read(file io.Reader) error {
	// Initialize progress tracking for file reading
	progEstimator := progress.NewProgressEstimator(!r.Quiet)

	// Start reading operation
	progEstimator.StartOperation("Reading protobuf data", 2) // read + parse phases

	progEstimator.UpdateProgress(1)

	allBytes, err := analysisio.ReadAll(file, r.Limits)
	if err != nil {
		progEstimator.FinishOperation()
		return fmt.Errorf("read protobuf input: %w", err)
	}

	progEstimator.UpdateProgress(1)

	var results pb.AnalysisResults

	err = proto.Unmarshal(allBytes, &results)
	if err != nil {
		progEstimator.FinishOperation()
		return fmt.Errorf("%w: unmarshal protobuf envelope: %w", ErrAnalysisMalformed, err)
	}

	err = analysisio.ValidateAndMigrateAnalysisResults(&results, r.Limits)
	if err != nil {
		progEstimator.FinishOperation()
		return err
	}

	r.data = &results

	progEstimator.FinishOperation()

	return nil
}

// GetName retrieves the repository name from the Protobuf metadata.
func (r *ProtobufReader) GetName() string {
	if r.data.GetHeader() != nil {
		return r.data.GetHeader().GetRepository()
	}

	return ""
}

// GetHeader retrieves the start and end timestamps from the Protobuf metadata.
func (r *ProtobufReader) GetHeader() (int64, int64) {
	if r.data.GetHeader() != nil {
		return r.data.GetHeader().GetBeginUnixTime(), r.data.GetHeader().GetEndUnixTime()
	}

	return 0, 0
}

// GetProjectBurndown retrieves the project-level burndown matrix.
func (r *ProtobufReader) GetProjectBurndown() (string, [][]int) {
	// Parse burndown data from Contents
	burndownData, _ := r.parseBurndownAnalysisResults()
	if burndownData == nil || burndownData.GetProject() == nil {
		return "", nil
	}

	matrix := parseBurndownSparseMatrix(burndownData.GetProject())

	return r.GetName(), transposeMatrix(matrix)
}

// GetFilesBurndown retrieves burndown data for files.
func (r *ProtobufReader) GetFilesBurndown() ([]FileBurndown, error) {
	burndownData, err := r.parseBurndownAnalysisResults()
	if err != nil {
		return nil, err
	}

	if len(burndownData.GetFiles()) == 0 {
		return nil, fmt.Errorf("%w: files burndown", ErrAnalysisMissing)
	}

	// Process each file's burndown matrix
	var fileBurndowns []FileBurndown

	for _, fileMatrix := range burndownData.GetFiles() {
		matrix := parseBurndownSparseMatrix(fileMatrix)
		transposed := transposeMatrix(matrix)
		fileBurndowns = append(fileBurndowns, FileBurndown{
			Filename: fileMatrix.GetName(),
			Matrix:   transposed,
		})
	}

	return fileBurndowns, nil
}

// GetPeopleBurndown retrieves burndown data for people.
func (r *ProtobufReader) GetPeopleBurndown() ([]PeopleBurndown, error) {
	burndownData, err := r.parseBurndownAnalysisResults()
	if err != nil {
		return nil, err
	}

	if len(burndownData.GetPeople()) == 0 {
		return nil, fmt.Errorf("%w: people burndown", ErrAnalysisMissing)
	}

	// Process each person's burndown matrix
	var peopleBurndowns []PeopleBurndown

	for _, personMatrix := range burndownData.GetPeople() {
		matrix := parseBurndownSparseMatrix(personMatrix)
		transposed := transposeMatrix(matrix)
		peopleBurndowns = append(peopleBurndowns, PeopleBurndown{
			Person: personMatrix.GetName(),
			Matrix: transposed,
		})
	}

	return peopleBurndowns, nil
}

// GetRepositoriesBurndown retrieves per-repository burndown data from combined Hercules output.
func (r *ProtobufReader) GetRepositoriesBurndown() ([]RepositoryBurndown, error) {
	burndownData, err := r.parseBurndownAnalysisResults()
	if err != nil {
		return nil, err
	}

	if len(burndownData.GetRepositories()) == 0 {
		return nil, fmt.Errorf("%w: repository burndown", ErrAnalysisMissing)
	}

	repositories := make([]RepositoryBurndown, 0, len(burndownData.GetRepositories()))
	for _, repoMatrix := range burndownData.GetRepositories() {
		matrix := parseBurndownSparseMatrix(repoMatrix)
		repositories = append(repositories, RepositoryBurndown{
			Repository: repoMatrix.GetName(),
			Matrix:     transposeMatrix(matrix),
		})
	}

	return repositories, nil
}

// GetRepositoryNames retrieves repository_sequence from combined Hercules output.
func (r *ProtobufReader) GetRepositoryNames() ([]string, error) {
	burndownData, err := r.parseBurndownAnalysisResults()
	if err != nil {
		return nil, err
	}

	names := append([]string(nil), burndownData.GetRepositorySequence()...)

	return names, nil
}

// GetOwnershipBurndown retrieves the ownership matrix and sequence.
func (r *ProtobufReader) GetOwnershipBurndown() ([]string, map[string][][]int, error) {
	// Get people burndown data (matches Python behavior)
	peopleBurndowns, err := r.GetPeopleBurndown()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get people burndown data: %w", err)
	}

	// Extract people sequence (names) and build ownership map
	var peopleSequence []string
	ownership := make(map[string][][]int)

	for _, peopleBurndown := range peopleBurndowns {
		peopleSequence = append(peopleSequence, peopleBurndown.Person)

		// Transpose the matrix to match Python's .T behavior
		transposedMatrix := transposeMatrix(peopleBurndown.Matrix)
		ownership[peopleBurndown.Person] = transposedMatrix
	}

	return peopleSequence, ownership, nil
}

// GetPeopleInteraction retrieves the interaction matrix for people.
func (r *ProtobufReader) GetPeopleInteraction() ([]string, [][]int, error) {
	burndownData, err := r.parseBurndownAnalysisResults()
	if err != nil {
		return nil, nil, err
	}

	if burndownData.GetPeopleInteraction() == nil {
		return nil, nil, fmt.Errorf("%w: people interaction", ErrAnalysisMissing)
	}

	matrix := parseCompressedSparseRowMatrix(burndownData.GetPeopleInteraction())

	// Extract people names from the burndown people data
	var peopleNames []string
	for _, person := range burndownData.GetPeople() {
		peopleNames = append(peopleNames, person.GetName())
	}

	return peopleNames, matrix, nil
}

// GetFileCooccurrence retrieves file coupling data.
func (r *ProtobufReader) GetFileCooccurrence() ([]string, SparseMatrix, error) {
	couplesData, err := r.parseCouplesAnalysisResults()
	if err != nil {
		return nil, SparseMatrix{}, err
	}

	if couplesData.GetFileCouples() == nil || couplesData.GetFileCouples().GetMatrix() == nil {
		return nil, SparseMatrix{}, fmt.Errorf("%w: file coupling", ErrAnalysisMissing)
	}

	matrix, err := parseCompressedSparseCouplingMatrix(couplesData.GetFileCouples().GetMatrix())
	if err != nil {
		return nil, SparseMatrix{}, fmt.Errorf("%w: file coupling: %w", ErrAnalysisMalformed, err)
	}

	return couplesData.GetFileCouples().GetIndex(), matrix, nil
}

// GetPeopleCooccurrence retrieves people coupling data.
func (r *ProtobufReader) GetPeopleCooccurrence() ([]string, SparseMatrix, error) {
	couplesData, err := r.parseCouplesAnalysisResults()
	if err != nil {
		return nil, SparseMatrix{}, err
	}

	if couplesData.GetPeopleCouples() == nil || couplesData.GetPeopleCouples().GetMatrix() == nil {
		return nil, SparseMatrix{}, fmt.Errorf("%w: people coupling", ErrAnalysisMissing)
	}

	matrix, err := parseCompressedSparseCouplingMatrix(couplesData.GetPeopleCouples().GetMatrix())
	if err != nil {
		return nil, SparseMatrix{}, fmt.Errorf("%w: people coupling: %w", ErrAnalysisMalformed, err)
	}

	index, err := alignCouplingLabels(couplesData.GetPeopleCouples().GetIndex(), matrix, true)
	if err != nil {
		return nil, SparseMatrix{}, fmt.Errorf("%w: people coupling: %w", ErrAnalysisMalformed, err)
	}

	return index, matrix, nil
}

// GetShotnessCooccurrence retrieves shotness coupling data.
func (r *ProtobufReader) GetShotnessCooccurrence() ([]string, SparseMatrix, error) {
	shotnessRecords, err := r.GetShotnessRecords()
	if err != nil {
		return nil, SparseMatrix{}, err
	}

	return shotnessCouplingMatrix(shotnessRecords)
}

// GetShotnessRecords retrieves shotness records.
func (r *ProtobufReader) GetShotnessRecords() ([]ShotnessRecord, error) {
	shotnessData, err := r.parseShotnessAnalysisResults()
	if err != nil {
		return nil, err
	}

	pbRecords := shotnessData.GetRecords()

	records := make([]ShotnessRecord, len(pbRecords))
	for i, pbRecord := range pbRecords {
		records[i] = ShotnessRecord{
			Type:     pbRecord.GetType(),
			Name:     pbRecord.GetName(),
			File:     pbRecord.GetFile(),
			Counters: pbRecord.GetCounters(),
		}
	}

	return records, nil
}

// GetDeveloperStats retrieves developer statistics.
func (r *ProtobufReader) GetDeveloperStats() ([]DeveloperStat, error) {
	timeSeries, err := r.GetDeveloperTimeSeriesData()
	if err != nil {
		return nil, err
	}

	return aggregateDeveloperStats(timeSeries), nil
}

// GetLanguageStats retrieves language statistics.
func (r *ProtobufReader) GetLanguageStats() ([]LanguageStat, error) {
	timeSeries, err := r.GetDeveloperTimeSeriesData()
	if err != nil {
		return nil, fmt.Errorf("failed to get developer time series data: %w", err)
	}

	return aggregateLanguageStats(timeSeries)
}

// GetRuntimeStats retrieves runtime statistics.
func (r *ProtobufReader) GetRuntimeStats() (map[string]float64, error) {
	if r.data.GetHeader() == nil {
		return nil, errors.New("no header found for runtime stats")
	}

	runtimeStats := make(map[string]float64)

	if r.data.Header.RunTimePerItem != nil {
		maps.Copy(runtimeStats, r.data.GetHeader().GetRunTimePerItem())
	}

	return runtimeStats, nil
}

// GetSentimentByTick retrieves real sentiment data from CommentSentimentResults.
func (r *ProtobufReader) GetSentimentByTick() (map[int]SentimentTick, error) {
	sentimentData, err := r.parseSentimentAnalysisResults()
	if err != nil {
		return nil, err
	}

	if len(sentimentData.GetSentimentByTick()) == 0 {
		return nil, fmt.Errorf("%w: Sentiment", ErrAnalysisMissing)
	}

	result := make(map[int]SentimentTick, len(sentimentData.GetSentimentByTick()))
	for tick, sentiment := range sentimentData.GetSentimentByTick() {
		if sentiment == nil {
			continue
		}

		result[int(tick)] = SentimentTick{
			Value:    sentiment.GetValue(),
			Comments: append([]string(nil), sentiment.GetComments()...),
			Commits:  append([]string(nil), sentiment.GetCommits()...),
		}
	}

	return result, nil
}

// GetTemporalActivity retrieves temporal activity data with aggregate and per-tick views.
func (r *ProtobufReader) GetTemporalActivity() (*TemporalActivityData, error) {
	temporalData, err := r.parseTemporalActivityResults()
	if err != nil {
		return nil, err
	}

	activities := make(map[int]TemporalDeveloperActivity, len(temporalData.GetActivities()))
	for devID, activity := range temporalData.GetActivities() {
		if activity == nil {
			continue
		}

		activities[int(devID)] = TemporalDeveloperActivity{
			Weekdays: convertTemporalDimension(activity.GetWeekdays()),
			Hours:    convertTemporalDimension(activity.GetHours()),
			Months:   convertTemporalDimension(activity.GetMonths()),
			Weeks:    convertTemporalDimension(activity.GetWeeks()),
		}
	}

	ticks := make(map[int]map[int]TemporalActivityTick, len(temporalData.GetTicks()))
	for tickID, tickDevs := range temporalData.GetTicks() {
		if tickDevs == nil {
			continue
		}

		devs := make(map[int]TemporalActivityTick, len(tickDevs.GetDevs()))
		for devID, tick := range tickDevs.GetDevs() {
			if tick == nil {
				continue
			}

			devs[int(devID)] = TemporalActivityTick{
				Commits: int(tick.GetCommits()),
				Lines:   int(tick.GetLines()),
				Weekday: int(tick.GetWeekday()),
				Hour:    int(tick.GetHour()),
				Month:   int(tick.GetMonth()),
				Week:    int(tick.GetWeek()),
			}
		}

		ticks[int(tickID)] = devs
	}

	return &TemporalActivityData{
		Activities: activities,
		People:     append([]string(nil), temporalData.GetDevIndex()...),
		Ticks:      ticks,
		TickSize:   temporalData.GetTickSize(),
	}, nil
}

// GetBusFactor retrieves bus factor snapshots and subsystem values.
func (r *ProtobufReader) GetBusFactor() (*BusFactorData, error) {
	busFactorData, err := r.parseBusFactorResults()
	if err != nil {
		return nil, err
	}

	snapshots := make(map[int]BusFactorSnapshot, len(busFactorData.GetSnapshots()))
	for tick, snapshot := range busFactorData.GetSnapshots() {
		if snapshot == nil {
			continue
		}

		snapshots[int(tick)] = BusFactorSnapshot{
			BusFactor:   int(snapshot.GetBusFactor()),
			TotalLines:  snapshot.GetTotalLines(),
			AuthorLines: convertInt32Int64Map(snapshot.GetAuthorLines()),
		}
	}

	return &BusFactorData{
		Snapshots:          snapshots,
		People:             append([]string(nil), busFactorData.GetDevIndex()...),
		SubsystemBusFactor: convertStringInt32Map(busFactorData.GetSubsystemBusFactor()),
		Threshold:          busFactorData.GetThreshold(),
		TickSize:           busFactorData.GetTickSize(),
	}, nil
}

// GetOwnershipConcentration retrieves ownership concentration snapshots and subsystem metrics.
func (r *ProtobufReader) GetOwnershipConcentration() (*OwnershipConcentrationData, error) {
	ownershipData, err := r.parseOwnershipConcentrationResults()
	if err != nil {
		return nil, err
	}

	snapshots := make(map[int]OwnershipConcentrationSnapshot, len(ownershipData.GetSnapshots()))
	for tick, snapshot := range ownershipData.GetSnapshots() {
		if snapshot == nil {
			continue
		}

		snapshots[int(tick)] = OwnershipConcentrationSnapshot{
			Gini:        snapshot.GetGini(),
			HHI:         snapshot.GetHhi(),
			TotalLines:  snapshot.GetTotalLines(),
			AuthorLines: convertInt32Int64Map(snapshot.GetAuthorLines()),
		}
	}

	return &OwnershipConcentrationData{
		Snapshots:     snapshots,
		People:        append([]string(nil), ownershipData.GetDevIndex()...),
		SubsystemGini: copyStringFloat64Map(ownershipData.GetSubsystemGini()),
		SubsystemHHI:  copyStringFloat64Map(ownershipData.GetSubsystemHhi()),
		TickSize:      ownershipData.GetTickSize(),
	}, nil
}

// GetKnowledgeDiffusion retrieves per-file knowledge diffusion data.
func (r *ProtobufReader) GetKnowledgeDiffusion() (*KnowledgeDiffusionData, error) {
	diffusionData, err := r.parseKnowledgeDiffusionResults()
	if err != nil {
		return nil, err
	}

	files := make(map[string]KnowledgeDiffusionFile, len(diffusionData.GetFiles()))
	for fileName, fileData := range diffusionData.GetFiles() {
		if fileData == nil {
			continue
		}

		files[fileName] = KnowledgeDiffusionFile{
			UniqueEditors:         int(fileData.GetUniqueEditorsCount()),
			RecentEditors:         int(fileData.GetRecentEditorsCount()),
			UniqueEditorsOverTime: convertInt32Int32Map(fileData.GetUniqueEditorsOverTime()),
			Authors:               convertInt32Slice(fileData.GetAuthors()),
		}
	}

	return &KnowledgeDiffusionData{
		Files:        files,
		Distribution: convertInt32Int32Map(diffusionData.GetDistribution()),
		People:       append([]string(nil), diffusionData.GetDevIndex()...),
		WindowMonths: int(diffusionData.GetWindowMonths()),
		TickSize:     diffusionData.GetTickSize(),
	}, nil
}

// GetHotspotRisk retrieves file risk scores.
func (r *ProtobufReader) GetHotspotRisk() (*HotspotRiskData, error) {
	hotspotData, err := r.parseHotspotRiskResults()
	if err != nil {
		return nil, err
	}

	files := make([]HotspotRiskFile, 0, len(hotspotData.GetFiles()))
	for _, file := range hotspotData.GetFiles() {
		if file == nil {
			continue
		}

		files = append(files, HotspotRiskFile{
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
		})
	}

	return &HotspotRiskData{
		Files:      files,
		WindowDays: int(hotspotData.GetWindowDays()),
	}, nil
}

// GetRefactoringProxy retrieves refactoring proxy data from either contents or top-level field.
func (r *ProtobufReader) GetRefactoringProxy() (*RefactoringProxyData, error) {
	proxyData, err := r.parseRefactoringProxyResults()
	if err != nil {
		return nil, err
	}

	start, end := r.GetHeader()

	tickSizeDays := proxyData.GetTickSize() / int64(86400*1_000_000_000)
	if tickSizeDays == 0 && proxyData.GetTickSize() > 0 {
		tickSizeDays = 1
	}

	ticks := make([]RefactoringProxyTick, 0, len(proxyData.GetTicks()))
	for i, tickIndex := range proxyData.GetTicks() {
		rate := float32(0)
		if i < len(proxyData.GetRenameRatios()) {
			rate = proxyData.GetRenameRatios()[i]
		}

		isRefactoring := false
		if i < len(proxyData.GetIsRefactoring()) {
			isRefactoring = proxyData.GetIsRefactoring()[i]
		}

		totalChanges := 0
		if i < len(proxyData.GetTotalChanges()) {
			totalChanges = int(proxyData.GetTotalChanges()[i])
		}

		timestamp := start
		if tickSizeDays > 0 {
			timestamp = start + int64(tickIndex)*tickSizeDays*86400
		}

		ticks = append(ticks, RefactoringProxyTick{
			Timestamp:       timestamp,
			RefactoringRate: rate,
			IsRefactoring:   isRefactoring,
			TotalChanges:    totalChanges,
		})
	}

	return &RefactoringProxyData{
		Ticks:        ticks,
		Threshold:    proxyData.GetThreshold(),
		TickSizeDays: tickSizeDays,
		StartDate:    start,
		EndDate:      end,
	}, nil
}

// GetCommits retrieves commit statistics when Hercules was run with --commits-stat.
func (r *ProtobufReader) GetCommits() (*CommitsData, error) {
	commitsData, err := r.parseCommitsAnalysisResults()
	if err != nil {
		return nil, err
	}

	commits := make([]Commit, 0, len(commitsData.GetCommits()))
	for _, commit := range commitsData.GetCommits() {
		if commit == nil {
			continue
		}

		files := make([]CommitFile, 0, len(commit.GetFiles()))
		for _, file := range commit.GetFiles() {
			if file == nil {
				continue
			}

			files = append(files, CommitFile{
				Name:     file.GetName(),
				Language: file.GetLanguage(),
				Stats:    convertLineStats(file.GetStats()),
			})
		}

		commits = append(commits, Commit{
			Hash:         commit.GetHash(),
			WhenUnixTime: commit.GetWhenUnixTime(),
			Author:       int(commit.GetAuthor()),
			Files:        files,
		})
	}

	return &CommitsData{
		Commits:     commits,
		AuthorIndex: append([]string(nil), commitsData.GetAuthorIndex()...),
	}, nil
}

// GetFileHistory retrieves file history data when Hercules was run with --file-history.
func (r *ProtobufReader) GetFileHistory() (*FileHistoryData, error) {
	historyData, err := r.parseFileHistoryResults()
	if err != nil {
		return nil, err
	}

	files := make(map[string]FileHistory, len(historyData.GetFiles()))
	for path, history := range historyData.GetFiles() {
		if history == nil {
			continue
		}

		changes := make(map[int]LineStats, len(history.GetChangesByDeveloper()))
		for developer, stats := range history.GetChangesByDeveloper() {
			changes[int(developer)] = convertLineStats(stats)
		}

		files[path] = FileHistory{
			Commits:            append([]string(nil), history.GetCommits()...),
			ChangesByDeveloper: changes,
		}
	}

	return &FileHistoryData{Files: files}, nil
}

// GetDeveloperTimeSeriesData returns Python-compatible time series data for protobuf files
// This now parses real temporal data from DevsAnalysisResults.Ticks (matches Python's approach).
func (r *ProtobufReader) GetDeveloperTimeSeriesData() (*DeveloperTimeSeriesData, error) {
	devsData, err := r.parseDevsAnalysisResults()
	if err != nil {
		return nil, err
	}

	return &DeveloperTimeSeriesData{
		People: append([]string(nil), devsData.GetDevIndex()...),
		Days:   protobufDeveloperDays(devsData),
	}, nil
}

func protobufDeveloperDays(devsData *pb.DevsAnalysisResults) map[int]map[int]DevDay {
	ticks := devsData.GetTicks()

	days := make(map[int]map[int]DevDay, len(ticks))
	for tickKey, tickDevs := range ticks {
		if tickDevs == nil {
			continue
		}

		days[int(tickKey)] = protobufDeveloperDay(tickDevs)
	}

	return days
}

func protobufDeveloperDay(tickDevs *pb.TickDevs) map[int]DevDay {
	devs := tickDevs.GetDevs()

	day := make(map[int]DevDay, len(devs))
	for devIndex, devTick := range devs {
		if devTick != nil {
			day[int(devIndex)] = protobufDevDay(devTick)
		}
	}

	return day
}

func protobufDevDay(devTick *pb.DevTick) DevDay {
	day := DevDay{
		Commits:   int(devTick.GetCommits()),
		Languages: protobufLanguages(devTick.GetLanguages()),
	}

	stats := devTick.GetStats()
	if stats != nil {
		day.LinesAdded = int(stats.GetAdded())
		day.LinesRemoved = int(stats.GetRemoved())
		day.LinesModified = int(stats.GetChanged())
	}

	return day
}

func protobufLanguages(stats map[string]*pb.LineStats) map[string][]int {
	languages := make(map[string][]int, len(stats))
	for language, values := range stats {
		if values != nil {
			languages[language] = []int{
				int(values.GetAdded()),
				int(values.GetRemoved()),
				int(values.GetChanged()),
			}
		}
	}

	return languages
}

func aggregateDeveloperStats(timeSeries *DeveloperTimeSeriesData) []DeveloperStat {
	statsByDev := initialDeveloperStats(timeSeries.People)
	for _, dayStats := range timeSeries.Days {
		for devIndex, day := range dayStats {
			addDeveloperDay(developerStat(statsByDev, devIndex), day)
		}
	}

	return sortedDeveloperStats(statsByDev)
}

func initialDeveloperStats(people []string) map[int]*DeveloperStat {
	stats := make(map[int]*DeveloperStat, len(people))
	for devIndex, devName := range people {
		stats[devIndex] = newDeveloperStat(devName)
	}

	return stats
}

func developerStat(stats map[int]*DeveloperStat, devIndex int) *DeveloperStat {
	if stat, ok := stats[devIndex]; ok {
		return stat
	}

	stat := newDeveloperStat(fmt.Sprintf("developer-%d", devIndex))
	stats[devIndex] = stat

	return stat
}

func newDeveloperStat(name string) *DeveloperStat {
	return &DeveloperStat{
		Name:      name,
		Languages: make(map[string]int),
	}
}

func addDeveloperDay(stat *DeveloperStat, day DevDay) {
	stat.Commits += day.Commits
	stat.LinesAdded += day.LinesAdded
	stat.LinesRemoved += day.LinesRemoved

	stat.LinesModified += day.LinesModified
	for language, values := range day.Languages {
		stat.Languages[language] += sumInts(values)
	}
}

func sortedDeveloperStats(statsByDev map[int]*DeveloperStat) []DeveloperStat {
	indexes := make([]int, 0, len(statsByDev))
	for devIndex := range statsByDev {
		indexes = append(indexes, devIndex)
	}

	sort.Ints(indexes)

	stats := make([]DeveloperStat, 0, len(indexes))
	for _, devIndex := range indexes {
		stats = append(stats, *statsByDev[devIndex])
	}

	return stats
}

// parseBurndownSparseMatrix converts protobuf BurndownSparseMatrix to dense matrix
// This matches the Python _parse_burndown_matrix logic.
func parseBurndownSparseMatrix(matrix *pb.BurndownSparseMatrix) [][]int {
	if matrix == nil {
		return [][]int{}
	}

	result := make([][]int, matrix.GetNumberOfRows())
	for i := range result {
		result[i] = make([]int, matrix.GetNumberOfColumns())
	}

	// Convert from row/column format to dense matrix (matches Python logic)
	for y, row := range matrix.GetRows() {
		if y >= int(matrix.GetNumberOfRows()) {
			break
		}

		for x, value := range row.GetColumns() {
			if x >= int(matrix.GetNumberOfColumns()) {
				break
			}

			result[y][x] = int(value)
		}
	}

	return result
}

// parseCompressedSparseRowMatrix converts protobuf CompressedSparseRowMatrix to dense matrix.
func parseCompressedSparseRowMatrix(matrix *pb.CompressedSparseRowMatrix) [][]int {
	if matrix == nil {
		return [][]int{}
	}

	result := make([][]int, matrix.GetNumberOfRows())
	for i := range result {
		result[i] = make([]int, matrix.GetNumberOfColumns())
	}

	// Convert from CSR format to dense matrix with bounds checking
	for i := range matrix.GetNumberOfRows() {
		if int(i+1) >= len(matrix.GetIndptr()) {
			break
		}

		start := matrix.GetIndptr()[i]
		end := matrix.GetIndptr()[i+1]

		for j := start; j < end; j++ {
			if int(j) >= len(matrix.GetIndices()) || int(j) >= len(matrix.GetData()) {
				break
			}

			col := matrix.GetIndices()[j]
			if int(col) >= int(matrix.GetNumberOfColumns()) {
				continue
			}

			value := matrix.GetData()[j]
			result[i][col] = int(value)
		}
	}

	return result
}

func parseCompressedSparseCouplingMatrix(
	matrix *pb.CompressedSparseRowMatrix,
) (SparseMatrix, error) {
	if matrix == nil {
		return SparseMatrix{}, nil
	}

	rows, columns := int(matrix.GetNumberOfRows()), int(matrix.GetNumberOfColumns())
	if rows < 0 || columns < 0 || len(matrix.GetIndptr()) != rows+1 {
		return SparseMatrix{}, fmt.Errorf(
			"invalid CSR dimensions or row offsets for %dx%d matrix", rows, columns,
		)
	}

	entries := make([]SparseEntry, 0, len(matrix.GetData()))
	for row := range rows {
		rowEntries, err := parseCompressedSparseRow(matrix, row)
		if err != nil {
			return SparseMatrix{}, err
		}

		entries = append(entries, rowEntries...)
	}

	return NewSparseMatrix(rows, columns, entries)
}

func parseCompressedSparseRow(matrix *pb.CompressedSparseRowMatrix, row int) ([]SparseEntry, error) {
	start, end := int(matrix.GetIndptr()[row]), int(matrix.GetIndptr()[row+1])
	if start < 0 || end < start || end > len(matrix.GetData()) || end > len(matrix.GetIndices()) {
		return nil, fmt.Errorf("invalid CSR offsets [%d:%d] for row %d", start, end, row)
	}

	entries := make([]SparseEntry, 0, end-start)
	for index := start; index < end; index++ {
		value := int(matrix.GetData()[index])
		if int64(value) != matrix.GetData()[index] {
			return nil, fmt.Errorf("CSR value at index %d overflows int", index)
		}

		entries = append(entries, SparseEntry{
			Row: row, Column: int(matrix.GetIndices()[index]), Value: value,
		})
	}

	return entries, nil
}

// parseBurndownAnalysisResults extracts and parses burndown data from the Contents map.
func (r *ProtobufReader) parseBurndownAnalysisResults() (*pb.BurndownAnalysisResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: Burndown", ErrAnalysisMissing)
	}

	var burndownData pb.BurndownAnalysisResults

	err := r.unmarshalContent("Burndown", &burndownData)
	if err != nil {
		return nil, err
	}

	return &burndownData, nil
}

// GetBurndownParameters retrieves burndown parameters in Python-compatible format.
func (r *ProtobufReader) GetBurndownParameters() (burndown.BurndownParameters, error) {
	burndownData, err := r.parseBurndownAnalysisResults()
	if err != nil {
		return burndown.BurndownParameters{}, err
	}

	sampling := int(burndownData.GetSampling())
	if sampling <= 0 {
		sampling = 1
	}

	granularity := int(burndownData.GetGranularity())
	if granularity <= 0 {
		granularity = 1
	}

	tickSize := float64(burndownData.GetTickSize()) / 1e9 // Hercules stores time.Duration in nanoseconds.
	if tickSize <= 0 {
		tickSize = 86400
	}

	return burndown.BurndownParameters{
		Sampling:    sampling,
		Granularity: granularity,
		TickSize:    tickSize,
	}, nil
}

// GetProjectBurndownWithHeader retrieves project burndown with full header info.
func (r *ProtobufReader) GetProjectBurndownWithHeader() (burndown.BurndownHeader, string, [][]int, error) {
	burndownData, err := r.parseBurndownAnalysisResults()
	if err != nil {
		return burndown.BurndownHeader{}, "", nil, err
	}

	if burndownData.GetProject() == nil {
		return burndown.BurndownHeader{}, "", nil, fmt.Errorf("%w: project burndown", ErrAnalysisMissing)
	}

	// Get header information
	start, last := r.GetHeader()

	params, err := r.GetBurndownParameters()
	if err != nil {
		return burndown.BurndownHeader{}, "", nil, err
	}

	header := burndown.BurndownHeader{
		Start:       start,
		Last:        last,
		Sampling:    params.Sampling,
		Granularity: params.Granularity,
		TickSize:    params.TickSize,
	}

	// Get matrix and name
	name, matrix := r.GetProjectBurndown()

	return header, name, matrix, nil
}

// parseCouplesAnalysisResults extracts and parses couples data from the Contents map.
func (r *ProtobufReader) parseCouplesAnalysisResults() (*pb.CouplesAnalysisResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: Couples", ErrAnalysisMissing)
	}

	var couplesData pb.CouplesAnalysisResults

	err := r.unmarshalContent("Couples", &couplesData)
	if err != nil {
		return nil, err
	}

	return &couplesData, nil
}

// parseShotnessAnalysisResults extracts and parses shotness data from the Contents map.
func (r *ProtobufReader) parseShotnessAnalysisResults() (*pb.ShotnessAnalysisResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: Shotness", ErrAnalysisMissing)
	}

	var shotnessData pb.ShotnessAnalysisResults

	err := r.unmarshalContent("Shotness", &shotnessData)
	if err != nil {
		return nil, err
	}

	return &shotnessData, nil
}

// parseDevsAnalysisResults extracts and parses devs data from the Contents map.
func (r *ProtobufReader) parseDevsAnalysisResults() (*pb.DevsAnalysisResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: Devs", ErrAnalysisMissing)
	}

	var devsData pb.DevsAnalysisResults

	err := r.unmarshalContent("Devs", &devsData)
	if err != nil {
		return nil, err
	}

	return &devsData, nil
}

func (r *ProtobufReader) parseSentimentAnalysisResults() (*pb.CommentSentimentResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: Sentiment", ErrAnalysisMissing)
	}

	var sentimentData pb.CommentSentimentResults

	err := r.unmarshalContent("Sentiment", &sentimentData)
	if err != nil {
		return nil, err
	}

	return &sentimentData, nil
}

func (r *ProtobufReader) parseTemporalActivityResults() (*pb.TemporalActivityResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: TemporalActivity", ErrAnalysisMissing)
	}

	var temporalData pb.TemporalActivityResults

	err := r.unmarshalContent("TemporalActivity", &temporalData)
	if err != nil {
		return nil, err
	}

	return &temporalData, nil
}

func (r *ProtobufReader) parseBusFactorResults() (*pb.BusFactorAnalysisResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: BusFactor", ErrAnalysisMissing)
	}

	var busFactorData pb.BusFactorAnalysisResults

	err := r.unmarshalContent("BusFactor", &busFactorData)
	if err != nil {
		return nil, err
	}

	return &busFactorData, nil
}

func (r *ProtobufReader) parseOwnershipConcentrationResults() (*pb.OwnershipConcentrationResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: OwnershipConcentration", ErrAnalysisMissing)
	}

	var ownershipData pb.OwnershipConcentrationResults

	err := r.unmarshalContent("OwnershipConcentration", &ownershipData)
	if err != nil {
		return nil, err
	}

	return &ownershipData, nil
}

func (r *ProtobufReader) parseKnowledgeDiffusionResults() (*pb.KnowledgeDiffusionResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: KnowledgeDiffusion", ErrAnalysisMissing)
	}

	var diffusionData pb.KnowledgeDiffusionResults

	err := r.unmarshalContent("KnowledgeDiffusion", &diffusionData)
	if err != nil {
		return nil, err
	}

	return &diffusionData, nil
}

func (r *ProtobufReader) parseHotspotRiskResults() (*pb.HotspotRiskResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: HotspotRisk", ErrAnalysisMissing)
	}

	var hotspotData pb.HotspotRiskResults

	err := r.unmarshalContent("HotspotRisk", &hotspotData)
	if err != nil {
		return nil, err
	}

	return &hotspotData, nil
}

func (r *ProtobufReader) parseRefactoringProxyResults() (*pb.RefactoringProxyResults, error) {
	if r.data == nil {
		return nil, fmt.Errorf("%w: RefactoringProxy", ErrAnalysisMissing)
	}

	if r.data.GetRefactoringProxy() != nil {
		return r.data.GetRefactoringProxy(), nil
	}

	if r.data.Contents == nil {
		return nil, fmt.Errorf("%w: RefactoringProxy", ErrAnalysisMissing)
	}

	var proxyData pb.RefactoringProxyResults

	err := r.unmarshalContent("RefactoringProxy", &proxyData)
	if err != nil {
		return nil, err
	}

	return &proxyData, nil
}

func (r *ProtobufReader) parseCommitsAnalysisResults() (*pb.CommitsAnalysisResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: CommitsStat", ErrAnalysisMissing)
	}

	var commitsData pb.CommitsAnalysisResults

	err := r.unmarshalContent("CommitsStat", &commitsData)
	if err != nil {
		return nil, err
	}

	return &commitsData, nil
}

func (r *ProtobufReader) parseFileHistoryResults() (*pb.FileHistoryResultMessage, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: FileHistoryAnalysis", ErrAnalysisMissing)
	}

	var historyData pb.FileHistoryResultMessage

	err := r.unmarshalContent("FileHistoryAnalysis", &historyData)
	if err != nil {
		return nil, err
	}

	return &historyData, nil
}

func (r *ProtobufReader) unmarshalContent(key string, message proto.Message) error {
	contentBytes, exists := r.data.GetContents()[key]
	if !exists {
		return fmt.Errorf("%w: %s", ErrAnalysisMissing, key)
	}

	err := analysisio.Unmarshal(contentBytes, message, r.Limits)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}

	return nil
}

func convertTemporalDimension(dimension *pb.TemporalDimension) TemporalDimensionData {
	if dimension == nil {
		return TemporalDimensionData{}
	}

	return TemporalDimensionData{
		Commits: convertInt32Slice(dimension.GetCommits()),
		Lines:   convertInt32Slice(dimension.GetLines()),
	}
}

func convertInt32Slice(values []int32) []int {
	result := make([]int, len(values))
	for i, value := range values {
		result[i] = int(value)
	}

	return result
}

func convertInt32Int32Map(values map[int32]int32) map[int]int {
	result := make(map[int]int, len(values))
	for key, value := range values {
		result[int(key)] = int(value)
	}

	return result
}

func convertInt32Int64Map(values map[int32]int64) map[int]int64 {
	result := make(map[int]int64, len(values))
	for key, value := range values {
		result[int(key)] = value
	}

	return result
}

func convertStringInt32Map(values map[string]int32) map[string]int {
	result := make(map[string]int, len(values))
	for key, value := range values {
		result[key] = int(value)
	}

	return result
}

func copyStringFloat64Map(values map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(values))
	maps.Copy(result, values)

	return result
}

func convertLineStats(stats *pb.LineStats) LineStats {
	if stats == nil {
		return LineStats{}
	}

	return LineStats{
		Added:   int(stats.GetAdded()),
		Removed: int(stats.GetRemoved()),
		Changed: int(stats.GetChanged()),
	}
}
