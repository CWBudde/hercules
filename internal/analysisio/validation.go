// Package analysisio contains shared validation and bounded-input helpers for
// serialized Hercules analysis results.
package analysisio

import (
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"

	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/pb"
)

var (
	ErrAnalysisMalformed          = errors.New("analysis malformed")
	ErrAnalysisTooLarge           = errors.New("analysis too large")
	ErrAnalysisVersionUnsupported = errors.New("analysis version unsupported")
)

// Limits bounds work performed while decoding an analysis. Zero-valued fields
// use DefaultLimits() so embedding Limits in a reader remains backwards compatible.
type Limits struct {
	MaxInputBytes    int64
	MaxDecodedCells  int64
	MaxRows          int
	MaxColumns       int
	MaxNestedRecords int
}

type NamedLength struct {
	Name   string
	Length int
}

func DefaultLimits() Limits {
	return Limits{
		MaxInputBytes:    256 << 20,
		MaxDecodedCells:  64 << 20,
		MaxRows:          1 << 20,
		MaxColumns:       1 << 20,
		MaxNestedRecords: 4 << 20,
	}
}

func (limits Limits) WithDefaults() Limits {
	defaults := DefaultLimits()

	if limits.MaxInputBytes <= 0 {
		limits.MaxInputBytes = defaults.MaxInputBytes
	}

	if limits.MaxDecodedCells <= 0 {
		limits.MaxDecodedCells = defaults.MaxDecodedCells
	}

	if limits.MaxRows <= 0 {
		limits.MaxRows = defaults.MaxRows
	}

	if limits.MaxColumns <= 0 {
		limits.MaxColumns = defaults.MaxColumns
	}

	if limits.MaxNestedRecords <= 0 {
		limits.MaxNestedRecords = defaults.MaxNestedRecords
	}

	return limits
}

// ReadAll reads at most MaxInputBytes bytes and reports a typed size error if
// the source contains more. The extra byte is used only to distinguish an
// exactly-full valid input from a truncated one.
func ReadAll(reader io.Reader, limits Limits) ([]byte, error) {
	limits = limits.WithDefaults()
	if limits.MaxInputBytes == math.MaxInt64 {
		return nil, fmt.Errorf("%w: maximum input byte limit is too large", ErrAnalysisTooLarge)
	}

	data, err := io.ReadAll(io.LimitReader(reader, limits.MaxInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read input: %w", ErrAnalysisMalformed, err)
	}

	if int64(len(data)) > limits.MaxInputBytes {
		return nil, fmt.Errorf(
			"%w: input exceeds %d bytes", ErrAnalysisTooLarge, limits.MaxInputBytes,
		)
	}

	return data, nil
}

func ValidateInputSize(data []byte, limits Limits) error {
	limits = limits.WithDefaults()
	if int64(len(data)) > limits.MaxInputBytes {
		return fmt.Errorf(
			"%w: input contains %d bytes, limit is %d",
			ErrAnalysisTooLarge, len(data), limits.MaxInputBytes,
		)
	}

	return nil
}

func ValidateRecordCount(name string, count int, limits Limits) error {
	limits = limits.WithDefaults()

	if count < 0 {
		return fmt.Errorf("%w: %s has negative record count %d", ErrAnalysisMalformed, name, count)
	}

	if count > limits.MaxNestedRecords {
		return fmt.Errorf(
			"%w: %s has %d records, limit is %d",
			ErrAnalysisTooLarge, name, count, limits.MaxNestedRecords,
		)
	}

	return nil
}

func ValidateParallelLengths(name string, expected int, arrays ...NamedLength) error {
	for _, array := range arrays {
		if array.Length != expected {
			return fmt.Errorf(
				"%w: %s has %d entries but %s has %d",
				ErrAnalysisMalformed, array.Name, array.Length, name, expected,
			)
		}
	}

	return nil
}

func ValidateDimensions(name string, rows, columns int64, limits Limits) error {
	limits = limits.WithDefaults()

	if rows < 0 || columns < 0 {
		return fmt.Errorf(
			"%w: %s has negative dimensions %dx%d",
			ErrAnalysisMalformed, name, rows, columns,
		)
	}

	if rows > int64(limits.MaxRows) {
		return fmt.Errorf(
			"%w: %s has %d rows, limit is %d",
			ErrAnalysisTooLarge, name, rows, limits.MaxRows,
		)
	}

	if columns > int64(limits.MaxColumns) {
		return fmt.Errorf(
			"%w: %s has %d columns, limit is %d",
			ErrAnalysisTooLarge, name, columns, limits.MaxColumns,
		)
	}

	if rows != 0 && columns > math.MaxInt64/rows {
		return fmt.Errorf("%w: %s cell count overflows", ErrAnalysisTooLarge, name)
	}

	if rows*columns > limits.MaxDecodedCells {
		return fmt.Errorf(
			"%w: %s has %d decoded cells, limit is %d",
			ErrAnalysisTooLarge, name, rows*columns, limits.MaxDecodedCells,
		)
	}

	return nil
}

// ValidateSparseDimensions bounds a logical sparse matrix by its stored
// non-zero cells rather than rows*columns. Dense dimensions remain bounded
// independently so malformed declarations cannot create oversized row tables.
func ValidateSparseDimensions(
	name string, rows, columns, nonzero int64, limits Limits,
) error {
	limits = limits.WithDefaults()

	if rows < 0 || columns < 0 || nonzero < 0 {
		return fmt.Errorf(
			"%w: %s has negative sparse dimensions %dx%d or nonzero count %d",
			ErrAnalysisMalformed, name, rows, columns, nonzero,
		)
	}

	if rows > int64(limits.MaxRows) {
		return fmt.Errorf(
			"%w: %s has %d rows, limit is %d",
			ErrAnalysisTooLarge, name, rows, limits.MaxRows,
		)
	}

	if columns > int64(limits.MaxColumns) {
		return fmt.Errorf(
			"%w: %s has %d columns, limit is %d",
			ErrAnalysisTooLarge, name, columns, limits.MaxColumns,
		)
	}

	if nonzero > limits.MaxDecodedCells {
		return fmt.Errorf(
			"%w: %s has %d non-zero cells, limit is %d",
			ErrAnalysisTooLarge, name, nonzero, limits.MaxDecodedCells,
		)
	}

	return nil
}

func ValidateDenseMatrix[T any](name string, matrix [][]T, limits Limits) error {
	columns := 0
	if len(matrix) > 0 {
		columns = len(matrix[0])
	}

	err := ValidateDimensions(name, int64(len(matrix)), int64(columns), limits)
	if err != nil {
		return err
	}

	for row, values := range matrix {
		if len(values) != columns {
			return fmt.Errorf(
				"%w: %s row %d has %d columns, expected %d",
				ErrAnalysisMalformed, name, row, len(values), columns,
			)
		}
	}

	return nil
}

func ValidateBurndownMatrix(name string, matrix *pb.BurndownSparseMatrix, limits Limits) error {
	if matrix == nil {
		return fmt.Errorf("%w: %s is nil", ErrAnalysisMalformed, name)
	}

	rows, columns := int64(matrix.GetNumberOfRows()), int64(matrix.GetNumberOfColumns())

	err := ValidateDimensions(name, rows, columns, limits)
	if err != nil {
		return err
	}

	if int64(len(matrix.GetRows())) != rows {
		return fmt.Errorf(
			"%w: %s has %d row records, expected %d",
			ErrAnalysisMalformed, name, len(matrix.GetRows()), rows,
		)
	}

	for rowIndex, row := range matrix.GetRows() {
		if row == nil {
			return fmt.Errorf("%w: %s row %d is nil", ErrAnalysisMalformed, name, rowIndex)
		}

		if int64(len(row.GetColumns())) > columns {
			return fmt.Errorf(
				"%w: %s row %d has %d columns, maximum is %d",
				ErrAnalysisMalformed, name, rowIndex, len(row.GetColumns()), columns,
			)
		}
	}

	return nil
}

//nolint:cyclop,funlen // Each CSR invariant needs its own precise diagnostic.
func ValidateCSR(name string, matrix *pb.CompressedSparseRowMatrix, limits Limits) error {
	if matrix == nil {
		return fmt.Errorf("%w: %s is nil", ErrAnalysisMalformed, name)
	}

	rows, columns := int64(matrix.GetNumberOfRows()), int64(matrix.GetNumberOfColumns())
	data, indices, pointers := matrix.GetData(), matrix.GetIndices(), matrix.GetIndptr()

	err := ValidateSparseDimensions(name, rows, columns, int64(len(data)), limits)
	if err != nil {
		return err
	}
	if len(data) != len(indices) {
		return fmt.Errorf(
			"%w: %s has %d data values but %d indices",
			ErrAnalysisMalformed, name, len(data), len(indices),
		)
	}

	if int64(len(pointers)) != rows+1 {
		return fmt.Errorf(
			"%w: %s has %d row pointers, expected %d",
			ErrAnalysisMalformed, name, len(pointers), rows+1,
		)
	}

	if len(pointers) == 0 || pointers[0] != 0 {
		return fmt.Errorf("%w: %s first row pointer must be zero", ErrAnalysisMalformed, name)
	}

	terminal := int64(len(data))
	previous := int64(0)

	for index, pointer := range pointers {
		if pointer < previous || pointer < 0 || pointer > terminal {
			return fmt.Errorf(
				"%w: %s row pointer %d is %d after %d (valid range 0..%d)",
				ErrAnalysisMalformed, name, index, pointer, previous, terminal,
			)
		}

		previous = pointer
	}

	if pointers[len(pointers)-1] != terminal {
		return fmt.Errorf(
			"%w: %s terminal row pointer is %d, expected %d",
			ErrAnalysisMalformed, name, pointers[len(pointers)-1], terminal,
		)
	}

	for index, column := range indices {
		if column < 0 || int64(column) >= columns {
			return fmt.Errorf(
				"%w: %s column index %d is %d, valid range is [0,%d)",
				ErrAnalysisMalformed, name, index, column, columns,
			)
		}
	}

	return nil
}

//nolint:cyclop,funlen,gocognit,noinlineerr // The payload validates each matrix and array.
func ValidateBurndownResults(message *pb.BurndownAnalysisResults, limits Limits) error {
	if message == nil {
		return fmt.Errorf("%w: burndown payload is nil", ErrAnalysisMalformed)
	}

	var err error
	if message.GetProject() != nil {
		err = ValidateBurndownMatrix("burndown project", message.GetProject(), limits)
		if err != nil {
			return err
		}
	}

	err = ValidateRecordCount("burndown files", len(message.GetFiles()), limits)
	if err != nil {
		return err
	}

	for index, matrix := range message.GetFiles() {
		err = ValidateBurndownMatrix(fmt.Sprintf("burndown file %d", index), matrix, limits)
		if err != nil {
			return err
		}
	}

	if len(message.GetFiles()) != 0 || len(message.GetFilesOwnership()) != 0 {
		err = ValidateParallelLengths("burndown files", len(message.GetFiles()),
			NamedLength{"burndown file ownership", len(message.GetFilesOwnership())})
		if err != nil {
			return err
		}
	}

	err = ValidateRecordCount("burndown people", len(message.GetPeople()), limits)
	if err != nil {
		return err
	}

	for index, matrix := range message.GetPeople() {
		err = ValidateBurndownMatrix(fmt.Sprintf("burndown person %d", index), matrix, limits)
		if err != nil {
			return err
		}
	}

	if message.GetPeopleInteraction() != nil {
		if err := ValidateCSR("burndown people interaction", message.GetPeopleInteraction(), limits); err != nil {
			return err
		}
	}

	if err := ValidateRecordCount("burndown repositories", len(message.GetRepositories()), limits); err != nil {
		return err
	}

	for index, matrix := range message.GetRepositories() {
		if err := ValidateBurndownMatrix(fmt.Sprintf("burndown repository %d", index), matrix, limits); err != nil {
			return err
		}
	}

	return nil
}

//nolint:cyclop,gocognit,noinlineerr // The payload validates each independent matrix and array.
func ValidateCouplesResults(message *pb.CouplesAnalysisResults, limits Limits) error {
	if message == nil {
		return fmt.Errorf("%w: couples payload is nil", ErrAnalysisMalformed)
	}

	if err := validateCouples("file couples", message.GetFileCouples(), false, limits); err != nil {
		return err
	}

	if err := validateCouples("people couples", message.GetPeopleCouples(), true, limits); err != nil {
		return err
	}

	if message.GetFileCouples() != nil {
		if err := ValidateParallelLengths("file index", len(message.GetFileCouples().GetIndex()),
			NamedLength{"file line counts", len(message.GetFilesLines())}); err != nil {
			return err
		}
	}

	if message.GetPeopleCouples() != nil {
		if err := ValidateParallelLengths("people index", len(message.GetPeopleCouples().GetIndex()),
			NamedLength{"people touched files", len(message.GetPeopleFiles())}); err != nil {
			return err
		}
	}

	fileCount := 0
	if message.GetFileCouples() != nil {
		fileCount = len(message.GetFileCouples().GetIndex())
	}

	for person, files := range message.GetPeopleFiles() {
		if files == nil {
			return fmt.Errorf("%w: people touched files %d is nil", ErrAnalysisMalformed, person)
		}

		for _, file := range files.GetFiles() {
			if file < 0 || int(file) >= fileCount {
				return fmt.Errorf(
					"%w: person %d references file %d outside [0,%d)",
					ErrAnalysisMalformed, person, file, fileCount,
				)
			}
		}
	}

	return nil
}

//nolint:noinlineerr // Sequential guard clauses keep each matrix invariant local.
func validateCouples(name string, couples *pb.Couples, allowUnknown bool, limits Limits) error {
	if couples == nil {
		return nil
	}

	if err := ValidateRecordCount(name+" index", len(couples.GetIndex()), limits); err != nil {
		return err
	}

	if couples.GetMatrix() == nil {
		return fmt.Errorf("%w: %s matrix is nil", ErrAnalysisMalformed, name)
	}

	if err := ValidateCSR(name+" matrix", couples.GetMatrix(), limits); err != nil {
		return err
	}

	indexSize := int64(len(couples.GetIndex()))
	matrixSize := int64(couples.GetMatrix().GetNumberOfRows())

	validSize := matrixSize == indexSize
	if allowUnknown {
		validSize = validSize || matrixSize == indexSize+1
	}

	if !validSize || int64(couples.GetMatrix().GetNumberOfColumns()) != matrixSize {
		return fmt.Errorf(
			"%w: %s matrix is %dx%d for %d index entries",
			ErrAnalysisMalformed, name, couples.GetMatrix().GetNumberOfRows(),
			couples.GetMatrix().GetNumberOfColumns(), indexSize,
		)
	}

	return nil
}

//nolint:noinlineerr // Sequential guard clauses keep each array invariant local.
func ValidateRefactoringResults(message *pb.RefactoringProxyResults, limits Limits) error {
	if message == nil {
		return fmt.Errorf("%w: refactoring payload is nil", ErrAnalysisMalformed)
	}

	count := len(message.GetTicks())
	if err := ValidateRecordCount("refactoring ticks", count, limits); err != nil {
		return err
	}

	return ValidateParallelLengths(
		"refactoring ticks", count,
		NamedLength{"refactoring rename ratios", len(message.GetRenameRatios())},
		NamedLength{"refactoring flags", len(message.GetIsRefactoring())},
		NamedLength{"refactoring total changes", len(message.GetTotalChanges())},
	)
}

// Unmarshal validates the encoded size, unmarshals a protobuf message, and
// applies all validators known for that message type.
//
//nolint:noinlineerr // The validation stages deliberately return as soon as one fails.
func Unmarshal(data []byte, message proto.Message, limits Limits) error {
	if err := ValidateInputSize(data, limits); err != nil {
		return err
	}

	err := proto.Unmarshal(data, message)
	if err != nil {
		return fmt.Errorf("%w: decode protobuf: %w", ErrAnalysisMalformed, err)
	}

	return ValidateMessage(message, limits)
}

// ValidateMessage validates shared matrix invariants, parallel arrays, and
// aggregate nested-record limits for all protobuf leaf payloads.
//
//nolint:cyclop,gocognit,noinlineerr // The type switch delegates checks for every leaf payload.
func ValidateMessage(message proto.Message, limits Limits) error {
	var err error

	switch typed := message.(type) {
	case *pb.BurndownAnalysisResults:
		err = ValidateBurndownResults(typed, limits)
	case *pb.CouplesAnalysisResults:
		err = ValidateCouplesResults(typed, limits)
	case *pb.RefactoringProxyResults:
		err = ValidateRefactoringResults(typed, limits)
	case *pb.ImportsPerDeveloperResults:
		err = ValidateParallelLengths("import authors", len(typed.GetAuthorIndex()),
			NamedLength{"import records", len(typed.GetImports())})
	case *pb.TemporalActivityResults:
		for developer, activity := range typed.GetActivities() {
			if activity == nil {
				return fmt.Errorf(
					"%w: temporal activity for developer %d is nil",
					ErrAnalysisMalformed, developer,
				)
			}

			dimensions := []struct {
				name      string
				dimension *pb.TemporalDimension
			}{
				{"weekdays", activity.GetWeekdays()},
				{"hours", activity.GetHours()},
				{"months", activity.GetMonths()},
				{"weeks", activity.GetWeeks()},
			}
			for _, dimension := range dimensions {
				if dimension.dimension == nil {
					continue
				}

				if err := ValidateParallelLengths(
					"temporal "+dimension.name+" commits",
					len(dimension.dimension.GetCommits()),
					NamedLength{
						"temporal " + dimension.name + " lines",
						len(dimension.dimension.GetLines()),
					},
				); err != nil {
					return err
				}
			}
		}
	}

	if err != nil {
		return err
	}

	return validateNestedRecords(message, limits)
}

// ValidateAnalysisResults validates every built-in dynamic payload before a
// renderer accessor can allocate from dimensions declared inside it.
//
//nolint:cyclop,funlen,gocognit,noinlineerr // Dynamic contents require type-specific decoding.
func ValidateAnalysisResults(results *pb.AnalysisResults, limits Limits) error {
	if results == nil {
		return fmt.Errorf("%w: protobuf envelope is nil", ErrAnalysisMalformed)
	}

	if err := ValidateRecordCount("analysis contents", len(results.GetContents()), limits); err != nil {
		return err
	}

	for name, data := range results.GetContents() {
		var message proto.Message

		switch name {
		case "Burndown":
			message = &pb.BurndownAnalysisResults{}
		case "Couples":
			message = &pb.CouplesAnalysisResults{}
		case "Shotness":
			message = &pb.ShotnessAnalysisResults{}
		case "Devs":
			message = &pb.DevsAnalysisResults{}
		case "Sentiment":
			message = &pb.CommentSentimentResults{}
		case "TemporalActivity":
			message = &pb.TemporalActivityResults{}
		case "BusFactor":
			message = &pb.BusFactorAnalysisResults{}
		case "OwnershipConcentration":
			message = &pb.OwnershipConcentrationResults{}
		case "KnowledgeDiffusion":
			message = &pb.KnowledgeDiffusionResults{}
		case "HotspotRisk":
			message = &pb.HotspotRiskResults{}
		case "RefactoringProxy":
			message = &pb.RefactoringProxyResults{}
		case "CommitsStat":
			message = &pb.CommitsAnalysisResults{}
		case "FileHistoryAnalysis":
			message = &pb.FileHistoryResultMessage{}
		default:
			continue
		}

		if err := Unmarshal(data, message, limits); err != nil {
			// Dynamic payloads have historically been decoded lazily. Preserve
			// that behavior for invalid wire bytes; the accessor will return the
			// typed error. Structurally valid hostile payloads are still rejected
			// here before a no-error accessor can allocate from them.
			if errors.Is(err, ErrAnalysisMalformed) {
				var syntaxCheck proto.Message

				switch name {
				case "Burndown":
					syntaxCheck = &pb.BurndownAnalysisResults{}
				case "Couples":
					syntaxCheck = &pb.CouplesAnalysisResults{}
				default:
					continue
				}

				if syntaxErr := proto.Unmarshal(data, syntaxCheck); syntaxErr != nil {
					continue
				}
			}

			return fmt.Errorf("%s: %w", name, err)
		}
	}

	if results.GetRefactoringProxy() != nil {
		if err := ValidateRefactoringResults(results.GetRefactoringProxy(), limits); err != nil {
			return err
		}
	}

	return nil
}

func validateNestedRecords(message proto.Message, limits Limits) error {
	total := 0
	seen := map[uintptr]struct{}{}

	return validateNestedValue(reflect.ValueOf(message), &total, seen, limits)
}

//nolint:cyclop,funlen,gocognit,noinlineerr // Reflection kinds require distinct traversal rules.
func validateNestedValue(
	value reflect.Value, total *int, seen map[uintptr]struct{}, limits Limits,
) error {
	if !value.IsValid() {
		return nil
	}

	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return nil
		}

		return validateNestedValue(value.Elem(), total, seen, limits)
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}

		pointer := value.Pointer()
		if _, exists := seen[pointer]; exists {
			return nil
		}

		seen[pointer] = struct{}{}

		return validateNestedValue(value.Elem(), total, seen, limits)
	case reflect.Struct:
		for field := range value.Fields() {
			fieldValue := value.FieldByIndex(field.Index)
			if err := validateNestedValue(fieldValue, total, seen, limits); err != nil {
				return err
			}
		}
	case reflect.Map:
		if value.IsNil() {
			return nil
		}

		if err := addNestedRecords(value.Len(), total, limits); err != nil {
			return err
		}

		iterator := value.MapRange()

		for iterator.Next() {
			if err := validateNestedValue(iterator.Value(), total, seen, limits); err != nil {
				return err
			}
		}
	case reflect.Array:
		for index := range value.Len() {
			if err := validateNestedValue(value.Index(index), total, seen, limits); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if value.IsNil() || value.Type().Elem().Kind() == reflect.Uint8 {
			return nil
		}

		if err := addNestedRecords(value.Len(), total, limits); err != nil {
			return err
		}

		elementKind := value.Type().Elem().Kind()
		if elementKind != reflect.Pointer &&
			elementKind != reflect.Interface &&
			elementKind != reflect.Map &&
			elementKind != reflect.Slice &&
			elementKind != reflect.Struct {
			return nil
		}

		for index := range value.Len() {
			if err := validateNestedValue(value.Index(index), total, seen, limits); err != nil {
				return err
			}
		}
	}

	return nil
}

func addNestedRecords(count int, total *int, limits Limits) error {
	if count > math.MaxInt-*total {
		return fmt.Errorf("%w: nested record count overflows", ErrAnalysisTooLarge)
	}

	*total += count

	return ValidateRecordCount("protobuf nested values", *total, limits)
}
