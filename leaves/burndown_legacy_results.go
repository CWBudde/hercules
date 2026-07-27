package leaves

import (
	"fmt"
	"io"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/burndown"
	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/linehistory"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
)

func (analyser *LegacyBurndownAnalysis) Finalize() any {
	globalHistory, lastTick := analyser.groupSparseHistory(analyser.globalHistory, -1)
	fileHistories, fileOwnership := analyser.finalizeFiles(lastTick)
	peopleHistories := analyser.finalizePeopleHistories(globalHistory, lastTick)

	result := BurndownResult{
		GlobalHistory: globalHistory, FileHistories: fileHistories, FileOwnership: fileOwnership,
		PeopleHistories: peopleHistories, PeopleMatrix: analyser.finalizePeopleMatrix(),
		tickSize: analyser.tickSize, reversedPeopleDict: analyser.reversedPeopleDict,
		sampling: analyser.Sampling, granularity: analyser.Granularity,
	}

	err := validateBurndownResultBalances(&result, "legacy finalization")
	if err != nil {
		return err
	}

	return result
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (analyser *LegacyBurndownAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	burndownResult, ok := result.(BurndownResult)
	if !ok {
		return fmt.Errorf("%w: '%v'", errUnexpectedBurndownResult, result)
	}

	err := validateBurndownResultBalances(&burndownResult, "legacy serialization")
	if err != nil {
		return err
	}

	if binary {
		return analyser.serializeBinary(&burndownResult, writer)
	}

	analyser.serializeText(&burndownResult, writer)

	return nil
}

// Deserialize converts the specified protobuf bytes to BurndownResult.
func (analyser *LegacyBurndownAnalysis) Deserialize(pbmessage []byte) (any, error) {
	msg := pb.BurndownAnalysisResults{}

	err := unmarshalAnalysis(pbmessage, &msg)
	if err != nil {
		return nil, fmt.Errorf("unmarshal burndown result: %w", err)
	}

	convertCSR := func(mat *pb.BurndownSparseMatrix) DenseHistory {
		res := make(DenseHistory, mat.GetNumberOfRows())
		for i := range mat.GetNumberOfRows() {
			res[i] = make([]int64, mat.GetNumberOfColumns())
			for j := range len(mat.GetRows()[i].GetColumns()) {
				res[i][j] = int64(mat.GetRows()[i].GetColumns()[j])
			}
		}

		return res
	}

	result := BurndownResult{
		GlobalHistory: convertCSR(msg.GetProject()),
		FileHistories: map[string]DenseHistory{},
		FileOwnership: map[string]map[int]int{},
		tickSize:      time.Duration(msg.GetTickSize()),

		granularity: int(msg.GetGranularity()),
		sampling:    int(msg.GetSampling()),
	}
	for i, mat := range msg.GetFiles() {
		result.FileHistories[mat.GetName()] = convertCSR(mat)
		ownership := map[int]int{}

		result.FileOwnership[mat.GetName()] = ownership
		for key, val := range msg.GetFilesOwnership()[i].GetValue() {
			ownership[int(key)] = int(val)
		}
	}

	result.reversedPeopleDict = make([]string, len(msg.GetPeople()))

	result.PeopleHistories = make([]DenseHistory, len(msg.GetPeople()))
	for i, mat := range msg.GetPeople() {
		result.PeopleHistories[i] = convertCSR(mat)
		result.reversedPeopleDict[i] = mat.GetName()
	}

	if msg.GetPeopleInteraction() != nil {
		result.PeopleMatrix = make(DenseHistory, msg.GetPeopleInteraction().GetNumberOfRows())
	}

	for i := 0; i < len(result.PeopleMatrix); i++ {
		result.PeopleMatrix[i] = make([]int64, msg.GetPeopleInteraction().GetNumberOfColumns())
		for j := int(msg.GetPeopleInteraction().GetIndptr()[i]); j < int(msg.GetPeopleInteraction().GetIndptr()[i+1]); j++ {
			result.PeopleMatrix[i][msg.GetPeopleInteraction().GetIndices()[j]] = msg.GetPeopleInteraction().GetData()[j]
		}
	}

	return result, nil
}

// MergeResults combines two BurndownResult-s together.
func (analyser *LegacyBurndownAnalysis) MergeResults(
	r1, r2 any, c1, c2 *core.CommonAnalysisResult,
) any {
	return (&BurndownAnalysis{}).MergeResults(r1, r2, c1, c2)
}

func (analyser *LegacyBurndownAnalysis) finalizeFiles(
	lastTick int,
) (map[string]DenseHistory, map[string]map[int]int) {
	histories := map[string]DenseHistory{}
	ownerships := map[string]map[int]int{}

	for key, history := range analyser.fileHistories {
		if len(history) == 0 {
			continue
		}

		file := analyser.files[key]
		if file == nil {
			continue
		}

		histories[key], _ = analyser.groupSparseHistory(history, lastTick)
		previousLine := 0
		previousAuthor := core.AuthorMissing
		ownership := map[int]int{}
		ownerships[key] = ownership

		file.ForEach(func(line, value int) {
			length := line - previousLine
			if length > 0 {
				ownership[previousAuthor] += length
			}

			previousLine = line

			previousAuthor, _ = analyser.unpackPersonWithTick(value)
			if previousAuthor == core.AuthorMissing {
				previousAuthor = -1
			}
		})
	}

	return histories, ownerships
}

func (analyser *LegacyBurndownAnalysis) finalizePeopleHistories(
	global DenseHistory, lastTick int,
) []DenseHistory {
	histories := make([]DenseHistory, analyser.PeopleNumber)
	for personIndex := range histories {
		history := analyser.peopleHistories[personIndex]
		if len(history) > 0 {
			histories[personIndex], _ = analyser.groupSparseHistory(history, lastTick)
		} else {
			histories[personIndex] = make(DenseHistory, len(global))
			for rowIndex, row := range global {
				histories[personIndex][rowIndex] = make([]int64, len(row))
			}
		}
	}

	return histories
}

func (analyser *LegacyBurndownAnalysis) finalizePeopleMatrix() DenseHistory {
	if len(analyser.matrix) == 0 {
		return nil
	}

	matrix := make(DenseHistory, analyser.PeopleNumber)
	for personIndex := range matrix {
		matrix[personIndex] = make([]int64, analyser.PeopleNumber+2)
		for key, value := range analyser.matrix[personIndex] {
			switch key {
			case core.AuthorMissing:
				key = -1
			case authorSelf:
				key = -2
			}

			matrix[personIndex][key+2] = value
		}
	}

	return matrix
}

// mergeMatrices takes two [number of samples][number of bands] matrices,
// resamples them to ticks so that they become square, sums and resamples back to the
// least of (sampling1, sampling2) and (granularity1, granularity2).
func (analyser *LegacyBurndownAnalysis) mergeMatrices(
	matrix1, matrix2 DenseHistory, granularity1, sampling1, granularity2, sampling2 int, tickSize time.Duration,
	common1, common2 *core.CommonAnalysisResult,
) DenseHistory {
	return burndown.MergeBurndownMatrices(
		matrix1, matrix2,
		granularity1, sampling1, granularity2, sampling2,
		tickSize, common1, common2,
	)
}

// Explode `matrix` so that it is daily sampled and has daily bands, shift by `offset` ticks
// and add to the accumulator. `daily` size is square and is guaranteed to fit `matrix` by
// the caller.
// Rows: *at least* len(matrix) * sampling + offset
// Columns: *at least* len(matrix[...]) * granularity + offset
// `matrix` can be sparse, so that the last columns which are equal to 0 are truncated.
func addBurndownMatrix(matrix DenseHistory, granularity, sampling int, accPerTick [][]float32, offset int) {
	burndown.AddBurndownMatrix(matrix, granularity, sampling, accPerTick, offset)
}

func (analyser *LegacyBurndownAnalysis) serializeText(result *BurndownResult, writer io.Writer) {
	(&BurndownAnalysis{}).serializeText(result, writer)
}

func (analyser *LegacyBurndownAnalysis) serializeBinary(result *BurndownResult, writer io.Writer) error {
	granularity, err := intToProtoInt32(result.granularity, "legacy burndown granularity")
	if err != nil {
		return err
	}

	sampling, err := intToProtoInt32(result.sampling, "legacy burndown sampling")
	if err != nil {
		return err
	}

	message := pb.BurndownAnalysisResults{
		Granularity: granularity,
		Sampling:    sampling,
		TickSize:    int64(result.tickSize),
	}
	if len(result.GlobalHistory) > 0 {
		message.Project = pb.ToBurndownSparseMatrix(result.GlobalHistory, "project")
	}

	if len(result.FileHistories) > 0 {
		message.Files, message.FilesOwnership, err = legacyFilesToProto(result)
		if err != nil {
			return err
		}
	}

	if len(result.PeopleHistories) > 0 {
		message.People = legacyPeopleToProto(result)
	}

	if result.PeopleMatrix != nil {
		message.PeopleInteraction = pb.DenseToCompressedSparseRowMatrix(result.PeopleMatrix)
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal burndown result: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write burndown result: %w", err)
	}

	return nil
}

func legacyFilesToProto(
	result *BurndownResult,
) ([]*pb.BurndownSparseMatrix, []*pb.FilesOwnership, error) {
	files := make([]*pb.BurndownSparseMatrix, len(result.FileHistories))
	filesOwnership := make([]*pb.FilesOwnership, len(result.FileHistories))

	for fileIndex, fileName := range sortedKeys(result.FileHistories) {
		files[fileIndex] = pb.ToBurndownSparseMatrix(result.FileHistories[fileName], fileName)

		ownership, err := legacyOwnershipToProto(result.FileOwnership[fileName])
		if err != nil {
			return nil, nil, err
		}

		filesOwnership[fileIndex] = &pb.FilesOwnership{Value: ownership}
	}

	return files, filesOwnership, nil
}

func legacyOwnershipToProto(ownership map[int]int) (map[int32]int32, error) {
	converted := make(map[int32]int32, len(ownership))

	for developer, lines := range ownership {
		developerID, err := intToProtoInt32(developer, "legacy burndown file owner")
		if err != nil {
			return nil, err
		}

		lineCount, err := intToProtoInt32(lines, "legacy burndown file ownership line count")
		if err != nil {
			return nil, err
		}

		converted[developerID] = lineCount
	}

	return converted, nil
}

func legacyPeopleToProto(result *BurndownResult) []*pb.BurndownSparseMatrix {
	people := make([]*pb.BurndownSparseMatrix, len(result.PeopleHistories))

	for developer, history := range result.PeopleHistories {
		if len(history) > 0 {
			people[developer] = pb.ToBurndownSparseMatrix(
				history, result.reversedPeopleDict[developer],
			)
		}
	}

	return people
}

// We do a hack and store the tick in the first 14 bits and the author index in the last 18.
// Strictly speaking, int can be 64-bit and then the author index occupies 32+18 bits.
// This hack is needed to simplify the values storage inside File-s. We can compare
// different values together and they are compared as ticks for the same author.
func (analyser *LegacyBurndownAnalysis) packPersonWithTick(person, tick int) int {
	if analyser.PeopleNumber == 0 {
		return tick
	}

	result := tick & linehistory.TreeMergeMark

	result |= person << linehistory.TreeMaxBinPower

	// This effectively means max (16383 - 1) ticks (>44 years) and (262143 - 3) devs.
	// One tick less because burndown.TreeMergeMark = ((1 << 14) - 1) is a special tick.
	// Three devs less because:
	// - math.MaxUint32 is the special rbtree value with tick == TreeMergeMark (-1)
	// - core.AuthorMissing (-2)
	// - authorSelf (-3)
	return result
}

func (analyser *LegacyBurndownAnalysis) packChangePersonWithTick(person, tick int) int {
	if tick == linehistory.TreeMergeMark {
		return linehistory.TreeMergeMark
	}

	return analyser.packPersonWithTick(person, tick)
}

func (analyser *LegacyBurndownAnalysis) validatePackedOwnership(person, tick int) error {
	if tick < 0 || tick >= linehistory.TreeMergeMark {
		return fmt.Errorf(
			"%w: got %d, allowed range is [0, %d]",
			ErrLegacyBurndownTickCapacity, tick, linehistory.TreeMergeMark-1,
		)
	}

	if analyser.PeopleNumber > 0 &&
		(person < 0 || person >= analyser.PeopleNumber) &&
		person != core.AuthorMissing {
		return fmt.Errorf(
			"%w: got %d, configured people count is %d",
			ErrLegacyBurndownAuthorCapacity, person, analyser.PeopleNumber,
		)
	}

	return nil
}

func (analyser *LegacyBurndownAnalysis) validateConfiguredPackedCapacity(
	facts map[string]any,
) error {
	if analyser.PeopleNumber > int(core.AuthorMissing) {
		return fmt.Errorf(
			"%w: got %d people, maximum is %d",
			ErrLegacyBurndownAuthorCapacity, analyser.PeopleNumber, core.AuthorMissing,
		)
	}

	commits, commitsOK := facts[core.ConfigPipelineCommits].([]*object.Commit)
	if !commitsOK || len(commits) == 0 || commits[0] == nil || analyser.tickSize <= 0 {
		return nil
	}

	tick0 := items.FloorTime(commits[0].Committer.When, analyser.tickSize)
	previousTick := 0

	for _, commit := range commits {
		if commit == nil {
			continue
		}

		tick := max(int(commit.Committer.When.Sub(tick0)/analyser.tickSize), previousTick)
		if tick >= linehistory.TreeMergeMark {
			return fmt.Errorf(
				"%w: commit %s requires tick %d, maximum is %d",
				ErrLegacyBurndownTickCapacity,
				commit.Hash.String(),
				tick,
				linehistory.TreeMergeMark-1,
			)
		}

		previousTick = tick
	}

	return nil
}

func (analyser *LegacyBurndownAnalysis) unpackPersonWithTick(value int) (int, int) {
	if analyser.PeopleNumber == 0 {
		return core.AuthorMissing, value
	}

	return value >> linehistory.TreeMaxBinPower, value & linehistory.TreeMergeMark
}
