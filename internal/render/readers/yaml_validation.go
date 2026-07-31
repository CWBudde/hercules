package readers

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/cwbudde/hercules/internal/analysisio"
	"github.com/cwbudde/hercules/internal/pb"
)

func validateYAMLAnalysis(data map[string]any, limits analysisio.Limits) error {
	header, sourceVersion, err := validateYAMLHeader(data)
	if err != nil {
		return err
	}

	total := 0

	err = validateYAMLRecords(data, &total, limits)
	if err != nil {
		return err
	}

	err = validateYAMLBurndown(data, limits)
	if err != nil {
		return err
	}

	err = validateYAMLCouples(data, limits)
	if err != nil {
		return err
	}

	if sourceVersion != pb.SchemaVersion {
		header["version"] = int(pb.SchemaVersion)
	}

	return nil
}

func validateYAMLBurndown(data map[string]any, limits analysisio.Limits) error {
	burndown, exists := data["Burndown"]
	if !exists {
		return nil
	}

	values, ok := burndown.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: Burndown must be a mapping", ErrAnalysisMalformed)
	}

	err := validateYAMLMatrixField(values, "project", "burndown project", limits)
	if err != nil {
		return err
	}

	for _, field := range []string{"files", "people"} {
		err := validateYAMLBurndownCollection(values, field, limits)
		if err != nil {
			return err
		}
	}

	return validateYAMLMatrixField(
		values, "people_interaction", "burndown people interaction", limits,
	)
}

func validateYAMLBurndownCollection(
	values map[string]any,
	field string,
	limits analysisio.Limits,
) error {
	raw, present := values[field]
	if !present {
		return nil
	}

	matrices, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: Burndown.%s must be a mapping", ErrAnalysisMalformed, field)
	}

	for name, matrix := range matrices {
		text, ok := matrix.(string)
		if !ok {
			return fmt.Errorf(
				"%w: Burndown.%s[%q] must be a matrix string",
				ErrAnalysisMalformed, field, name,
			)
		}

		_, err := parseBurndownMatrixChecked(
			fmt.Sprintf("Burndown.%s[%q]", field, name), text, limits,
		)
		if err != nil {
			return err
		}
	}

	return nil
}

func validateYAMLCouples(data map[string]any, limits analysisio.Limits) error {
	couples, exists := data["Couples"]
	if !exists {
		return nil
	}

	values, ok := couples.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: Couples must be a mapping", ErrAnalysisMalformed)
	}

	for _, field := range []string{"file_couples_matrix", "people_couples_matrix"} {
		err := validateYAMLCouplingMatrix(values, field, limits)
		if err != nil {
			return err
		}
	}

	for _, field := range []string{"files_coocc", "people_coocc"} {
		err := validateYAMLNestedCouplingMatrix(values, field, limits)
		if err != nil {
			return err
		}
	}

	return nil
}

func validateYAMLCouplingMatrix(
	values map[string]any,
	field string,
	limits analysisio.Limits,
) error {
	raw, present := values[field]
	if !present {
		return nil
	}

	text, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%w: Couples.%s must be a matrix string", ErrAnalysisMalformed, field)
	}

	_, err := parseSparseMatrixTextChecked("Couples."+field, text, limits)

	return err
}

func validateYAMLNestedCouplingMatrix(
	values map[string]any,
	field string,
	limits analysisio.Limits,
) error {
	raw, present := values[field]
	if !present {
		return nil
	}

	nested, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: Couples.%s must be a mapping", ErrAnalysisMalformed, field)
	}

	matrix, present := nested["matrix"]
	if !present {
		return nil
	}

	name := "Couples." + field + ".matrix"

	switch typed := matrix.(type) {
	case string:
		_, err := parseSparseMatrixTextChecked(name, typed, limits)
		return err
	case []any:
		return validateYAMLSparseRows(name, typed, limits)
	default:
		return fmt.Errorf(
			"%w: Couples.%s.matrix has unsupported type %T",
			ErrAnalysisMalformed, field, matrix,
		)
	}
}

func validateYAMLHeader(data map[string]any) (map[string]any, int32, error) {
	rawHeader, exists := data["hercules"]
	if !exists {
		return nil, 0, fmt.Errorf("%w: YAML metadata header is missing", ErrAnalysisMalformed)
	}

	header, ok := rawHeader.(map[string]any)
	if !ok || header == nil {
		return nil, 0, fmt.Errorf("%w: YAML metadata header must be a mapping", ErrAnalysisMalformed)
	}

	rawVersion, exists := header["version"]
	if !exists {
		return nil, 0, fmt.Errorf("%w: YAML metadata schema version is missing", ErrAnalysisMalformed)
	}

	version, ok := strictYAMLVersion(rawVersion)
	if !ok || version < 0 {
		return nil, 0, fmt.Errorf(
			"%w: YAML metadata schema version %v is malformed",
			ErrAnalysisMalformed, rawVersion,
		)
	}

	switch version {
	case pb.LegacyYAMLSchemaVersion, pb.SchemaVersion:
		return header, version, nil
	default:
		return nil, 0, fmt.Errorf(
			"%w: YAML schema version %d is not supported; expected %d or legacy %d",
			ErrAnalysisVersionUnsupported, version, pb.SchemaVersion, pb.LegacyYAMLSchemaVersion,
		)
	}
}

func strictYAMLVersion(value any) (int32, bool) {
	var number int64

	switch typed := value.(type) {
	case int:
		number = int64(typed)
	case int32:
		number = int64(typed)
	case int64:
		number = typed
	default:
		return 0, false
	}

	if number < math.MinInt32 || number > math.MaxInt32 {
		return 0, false
	}

	return int32(number), true
}

func validateYAMLMatrixField(
	values map[string]any, field, name string, limits analysisio.Limits,
) error {
	raw, exists := values[field]
	if !exists {
		return nil
	}

	text, ok := raw.(string)
	if !ok {
		return fmt.Errorf("%w: %s must be a matrix string", ErrAnalysisMalformed, name)
	}

	_, err := parseBurndownMatrixChecked(name, text, limits)

	return err
}

func parseBurndownMatrixChecked(
	name, data string, limits analysisio.Limits,
) ([][]int, error) {
	limits = limits.WithDefaults()

	lineCount := strings.Count(data, "\n") + 1

	err := analysisio.ValidateDimensions(name, int64(lineCount), 1, limits)
	if err != nil {
		return nil, err
	}

	matrix := make([][]int, 0, min(lineCount, limits.MaxRows))
	columns := -1

	lineIndex := 0
	for line := range strings.Lines(data) {
		numbers, nextColumns, err := checkedMatrixLineFields(name, line, lineIndex, columns, limits)
		if err != nil {
			return nil, err
		}

		columns = nextColumns

		if len(numbers) == 0 {
			lineIndex++
			continue
		}

		err = analysisio.ValidateDimensions(
			name, int64(len(matrix)+1), int64(columns), limits,
		)
		if err != nil {
			return nil, err
		}

		row, err := parseCheckedMatrixRow(name, numbers, lineIndex)
		if err != nil {
			return nil, err
		}

		matrix = append(matrix, row)
		lineIndex++
	}

	err = analysisio.ValidateDenseMatrix(name, matrix, limits)
	if err != nil {
		return nil, err
	}

	return matrix, nil
}

func checkedMatrixLineFields(
	name, line string,
	lineIndex, columns int,
	limits analysisio.Limits,
) ([]string, int, error) {
	fieldCount := countFields(line)
	if fieldCount > limits.MaxColumns {
		return nil, columns, fmt.Errorf(
			"%w: %s row %d has %d columns, limit is %d",
			ErrAnalysisTooLarge, name, lineIndex, fieldCount, limits.MaxColumns,
		)
	}

	numbers := strings.Fields(line)
	if len(numbers) == 0 {
		return nil, columns, nil
	}

	if columns < 0 {
		return numbers, len(numbers), nil
	}

	if len(numbers) != columns {
		return nil, columns, fmt.Errorf(
			"%w: %s row %d has %d columns, expected %d",
			ErrAnalysisMalformed, name, lineIndex, len(numbers), columns,
		)
	}

	return numbers, columns, nil
}

func parseCheckedMatrixRow(name string, numbers []string, rowIndex int) ([]int, error) {
	row := make([]int, len(numbers))
	for column, number := range numbers {
		value, err := strconv.Atoi(number)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: %s row %d column %d is not an integer",
				ErrAnalysisMalformed, name, rowIndex, column,
			)
		}

		row[column] = value
	}

	return row, nil
}

// parseSparseMatrixTextChecked parses the historical whitespace-delimited
// coupling format without allocating a dense rows-by-columns matrix.
func parseSparseMatrixTextChecked(
	name, data string, limits analysisio.Limits,
) (SparseMatrix, error) {
	limits = limits.WithDefaults()

	lineCount := strings.Count(data, "\n") + 1

	err := analysisio.ValidateDimensions(name, int64(lineCount), 1, limits)
	if err != nil {
		return SparseMatrix{}, err
	}

	entries := make([]SparseEntry, 0)
	rows, columns := 0, -1

	lineIndex := 0
	for line := range strings.Lines(data) {
		numbers, nextColumns, err := checkedMatrixLineFields(name, line, lineIndex, columns, limits)
		if err != nil {
			return SparseMatrix{}, err
		}

		columns = nextColumns

		if len(numbers) == 0 {
			lineIndex++
			continue
		}

		err = analysisio.ValidateDimensions(
			name, int64(rows+1), int64(columns), limits,
		)
		if err != nil {
			return SparseMatrix{}, err
		}

		rowEntries, err := parseCheckedSparseRow(name, numbers, lineIndex, rows)
		if err != nil {
			return SparseMatrix{}, err
		}

		entries = append(entries, rowEntries...)
		rows++
		lineIndex++
	}

	if columns < 0 {
		columns = 0
	}

	matrix, err := NewSparseMatrix(rows, columns, entries)
	if err != nil {
		return SparseMatrix{}, fmt.Errorf("%w: %s: %w", ErrAnalysisMalformed, name, err)
	}

	return matrix, nil
}

func parseCheckedSparseRow(
	name string,
	numbers []string,
	lineIndex, row int,
) ([]SparseEntry, error) {
	entries := make([]SparseEntry, 0)

	for column, number := range numbers {
		value, err := strconv.Atoi(number)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: %s row %d column %d is not an integer",
				ErrAnalysisMalformed, name, lineIndex, column,
			)
		}

		if value != 0 {
			entries = append(entries, SparseEntry{Row: row, Column: column, Value: value})
		}
	}

	return entries, nil
}

func countFields(value string) int {
	count := 0
	inField := false

	for _, character := range value {
		if unicode.IsSpace(character) {
			inField = false
			continue
		}

		if !inField {
			count++
			inField = true
		}
	}

	return count
}

func validateYAMLSparseRows(name string, rows []any, limits analysisio.Limits) error {
	maxColumn := -1
	nonzero := 0

	for rowIndex, raw := range rows {
		values, ok := asMap(raw)
		if !ok {
			return fmt.Errorf("%w: %s row %d must be a mapping", ErrAnalysisMalformed, name, rowIndex)
		}

		for rawColumn, rawValue := range values {
			column, ok := convertToInt(rawColumn)
			if !ok || column < 0 {
				return fmt.Errorf(
					"%w: %s row %d has invalid column %v",
					ErrAnalysisMalformed, name, rowIndex, rawColumn,
				)
			}

			if _, ok := convertToInt(rawValue); !ok {
				return fmt.Errorf(
					"%w: %s row %d column %d has a non-integer value",
					ErrAnalysisMalformed, name, rowIndex, column,
				)
			}

			maxColumn = max(maxColumn, column)
			nonzero++
		}
	}

	return analysisio.ValidateSparseDimensions(
		name, int64(len(rows)), int64(maxColumn+1), int64(nonzero), limits,
	)
}

//nolint:cyclop,gocognit,noinlineerr // Recursive container cases share one aggregate counter.
func validateYAMLRecords(value any, total *int, limits analysisio.Limits) error {
	add := func(count int) error {
		if count > int(^uint(0)>>1)-*total {
			return fmt.Errorf("%w: YAML nested record count overflows", ErrAnalysisTooLarge)
		}

		*total += count

		return analysisio.ValidateRecordCount("YAML nested values", *total, limits)
	}

	switch typed := value.(type) {
	case map[string]any:
		if err := add(len(typed)); err != nil {
			return err
		}

		for _, nested := range typed {
			if err := validateYAMLRecords(nested, total, limits); err != nil {
				return err
			}
		}
	case map[any]any:
		if err := add(len(typed)); err != nil {
			return err
		}

		for _, nested := range typed {
			if err := validateYAMLRecords(nested, total, limits); err != nil {
				return err
			}
		}
	case []any:
		if err := add(len(typed)); err != nil {
			return err
		}

		for _, nested := range typed {
			if err := validateYAMLRecords(nested, total, limits); err != nil {
				return err
			}
		}
	case []string:
		return add(len(typed))
	}

	return nil
}
