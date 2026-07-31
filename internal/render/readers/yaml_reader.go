package readers

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/cwbudde/hercules/internal/analysisio"
	"github.com/cwbudde/hercules/internal/render/burndown"
	"github.com/cwbudde/hercules/internal/render/progress"
)

type YamlReader struct {
	data   map[string]any
	Limits analysisio.Limits
	Quiet  bool
}

func (r *YamlReader) Read(file io.Reader) error {
	// Initialize progress tracking for YAML reading
	progEstimator := progress.NewProgressEstimator(!r.Quiet)

	progEstimator.StartOperation("Reading YAML data", 1)

	input, err := analysisio.ReadAll(file, r.Limits)
	if err != nil {
		progEstimator.FinishOperation()
		return fmt.Errorf("read YAML input: %w", err)
	}

	var data map[string]any
	err = yaml.Unmarshal(input, &data)
	if err != nil {
		progEstimator.FinishOperation()
		return fmt.Errorf("%w: decode YAML: %w", ErrAnalysisMalformed, err)
	}

	err = validateYAMLAnalysis(data, r.Limits)
	if err != nil {
		progEstimator.FinishOperation()
		return err
	}

	r.data = data

	progEstimator.UpdateProgress(1)
	progEstimator.FinishOperation()

	return nil
}

func (r *YamlReader) GetName() string {
	herculesData, ok := r.data["hercules"].(map[string]any)
	if !ok {
		return ""
	}

	repository, _ := herculesData["repository"].(string)

	return repository
}

func (r *YamlReader) GetHeader() (int64, int64) {
	herculesData, ok := r.data["hercules"].(map[string]any)
	if !ok {
		return 0, 0
	}

	begin, beginOK := convertToInt(herculesData["begin_unix_time"])

	end, endOK := convertToInt(herculesData["end_unix_time"])
	if !beginOK || !endOK {
		return 0, 0
	}

	return int64(begin), int64(end)
}

func (r *YamlReader) GetProjectBurndown() (string, [][]int) {
	burndownData, ok := r.data["Burndown"].(map[string]any)
	if !ok {
		return "", nil
	}

	repo := r.GetName()

	project, ok := burndownData["project"].(string)
	if !ok {
		return "", nil
	}

	matrix := parseBurndownMatrix(project)

	return repo, transposeMatrix(matrix)
}

func (r *YamlReader) GetFilesBurndown() ([]FileBurndown, error) {
	burndownData, ok := r.data["Burndown"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: Burndown", ErrAnalysisMissing)
	}

	filesData, ok := burndownData["files"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: files burndown", ErrAnalysisMissing)
	}

	var fileBurndowns []FileBurndown

	for filename, matrixData := range filesData {
		matrixText, ok := matrixData.(string)
		if !ok {
			return nil, fmt.Errorf("%w: file burndown %q is not a matrix string", ErrAnalysisMalformed, filename)
		}

		matrix := parseBurndownMatrix(matrixText)
		fileBurndowns = append(fileBurndowns, FileBurndown{
			Filename: filename,
			Matrix:   transposeMatrix(matrix),
		})
	}

	return fileBurndowns, nil
}

func (r *YamlReader) GetPeopleBurndown() ([]PeopleBurndown, error) {
	burndownData, ok := r.data["Burndown"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: Burndown", ErrAnalysisMissing)
	}

	peopleData, ok := burndownData["people"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: people burndown", ErrAnalysisMissing)
	}

	var peopleBurndowns []PeopleBurndown

	for person, matrixData := range peopleData {
		matrixText, ok := matrixData.(string)
		if !ok {
			return nil, fmt.Errorf("%w: people burndown %q is not a matrix string", ErrAnalysisMalformed, person)
		}

		matrix := parseBurndownMatrix(matrixText)
		peopleBurndowns = append(peopleBurndowns, PeopleBurndown{
			Person: person,
			Matrix: transposeMatrix(matrix),
		})
	}

	return peopleBurndowns, nil
}

func (r *YamlReader) GetOwnershipBurndown() ([]string, map[string][][]int, error) {
	burndownData, ok := r.data["Burndown"].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("%w: Burndown", ErrAnalysisMissing)
	}

	peopleSequence, ok := stringSlice(burndownData["people_sequence"])
	if !ok {
		return nil, nil, fmt.Errorf("%w: people sequence", ErrAnalysisMissing)
	}

	peopleData, ok := burndownData["people"].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("%w: people burndown", ErrAnalysisMissing)
	}

	ownership := make(map[string][][]int)

	for person, matrixData := range peopleData {
		matrixText, ok := matrixData.(string)
		if !ok {
			return nil, nil, fmt.Errorf(
				"%w: people burndown %q is not a matrix string",
				ErrAnalysisMalformed, person,
			)
		}

		matrix := parseBurndownMatrix(matrixText)
		ownership[person] = matrix
	}

	return peopleSequence, ownership, nil
}

func (r *YamlReader) GetPeopleInteraction() ([]string, [][]int, error) {
	burndownData, ok := r.data["Burndown"].(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("%w: Burndown", ErrAnalysisMissing)
	}

	peopleSequence, ok := stringSlice(burndownData["people_sequence"])
	if !ok {
		return nil, nil, fmt.Errorf("%w: people sequence", ErrAnalysisMissing)
	}

	interactionData, ok := burndownData["people_interaction"].(string)
	if !ok {
		return nil, nil, fmt.Errorf("%w: people interaction", ErrAnalysisMissing)
	}

	matrix := parseBurndownMatrix(interactionData)

	return peopleSequence, matrix, nil
}

func (r *YamlReader) GetFileCooccurrence() ([]string, SparseMatrix, error) {
	return r.getCouplesCooccurrence(
		"files_coocc", "file_couples_index", "file_couples_matrix", false,
	)
}

func (r *YamlReader) GetPeopleCooccurrence() ([]string, SparseMatrix, error) {
	return r.getCouplesCooccurrence(
		"people_coocc", "people_couples_index", "people_couples_matrix", true,
	)
}

func (r *YamlReader) getCouplesCooccurrence(
	nestedKey, flatIndexKey, flatMatrixKey string, allowUnknown bool,
) ([]string, SparseMatrix, error) {
	couplesData, ok := r.data["Couples"].(map[string]any)
	if !ok {
		return nil, SparseMatrix{}, fmt.Errorf("%w: Couples", ErrAnalysisMissing)
	}

	if nested, exists := couplesData[nestedKey].(map[string]any); exists {
		return parseNestedCooccurrence(nested, r.Limits, allowUnknown)
	}

	index, ok := stringSlice(couplesData[flatIndexKey])
	if !ok {
		return nil, SparseMatrix{}, fmt.Errorf("%w: %s", ErrAnalysisMissing, flatIndexKey)
	}

	matrixData, ok := couplesData[flatMatrixKey].(string)
	if !ok {
		return nil, SparseMatrix{}, fmt.Errorf("%w: %s", ErrAnalysisMissing, flatMatrixKey)
	}

	matrix, err := parseSparseMatrixTextChecked(
		"Couples."+flatMatrixKey, matrixData, r.Limits,
	)
	if err != nil {
		return nil, SparseMatrix{}, err
	}

	index, err = alignCouplingLabels(index, matrix, allowUnknown)
	if err != nil {
		return nil, SparseMatrix{}, err
	}

	return index, matrix, nil
}

func parseNestedCooccurrence(
	data map[string]any, limits analysisio.Limits, allowUnknown bool,
) ([]string, SparseMatrix, error) {
	index, indexOk := stringSlice(data["index"])
	if !indexOk {
		return nil, SparseMatrix{}, fmt.Errorf("%w: coupling index", ErrAnalysisMalformed)
	}

	if matrixStr, ok := data["matrix"].(string); ok {
		matrix, err := parseSparseMatrixTextChecked(
			"coupling matrix", matrixStr, limits,
		)
		if err != nil {
			return nil, SparseMatrix{}, err
		}

		index, err = alignCouplingLabels(index, matrix, allowUnknown)
		if err != nil {
			return nil, SparseMatrix{}, err
		}

		return index, matrix, nil
	}

	if matrixData, ok := data["matrix"].([]any); ok {
		if allowUnknown && len(matrixData) == len(index)+1 {
			index = append(index, "unknown")
		}

		matrix, err := parseSparseCooccurrenceRows(matrixData, len(index), limits)
		if err != nil {
			return nil, SparseMatrix{}, err
		}

		return index, matrix, nil
	}

	return nil, SparseMatrix{}, fmt.Errorf("%w: coupling matrix", ErrAnalysisMalformed)
}

func stringSlice(value any) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...), true
	case []any:
		result := make([]string, len(values))
		for index, value := range values {
			text, ok := value.(string)
			if !ok {
				return nil, false
			}

			result[index] = text
		}

		return result, true
	default:
		return nil, false
	}
}

func alignCouplingLabels(
	labels []string, matrix SparseMatrix, allowUnknown bool,
) ([]string, error) {
	if matrix.Rows == len(labels) && matrix.Columns == len(labels) {
		return labels, nil
	}

	if allowUnknown && matrix.Rows == len(labels)+1 && matrix.Columns == len(labels)+1 {
		return append(labels, "unknown"), nil
	}

	return nil, fmt.Errorf(
		"%w: coupling matrix is %dx%d for %d labels",
		ErrAnalysisMalformed, matrix.Rows, matrix.Columns, len(labels),
	)
}

func (r *YamlReader) GetShotnessCooccurrence() ([]string, SparseMatrix, error) {
	shotnessRecords, err := r.GetShotnessRecords()
	if err != nil {
		return nil, SparseMatrix{}, err
	}

	return shotnessCouplingMatrix(shotnessRecords)
}

// shotnessCouplingMatrix computes a weighted similarity matrix over Shotness's
// entity-indexed co-occurrence profiles. Cell (i, j) is the dot product
//
//	sum_k counters_i[k] * counters_j[k]
//
// over aligned entity dimensions, so the matrix is symmetric. A diagonal cell
// is the entity's squared profile magnitude (sum_k counters_i[k]^2); it is
// useful as a self-similarity baseline, but is excluded from ranked pairs.
func shotnessCouplingMatrix(records []ShotnessRecord) ([]string, SparseMatrix, error) {
	index, byDimension, err := buildShotnessProfiles(records)
	if err != nil {
		return nil, SparseMatrix{}, err
	}

	upper, err := accumulateShotnessProducts(index, byDimension)
	if err != nil {
		return nil, SparseMatrix{}, err
	}

	entries, err := shotnessSparseEntries(index, upper)
	if err != nil {
		return nil, SparseMatrix{}, err
	}

	matrix, err := NewSparseMatrix(len(records), len(records), entries)
	if err != nil {
		return nil, SparseMatrix{}, err
	}

	return index, matrix, nil
}

func buildShotnessProfiles(
	records []ShotnessRecord,
) ([]string, [][]shotnessDimensionValue, error) {
	baseLabelCounts := make(map[string]int, len(records))
	for _, record := range records {
		baseLabelCounts[fmt.Sprintf("%s:%s", record.File, record.Name)]++
	}

	index := make([]string, len(records))
	stableLabels := make(map[string]struct{}, len(records))

	byDimension := make([][]shotnessDimensionValue, len(records))
	for i, record := range records {
		index[i] = shotnessLabel(record, baseLabelCounts)
		if _, exists := stableLabels[index[i]]; exists {
			return nil, nil, fmt.Errorf(
				"%w: duplicate shotness entity identity %q",
				ErrAnalysisMalformed, index[i],
			)
		}

		stableLabels[index[i]] = struct{}{}

		err := appendShotnessCounters(byDimension, i, index[i], record.Counters)
		if err != nil {
			return nil, nil, err
		}
	}

	return index, byDimension, nil
}

func shotnessLabel(record ShotnessRecord, counts map[string]int) string {
	label := fmt.Sprintf("%s:%s", record.File, record.Name)
	if counts[label] > 1 {
		return fmt.Sprintf("%s [%s]", label, record.Type)
	}

	return label
}

func appendShotnessCounters(
	byDimension [][]shotnessDimensionValue,
	entity int,
	label string,
	counters map[int32]int32,
) error {
	for dimension, count := range counters {
		if dimension < 0 || int64(dimension) >= int64(len(byDimension)) {
			return fmt.Errorf(
				"%w: shotness entity %q has out-of-range counter dimension %d for %d entities",
				ErrAnalysisMalformed, label, dimension, len(byDimension),
			)
		}

		if count < 0 {
			return fmt.Errorf(
				"%w: shotness entity %q has negative co-occurrence %d at dimension %d",
				ErrAnalysisMalformed, label, count, dimension,
			)
		}

		if count > 0 {
			byDimension[dimension] = append(
				byDimension[dimension],
				shotnessDimensionValue{entity: entity, value: int64(count)},
			)
		}
	}

	return nil
}

func accumulateShotnessProducts(
	index []string,
	byDimension [][]shotnessDimensionValue,
) ([]map[int]int64, error) {
	upper := make([]map[int]int64, len(index))
	for _, values := range byDimension {
		err := accumulateShotnessDimension(index, upper, values)
		if err != nil {
			return nil, err
		}
	}

	return upper, nil
}

func accumulateShotnessDimension(
	index []string,
	upper []map[int]int64,
	values []shotnessDimensionValue,
) error {
	for leftIndex, left := range values {
		for _, right := range values[leftIndex:] {
			err := addShotnessProduct(index, upper, left, right)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func addShotnessProduct(
	index []string,
	upper []map[int]int64,
	left, right shotnessDimensionValue,
) error {
	row, column := left.entity, right.entity
	if row > column {
		row, column = column, row
	}

	if upper[row] == nil {
		upper[row] = make(map[int]int64)
	}

	product := left.value * right.value
	maxInt := int64(^uint(0) >> 1)

	if product > maxInt-upper[row][column] {
		return fmt.Errorf(
			"%w: shotness coupling score overflows int for %q and %q",
			ErrAnalysisMalformed, index[row], index[column],
		)
	}

	upper[row][column] += product

	return nil
}

func shotnessSparseEntries(index []string, upper []map[int]int64) ([]SparseEntry, error) {
	maxInt := int64(^uint(0) >> 1)
	entries := make([]SparseEntry, 0)
	var pairTotal int64

	for row, values := range upper {
		for _, column := range sortedShotnessColumns(values) {
			score := values[column]
			if score == 0 {
				continue
			}

			entries = append(entries, SparseEntry{
				Row: row, Column: column, Value: int(score),
			})
			if row != column {
				if score > maxInt-pairTotal {
					return nil, fmt.Errorf(
						"%w: total shotness coupling score overflows int while adding %q and %q",
						ErrAnalysisMalformed, index[row], index[column],
					)
				}

				pairTotal += score
				entries = append(entries, SparseEntry{
					Row: column, Column: row, Value: int(score),
				})
			}
		}
	}

	return entries, nil
}

func sortedShotnessColumns(values map[int]int64) []int {
	columns := make([]int, 0, len(values))
	for column := range values {
		columns = append(columns, column)
	}

	sort.Ints(columns)

	return columns
}

type shotnessDimensionValue struct {
	entity int
	value  int64
}

func (r *YamlReader) GetShotnessRecords() ([]ShotnessRecord, error) {
	shotnessData, ok := r.data["Shotness"].([]any)
	if !ok {
		return []ShotnessRecord{}, fmt.Errorf("%w: Shotness", ErrAnalysisMissing)
	}

	var records []ShotnessRecord

	for _, recordInterface := range shotnessData {
		record, ok := recordInterface.(map[string]any)
		if !ok {
			continue // Skip invalid records
		}

		counters := parseShotnessCounters(record["counters"])

		// Safely extract string values
		var recordType, recordName, recordFile string
		if t, ok := record["type"].(string); ok {
			recordType = t
		} else if t, ok := record["internal_role"].(string); ok {
			recordType = t
		}

		if n, ok := record["name"].(string); ok {
			recordName = n
		}

		if f, ok := record["file"].(string); ok {
			recordFile = f
		}

		records = append(records, ShotnessRecord{
			Type:     recordType,
			Name:     recordName,
			File:     recordFile,
			Counters: counters,
		})
	}

	return records, nil
}

func parseShotnessCounters(raw any) map[int32]int32 {
	counters := make(map[int32]int32)
	add := func(rawKey, rawValue any) {
		key, keyOK := shotnessCounterInt32(rawKey)

		value, valueOK := shotnessCounterInt32(rawValue)
		if keyOK && valueOK {
			counters[key] = value
		}
	}

	switch data := raw.(type) {
	case map[any]any:
		for key, value := range data {
			add(key, value)
		}
	case map[string]any:
		for key, value := range data {
			add(key, value)
		}
	}

	return counters
}

func shotnessCounterInt32(value any) (int32, bool) {
	switch typed := value.(type) {
	case int:
		return checkedInt32(int64(typed))
	case int32:
		return typed, true
	case int64:
		return checkedInt32(typed)
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 32)
		if err != nil {
			return 0, false
		}

		return int32(parsed), true
	default:
		return 0, false
	}
}

func checkedInt32(value int64) (int32, bool) {
	const (
		minInt32 = -1 << 31
		maxInt32 = 1<<31 - 1
	)
	if value < minInt32 || value > maxInt32 {
		return 0, false
	}

	return int32(value), true
}

func (r *YamlReader) GetDeveloperStats() ([]DeveloperStat, error) {
	// This method is deprecated in favor of GetDeveloperTimeSeriesData for Python compatibility
	devData, err := r.GetDeveloperTimeSeriesData()
	if err != nil {
		return nil, err
	}

	// Convert time series data to flat stats (aggregated)
	developerMap := make(map[string]*DeveloperStat)

	for _, dayStats := range devData.Days {
		for devIdx, stats := range dayStats {
			if devIdx < len(devData.People) {
				devName := devData.People[devIdx]
				if existing, exists := developerMap[devName]; exists {
					existing.Commits += stats.Commits
					existing.LinesAdded += stats.LinesAdded
					existing.LinesRemoved += stats.LinesRemoved
					existing.LinesModified += stats.LinesModified
				} else {
					developerMap[devName] = &DeveloperStat{
						Name:          devName,
						Commits:       stats.Commits,
						LinesAdded:    stats.LinesAdded,
						LinesRemoved:  stats.LinesRemoved,
						LinesModified: stats.LinesModified,
					}
				}
			}
		}
	}

	var result []DeveloperStat
	for _, stats := range developerMap {
		result = append(result, *stats)
	}

	return result, nil
}

// GetDeveloperTimeSeriesData returns Python-compatible time series data: (people, days)
// where days is {day: {dev: DevDay}}.
func (r *YamlReader) GetDeveloperTimeSeriesData() (*DeveloperTimeSeriesData, error) {
	devsData, ok := r.data["Devs"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w: Devs", ErrAnalysisMissing)
	}

	people, err := yamlDeveloperPeople(devsData)
	if err != nil {
		return nil, err
	}

	ticks, ok := asMap(devsData["ticks"])
	if !ok {
		return nil, fmt.Errorf("%w: Devs ticks", ErrAnalysisMissing)
	}

	return &DeveloperTimeSeriesData{
		People: people,
		Days:   yamlDeveloperDays(ticks),
	}, nil
}

func yamlDeveloperPeople(devsData map[string]any) ([]string, error) {
	rawPeople, ok := devsData["people"].([]any)
	if !ok {
		rawPeople, ok = devsData["dev_index"].([]any)
	}

	if !ok {
		return nil, fmt.Errorf("%w: Devs people index", ErrAnalysisMissing)
	}

	people := make([]string, 0, len(rawPeople))
	for _, person := range rawPeople {
		if name, ok := person.(string); ok {
			people = append(people, name)
		}
	}

	return people, nil
}

func yamlDeveloperDays(ticks map[any]any) map[int]map[int]DevDay {
	days := make(map[int]map[int]DevDay)

	for dayKey, dayData := range ticks {
		dayInt, ok := convertToInt(dayKey)
		if !ok {
			continue
		}

		dayMap, ok := asMap(dayData)
		if !ok {
			continue
		}

		devs := dayMap
		if nestedDevs, ok := lookupMap(dayMap, "devs"); ok {
			devs = nestedDevs
		}

		days[dayInt] = yamlDeveloperDay(devs)
	}

	return days
}

func yamlDeveloperDay(devs map[any]any) map[int]DevDay {
	day := make(map[int]DevDay)

	for devKey, devData := range devs {
		devIndex, ok := convertToInt(devKey)
		if !ok {
			continue
		}

		devDay, ok := parseYamlDevDay(devData)
		if ok {
			day[devIndex] = devDay
		}
	}

	return day
}

func (r *YamlReader) GetLanguageStats() ([]LanguageStat, error) {
	timeSeries, err := r.GetDeveloperTimeSeriesData()
	if err != nil {
		return nil, fmt.Errorf("failed to get developer time series data: %w", err)
	}

	return aggregateLanguageStats(timeSeries)
}

func (r *YamlReader) GetRuntimeStats() (map[string]float64, error) {
	// Stub: Runtime stats are typically not present in YAML files.
	return nil, errors.New("runtime stats not implemented for YAML")
}

// Helper function to parse burndown matrices.
func parseBurndownMatrix(data string) [][]int {
	matrix, err := parseBurndownMatrixChecked(
		"YAML matrix", data, analysisio.DefaultLimits(),
	)
	if err != nil {
		return [][]int{}
	}

	return matrix
}

// parseSparseCooccurrenceRows converts Python's array-of-maps format directly
// to CSR. size is the aligned coupling index length, including empty columns.
func parseSparseCooccurrenceRows(
	data []any, size int, limits analysisio.Limits,
) (SparseMatrix, error) {
	err := validateYAMLSparseRows(
		"YAML co-occurrence matrix", data, limits,
	)
	if err != nil {
		return SparseMatrix{}, err
	}

	if len(data) != size {
		return SparseMatrix{}, fmt.Errorf(
			"%w: coupling matrix has %d rows for %d labels",
			ErrAnalysisMalformed, len(data), size,
		)
	}

	entries := make([]SparseEntry, 0)

	for row, rowData := range data {
		rowMap, _ := asMap(rowData)
		for rawColumn, rawValue := range rowMap {
			column, _ := convertToInt(rawColumn)
			value, _ := convertToInt(rawValue)

			if column >= size {
				return SparseMatrix{}, fmt.Errorf(
					"%w: coupling matrix row %d column %d exceeds %d labels",
					ErrAnalysisMalformed, row, column, size,
				)
			}

			if value != 0 {
				entries = append(entries, SparseEntry{
					Row: row, Column: column, Value: value,
				})
			}
		}
	}

	matrix, err := NewSparseMatrix(size, size, entries)
	if err != nil {
		return SparseMatrix{}, fmt.Errorf(
			"%w: YAML co-occurrence matrix: %w", ErrAnalysisMalformed, err,
		)
	}

	return matrix, nil
}

// convertToInt safely converts various number types to int.
func convertToInt(val any) (int, bool) {
	switch v := val.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		i, err := strconv.Atoi(v)
		if err == nil {
			return i, true
		}
	}

	return 0, false
}

func asMap(value any) (map[any]any, bool) {
	switch typed := value.(type) {
	case map[any]any:
		return typed, true
	case map[string]any:
		result := make(map[any]any, len(typed))
		for key, val := range typed {
			result[key] = val
		}

		return result, true
	default:
		return nil, false
	}
}

func lookupMap(values map[any]any, key string) (map[any]any, bool) {
	for candidateKey, value := range values {
		if candidateKey == key {
			return asMap(value)
		}
	}

	return nil, false
}

func parseYamlDevDay(value any) (DevDay, bool) {
	if values, ok := value.([]any); ok {
		return parseYamlDevDayList(values)
	}

	devMap, ok := asMap(value)
	if !ok {
		return DevDay{}, false
	}

	var day DevDay
	if commits, ok := lookupInt(devMap, "commits"); ok {
		day.Commits = commits
	}

	if added, ok := lookupInt(devMap, "added"); ok {
		day.LinesAdded = added
	}

	if removed, ok := lookupInt(devMap, "removed"); ok {
		day.LinesRemoved = removed
	}

	if changed, ok := lookupInt(devMap, "changed"); ok {
		day.LinesModified = changed
	}

	if languages, ok := lookupLanguages(devMap, "languages"); ok {
		day.Languages = languages
	}

	return day, true
}

func parseYamlDevDayList(values []any) (DevDay, bool) {
	if len(values) < 4 {
		return DevDay{}, false
	}

	var day DevDay
	if commits, ok := convertToInt(values[0]); ok {
		day.Commits = commits
	}

	if added, ok := convertToInt(values[1]); ok {
		day.LinesAdded = added
	}

	if removed, ok := convertToInt(values[2]); ok {
		day.LinesRemoved = removed
	}

	if changed, ok := convertToInt(values[3]); ok {
		day.LinesModified = changed
	}

	if len(values) >= 5 {
		if languages, ok := parseYamlLanguages(values[4]); ok {
			day.Languages = languages
		}
	}

	return day, true
}

func lookupInt(values map[any]any, key string) (int, bool) {
	for candidateKey, value := range values {
		if candidateKey == key {
			return convertToInt(value)
		}
	}

	return 0, false
}

func lookupLanguages(values map[any]any, key string) (map[string][]int, bool) {
	for candidateKey, value := range values {
		if candidateKey == key {
			return parseYamlLanguages(value)
		}
	}

	return nil, false
}

func parseYamlLanguages(value any) (map[string][]int, bool) {
	languageMap, ok := asMap(value)
	if !ok {
		return nil, false
	}

	languages := make(map[string][]int)

	for languageKey, statsValue := range languageMap {
		language, ok := languageKey.(string)
		if !ok {
			continue
		}

		stats, ok := parseYamlIntList(statsValue)
		if !ok || len(stats) < 3 {
			continue
		}

		languages[language] = stats[:3]
	}

	return languages, true
}

func parseYamlIntList(value any) ([]int, bool) {
	rawValues, ok := value.([]any)
	if !ok {
		return nil, false
	}

	values := make([]int, 0, len(rawValues))
	for _, rawValue := range rawValues {
		if intValue, ok := convertToInt(rawValue); ok {
			values = append(values, intValue)
		}
	}

	return values, true
}

// GetBurndownParameters retrieves burndown parameters for YAML reader.
func (r *YamlReader) GetBurndownParameters() (burndown.BurndownParameters, error) {
	burndownData, ok := r.data["Burndown"].(map[string]any)
	if !ok {
		return burndown.BurndownParameters{}, fmt.Errorf("%w: Burndown", ErrAnalysisMissing)
	}

	// Extract parameters from YAML - these ARE present in hercules YAML output
	sampling := 1                // Default
	granularity := 1             // Default
	var tickSize float64 = 86400 // Default (24 hours)

	if val, exists := burndownData["sampling"]; exists {
		if intVal, ok := val.(int); ok {
			sampling = intVal
		}
	}

	if val, exists := burndownData["granularity"]; exists {
		if intVal, ok := val.(int); ok {
			granularity = intVal
		}
	}

	if val, exists := burndownData["tick_size"]; exists {
		if intVal, ok := val.(int); ok {
			tickSize = float64(intVal)
		} else if floatVal, ok := val.(float64); ok {
			tickSize = floatVal
		}
	}

	return burndown.BurndownParameters{
		Sampling:    sampling,
		Granularity: granularity,
		TickSize:    tickSize,
	}, nil
}

// GetProjectBurndownWithHeader retrieves project burndown with header for YAML reader.
func (r *YamlReader) GetProjectBurndownWithHeader() (burndown.BurndownHeader, string, [][]int, error) {
	// Get the basic data
	name, matrix := r.GetProjectBurndown()
	if len(matrix) == 0 {
		return burndown.BurndownHeader{}, "", nil, fmt.Errorf("%w: project burndown", ErrAnalysisMissing)
	}

	// Get header info
	start, last := r.GetHeader()
	params, _ := r.GetBurndownParameters()

	header := burndown.BurndownHeader{
		Start:       start,
		Last:        last,
		Sampling:    params.Sampling,
		Granularity: params.Granularity,
		TickSize:    params.TickSize,
	}

	return header, name, matrix, nil
}
