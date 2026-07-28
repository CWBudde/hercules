package render

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/cwbudde/hercules/internal/render/readers"
)

type jsonModeExtractor func(readers.Reader) (interface{}, error)

var jsonModeExtractors = map[string]jsonModeExtractor{
	"devs":                    extractDeveloperStatsJSON,
	"devs-efforts":            extractDeveloperTimeSeriesJSON,
	"old-vs-new":              extractDeveloperTimeSeriesJSON,
	"devs-parallel":           extractDeveloperTimeSeriesJSON,
	"burndown-project":        extractProjectBurndownJSON,
	"burndown-file":           extractFileBurndownJSON,
	"burndown-person":         extractPeopleBurndownJSON,
	"burndown-repository":     extractRepositoryBurndownJSON,
	"burndown-repos-combined": extractRepositoryBurndownJSON,
	"ownership":               extractOwnershipJSON,
	"overwrites-matrix":       extractOverwritesJSON,
	"couples-files":           extractFileCouplingJSON,
	"couples-people":          extractPeopleCouplingJSON,
	"couples-shotness":        extractShotnessCouplingJSON,
	"shotness":                extractShotnessJSON,
	"run-times":               extractRuntimeStatsJSON,
	"languages":               extractLanguageStatsJSON,
	"sentiment":               extractSentimentJSON,
	"temporal-activity":       extractTemporalActivityJSON,
	"bus-factor":              extractBusFactorJSON,
	"ownership-concentration": extractOwnershipConcentrationJSON,
	"knowledge-diffusion":     extractKnowledgeDiffusionJSON,
	"hotspot-risk":            extractHotspotRiskJSON,
	"refactoring-proxy":       extractRefactoringProxyJSON,
}

// extractModeDataForJSON extracts raw reader data for JSON output without rendering plots.
func extractModeDataForJSON(reader readers.Reader, mode string) (interface{}, error) {
	extract, ok := jsonModeExtractors[mode]
	if !ok {
		return nil, fmt.Errorf("JSON output is not implemented for mode %s", mode)
	}
	return extract(reader)
}

func extractDeveloperStatsJSON(reader readers.Reader) (interface{}, error) {
	stats, err := reader.GetDeveloperStats()
	return jsonField("developer_stats", stats, err)
}

func extractDeveloperTimeSeriesJSON(reader readers.Reader) (interface{}, error) {
	data, err := reader.GetDeveloperTimeSeriesData()
	return jsonField("developer_time_series", data, err)
}

func extractProjectBurndownJSON(reader readers.Reader) (interface{}, error) {
	header, name, matrix, err := reader.GetProjectBurndownWithHeader()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"type": "burndown", "target": "project", "header": header, "name": name, "matrix": matrix,
	}, nil
}

func extractFileBurndownJSON(reader readers.Reader) (interface{}, error) {
	files, err := reader.GetFilesBurndown()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"type": "burndown", "target": "file", "files": files}, nil
}

func extractPeopleBurndownJSON(reader readers.Reader) (interface{}, error) {
	people, err := reader.GetPeopleBurndown()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"type": "burndown", "target": "person", "people": people}, nil
}

func extractRepositoryBurndownJSON(reader readers.Reader) (interface{}, error) {
	repositoryReader, ok := reader.(readers.RepositoryBurndownReader)
	if !ok {
		return nil, fmt.Errorf("%w: repository burndown", readers.ErrAnalysisMissing)
	}
	repositories, err := repositoryReader.GetRepositoriesBurndown()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"type": "burndown", "target": "repository", "repositories": repositories,
	}, nil
}

func extractOwnershipJSON(reader readers.Reader) (interface{}, error) {
	names, matrices, err := reader.GetOwnershipBurndown()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"type": "ownership", "file_names": names, "matrices": matrices}, nil
}

func extractOverwritesJSON(reader readers.Reader) (interface{}, error) {
	people, matrix, err := reader.GetPeopleInteraction()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"type": "overwrites_matrix", "people": people, "matrix": matrix}, nil
}

func extractFileCouplingJSON(reader readers.Reader) (interface{}, error) {
	names, matrix, err := reader.GetFileCooccurrence()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"file_names": names, "coupling_matrix": matrix}, nil
}

func extractPeopleCouplingJSON(reader readers.Reader) (interface{}, error) {
	names, matrix, err := reader.GetPeopleCooccurrence()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"people_names": names, "coupling_matrix": matrix}, nil
}

func extractShotnessCouplingJSON(reader readers.Reader) (interface{}, error) {
	names, matrix, err := reader.GetShotnessCooccurrence()
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"entity_names": names, "coupling_matrix": matrix}, nil
}

func extractShotnessJSON(reader readers.Reader) (interface{}, error) {
	records, err := reader.GetShotnessRecords()
	return jsonField("shotness_records", records, err)
}

func extractRuntimeStatsJSON(reader readers.Reader) (interface{}, error) {
	stats, err := reader.GetRuntimeStats()
	return jsonField("runtime_stats", stats, err)
}

func extractLanguageStatsJSON(reader readers.Reader) (interface{}, error) {
	stats, err := reader.GetLanguageStats()
	return jsonField("language_stats", stats, err)
}

func extractSentimentJSON(reader readers.Reader) (interface{}, error) {
	sentimentReader, ok := reader.(readers.SentimentReader)
	if !ok {
		return nil, fmt.Errorf("%w: sentiment", readers.ErrAnalysisMissing)
	}
	data, err := sentimentReader.GetSentimentByTick()
	return jsonField("sentiment_by_tick", data, err)
}

func extractTemporalActivityJSON(reader readers.Reader) (interface{}, error) {
	temporalReader, ok := reader.(readers.TemporalActivityReader)
	if !ok {
		return nil, fmt.Errorf("%w: temporal activity", readers.ErrAnalysisMissing)
	}
	data, err := temporalReader.GetTemporalActivity()
	return jsonField("temporal_activity", data, err)
}

func extractBusFactorJSON(reader readers.Reader) (interface{}, error) {
	busFactorReader, ok := reader.(readers.BusFactorReader)
	if !ok {
		return nil, fmt.Errorf("%w: bus factor", readers.ErrAnalysisMissing)
	}
	data, err := busFactorReader.GetBusFactor()
	return jsonField("bus_factor", data, err)
}

func extractOwnershipConcentrationJSON(reader readers.Reader) (interface{}, error) {
	ownershipReader, ok := reader.(readers.OwnershipConcentrationReader)
	if !ok {
		return nil, fmt.Errorf("%w: ownership concentration", readers.ErrAnalysisMissing)
	}
	data, err := ownershipReader.GetOwnershipConcentration()
	return jsonField("ownership_concentration", data, err)
}

func extractKnowledgeDiffusionJSON(reader readers.Reader) (interface{}, error) {
	diffusionReader, ok := reader.(readers.KnowledgeDiffusionReader)
	if !ok {
		return nil, fmt.Errorf("%w: knowledge diffusion", readers.ErrAnalysisMissing)
	}
	data, err := diffusionReader.GetKnowledgeDiffusion()
	return jsonField("knowledge_diffusion", data, err)
}

func extractHotspotRiskJSON(reader readers.Reader) (interface{}, error) {
	hotspotReader, ok := reader.(readers.HotspotRiskReader)
	if !ok {
		return nil, fmt.Errorf("%w: hotspot risk", readers.ErrAnalysisMissing)
	}
	data, err := hotspotReader.GetHotspotRisk()
	return jsonField("hotspot_risk", data, err)
}

func extractRefactoringProxyJSON(reader readers.Reader) (interface{}, error) {
	refactoringReader, ok := reader.(readers.RefactoringProxyReader)
	if !ok {
		return nil, fmt.Errorf("%w: refactoring proxy", readers.ErrAnalysisMissing)
	}
	data, err := refactoringReader.GetRefactoringProxy()
	return jsonField("refactoring_proxy", data, err)
}

func jsonField(key string, value interface{}, err error) (interface{}, error) {
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{key: value}, nil
}

// saveJSONResults saves the analysis results as JSON
func saveJSONResults(results map[string]interface{}, outputPath string) error {
	file, err := os.Create(outputPath) // #nosec G304 - JSON output path is explicitly requested by caller.
	if err != nil {
		return fmt.Errorf("failed to create JSON output file: %w", err)
	}

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ") // Pretty print

	// Add metadata
	output := map[string]interface{}{
		"meta": map[string]interface{}{
			"generated_by":   "labours-go",
			"generated_at":   time.Now().Format(time.RFC3339),
			"modes_executed": len(results),
		},
		"results": results,
	}

	if err := encoder.Encode(output); err != nil {
		_ = file.Close()
		return fmt.Errorf("encode JSON output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close JSON output: %w", err)
	}
	return nil
}
