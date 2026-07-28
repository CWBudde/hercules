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

type combineAccumulator struct {
	repositories []string
	errors       map[string][]string
	results      map[string]any
	metadata     *hercules.CommonAnalysisResult
	mergedInputs int
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
	printErrors(accumulator.errors)
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
	results, metadata, repository, messages := loadMessage(fileName)
	if metadata == nil {
		accumulator.errors[fileName] = messages
		return
	}

	initializeBurndownRepository(results, repository)
	merged, mergeErrors := mergeResults(
		accumulator.results, accumulator.metadata, results, metadata, only,
	)
	for _, err := range mergeErrors {
		messages = append(messages, err.Error())
	}
	if merged > 0 {
		accumulator.mergedInputs++
		accumulator.repositories = append(accumulator.repositories, repository)
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

func writeCombinedResult(destination io.Writer, accumulator *combineAccumulator) error {
	message := pb.AnalysisResults{
		Header: &pb.Metadata{
			Version:    pb.SchemaVersion,
			Hash:       hercules.BinaryGitHash,
			Repository: strings.Join(accumulator.repositories, " & "),
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

func loadMessage(fileName string) (
	map[string]any, *hercules.CommonAnalysisResult, string, []string,
) {
	message, loadErr := readAnalysisMessage(fileName)
	if loadErr != "" {
		return nil, nil, "", []string{loadErr}
	}

	results, errs := deserializeAnalysisContents(fileName, message.GetContents())
	return results, hercules.MetadataToCommonAnalysisResult(message.GetHeader()),
		message.GetHeader().GetRepository(), errs
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

func deserializeAnalysisContents(fileName string, contents map[string][]byte) (map[string]any, []string) {
	results := map[string]any{}
	var errs []string
	for key, val := range contents {
		summoned := hercules.Registry.Summon(key)
		if len(summoned) == 0 {
			errs = append(errs, fileName+": item not found: "+key)
			continue
		}
		mpi, ok := summoned[0].(hercules.ResultMergeablePipelineItem)
		if !ok {
			errs = append(errs, fileName+": "+key+": ResultMergeablePipelineItem is not implemented")
			continue
		}
		msg, err := mpi.Deserialize(val)
		if err != nil {
			errs = append(errs, fileName+": deserialization failed: "+key+": "+err.Error())
			continue
		}
		results[key] = msg
	}
	return results, errs
}

func printErrors(allErrors map[string][]string) {
	needToPrintErrors := false
	for _, errs := range allErrors {
		if len(errs) > 0 {
			needToPrintErrors = true
			break
		}
	}
	if !needToPrintErrors {
		return
	}
	fmt.Fprintln(os.Stderr, "Errors:")
	files := make([]string, 0, len(allErrors))
	for fileName := range allErrors {
		files = append(files, fileName)
	}
	sort.Strings(files)
	for _, key := range files {
		errs := allErrors[key]
		if len(errs) > 0 {
			fmt.Fprintln(os.Stderr, "  "+key)
			for _, err := range errs {
				fmt.Fprintln(os.Stderr, "    "+err)
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

func getOptionsString() string {
	registeredLeaves := hercules.Registry.GetLeaves()
	leaves := make([]string, 0, len(registeredLeaves))
	for _, leaf := range registeredLeaves {
		leaves = append(leaves, leaf.Name())
	}
	return strings.Join(leaves, ", ")
}

func init() {
	rootCmd.AddCommand(combineCmd)
	combineCmd.SetUsageFunc(combineCmd.UsageFunc())
	combineCmd.Flags().String("only", "", "Consider only the specified analysis. "+
		"Empty means all available. Choices: "+getOptionsString()+".")
	combineCmd.Flags().Bool("profile", false, "Collect the profile to hercules.pprof.")
}
