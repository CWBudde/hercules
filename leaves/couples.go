package leaves

import (
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/utils/merkletrie"
	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/join"
	"github.com/cwbudde/hercules/internal/pb"
	items "github.com/cwbudde/hercules/internal/plumbing"
	"github.com/cwbudde/hercules/internal/plumbing/identity"
	"github.com/cwbudde/hercules/internal/yaml"
)

var (
	errCouplesMergeIntegrity = errors.New("couples merge integrity violation")
	errCouplesPBIntegrity    = errors.New("couples PB message integrity violation")
)

// CouplesAnalysis calculates the number of common commits for files and authors.
// The results are matrices, where cell at row X and column Y is the number of commits which
// changed X and Y together. In case with people, the numbers are summed for every common file.
type CouplesAnalysis struct {
	core.NoopMerger
	core.OneShotMergeProcessor

	// PeopleNumber is the number of developers for which to build the matrix. 0 disables this analysis.
	PeopleNumber int

	// people store how many times every developer committed to every file.
	people []map[string]int
	// peopleCommits is the number of commits each author made.
	peopleCommits []int
	// files store every file occurred in the same commit with every other file.
	files map[string]map[string]int
	// renames point from new file name to old file name.
	renames *[]rename
	// lastCommit is the last commit which was consumed.
	lastCommit *object.Commit
	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict
	reversedPeopleDict []string

	l core.Logger
}

// CouplesResult is returned by CouplesAnalysis.Finalize() and carries couples matrices from
// authors and files.
type CouplesResult struct {
	// PeopleMatrix is how many times developers changed files which were also changed by other developers.
	// The mapping's key is the other developer, and the value is the sum over all the files both developers changed.
	// Each element of that sum is min(C1, C2) where Ci is the number of commits developer i made which touched the file.
	PeopleMatrix []map[int]int64
	// PeopleFiles is how many times developers changed files. The first dimension (left []) is developers,
	// and the second dimension (right []) is file indexes.
	PeopleFiles [][]int
	// FilesMatrix is how many times file pairs occurred in the same commit.
	FilesMatrix []map[int]int64
	// FilesLines is the number of lines contained in each file from the last analyzed commit.
	FilesLines []int
	// Files is the names of the files. The order matches PeopleFiles' indexes and FilesMatrix.
	Files []string

	// reversedPeopleDict references IdentityDetector.ReversedPeopleDict
	reversedPeopleDict []string
}

const (
	// CouplesMaximumMeaningfulContextSize is the threshold on the number of files in a commit to
	// consider them as grouped together.
	CouplesMaximumMeaningfulContextSize = 1000
)

type rename struct {
	FromName string
	ToName   string
}

// Name of this PipelineItem. Uniquely identifies the type, used for mapping keys, etc.
func (couples *CouplesAnalysis) Name() string {
	return "Couples"
}

// Provides returns the list of names of entities which are produced by this PipelineItem.
// Each produced entity will be inserted into `deps` of dependent Consume()-s according
// to this list. Also used by core.Registry to build the global map of providers.
func (couples *CouplesAnalysis) Provides() []string {
	return []string{}
}

// Requires returns the list of names of entities which are needed by this PipelineItem.
// Each requested entity will be inserted into `deps` of Consume(). In turn, those
// entities are Provides() upstream.
func (couples *CouplesAnalysis) Requires() []string {
	return []string{identity.DependencyAuthor, items.DependencyTreeChanges}
}

// ListConfigurationOptions returns the list of changeable public properties of this PipelineItem.
func (couples *CouplesAnalysis) ListConfigurationOptions() []core.ConfigurationOption {
	return []core.ConfigurationOption{}
}

// Configure sets the properties previously published by ListConfigurationOptions().
func (couples *CouplesAnalysis) Configure(facts map[string]any) error {
	if l, exists := facts[core.ConfigLogger].(core.Logger); exists {
		couples.l = l
	}

	if val, exists := facts[identity.FactIdentityDetectorReversedPeopleDict].([]string); exists {
		couples.PeopleNumber = len(val)
		couples.reversedPeopleDict = val
	}

	return nil
}

func (*CouplesAnalysis) ConfigureUpstream(facts map[string]any) error {
	return nil
}

// Flag for the command line switch which enables this analysis.
func (couples *CouplesAnalysis) Flag() string {
	return "couples"
}

// Description returns the text which explains what the analysis is doing.
func (couples *CouplesAnalysis) Description() string {
	return "The result is a square matrix, the value in each cell corresponds to the number " +
		"of times the pair of files appeared in the same commit or pair of developers " +
		"committed to the same file. This analysis does not require TensorFlow; TensorFlow " +
		"is only needed by labours for optional embedding visualizations."
}

// Initialize resets the temporary caches and prepares this PipelineItem for a series of Consume()
// calls. The repository which is going to be analysed is supplied as an argument.
func (couples *CouplesAnalysis) Initialize(repository *git.Repository) error {
	couples.l = core.NewLogger()
	couples.lastCommit = nil

	couples.people = make([]map[string]int, couples.PeopleNumber+1)
	for i := range couples.people {
		couples.people[i] = map[string]int{}
	}

	couples.peopleCommits = make([]int, couples.PeopleNumber+1)
	couples.files = map[string]map[string]int{}
	couples.renames = &[]rename{}
	couples.OneShotMergeProcessor.Initialize()

	return nil
}

// Consume runs this PipelineItem on the next commit data.
// `deps` contain all the results from upstream PipelineItem-s as requested by Requires().
// Additionally, DependencyCommit is always present there and represents the analysed *object.Commit.
// This function returns the mapping with analysis results. The keys must be the same as
// in Provides(). If there was an error, nil is returned.
func (couples *CouplesAnalysis) Consume(deps map[string]any) (map[string]any, error) {
	firstMerge := couples.ShouldConsumeCommit(deps)

	values, err := getCouplesDependencies(deps)
	if err != nil {
		return nil, err
	}

	couples.lastCommit = values.commit

	author := values.author
	if author == core.AuthorMissing {
		author = couples.PeopleNumber
	}

	if firstMerge {
		couples.peopleCommits[author]++
	}

	context := make([]string, 0, len(values.treeDiff))
	for _, change := range values.treeDiff {
		contextFile, includeInContext, err := consumeCouplesFileChange(couples, change, author)
		if err != nil {
			return nil, err
		}

		if includeInContext {
			context = append(context, contextFile)
		}
	}

	if len(context) <= CouplesMaximumMeaningfulContextSize {
		updateFileCouplings(couples, context)
	}

	return noDependencies(), nil
}

type couplesDependencies struct {
	commit   *object.Commit
	author   int
	treeDiff object.Changes
}

func getCouplesDependencies(deps map[string]any) (couplesDependencies, error) {
	reader := factReader{facts: deps}
	values := couplesDependencies{
		commit:   readFact[*object.Commit](&reader, core.DependencyCommit),
		author:   readFact[int](&reader, identity.DependencyAuthor),
		treeDiff: readFact[object.Changes](&reader, items.DependencyTreeChanges),
	}

	return values, reader.err
}

func consumeCouplesFileChange(
	couples *CouplesAnalysis,
	change *object.Change,
	author int,
) (string, bool, error) {
	action, err := change.Action()
	if err != nil {
		return "", false, fmt.Errorf("determine action for tree change: %w", err)
	}

	targetName := change.To.Name
	sourceName := change.From.Name

	switch action {
	case merkletrie.Insert:
		couples.people[author][targetName]++

		return targetName, true, nil
	case merkletrie.Delete:
		couples.people[author][sourceName]++

		return "", false, nil
	case merkletrie.Modify:
		if sourceName != targetName {
			*couples.renames = append(
				*couples.renames, rename{ToName: targetName, FromName: sourceName},
			)
		}

		couples.people[author][targetName]++

		return targetName, true, nil
	default:
		return "", false, nil
	}
}

func updateFileCouplings(couples *CouplesAnalysis, context []string) {
	for _, file := range context {
		lane := couples.files[file]
		if lane == nil {
			lane = map[string]int{}
			couples.files[file] = lane
		}

		for _, otherFile := range context {
			lane[otherFile]++
		}
	}
}

// Finalize returns the result of the analysis. Further Consume() calls are not expected.

func (couples *CouplesAnalysis) Finalize() any {
	currentFiles, err := couples.currentFilesWithError()
	if err != nil {
		return err
	}

	files, people := couples.propagateRenames(currentFiles)
	filesSequence := make([]string, len(files))

	i := 0
	for file := range files {
		filesSequence[i] = file
		i++
	}

	sort.Strings(filesSequence)

	filesIndex := map[string]int{}
	for i, file := range filesSequence {
		filesIndex[file] = i
	}

	filesLines, err := couples.fileLineCounts(filesSequence)
	if err != nil {
		return err
	}

	peopleMatrix, peopleFiles := couples.peopleCouplingMatrices(people, filesIndex)
	filesMatrix := fileCouplingMatrix(files, filesSequence, filesIndex)

	return CouplesResult{
		PeopleMatrix: peopleMatrix, PeopleFiles: peopleFiles,
		Files: filesSequence, FilesLines: filesLines, FilesMatrix: filesMatrix,
		reversedPeopleDict: couples.reversedPeopleDict,
	}
}

func fileCouplingMatrix(
	files map[string]map[string]int, filesSequence []string, filesIndex map[string]int,
) []map[int]int64 {
	matrix := make([]map[int]int64, len(filesIndex))
	for i := range matrix {
		matrix[i] = map[int]int64{}
		for otherFile, cooccurrences := range files[filesSequence[i]] {
			matrix[i][filesIndex[otherFile]] = int64(cooccurrences)
		}
	}

	return matrix
}

// Fork clones this pipeline item.
func (couples *CouplesAnalysis) Fork(n int) []core.PipelineItem {
	return core.ForkCopyPipelineItem(couples, n)
}

// Serialize converts the analysis result as returned by Finalize() to text or bytes.
// The text format is YAML and the bytes format is Protocol Buffers.
func (couples *CouplesAnalysis) Serialize(result any, binary bool, writer io.Writer) error {
	couplesResult, err := requiredResult[CouplesResult](result)
	if err != nil {
		return fmt.Errorf("serialize couples: %w", err)
	}

	if binary {
		return couples.serializeBinary(&couplesResult, writer)
	}

	couples.serializeText(&couplesResult, writer)

	return nil
}

// Deserialize converts the specified protobuf bytes to CouplesResult.
func (couples *CouplesAnalysis) Deserialize(pbmessage []byte) (any, error) {
	message := pb.CouplesAnalysisResults{}

	err := unmarshalAnalysis(pbmessage, &message)
	if err != nil {
		return nil, fmt.Errorf("unmarshal couples protobuf: %w", err)
	}

	result := CouplesResult{
		Files:              message.GetFileCouples().GetIndex(),
		FilesLines:         make([]int, len(message.GetFileCouples().GetIndex())),
		FilesMatrix:        make([]map[int]int64, message.GetFileCouples().GetMatrix().GetNumberOfRows()),
		PeopleFiles:        make([][]int, len(message.GetPeopleCouples().GetIndex())),
		PeopleMatrix:       make([]map[int]int64, message.GetPeopleCouples().GetMatrix().GetNumberOfRows()),
		reversedPeopleDict: message.GetPeopleCouples().GetIndex(),
	}
	for i, files := range message.GetPeopleFiles() {
		result.PeopleFiles[i] = make([]int, len(files.GetFiles()))
		for j, val := range files.GetFiles() {
			result.PeopleFiles[i][j] = int(val)
		}
	}

	if len(message.GetFileCouples().GetIndex()) != len(message.GetFilesLines()) {
		err := fmt.Errorf("%w: file_couples (%d) != file_lines (%d)",
			errCouplesPBIntegrity, len(message.GetFileCouples().GetIndex()), len(message.GetFilesLines()))
		couples.l.Critical(err)

		return nil, err
	}

	for i, v := range message.GetFilesLines() {
		result.FilesLines[i] = int(v)
	}

	convertCSR := func(dest []map[int]int64, src *pb.CompressedSparseRowMatrix) {
		for indptr := range src.GetIndptr() {
			if indptr == 0 {
				continue
			}

			dest[indptr-1] = map[int]int64{}
			for j := src.GetIndptr()[indptr-1]; j < src.GetIndptr()[indptr]; j++ {
				dest[indptr-1][int(src.GetIndices()[j])] = src.GetData()[j]
			}
		}
	}
	convertCSR(result.FilesMatrix, message.GetFileCouples().GetMatrix())
	convertCSR(result.PeopleMatrix, message.GetPeopleCouples().GetMatrix())

	return result, nil
}

// MergeResults combines two CouplesAnalysis-s together.
func (couples *CouplesAnalysis) MergeResults(
	result1, result2 any, common1, common2 *core.CommonAnalysisResult,
) any {
	cr1, cr2, err := couplesMergeInputs(result1, result2)
	if err != nil {
		return err
	}

	merged := CouplesResult{}
	var people, files join.IdentityMapping
	people, merged.reversedPeopleDict = join.PeopleIdentities(
		cr1.reversedPeopleDict, cr2.reversedPeopleDict,
	)
	files, merged.Files = join.LiteralIdentities(cr1.Files, cr2.Files)

	merged.FilesLines = make([]int, len(merged.Files))
	addMergedFileLines(merged.FilesLines, cr1.FilesLines, files.First)
	addMergedFileLines(merged.FilesLines, cr2.FilesLines, files.Second)

	if err := mergeCouplesPeopleFiles(&merged, cr1, cr2, people, files); err != nil {
		return err
	}

	if err := mergeCouplesMatrices(&merged, cr1, cr2, people, files); err != nil {
		return err
	}

	return merged
}

func couplesMergeInputs(result1, result2 any) (CouplesResult, CouplesResult, error) {
	first, err := requiredResult[CouplesResult](result1)
	if err != nil {
		return CouplesResult{}, CouplesResult{}, fmt.Errorf("merge couples first result: %w", err)
	}

	second, err := requiredResult[CouplesResult](result2)
	if err != nil {
		return CouplesResult{}, CouplesResult{}, fmt.Errorf("merge couples second result: %w", err)
	}

	if err := validateCouplesMergeResult(first); err != nil {
		return CouplesResult{}, CouplesResult{}, fmt.Errorf("merge couples first result: %w", err)
	}

	if err := validateCouplesMergeResult(second); err != nil {
		return CouplesResult{}, CouplesResult{}, fmt.Errorf("merge couples second result: %w", err)
	}

	return first, second, nil
}

func mergeCouplesPeopleFiles(
	merged *CouplesResult,
	first, second CouplesResult,
	people, files join.IdentityMapping,
) error {
	dicts := make([]map[int]bool, len(merged.reversedPeopleDict))

	err := addMergedPeopleFiles(dicts, first, people.First, files.First, len(merged.Files))
	if err != nil {
		return fmt.Errorf("merge couples first people files: %w", err)
	}

	err = addMergedPeopleFiles(dicts, second, people.Second, files.Second, len(merged.Files))
	if err != nil {
		return fmt.Errorf("merge couples second people files: %w", err)
	}

	merged.PeopleFiles = make([][]int, len(dicts))
	for person, personFiles := range dicts {
		merged.PeopleFiles[person] = make([]int, 0, len(personFiles))
		for file := range personFiles {
			merged.PeopleFiles[person] = append(merged.PeopleFiles[person], file)
		}

		sort.Ints(merged.PeopleFiles[person])
	}

	return nil
}

func mergeCouplesMatrices(
	merged *CouplesResult,
	first, second CouplesResult,
	people, files join.IdentityMapping,
) error {
	merged.PeopleMatrix = make([]map[int]int64, len(merged.reversedPeopleDict)+1)

	err := addMergedPeopleMatrix(merged.PeopleMatrix, first, people.First)
	if err != nil {
		return fmt.Errorf("merge couples first people matrix: %w", err)
	}

	err = addMergedPeopleMatrix(merged.PeopleMatrix, second, people.Second)
	if err != nil {
		return fmt.Errorf("merge couples second people matrix: %w", err)
	}

	merged.FilesMatrix = make([]map[int]int64, len(merged.Files))

	err = addMergedFilesMatrix(merged.FilesMatrix, first, files.First)
	if err != nil {
		return fmt.Errorf("merge couples first files matrix: %w", err)
	}

	err = addMergedFilesMatrix(merged.FilesMatrix, second, files.Second)
	if err != nil {
		return fmt.Errorf("merge couples second files matrix: %w", err)
	}

	return nil
}

func addMergedFileLines(target, source, destinations []int) {
	for sourceIndex, lines := range source {
		target[destinations[sourceIndex]] += lines
	}
}

func validateCouplesMergeResult(result CouplesResult) error {
	// The zero value is the canonical result of deserializing an empty Couples
	// payload and was accepted by MergeResults before integrity validation was
	// introduced. Preserve that identity element; partially populated results
	// still go through the strict dimensional checks below.
	if isEmptyCouplesResult(result) {
		return nil
	}

	err := validateCouplesFiles(result)
	if err != nil {
		return err
	}

	return validateCouplesPeople(result)
}

func isEmptyCouplesResult(result CouplesResult) bool {
	return len(result.reversedPeopleDict) == 0 &&
		len(result.Files) == 0 &&
		len(result.FilesLines) == 0 &&
		len(result.FilesMatrix) == 0 &&
		len(result.PeopleFiles) == 0 &&
		len(result.PeopleMatrix) == 0
}

func validateCouplesFiles(result CouplesResult) error {
	if len(result.FilesLines) != len(result.Files) {
		return fmt.Errorf(
			"%w: file lines has %d entries for %d files",
			errCouplesMergeIntegrity, len(result.FilesLines), len(result.Files),
		)
	}

	if len(result.FilesMatrix) != len(result.Files) {
		return fmt.Errorf(
			"%w: file matrix has %d rows for %d files",
			errCouplesMergeIntegrity, len(result.FilesMatrix), len(result.Files),
		)
	}

	for row, couplings := range result.FilesMatrix {
		for column := range couplings {
			if column < 0 || column >= len(result.Files) {
				return fmt.Errorf(
					"%w: file matrix column %d in row %d is outside [0, %d)",
					errCouplesMergeIntegrity, column, row, len(result.Files),
				)
			}
		}
	}

	return nil
}

func validateCouplesPeople(result CouplesResult) error {
	err := validateCouplesPeopleFiles(result)
	if err != nil {
		return err
	}

	return validateCouplesPeopleMatrix(result)
}

func validateCouplesPeopleFiles(result CouplesResult) error {
	peopleCount := len(result.reversedPeopleDict)
	// Finalize retains an extra scratch row for the unknown author; serialization omits it.
	if len(result.PeopleFiles) < peopleCount || len(result.PeopleFiles) > peopleCount+1 {
		return fmt.Errorf(
			"%w: people files has %d rows for %d people",
			errCouplesMergeIntegrity, len(result.PeopleFiles), peopleCount,
		)
	}

	for person, sourceFiles := range result.PeopleFiles {
		for _, file := range sourceFiles {
			if file < 0 || file >= len(result.Files) {
				return fmt.Errorf(
					"%w: file index %d in people files row %d is outside [0, %d)",
					errCouplesMergeIntegrity, file, person, len(result.Files),
				)
			}
		}
	}

	return nil
}

func validateCouplesPeopleMatrix(result CouplesResult) error {
	peopleCount := len(result.reversedPeopleDict)
	if len(result.PeopleMatrix) != peopleCount+1 {
		return fmt.Errorf(
			"%w: people matrix has %d rows for %d people plus the unknown-author row",
			errCouplesMergeIntegrity, len(result.PeopleMatrix), peopleCount,
		)
	}

	for row, couplings := range result.PeopleMatrix {
		for column := range couplings {
			if column < 0 || column > peopleCount {
				return fmt.Errorf(
					"%w: people matrix column %d in row %d is outside [0, %d]",
					errCouplesMergeIntegrity, column, row, peopleCount,
				)
			}
		}
	}

	return nil
}

func (couples *CouplesAnalysis) fileLineCounts(files []string) ([]int, error) {
	lines := make([]int, len(files))
	for fileIndex, name := range files {
		file, err := couples.lastCommit.File(name)
		if err != nil {
			err := fmt.Errorf("cannot find file %s in commit %s: %w",
				name, couples.lastCommit.Hash.String(), err)
			couples.l.Critical(err)

			return nil, err
		}

		blob := items.CachedBlob{Blob: file.Blob}

		err = blob.Cache()
		if err != nil {
			err = fmt.Errorf("cannot read blob %s of file %s: %w",
				blob.Hash.String(), name, err)
			couples.l.Critical(err)

			return nil, err
		}

		lines[fileIndex], _ = blob.CountLines()
	}

	return lines, nil
}

func (couples *CouplesAnalysis) peopleCouplingMatrices(
	people []map[string]int, filesIndex map[string]int,
) ([]map[int]int64, [][]int) {
	peopleMatrix := make([]map[int]int64, couples.PeopleNumber+1)
	peopleFiles := make([][]int, couples.PeopleNumber+1)

	for personIndex := range peopleMatrix {
		peopleMatrix[personIndex] = map[int]int64{}
		for file, commits := range people[personIndex] {
			if fileIndex, exists := filesIndex[file]; exists {
				peopleFiles[personIndex] = append(peopleFiles[personIndex], fileIndex)
			}

			for otherPersonIndex, otherFiles := range people {
				if delta := min(otherFiles[file], commits); delta > 0 {
					peopleMatrix[personIndex][otherPersonIndex] += int64(delta)
				}
			}
		}

		sort.Ints(peopleFiles[personIndex])
	}

	return peopleMatrix, peopleFiles
}

func addMergedPeopleFiles(
	target []map[int]bool, source CouplesResult,
	people, files []int, fileTargetLength int,
) error {
	for person, sourceFiles := range source.PeopleFiles {
		// There is no public identity for the optional unknown-author scratch row.
		if person >= len(source.reversedPeopleDict) {
			continue
		}

		err := addMergedPersonFiles(
			target, sourceFiles, source.Files, people, files, person, fileTargetLength,
		)
		if err != nil {
			return err
		}
	}

	return nil
}

func addMergedPersonFiles(
	target []map[int]bool,
	sourceFiles []int,
	sourceFileNames []string,
	people, files []int,
	person, fileTargetLength int,
) error {
	targetPerson, err := mergedCouplesIndex(people, person, len(target), "person")
	if err != nil {
		return err
	}

	if target[targetPerson] == nil {
		target[targetPerson] = map[int]bool{}
	}

	for _, file := range sourceFiles {
		if file < 0 || file >= len(sourceFileNames) {
			return fmt.Errorf(
				"%w: people-files source index %d is outside [0, %d)",
				errCouplesMergeIntegrity, file, len(sourceFileNames),
			)
		}

		targetFile, err := mergedCouplesIndex(files, file, fileTargetLength, "file")
		if err != nil {
			return err
		}

		target[targetPerson][targetFile] = true
	}

	return nil
}

func addMergedPeopleMatrix(
	target []map[int]int64, source CouplesResult, people []int,
) error {
	for person, couplings := range source.PeopleMatrix {
		targetPerson, err := mergedCouplesPersonIndex(
			person, source.reversedPeopleDict, people, len(target),
		)
		if err != nil {
			return err
		}

		if target[targetPerson] == nil {
			target[targetPerson] = map[int]int64{}
		}

		for other, value := range couplings {
			targetOther, err := mergedCouplesPersonIndex(
				other, source.reversedPeopleDict, people, len(target),
			)
			if err != nil {
				return err
			}

			target[targetPerson][targetOther] += value
		}
	}

	return nil
}

func addMergedFilesMatrix(
	target []map[int]int64, source CouplesResult, files []int,
) error {
	for file, couplings := range source.FilesMatrix {
		err := addMergedFileCouplings(target, couplings, source.Files, files, file)
		if err != nil {
			return err
		}
	}

	return nil
}

func addMergedFileCouplings(
	target []map[int]int64,
	couplings map[int]int64,
	sourceFiles []string,
	files []int,
	file int,
) error {
	if file < 0 || file >= len(sourceFiles) {
		return fmt.Errorf(
			"%w: file matrix source row %d is outside [0, %d)",
			errCouplesMergeIntegrity, file, len(sourceFiles),
		)
	}

	targetFile, err := mergedCouplesIndex(files, file, len(target), "file")
	if err != nil {
		return err
	}

	if target[targetFile] == nil {
		target[targetFile] = map[int]int64{}
	}

	for other, value := range couplings {
		if other < 0 || other >= len(sourceFiles) {
			return fmt.Errorf(
				"%w: file matrix source column %d is outside [0, %d)",
				errCouplesMergeIntegrity, other, len(sourceFiles),
			)
		}

		targetOther, err := mergedCouplesIndex(files, other, len(target), "file")
		if err != nil {
			return err
		}

		target[targetFile][targetOther] += value
	}

	return nil
}

func mergedCouplesPersonIndex(
	source int, identities []string, joined []int, targetLength int,
) (int, error) {
	if source < 0 || source > len(identities) {
		return 0, fmt.Errorf(
			"%w: people matrix source index %d is outside [0, %d]",
			errCouplesMergeIntegrity, source, len(identities),
		)
	}

	if source == len(identities) {
		if targetLength == 0 {
			return 0, fmt.Errorf(
				"%w: people matrix has no destination for the unknown author",
				errCouplesMergeIntegrity,
			)
		}

		return targetLength - 1, nil
	}

	return mergedCouplesIndex(joined, source, targetLength, "person")
}

func mergedCouplesIndex(
	joined []int, source, targetLength int, kind string,
) (int, error) {
	if source < 0 || source >= len(joined) {
		return 0, fmt.Errorf(
			"%w: %s source index %d is outside [0, %d)",
			errCouplesMergeIntegrity, kind, source, len(joined),
		)
	}

	destination := joined[source]
	if destination < 0 || destination >= targetLength {
		return 0, fmt.Errorf(
			"%w: joined %s destination %d for source %d is outside [0, %d)",
			errCouplesMergeIntegrity, kind, destination, source, targetLength,
		)
	}

	return destination, nil
}

func (couples *CouplesAnalysis) serializeText(result *CouplesResult, writer io.Writer) {
	// Pipeline text serialization has no error channel; explicit blank
	// assignments document every intentionally discarded writer error.
	_, _ = fmt.Fprintln(writer, "  files_coocc:")
	_, _ = fmt.Fprintln(writer, "    index:")
	writeCouplesIndex(writer, result.Files)

	_, _ = fmt.Fprintln(writer, "    lines:")

	for _, l := range result.FilesLines {
		_, _ = fmt.Fprintf(writer, "      - %d\n", l)
	}

	_, _ = fmt.Fprintln(writer, "    matrix:")
	writeCouplesMatrix(writer, result.FilesMatrix)

	_, _ = fmt.Fprintln(writer, "  people_coocc:")
	_, _ = fmt.Fprintln(writer, "    index:")
	writeCouplesIndex(writer, result.reversedPeopleDict)

	_, _ = fmt.Fprintln(writer, "    matrix:")
	writeCouplesMatrix(writer, result.PeopleMatrix)

	_, _ = fmt.Fprintln(writer, "    author_files:") // sorted by number of files each author changed
	writeAuthorFiles(writer, result)
}

func writeCouplesIndex(writer io.Writer, values []string) {
	for _, value := range values {
		_, _ = fmt.Fprintf(writer, "      - %s\n", yaml.SafeString(value))
	}
}

func writeCouplesMatrix(writer io.Writer, matrix []map[int]int64) {
	for _, row := range matrix {
		_, _ = fmt.Fprint(writer, "      - {")

		indices := make([]int, 0, len(row))
		for index := range row {
			indices = append(indices, index)
		}

		sort.Ints(indices)

		for i, index := range indices {
			_, _ = fmt.Fprintf(writer, "%d: %d", index, row[index])

			if i < len(indices)-1 {
				_, _ = fmt.Fprint(writer, ", ")
			}
		}

		_, _ = fmt.Fprintln(writer, "}")
	}
}

func writeAuthorFiles(writer io.Writer, result *CouplesResult) {
	peopleFiles := sortByNumberOfFiles(result.PeopleFiles, result.reversedPeopleDict, result.Files)
	for _, authorFiles := range peopleFiles {
		_, _ = fmt.Fprintf(writer, "      - %s:\n", yaml.SafeString(authorFiles.Author))
		sort.Strings(authorFiles.Files)

		for _, file := range authorFiles.Files {
			_, _ = fmt.Fprintf(writer, "        - %s\n", yaml.SafeString(file)) // sorted by path
		}
	}
}

func sortByNumberOfFiles(
	peopleFiles [][]int, peopleDict, filesDict []string,
) authorFilesList {
	var pfl authorFilesList

	for peopleIdx, files := range peopleFiles {
		if peopleIdx < len(peopleDict) {
			fileNames := make([]string, len(files))
			for i, fi := range files {
				fileNames[i] = filesDict[fi]
			}

			pfl = append(pfl, authorFiles{peopleDict[peopleIdx], fileNames})
		}
	}

	sort.Sort(pfl)

	return pfl
}

type authorFiles struct {
	Author string
	Files  []string
}

type authorFilesList []authorFiles

func (s authorFilesList) Len() int {
	return len(s)
}

func (s authorFilesList) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s authorFilesList) Less(i, j int) bool {
	return len(s[i].Files) < len(s[j].Files)
}

func (couples *CouplesAnalysis) serializeBinary(result *CouplesResult, writer io.Writer) error {
	message := pb.CouplesAnalysisResults{}

	message.FileCouples = &pb.Couples{
		Index:  result.Files,
		Matrix: pb.MapToCompressedSparseRowMatrix(result.FilesMatrix),
	}
	message.PeopleCouples = &pb.Couples{
		Index:  result.reversedPeopleDict,
		Matrix: pb.MapToCompressedSparseRowMatrix(result.PeopleMatrix),
	}

	message.PeopleFiles = make([]*pb.TouchedFiles, len(result.reversedPeopleDict))
	for key := range result.reversedPeopleDict {
		files := result.PeopleFiles[key]

		int32Files := make([]int32, len(files))
		for fileIndex, file := range files {
			fileID, err := intToProtoInt32(file, "couples touched-file index")
			if err != nil {
				return err
			}

			int32Files[fileIndex] = fileID
		}

		message.PeopleFiles[key] = &pb.TouchedFiles{
			Files: int32Files,
		}
	}

	message.FilesLines = make([]int32, len(result.FilesLines))
	for fileIndex, lines := range result.FilesLines {
		lineCount, err := intToProtoInt32(lines, "couples file line count")
		if err != nil {
			return err
		}

		message.FilesLines[fileIndex] = lineCount
	}

	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal couples protobuf: %w", err)
	}

	_, err = writer.Write(serialized)
	if err != nil {
		return fmt.Errorf("write couples protobuf: %w", err)
	}

	return nil
}

// currentFiles return the list of files in the last consumed commit.
func (couples *CouplesAnalysis) currentFiles() map[string]bool {
	files, err := couples.currentFilesWithError()
	if err != nil {
		couples.l.Critical(err)
	}

	return files
}

func (couples *CouplesAnalysis) currentFilesWithError() (map[string]bool, error) {
	files := map[string]bool{}
	if couples.lastCommit == nil {
		for key := range couples.files {
			files[key] = true
		}

		return files, nil
	}

	tree, err := couples.lastCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf(
			"load tree for commit %s: %w", couples.lastCommit.Hash.String(), err,
		)
	}

	fileIter := tree.Files()

	err = fileIter.ForEach(func(fobj *object.File) error {
		files[fobj.Name] = true

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf(
			"iterate files for commit %s: %w", couples.lastCommit.Hash.String(), err,
		)
	}

	return files, nil
}

// propagateRenames applies `renames` over the files from `lastCommit`.
func (couples *CouplesAnalysis) propagateRenames(files map[string]bool) (
	map[string]map[string]int, []map[string]int,
) {
	renames := *couples.renames
	reducedFiles := couples.reduceFileCouplings(files)
	aliases, pointers := renameAliases(renames, reducedFiles)
	adjustments := couples.renameAdjustments(aliases)
	applyRenameAdjustments(reducedFiles, aliases, adjustments)
	people := couples.reducePeopleFiles(files, aliases, pointers)

	return reducedFiles, people
}

func (couples *CouplesAnalysis) reduceFileCouplings(
	files map[string]bool,
) map[string]map[string]int {
	reduced := map[string]map[string]int{}

	for file := range files {
		couplings := map[string]int{}

		for other := range files {
			if count := couples.files[file][other]; count > 0 {
				couplings[other] = count
			}
		}

		if len(couplings) > 0 {
			reduced[file] = couplings
		}
	}

	return reduced
}

func renameAliases(
	renames []rename, reducedFiles map[string]map[string]int,
) (map[string]map[string]bool, map[string]string) {
	aliases := map[string]map[string]bool{}
	pointers := map[string]string{}

	for i := range renames {
		rename := renames[len(renames)-i-1]

		toName := rename.ToName
		if newTo, exists := pointers[toName]; exists {
			toName = newTo
		}

		if _, exists := reducedFiles[toName]; exists {
			if rename.FromName != toName {
				if aliases[toName] == nil {
					aliases[toName] = map[string]bool{}
				}

				aliases[toName][rename.FromName] = true
				pointers[rename.FromName] = toName
			}
		}
	}

	return aliases, pointers
}

func (couples *CouplesAnalysis) renameAdjustments(
	aliases map[string]map[string]bool,
) map[string]map[string]int {
	adjustments := map[string]map[string]int{}

	for final, set := range aliases {
		adjustment := map[string]int{}

		for alias := range set {
			for k, v := range couples.files[alias] {
				adjustment[k] += v
			}
		}

		adjustments[final] = adjustment
	}

	return adjustments
}

func applyRenameAdjustments(
	reducedFiles map[string]map[string]int,
	aliases map[string]map[string]bool,
	adjustments map[string]map[string]int,
) {
	for _, adjustment := range adjustments {
		for final, set := range aliases {
			for alias := range set {
				adjustment[final] += adjustment[alias]
				delete(adjustment, alias)
			}
		}
	}

	for final, adjustment := range adjustments {
		for key, val := range adjustment {
			if coocc, exists := reducedFiles[final][key]; exists {
				reducedFiles[final][key] = coocc + val
				reducedFiles[key][final] = coocc + val
			}
		}
	}
}

func (couples *CouplesAnalysis) reducePeopleFiles(
	files map[string]bool, aliases map[string]map[string]bool, pointers map[string]string,
) []map[string]int {
	people := make([]map[string]int, len(couples.people))
	for personIndex, counts := range couples.people {
		people[personIndex] = reducePersonFileCounts(counts, files, aliases, pointers)
	}

	return people
}

func reducePersonFileCounts(
	counts map[string]int,
	files map[string]bool,
	aliases map[string]map[string]bool,
	pointers map[string]string,
) map[string]int {
	reducedCounts := map[string]int{}

	for file := range files {
		count := counts[file]
		for alias := range aliases[file] {
			count += counts[alias]
		}

		if count > 0 {
			reducedCounts[file] = count
		}
	}

	for file, count := range counts {
		_, isFile := files[file]

		_, isPointer := pointers[file]
		if !isFile && !isPointer {
			reducedCounts[file] = count
		}
	}

	return reducedCounts
}

var _ = core.RegisterPipelineItem(&CouplesAnalysis{})
