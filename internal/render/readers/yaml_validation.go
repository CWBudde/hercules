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

//nolint:cyclop,gocognit,noinlineerr // YAML analyses contain several independent optional sections.
func validateYAMLAnalysis(data map[string]interface{}, limits analysisio.Limits) error {
	header, sourceVersion, err := validateYAMLHeader(data)
	if err != nil {
		return err
	}

	total := 0
	if err := validateYAMLRecords(data, &total, limits); err != nil {
		return err
	}

	burndown, exists := data["Burndown"]
	if exists {
		values, ok := burndown.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%w: Burndown must be a mapping", ErrAnalysisMalformed)
		}
		if err := validateYAMLMatrixField(values, "project", "burndown project", limits); err != nil {
			return err
		}
		for _, field := range []string{"files", "people"} {
			raw, present := values[field]
			if !present {
				continue
			}
			matrices, ok := raw.(map[string]interface{})
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
				if _, err := parseBurndownMatrixChecked(
					fmt.Sprintf("Burndown.%s[%q]", field, name), text, limits,
				); err != nil {
					return err
				}
			}
		}
		if err := validateYAMLMatrixField(
			values, "people_interaction", "burndown people interaction", limits,
		); err != nil {
			return err
		}
	}

	couples, exists := data["Couples"]
	if !exists {
		if sourceVersion != pb.SchemaVersion {
			header["version"] = int(pb.SchemaVersion)
		}
		return nil
	}
	values, ok := couples.(map[string]interface{})
	if !ok {
		return fmt.Errorf("%w: Couples must be a mapping", ErrAnalysisMalformed)
	}
	for _, field := range []string{"file_couples_matrix", "people_couples_matrix"} {
		if err := validateYAMLMatrixField(values, field, "Couples."+field, limits); err != nil {
			return err
		}
	}
	for _, field := range []string{"files_coocc", "people_coocc"} {
		raw, present := values[field]
		if !present {
			continue
		}
		nested, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%w: Couples.%s must be a mapping", ErrAnalysisMalformed, field)
		}
		matrix, present := nested["matrix"]
		if !present {
			continue
		}
		switch typed := matrix.(type) {
		case string:
			if _, err := parseBurndownMatrixChecked("Couples."+field+".matrix", typed, limits); err != nil {
				return err
			}
		case []interface{}:
			if err := validateYAMLSparseRows("Couples."+field+".matrix", typed, limits); err != nil {
				return err
			}
		default:
			return fmt.Errorf(
				"%w: Couples.%s.matrix has unsupported type %T",
				ErrAnalysisMalformed, field, matrix,
			)
		}
	}
	if sourceVersion != pb.SchemaVersion {
		header["version"] = int(pb.SchemaVersion)
	}
	return nil
}

func validateYAMLHeader(data map[string]interface{}) (map[string]interface{}, int32, error) {
	rawHeader, exists := data["hercules"]
	if !exists {
		return nil, 0, fmt.Errorf("%w: YAML metadata header is missing", ErrAnalysisMalformed)
	}

	header, ok := rawHeader.(map[string]interface{})
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

func strictYAMLVersion(value interface{}) (int32, bool) {
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
	values map[string]interface{}, field, name string, limits analysisio.Limits,
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

//nolint:noinlineerr // Parsing stops at the first malformed row or cell.
func parseBurndownMatrixChecked(
	name, data string, limits analysisio.Limits,
) ([][]int, error) {
	limits = limits.WithDefaults()
	lineCount := strings.Count(data, "\n") + 1
	if err := analysisio.ValidateDimensions(name, int64(lineCount), 1, limits); err != nil {
		return nil, err
	}
	matrix := make([][]int, 0, min(lineCount, limits.MaxRows))
	columns := -1
	lineIndex := 0
	for line := range strings.Lines(data) {
		fieldCount := countFields(line)
		if fieldCount > limits.MaxColumns {
			return nil, fmt.Errorf(
				"%w: %s row %d has %d columns, limit is %d",
				ErrAnalysisTooLarge, name, lineIndex, fieldCount, limits.MaxColumns,
			)
		}
		numbers := strings.Fields(line)
		if len(numbers) == 0 {
			lineIndex++
			continue
		}
		if columns < 0 {
			columns = len(numbers)
		} else if len(numbers) != columns {
			return nil, fmt.Errorf(
				"%w: %s row %d has %d columns, expected %d",
				ErrAnalysisMalformed, name, lineIndex, len(numbers), columns,
			)
		}
		if err := analysisio.ValidateDimensions(
			name, int64(len(matrix)+1), int64(columns), limits,
		); err != nil {
			return nil, err
		}
		row := make([]int, len(numbers))
		for column, number := range numbers {
			value, err := strconv.Atoi(number)
			if err != nil {
				return nil, fmt.Errorf(
					"%w: %s row %d column %d is not an integer",
					ErrAnalysisMalformed, name, lineIndex, column,
				)
			}
			row[column] = value
		}
		matrix = append(matrix, row)
		lineIndex++
	}
	if columns < 0 {
		columns = 0
	}
	if err := analysisio.ValidateDenseMatrix(name, matrix, limits); err != nil {
		return nil, err
	}
	return matrix, nil
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

func validateYAMLSparseRows(name string, rows []interface{}, limits analysisio.Limits) error {
	maxColumn := -1
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
		}
	}
	return analysisio.ValidateDimensions(name, int64(len(rows)), int64(maxColumn+1), limits)
}

//nolint:cyclop,gocognit,noinlineerr // Recursive container cases share one aggregate counter.
func validateYAMLRecords(value interface{}, total *int, limits analysisio.Limits) error {
	add := func(count int) error {
		if count > int(^uint(0)>>1)-*total {
			return fmt.Errorf("%w: YAML nested record count overflows", ErrAnalysisTooLarge)
		}
		*total += count
		return analysisio.ValidateRecordCount("YAML nested values", *total, limits)
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		if err := add(len(typed)); err != nil {
			return err
		}
		for _, nested := range typed {
			if err := validateYAMLRecords(nested, total, limits); err != nil {
				return err
			}
		}
	case map[interface{}]interface{}:
		if err := add(len(typed)); err != nil {
			return err
		}
		for _, nested := range typed {
			if err := validateYAMLRecords(nested, total, limits); err != nil {
				return err
			}
		}
	case []interface{}:
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
