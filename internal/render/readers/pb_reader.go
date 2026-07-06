package readers

import (
	"fmt"
	"io"
	"sort"

	"github.com/cwbudde/hercules/internal/pb"
	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/progress"
	"github.com/gogo/protobuf/proto"
	"github.com/spf13/viper"
)

type ProtobufReader struct {
	data *pb.AnalysisResults
}

// Read loads the Protobuf data into the ProtobufReader structure
func (r *ProtobufReader) Read(file io.Reader) error {
	// Initialize progress tracking for file reading
	quiet := viper.GetBool("quiet")
	progEstimator := progress.NewProgressEstimator(!quiet)

	// Start reading operation
	progEstimator.StartOperation("Reading protobuf data", 2) // read + parse phases

	progEstimator.UpdateProgress(1)
	allBytes, err := io.ReadAll(file)
	if err != nil {
		progEstimator.FinishOperation()
		return fmt.Errorf("error reading Protobuf file: %v", err)
	}

	progEstimator.UpdateProgress(1)
	var results pb.AnalysisResults
	if err := proto.Unmarshal(allBytes, &results); err != nil {
		progEstimator.FinishOperation()
		return fmt.Errorf("error unmarshalling Protobuf: %v", err)
	}

	r.data = &results
	progEstimator.FinishOperation()
	return nil
}

// GetName retrieves the repository name from the Protobuf metadata
func (r *ProtobufReader) GetName() string {
	if r.data.Header != nil {
		return r.data.Header.Repository
	}
	return ""
}

// GetHeader retrieves the start and end timestamps from the Protobuf metadata
func (r *ProtobufReader) GetHeader() (int64, int64) {
	if r.data.Header != nil {
		return r.data.Header.BeginUnixTime, r.data.Header.EndUnixTime
	}
	return 0, 0
}

// GetProjectBurndown retrieves the project-level burndown matrix
func (r *ProtobufReader) GetProjectBurndown() (string, [][]int) {
	// Parse burndown data from Contents
	burndownData := r.parseBurndownAnalysisResults()
	if burndownData == nil || burndownData.Project == nil {
		return "", nil
	}

	matrix := parseBurndownSparseMatrix(burndownData.Project)
	return r.GetName(), transposeMatrix(matrix)
}

// GetFilesBurndown retrieves burndown data for files
func (r *ProtobufReader) GetFilesBurndown() ([]FileBurndown, error) {
	burndownData := r.parseBurndownAnalysisResults()
	if burndownData == nil || len(burndownData.Files) == 0 {
		return nil, fmt.Errorf("no files burndown data found")
	}

	// Process each file's burndown matrix
	var fileBurndowns []FileBurndown
	for _, fileMatrix := range burndownData.Files {
		matrix := parseBurndownSparseMatrix(fileMatrix)
		transposed := transposeMatrix(matrix)
		fileBurndowns = append(fileBurndowns, FileBurndown{
			Filename: fileMatrix.Name,
			Matrix:   transposed,
		})
	}
	return fileBurndowns, nil
}

// GetPeopleBurndown retrieves burndown data for people
func (r *ProtobufReader) GetPeopleBurndown() ([]PeopleBurndown, error) {
	burndownData := r.parseBurndownAnalysisResults()
	if burndownData == nil || len(burndownData.People) == 0 {
		return nil, fmt.Errorf("no people burndown data found")
	}

	// Process each person's burndown matrix
	var peopleBurndowns []PeopleBurndown
	for _, personMatrix := range burndownData.People {
		matrix := parseBurndownSparseMatrix(personMatrix)
		transposed := transposeMatrix(matrix)
		peopleBurndowns = append(peopleBurndowns, PeopleBurndown{
			Person: personMatrix.Name,
			Matrix: transposed,
		})
	}
	return peopleBurndowns, nil
}

// GetRepositoriesBurndown retrieves per-repository burndown data from combined Hercules output.
func (r *ProtobufReader) GetRepositoriesBurndown() ([]RepositoryBurndown, error) {
	burndownData := r.parseBurndownAnalysisResults()
	if burndownData == nil || len(burndownData.Repositories) == 0 {
		return nil, fmt.Errorf("no repository burndown data found")
	}

	repositories := make([]RepositoryBurndown, 0, len(burndownData.Repositories))
	for _, repoMatrix := range burndownData.Repositories {
		matrix := parseBurndownSparseMatrix(repoMatrix)
		repositories = append(repositories, RepositoryBurndown{
			Repository: repoMatrix.Name,
			Matrix:     transposeMatrix(matrix),
		})
	}
	return repositories, nil
}

// GetRepositoryNames retrieves repository_sequence from combined Hercules output.
func (r *ProtobufReader) GetRepositoryNames() ([]string, error) {
	burndownData := r.parseBurndownAnalysisResults()
	if burndownData == nil {
		return nil, fmt.Errorf("no burndown data found")
	}
	names := append([]string(nil), burndownData.RepositorySequence...)
	return names, nil
}

// GetOwnershipBurndown retrieves the ownership matrix and sequence
func (r *ProtobufReader) GetOwnershipBurndown() ([]string, map[string][][]int, error) {
	// Get people burndown data (matches Python behavior)
	peopleBurndowns, err := r.GetPeopleBurndown()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get people burndown data: %v", err)
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

// GetPeopleInteraction retrieves the interaction matrix for people
func (r *ProtobufReader) GetPeopleInteraction() ([]string, [][]int, error) {
	burndownData := r.parseBurndownAnalysisResults()
	if burndownData == nil || burndownData.PeopleInteraction == nil {
		return nil, nil, fmt.Errorf("no people interaction data found")
	}

	matrix := parseCompressedSparseRowMatrix(burndownData.PeopleInteraction)

	// Extract people names from the burndown people data
	var peopleNames []string
	for _, person := range burndownData.People {
		peopleNames = append(peopleNames, person.Name)
	}

	return peopleNames, matrix, nil
}

// GetFileCooccurrence retrieves file coupling data
func (r *ProtobufReader) GetFileCooccurrence() ([]string, [][]int, error) {
	couplesData := r.parseCouplesAnalysisResults()
	if couplesData == nil || couplesData.FileCouples == nil || couplesData.FileCouples.Matrix == nil {
		return nil, nil, fmt.Errorf("no file coupling data found")
	}

	matrix := parseCompressedSparseRowMatrix(couplesData.FileCouples.Matrix)
	return couplesData.FileCouples.Index, matrix, nil
}

// GetPeopleCooccurrence retrieves people coupling data
func (r *ProtobufReader) GetPeopleCooccurrence() ([]string, [][]int, error) {
	couplesData := r.parseCouplesAnalysisResults()
	if couplesData == nil || couplesData.PeopleCouples == nil || couplesData.PeopleCouples.Matrix == nil {
		return nil, nil, fmt.Errorf("no people coupling data found")
	}

	matrix := parseCompressedSparseRowMatrix(couplesData.PeopleCouples.Matrix)
	return couplesData.PeopleCouples.Index, matrix, nil
}

// GetShotnessCooccurrence retrieves shotness coupling data
func (r *ProtobufReader) GetShotnessCooccurrence() ([]string, [][]int, error) {
	shotnessRecords, err := r.GetShotnessRecords()
	if err != nil {
		return nil, nil, err
	}

	index, matrix := shotnessCounterMatrix(shotnessRecords)
	return index, matrix, nil
}

// GetShotnessRecords retrieves shotness records
func (r *ProtobufReader) GetShotnessRecords() ([]ShotnessRecord, error) {
	shotnessData := r.parseShotnessAnalysisResults()
	if shotnessData == nil || len(shotnessData.Records) == 0 {
		return []ShotnessRecord{}, fmt.Errorf("no shotness data found - ensure the input data contains shotness analysis results")
	}

	pbRecords := shotnessData.Records
	records := make([]ShotnessRecord, len(pbRecords))
	for i, pbRecord := range pbRecords {
		records[i] = ShotnessRecord{
			Type:     pbRecord.Type,
			Name:     pbRecord.Name,
			File:     pbRecord.File,
			Counters: pbRecord.Counters,
		}
	}

	return records, nil
}

// GetDeveloperStats retrieves developer statistics
func (r *ProtobufReader) GetDeveloperStats() ([]DeveloperStat, error) {
	timeSeries, err := r.GetDeveloperTimeSeriesData()
	if err != nil {
		return nil, err
	}
	return aggregateDeveloperStats(timeSeries), nil
}

// GetLanguageStats retrieves language statistics
func (r *ProtobufReader) GetLanguageStats() ([]LanguageStat, error) {
	timeSeries, err := r.GetDeveloperTimeSeriesData()
	if err != nil {
		return nil, fmt.Errorf("failed to get developer time series data: %v", err)
	}
	return aggregateLanguageStats(timeSeries)
}

// GetRuntimeStats retrieves runtime statistics
func (r *ProtobufReader) GetRuntimeStats() (map[string]float64, error) {
	if r.data.Header == nil {
		return nil, fmt.Errorf("no header found for runtime stats")
	}

	runtimeStats := make(map[string]float64)
	if r.data.Header.RunTimePerItem != nil {
		for key, value := range r.data.Header.RunTimePerItem {
			runtimeStats[key] = value
		}
	}

	return runtimeStats, nil
}

// GetSentimentByTick retrieves real sentiment data from CommentSentimentResults.
func (r *ProtobufReader) GetSentimentByTick() (map[int]SentimentTick, error) {
	sentimentData, err := r.parseSentimentAnalysisResults()
	if err != nil {
		return nil, err
	}
	if len(sentimentData.SentimentByTick) == 0 {
		return nil, fmt.Errorf("%w: Sentiment", ErrAnalysisMissing)
	}

	result := make(map[int]SentimentTick, len(sentimentData.SentimentByTick))
	for tick, sentiment := range sentimentData.SentimentByTick {
		if sentiment == nil {
			continue
		}
		result[int(tick)] = SentimentTick{
			Value:    sentiment.Value,
			Comments: append([]string(nil), sentiment.Comments...),
			Commits:  append([]string(nil), sentiment.Commits...),
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

	activities := make(map[int]TemporalDeveloperActivity, len(temporalData.Activities))
	for devID, activity := range temporalData.Activities {
		if activity == nil {
			continue
		}
		activities[int(devID)] = TemporalDeveloperActivity{
			Weekdays: convertTemporalDimension(activity.Weekdays),
			Hours:    convertTemporalDimension(activity.Hours),
			Months:   convertTemporalDimension(activity.Months),
			Weeks:    convertTemporalDimension(activity.Weeks),
		}
	}

	ticks := make(map[int]map[int]TemporalActivityTick, len(temporalData.Ticks))
	for tickID, tickDevs := range temporalData.Ticks {
		if tickDevs == nil {
			continue
		}
		devs := make(map[int]TemporalActivityTick, len(tickDevs.Devs))
		for devID, tick := range tickDevs.Devs {
			if tick == nil {
				continue
			}
			devs[int(devID)] = TemporalActivityTick{
				Commits: int(tick.Commits),
				Lines:   int(tick.Lines),
				Weekday: int(tick.Weekday),
				Hour:    int(tick.Hour),
				Month:   int(tick.Month),
				Week:    int(tick.Week),
			}
		}
		ticks[int(tickID)] = devs
	}

	return &TemporalActivityData{
		Activities: activities,
		People:     append([]string(nil), temporalData.DevIndex...),
		Ticks:      ticks,
		TickSize:   temporalData.TickSize,
	}, nil
}

// GetBusFactor retrieves bus factor snapshots and subsystem values.
func (r *ProtobufReader) GetBusFactor() (*BusFactorData, error) {
	busFactorData, err := r.parseBusFactorResults()
	if err != nil {
		return nil, err
	}

	snapshots := make(map[int]BusFactorSnapshot, len(busFactorData.Snapshots))
	for tick, snapshot := range busFactorData.Snapshots {
		if snapshot == nil {
			continue
		}
		snapshots[int(tick)] = BusFactorSnapshot{
			BusFactor:   int(snapshot.BusFactor),
			TotalLines:  snapshot.TotalLines,
			AuthorLines: convertInt32Int64Map(snapshot.AuthorLines),
		}
	}

	return &BusFactorData{
		Snapshots:          snapshots,
		People:             append([]string(nil), busFactorData.DevIndex...),
		SubsystemBusFactor: convertStringInt32Map(busFactorData.SubsystemBusFactor),
		Threshold:          busFactorData.Threshold,
		TickSize:           busFactorData.TickSize,
	}, nil
}

// GetOwnershipConcentration retrieves ownership concentration snapshots and subsystem metrics.
func (r *ProtobufReader) GetOwnershipConcentration() (*OwnershipConcentrationData, error) {
	ownershipData, err := r.parseOwnershipConcentrationResults()
	if err != nil {
		return nil, err
	}

	snapshots := make(map[int]OwnershipConcentrationSnapshot, len(ownershipData.Snapshots))
	for tick, snapshot := range ownershipData.Snapshots {
		if snapshot == nil {
			continue
		}
		snapshots[int(tick)] = OwnershipConcentrationSnapshot{
			Gini:        snapshot.Gini,
			HHI:         snapshot.Hhi,
			TotalLines:  snapshot.TotalLines,
			AuthorLines: convertInt32Int64Map(snapshot.AuthorLines),
		}
	}

	return &OwnershipConcentrationData{
		Snapshots:     snapshots,
		People:        append([]string(nil), ownershipData.DevIndex...),
		SubsystemGini: copyStringFloat64Map(ownershipData.SubsystemGini),
		SubsystemHHI:  copyStringFloat64Map(ownershipData.SubsystemHhi),
		TickSize:      ownershipData.TickSize,
	}, nil
}

// GetKnowledgeDiffusion retrieves per-file knowledge diffusion data.
func (r *ProtobufReader) GetKnowledgeDiffusion() (*KnowledgeDiffusionData, error) {
	diffusionData, err := r.parseKnowledgeDiffusionResults()
	if err != nil {
		return nil, err
	}

	files := make(map[string]KnowledgeDiffusionFile, len(diffusionData.Files))
	for fileName, fileData := range diffusionData.Files {
		if fileData == nil {
			continue
		}
		files[fileName] = KnowledgeDiffusionFile{
			UniqueEditors:         int(fileData.UniqueEditorsCount),
			RecentEditors:         int(fileData.RecentEditorsCount),
			UniqueEditorsOverTime: convertInt32Int32Map(fileData.UniqueEditorsOverTime),
			Authors:               convertInt32Slice(fileData.Authors),
		}
	}

	return &KnowledgeDiffusionData{
		Files:        files,
		Distribution: convertInt32Int32Map(diffusionData.Distribution),
		People:       append([]string(nil), diffusionData.DevIndex...),
		WindowMonths: int(diffusionData.WindowMonths),
		TickSize:     diffusionData.TickSize,
	}, nil
}

// GetHotspotRisk retrieves file risk scores.
func (r *ProtobufReader) GetHotspotRisk() (*HotspotRiskData, error) {
	hotspotData, err := r.parseHotspotRiskResults()
	if err != nil {
		return nil, err
	}

	files := make([]HotspotRiskFile, 0, len(hotspotData.Files))
	for _, file := range hotspotData.Files {
		if file == nil {
			continue
		}
		files = append(files, HotspotRiskFile{
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
		})
	}

	return &HotspotRiskData{
		Files:      files,
		WindowDays: int(hotspotData.WindowDays),
	}, nil
}

// GetRefactoringProxy retrieves refactoring proxy data from either contents or top-level field.
func (r *ProtobufReader) GetRefactoringProxy() (*RefactoringProxyData, error) {
	proxyData, err := r.parseRefactoringProxyResults()
	if err != nil {
		return nil, err
	}

	start, end := r.GetHeader()
	tickSizeDays := proxyData.TickSize / int64(86400*1_000_000_000)
	if tickSizeDays == 0 && proxyData.TickSize > 0 {
		tickSizeDays = 1
	}

	ticks := make([]RefactoringProxyTick, 0, len(proxyData.Ticks))
	for i, tickIndex := range proxyData.Ticks {
		rate := float32(0)
		if i < len(proxyData.RenameRatios) {
			rate = proxyData.RenameRatios[i]
		}
		isRefactoring := false
		if i < len(proxyData.IsRefactoring) {
			isRefactoring = proxyData.IsRefactoring[i]
		}
		totalChanges := 0
		if i < len(proxyData.TotalChanges) {
			totalChanges = int(proxyData.TotalChanges[i])
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
		Threshold:    proxyData.Threshold,
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

	commits := make([]Commit, 0, len(commitsData.Commits))
	for _, commit := range commitsData.Commits {
		if commit == nil {
			continue
		}
		files := make([]CommitFile, 0, len(commit.Files))
		for _, file := range commit.Files {
			if file == nil {
				continue
			}
			files = append(files, CommitFile{
				Name:     file.Name,
				Language: file.Language,
				Stats:    convertLineStats(file.Stats),
			})
		}
		commits = append(commits, Commit{
			Hash:         commit.Hash,
			WhenUnixTime: commit.WhenUnixTime,
			Author:       int(commit.Author),
			Files:        files,
		})
	}

	return &CommitsData{
		Commits:     commits,
		AuthorIndex: append([]string(nil), commitsData.AuthorIndex...),
	}, nil
}

// GetFileHistory retrieves file history data when Hercules was run with --file-history.
func (r *ProtobufReader) GetFileHistory() (*FileHistoryData, error) {
	historyData, err := r.parseFileHistoryResults()
	if err != nil {
		return nil, err
	}

	files := make(map[string]FileHistory, len(historyData.Files))
	for path, history := range historyData.Files {
		if history == nil {
			continue
		}
		changes := make(map[int]LineStats, len(history.ChangesByDeveloper))
		for developer, stats := range history.ChangesByDeveloper {
			changes[int(developer)] = convertLineStats(stats)
		}
		files[path] = FileHistory{
			Commits:            append([]string(nil), history.Commits...),
			ChangesByDeveloper: changes,
		}
	}

	return &FileHistoryData{Files: files}, nil
}

// GetDeveloperTimeSeriesData returns Python-compatible time series data for protobuf files
// This now parses real temporal data from DevsAnalysisResults.Ticks (matches Python's approach)
func (r *ProtobufReader) GetDeveloperTimeSeriesData() (*DeveloperTimeSeriesData, error) {
	// Parse real developer time series data from protobuf (like Python does)
	devsData := r.parseDevsAnalysisResults()
	if devsData == nil {
		return nil, fmt.Errorf("no developer analysis data found")
	}

	// Extract people list from dev_index (matches Python's people = list(self.contents["Devs"].dev_index))
	people := make([]string, len(devsData.DevIndex))
	copy(people, devsData.DevIndex)

	// Parse real time series data from ticks (matches Python's self.contents["Devs"].ticks.items())
	days := make(map[int]map[int]DevDay)

	// Iterate through all time ticks
	for tickKey, tickDevs := range devsData.Ticks {
		if tickDevs == nil {
			continue
		}

		// Create developer map for this time tick
		dayDevs := make(map[int]DevDay)

		// Iterate through all developers in this tick
		for devIndex, devTick := range tickDevs.Devs {
			if devTick == nil {
				continue
			}

			// Convert languages map from protobuf format to DevDay format
			languages := make(map[string][]int)
			if devTick.Languages != nil {
				for lang, langStats := range devTick.Languages {
					if langStats != nil {
						// Python format: {lang: [added, removed, changed]}
						languages[lang] = []int{
							int(langStats.Added),
							int(langStats.Removed),
							int(langStats.Changed),
						}
					}
				}
			}

			// Convert protobuf DevTick to Go DevDay format (matches Python's DevDay structure)
			dayDevs[int(devIndex)] = DevDay{
				Commits:   int(devTick.Commits),
				Languages: languages,
			}
			if devTick.Stats != nil {
				devDay := dayDevs[int(devIndex)]
				devDay.LinesAdded = int(devTick.Stats.Added)
				devDay.LinesRemoved = int(devTick.Stats.Removed)
				devDay.LinesModified = int(devTick.Stats.Changed)
				dayDevs[int(devIndex)] = devDay
			}
		}

		// Store this day's data using the real time tick key
		days[int(tickKey)] = dayDevs
	}

	// Return the same format as Python: (people, days)
	return &DeveloperTimeSeriesData{
		People: people,
		Days:   days,
	}, nil
}

func aggregateDeveloperStats(timeSeries *DeveloperTimeSeriesData) []DeveloperStat {
	statsByDev := make(map[int]*DeveloperStat)
	for devIndex, devName := range timeSeries.People {
		statsByDev[devIndex] = &DeveloperStat{
			Name:      devName,
			Languages: make(map[string]int),
		}
	}

	for _, dayStats := range timeSeries.Days {
		for devIndex, day := range dayStats {
			stat, ok := statsByDev[devIndex]
			if !ok {
				stat = &DeveloperStat{
					Name:      fmt.Sprintf("developer-%d", devIndex),
					Languages: make(map[string]int),
				}
				statsByDev[devIndex] = stat
			}
			stat.Commits += day.Commits
			stat.LinesAdded += day.LinesAdded
			stat.LinesRemoved += day.LinesRemoved
			stat.LinesModified += day.LinesModified
			for language, values := range day.Languages {
				for _, value := range values {
					stat.Languages[language] += value
				}
			}
		}
	}

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
// This matches the Python _parse_burndown_matrix logic
func parseBurndownSparseMatrix(matrix *pb.BurndownSparseMatrix) [][]int {
	if matrix == nil {
		return [][]int{}
	}

	result := make([][]int, matrix.NumberOfRows)
	for i := range result {
		result[i] = make([]int, matrix.NumberOfColumns)
	}

	// Convert from row/column format to dense matrix (matches Python logic)
	for y, row := range matrix.Rows {
		if y >= int(matrix.NumberOfRows) {
			break
		}
		for x, value := range row.Columns {
			if x >= int(matrix.NumberOfColumns) {
				break
			}
			result[y][x] = int(value)
		}
	}

	return result
}

// parseCompressedSparseRowMatrix converts protobuf CompressedSparseRowMatrix to dense matrix
func parseCompressedSparseRowMatrix(matrix *pb.CompressedSparseRowMatrix) [][]int {
	if matrix == nil {
		return [][]int{}
	}

	result := make([][]int, matrix.NumberOfRows)
	for i := range result {
		result[i] = make([]int, matrix.NumberOfColumns)
	}

	// Convert from CSR format to dense matrix with bounds checking
	for i := int32(0); i < matrix.NumberOfRows; i++ {
		if int(i+1) >= len(matrix.Indptr) {
			break
		}
		start := matrix.Indptr[i]
		end := matrix.Indptr[i+1]

		for j := start; j < end; j++ {
			if int(j) >= len(matrix.Indices) || int(j) >= len(matrix.Data) {
				break
			}
			col := matrix.Indices[j]
			if int(col) >= int(matrix.NumberOfColumns) {
				continue
			}
			value := matrix.Data[j]
			result[i][col] = int(value)
		}
	}

	return result
}

// parseBurndownAnalysisResults extracts and parses burndown data from the Contents map
func (r *ProtobufReader) parseBurndownAnalysisResults() *pb.BurndownAnalysisResults {
	if r.data == nil || r.data.Contents == nil {
		return nil
	}

	// Look for burndown data in Contents
	burndownBytes, exists := r.data.Contents["Burndown"]
	if !exists {
		return nil
	}

	// Parse the burndown data
	var burndownData pb.BurndownAnalysisResults
	if err := proto.Unmarshal(burndownBytes, &burndownData); err != nil {
		return nil
	}

	return &burndownData
}

// GetBurndownParameters retrieves burndown parameters in Python-compatible format
func (r *ProtobufReader) GetBurndownParameters() (burndown.BurndownParameters, error) {
	burndownData := r.parseBurndownAnalysisResults()
	if burndownData == nil {
		return burndown.BurndownParameters{}, fmt.Errorf("no burndown data found")
	}

	sampling := int(burndownData.Sampling)
	if sampling <= 0 {
		sampling = 1
	}
	granularity := int(burndownData.Granularity)
	if granularity <= 0 {
		granularity = 1
	}

	tickSize := float64(burndownData.TickSize) / 1e9 // Hercules stores time.Duration in nanoseconds.
	if tickSize <= 0 {
		tickSize = 86400
	}

	return burndown.BurndownParameters{
		Sampling:    sampling,
		Granularity: granularity,
		TickSize:    tickSize,
	}, nil
}

// GetProjectBurndownWithHeader retrieves project burndown with full header info
func (r *ProtobufReader) GetProjectBurndownWithHeader() (burndown.BurndownHeader, string, [][]int, error) {
	burndownData := r.parseBurndownAnalysisResults()
	if burndownData == nil || burndownData.Project == nil {
		return burndown.BurndownHeader{}, "", nil, fmt.Errorf("no project burndown data found")
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

// parseCouplesAnalysisResults extracts and parses couples data from the Contents map
func (r *ProtobufReader) parseCouplesAnalysisResults() *pb.CouplesAnalysisResults {
	if r.data == nil || r.data.Contents == nil {
		return nil
	}

	// Look for couples data in Contents
	couplesBytes, exists := r.data.Contents["Couples"]
	if !exists {
		return nil
	}

	// Parse the couples data
	var couplesData pb.CouplesAnalysisResults
	if err := proto.Unmarshal(couplesBytes, &couplesData); err != nil {
		return nil
	}

	return &couplesData
}

// parseShotnessAnalysisResults extracts and parses shotness data from the Contents map
func (r *ProtobufReader) parseShotnessAnalysisResults() *pb.ShotnessAnalysisResults {
	if r.data == nil || r.data.Contents == nil {
		return nil
	}

	// Look for shotness data in Contents
	shotnessBytes, exists := r.data.Contents["Shotness"]
	if !exists {
		return nil
	}

	// Parse the shotness data
	var shotnessData pb.ShotnessAnalysisResults
	if err := proto.Unmarshal(shotnessBytes, &shotnessData); err != nil {
		return nil
	}

	return &shotnessData
}

// parseDevsAnalysisResults extracts and parses devs data from the Contents map
func (r *ProtobufReader) parseDevsAnalysisResults() *pb.DevsAnalysisResults {
	if r.data == nil || r.data.Contents == nil {
		return nil
	}

	// Look for devs data in Contents
	devsBytes, exists := r.data.Contents["Devs"]
	if !exists {
		return nil
	}

	// Parse the devs data
	var devsData pb.DevsAnalysisResults
	if err := proto.Unmarshal(devsBytes, &devsData); err != nil {
		return nil
	}

	return &devsData
}

func (r *ProtobufReader) parseSentimentAnalysisResults() (*pb.CommentSentimentResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: Sentiment", ErrAnalysisMissing)
	}
	var sentimentData pb.CommentSentimentResults
	if err := r.unmarshalContent("Sentiment", &sentimentData); err != nil {
		return nil, err
	}
	return &sentimentData, nil
}

func (r *ProtobufReader) parseTemporalActivityResults() (*pb.TemporalActivityResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: TemporalActivity", ErrAnalysisMissing)
	}
	var temporalData pb.TemporalActivityResults
	if err := r.unmarshalContent("TemporalActivity", &temporalData); err != nil {
		return nil, err
	}
	return &temporalData, nil
}

func (r *ProtobufReader) parseBusFactorResults() (*pb.BusFactorAnalysisResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: BusFactor", ErrAnalysisMissing)
	}
	var busFactorData pb.BusFactorAnalysisResults
	if err := r.unmarshalContent("BusFactor", &busFactorData); err != nil {
		return nil, err
	}
	return &busFactorData, nil
}

func (r *ProtobufReader) parseOwnershipConcentrationResults() (*pb.OwnershipConcentrationResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: OwnershipConcentration", ErrAnalysisMissing)
	}
	var ownershipData pb.OwnershipConcentrationResults
	if err := r.unmarshalContent("OwnershipConcentration", &ownershipData); err != nil {
		return nil, err
	}
	return &ownershipData, nil
}

func (r *ProtobufReader) parseKnowledgeDiffusionResults() (*pb.KnowledgeDiffusionResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: KnowledgeDiffusion", ErrAnalysisMissing)
	}
	var diffusionData pb.KnowledgeDiffusionResults
	if err := r.unmarshalContent("KnowledgeDiffusion", &diffusionData); err != nil {
		return nil, err
	}
	return &diffusionData, nil
}

func (r *ProtobufReader) parseHotspotRiskResults() (*pb.HotspotRiskResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: HotspotRisk", ErrAnalysisMissing)
	}
	var hotspotData pb.HotspotRiskResults
	if err := r.unmarshalContent("HotspotRisk", &hotspotData); err != nil {
		return nil, err
	}
	return &hotspotData, nil
}

func (r *ProtobufReader) parseRefactoringProxyResults() (*pb.RefactoringProxyResults, error) {
	if r.data == nil {
		return nil, fmt.Errorf("%w: RefactoringProxy", ErrAnalysisMissing)
	}
	if r.data.RefactoringProxy != nil {
		return r.data.RefactoringProxy, nil
	}
	if r.data.Contents == nil {
		return nil, fmt.Errorf("%w: RefactoringProxy", ErrAnalysisMissing)
	}
	var proxyData pb.RefactoringProxyResults
	if err := r.unmarshalContent("RefactoringProxy", &proxyData); err != nil {
		return nil, err
	}
	return &proxyData, nil
}

func (r *ProtobufReader) parseCommitsAnalysisResults() (*pb.CommitsAnalysisResults, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: CommitsStat", ErrAnalysisMissing)
	}
	var commitsData pb.CommitsAnalysisResults
	if err := r.unmarshalContent("CommitsStat", &commitsData); err != nil {
		return nil, err
	}
	return &commitsData, nil
}

func (r *ProtobufReader) parseFileHistoryResults() (*pb.FileHistoryResultMessage, error) {
	if r.data == nil || r.data.Contents == nil {
		return nil, fmt.Errorf("%w: FileHistoryAnalysis", ErrAnalysisMissing)
	}
	var historyData pb.FileHistoryResultMessage
	if err := r.unmarshalContent("FileHistoryAnalysis", &historyData); err != nil {
		return nil, err
	}
	return &historyData, nil
}

func (r *ProtobufReader) unmarshalContent(key string, message proto.Message) error {
	contentBytes, exists := r.data.Contents[key]
	if !exists {
		return fmt.Errorf("%w: %s", ErrAnalysisMissing, key)
	}
	if err := proto.Unmarshal(contentBytes, message); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrAnalysisMalformed, key, err)
	}
	return nil
}

func convertTemporalDimension(dimension *pb.TemporalDimension) TemporalDimensionData {
	if dimension == nil {
		return TemporalDimensionData{}
	}
	return TemporalDimensionData{
		Commits: convertInt32Slice(dimension.Commits),
		Lines:   convertInt32Slice(dimension.Lines),
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
	for key, value := range values {
		result[key] = value
	}
	return result
}

func convertLineStats(stats *pb.LineStats) LineStats {
	if stats == nil {
		return LineStats{}
	}
	return LineStats{
		Added:   int(stats.Added),
		Removed: int(stats.Removed),
		Changed: int(stats.Changed),
	}
}
