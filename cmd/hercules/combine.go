package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/spf13/cobra"
	progress "gopkg.in/cheggaaa/pb.v1"

	"github.com/cwbudde/hercules"
	"github.com/cwbudde/hercules/internal/analysisio"
	"github.com/cwbudde/hercules/internal/burndown"
	"github.com/cwbudde/hercules/internal/pb"
	"github.com/cwbudde/hercules/leaves"
)

// combineCmd represents the combine command.
var combineCmd = &cobra.Command{
	Use:   "combine",
	Short: "Merge several binary analysis results together.",
	Long:  ``,
	Args:  cobra.MinimumNArgs(1),
	RunE:  runCombine,
}

// repositorySeparator joins the repository names of a combined result in its header.
const repositorySeparator = " & "

type combineAccumulator struct {
	repositories []string
	errors       map[string][]string
	// warnings names what was stepped over without failing the run - analyses which cannot take
	// part in a merge at all.
	warnings     map[string][]string
	results      map[string]any
	metadata     *hercules.CommonAnalysisResult
	mergedInputs int
}

// loadedMessage is the outcome of reading one input file.
type loadedMessage struct {
	results    map[string]any
	metadata   *hercules.CommonAnalysisResult
	repository string
	// qualifiedPaths reports the header flag of the same name: the file's path keys already name
	// the repository they belong to, so they must not be qualified a second time.
	qualifiedPaths bool
	// failures fail the whole run: the file could not be read or parsed, or an analysis it does
	// carry could not be deserialized.
	failures []string
	// skipped names analyses which do not implement ResultMergeablePipelineItem.
	skipped []string
}

func runCombine(cmd *cobra.Command, files []string) error {
	profile, err := cmd.Flags().GetBool("profile")
	if err != nil {
		return fmt.Errorf("read profile flag: %w", err)
	}
	if profile {
		startCombineProfileServer()
	}

	only, err := cmd.Flags().GetString("only")
	if err != nil {
		return fmt.Errorf("read only flag: %w", err)
	}

	accumulator := newCombineAccumulator()
	accumulator.mergeFiles(files, only)
	printMessages("Skipped:", accumulator.warnings)
	printMessages("Errors:", accumulator.errors)
	if only != "" {
		if _, exists := accumulator.results[only]; !exists {
			return errors.Join(
				fmt.Errorf("combine: requested analysis %q was not present in any valid input", only),
				accumulator.err(),
			)
		}
	}
	if accumulator.mergedInputs == 0 || len(accumulator.results) == 0 {
		return errors.Join(
			errors.New("combine: no valid analysis input was merged"),
			accumulator.err(),
		)
	}
	sort.Strings(accumulator.repositories)
	writeErr := writeCombinedResult(cmd.OutOrStdout(), accumulator)
	inputErr := accumulator.err()
	if writeErr != nil {
		return errors.Join(inputErr, writeErr)
	}
	return inputErr
}

func startCombineProfileServer() {
	server := &http.Server{
		Addr:              "localhost:6060",
		Handler:           nil,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			_, _ = fmt.Fprintf(os.Stderr, "profiling server: %v\n", err)
		}
	}()
}

func newCombineAccumulator() *combineAccumulator {
	return &combineAccumulator{
		errors:   map[string][]string{},
		warnings: map[string][]string{},
		results:  map[string]any{},
		metadata: &hercules.CommonAnalysisResult{},
	}
}

func (accumulator *combineAccumulator) mergeFiles(files []string, only string) {
	currentFile := ""
	bar := newCombineProgress(len(files), &currentFile)
	for _, currentFile = range files {
		bar.Increment()
		accumulator.mergeFile(currentFile, only)
	}
	bar.Finish()
	_, _ = os.Stderr.WriteString("\033[2K\r")
}

func newCombineProgress(fileCount int, currentFile *string) *progress.ProgressBar {
	bar := progress.New(fileCount)
	bar.Callback = func(message string) {
		_, _ = os.Stderr.WriteString("\033[2K\r" + message + " " + *currentFile)
	}
	bar.NotPrint = true
	bar.ShowPercent = false
	bar.ShowSpeed = false
	return bar.SetMaxWidth(80).Start()
}

func (accumulator *combineAccumulator) mergeFile(fileName, only string) {
	loaded := loadMessage(fileName, only)
	if len(loaded.skipped) > 0 {
		accumulator.warnings[fileName] = loaded.skipped
	}
	messages := loaded.failures
	if loaded.metadata == nil {
		accumulator.errors[fileName] = messages
		return
	}

	initializeBurndownRepository(loaded.results, loaded.repository)
	if !loaded.qualifiedPaths {
		messages = append(messages, qualifyRepositoryPaths(loaded.results, loaded.repository)...)
	}
	merged, mergeErrors := mergeResults(
		accumulator.results, accumulator.metadata, loaded.results, loaded.metadata, only,
	)
	for _, err := range mergeErrors {
		messages = append(messages, err.Error())
	}
	if merged > 0 {
		accumulator.mergedInputs++
		accumulator.repositories = append(accumulator.repositories, loaded.repository)
	}
	accumulator.errors[fileName] = messages
}

func (accumulator *combineAccumulator) err() error {
	var failures []error
	files := make([]string, 0, len(accumulator.errors))
	for fileName := range accumulator.errors {
		files = append(files, fileName)
	}
	sort.Strings(files)
	for _, fileName := range files {
		for _, message := range accumulator.errors[fileName] {
			failures = append(failures, fmt.Errorf("%s: %s", fileName, message))
		}
	}
	return errors.Join(failures...)
}

func initializeBurndownRepository(results map[string]any, repository string) {
	burndownResult, ok := results["Burndown"].(leaves.BurndownResult)
	if !ok || len(burndownResult.RepositoryHistories) > 0 || len(burndownResult.GlobalHistory) == 0 {
		return
	}
	burndownResult.ReversedRepositoryDict = []string{repository}
	burndownResult.RepositoryHistories = []burndown.DenseHistory{burndownResult.GlobalHistory}
	results["Burndown"] = burndownResult
}

// qualifyRepositoryPaths rewrites the path keys of every loaded result which carries them, so that
// the same path in two repositories stays two paths through the merge. Burndown gets the same
// treatment through its own repository axis (see initializeBurndownRepository); the analyses here
// have no such axis and would otherwise fuse their keys.
//
// Whether a file has been through this already is read off its header flag, not guessed from the
// repository name: the output of combine carries qualified keys whether it was built from one input
// or from thirty, and files written before the flag existed carry none whatever their header says.
//
// A file whose header names no repository keeps its bare keys - there is nothing to qualify them
// with - and stays exposed to the collision this prevents.
func qualifyRepositoryPaths(results map[string]any, repository string) []string {
	if repository == "" {
		return nil
	}

	var failures []string

	for key, value := range results {
		summoned := hercules.Registry.Summon(key)
		if len(summoned) == 0 {
			continue
		}
		item, ok := summoned[0].(hercules.RepositoryQualifiablePipelineItem)
		if !ok {
			continue
		}
		qualified := item.QualifyPaths(value, repository)
		if err, isErr := qualified.(error); isErr {
			failures = append(failures, "could not qualify "+key+" paths: "+err.Error())
			continue
		}
		results[key] = qualified
	}
	sort.Strings(failures)
	return failures
}

func writeCombinedResult(destination io.Writer, accumulator *combineAccumulator) error {
	message := pb.AnalysisResults{
		Header: &pb.Metadata{
			Version:    pb.SchemaVersion,
			Hash:       hercules.BinaryGitHash,
			Repository: strings.Join(accumulator.repositories, repositorySeparator),
			// Every merged input went through qualifyRepositoryPaths, so combining this output
			// again must not prefix its keys a second time.
			QualifiedPaths: true,
		},
		Contents: map[string][]byte{},
	}
	accumulator.metadata.FillMetadata(message.GetHeader())

	if err := serializeCombinedContents(message.GetContents(), accumulator.results); err != nil {
		return err
	}
	serialized, err := proto.Marshal(&message)
	if err != nil {
		return fmt.Errorf("marshal combined result: %w", err)
	}
	if _, err := destination.Write(serialized); err != nil {
		return fmt.Errorf("write combined result: %w", err)
	}
	return nil
}

func serializeCombinedContents(contents map[string][]byte, results map[string]any) error {
	for key, value := range results {
		items := hercules.Registry.Summon(key)
		if len(items) == 0 {
			return fmt.Errorf("serialize %s: pipeline item not found", key)
		}
		leaf, ok := items[0].(hercules.LeafPipelineItem)
		if !ok {
			return fmt.Errorf("serialize %s: pipeline item is not a leaf", key)
		}
		buffer := bytes.Buffer{}
		if err := leaf.Serialize(value, true, &buffer); err != nil {
			return fmt.Errorf("serialize %s: %w", key, err)
		}
		contents[key] = buffer.Bytes()
	}
	return nil
}

func loadMessage(fileName, only string) loadedMessage {
	message, loadErr := readAnalysisMessage(fileName)
	if loadErr != "" {
		return loadedMessage{failures: []string{loadErr}}
	}

	loaded := deserializeAnalysisContents(fileName, only, message.GetContents())
	loaded.metadata = hercules.MetadataToCommonAnalysisResult(message.GetHeader())
	loaded.repository = message.GetHeader().GetRepository()
	loaded.qualifiedPaths = message.GetHeader().GetQualifiedPaths()
	return loaded
}

func readAnalysisMessage(fileName string) (*pb.AnalysisResults, string) {
	fi, err := os.Stat(fileName)
	if err != nil {
		return nil, "Cannot access " + fileName + ": " + err.Error()
	}
	if fi.Size() == 0 {
		return nil, "Cannot parse " + fileName + ": file size is 0"
	}
	file, err := os.Open(fileName)
	if err != nil {
		return nil, "Cannot read " + fileName + ": " + err.Error()
	}
	buffer, err := analysisio.ReadAll(file, analysisio.DefaultLimits())
	if err != nil {
		_ = file.Close()
		return nil, "Cannot read " + fileName + ": " + err.Error()
	}
	if err := file.Close(); err != nil {
		return nil, "Cannot close " + fileName + ": " + err.Error()
	}
	message := pb.AnalysisResults{}
	if err := proto.Unmarshal(buffer, &message); err != nil {
		return nil, "Cannot parse " + fileName + ": " + err.Error()
	}
	if message.GetHeader() == nil {
		return nil, "Cannot parse " + fileName + ": corrupted header"
	}
	if err := analysisio.ValidateAndMigrateAnalysisResults(
		&message, analysisio.DefaultLimits(),
	); err != nil {
		return nil, "Cannot parse " + fileName + ": " + err.Error()
	}

	return &message, ""
}

// deserializeAnalysisContents decodes the analyses one input file carries.
//
// only, when set, restricts the work to that single analysis - everything else in the file is left
// untouched, which is what makes --only the escape hatch its help text promises. Filtering here
// rather than at merge time is the whole point: an analysis nobody asked for must not be able to
// fail the run on its way in.
//
// Analyses which do not implement ResultMergeablePipelineItem cannot take part in a combine at
// all. That is a property of the analysis, not a defect in the file, so they are named and stepped
// over instead of taking every mergeable analysis down with them. Asking for one by name in --only
// is the exception: there the caller wants exactly that analysis and gets a hard failure.
func deserializeAnalysisContents(
	fileName, only string, contents map[string][]byte,
) loadedMessage {
	loaded := loadedMessage{results: map[string]any{}}
	for key, val := range contents {
		if only != "" && key != only {
			continue
		}
		summoned := hercules.Registry.Summon(key)
		if len(summoned) == 0 {
			loaded.failures = append(loaded.failures, fileName+": item not found: "+key)
			continue
		}
		mpi, ok := summoned[0].(hercules.ResultMergeablePipelineItem)
		if !ok {
			message := key + ": ResultMergeablePipelineItem is not implemented"
			if only != "" {
				loaded.failures = append(loaded.failures, fileName+": "+message)
			} else {
				loaded.skipped = append(loaded.skipped, message+"; not merged")
			}
			continue
		}
		msg, err := mpi.Deserialize(val)
		if err != nil {
			loaded.failures = append(loaded.failures, fileName+": deserialization failed: "+key+": "+err.Error())
			continue
		}
		loaded.results[key] = msg
	}
	// Map iteration order is random, so sort to keep a run's diagnostics reproducible.
	sort.Strings(loaded.failures)
	sort.Strings(loaded.skipped)
	return loaded
}

func printMessages(title string, perFile map[string][]string) {
	anything := false
	for _, messages := range perFile {
		if len(messages) > 0 {
			anything = true
			break
		}
	}
	if !anything {
		return
	}
	fmt.Fprintln(os.Stderr, title)
	files := make([]string, 0, len(perFile))
	for fileName := range perFile {
		files = append(files, fileName)
	}
	sort.Strings(files)
	for _, key := range files {
		messages := perFile[key]
		if len(messages) > 0 {
			fmt.Fprintln(os.Stderr, "  "+key)
			for _, message := range messages {
				fmt.Fprintln(os.Stderr, "    "+message)
			}
		}
	}
}

func mergeResults(mergedResults map[string]any,
	mergedCommons *hercules.CommonAnalysisResult,
	anotherResults map[string]any,
	anotherCommons *hercules.CommonAnalysisResult,
	only string,
) (int, []error) {
	var errors []error
	merged := 0
	for key, val := range anotherResults {
		if only != "" && key != only {
			continue
		}
		if err := mergeResult(mergedResults, key, val, mergedCommons, anotherCommons); err != nil {
			errors = append(errors, err)
		} else {
			merged++
		}
	}
	if merged > 0 {
		if mergedCommons.CommitsNumber == 0 {
			*mergedCommons = *anotherCommons
		} else {
			mergedCommons.Merge(anotherCommons)
		}
	}
	return merged, errors
}

func mergeResult(
	mergedResults map[string]any,
	key string,
	value any,
	mergedCommons, anotherCommons *hercules.CommonAnalysisResult,
) error {
	mergedResult, exists := mergedResults[key]
	if !exists {
		mergedResults[key] = value
		return nil
	}
	summoned := hercules.Registry.Summon(key)
	if len(summoned) == 0 {
		return fmt.Errorf("could not merge %s: analysis is not registered", key)
	}
	item, ok := summoned[0].(hercules.ResultMergeablePipelineItem)
	if !ok {
		return fmt.Errorf("could not merge %s: analysis does not support merging", key)
	}
	mergedResult = item.MergeResults(mergedResult, value, mergedCommons, anotherCommons)
	if err, isErr := mergedResult.(error); isErr {
		return fmt.Errorf("could not merge %s: %w", item.Name(), err)
	}
	mergedResults[key] = mergedResult
	return nil
}

// getOptionsString lists the analyses --only can name. Leaves which do not implement
// ResultMergeablePipelineItem are left out: combine cannot merge them under any flag, so offering
// them as choices only invites the failure the flag exists to avoid.
func getOptionsString() string {
	registeredLeaves := hercules.Registry.GetLeaves()
	names := make([]string, 0, len(registeredLeaves))
	for _, leaf := range registeredLeaves {
		if _, mergeable := leaf.(hercules.ResultMergeablePipelineItem); mergeable {
			names = append(names, leaf.Name())
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func init() {
	rootCmd.AddCommand(combineCmd)
	combineCmd.SetUsageFunc(combineCmd.UsageFunc())
	combineCmd.Flags().String("only", "", "Consider only the specified analysis. "+
		"Empty means all available. Choices: "+getOptionsString()+".")
	combineCmd.Flags().Bool("profile", false, "Collect the profile to hercules.pprof.")
}
