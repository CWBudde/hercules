package render

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/cwbudde/hercules/internal/render/readers"
)

// extractModeDataForJSON extracts raw reader data for JSON output without rendering plots.
func extractModeDataForJSON(reader readers.Reader, mode string) (interface{}, error) {
	switch mode {
	case "devs":
		stats, err := reader.GetDeveloperStats()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"developer_stats": stats}, nil
	case "devs-efforts", "old-vs-new", "devs-parallel":
		data, err := reader.GetDeveloperTimeSeriesData()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"developer_time_series": data}, nil
	case "burndown-project":
		header, name, matrix, err := reader.GetProjectBurndownWithHeader()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "burndown", "target": "project", "header": header, "name": name, "matrix": matrix}, nil
	case "burndown-file":
		files, err := reader.GetFilesBurndown()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "burndown", "target": "file", "files": files}, nil
	case "burndown-person":
		people, err := reader.GetPeopleBurndown()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "burndown", "target": "person", "people": people}, nil
	case "burndown-repository", "burndown-repos-combined":
		repoReader, ok := reader.(readers.RepositoryBurndownReader)
		if !ok {
			return nil, fmt.Errorf("%w: repository burndown", readers.ErrAnalysisMissing)
		}
		repos, err := repoReader.GetRepositoriesBurndown()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "burndown", "target": "repository", "repositories": repos}, nil
	case "ownership":
		names, matrices, err := reader.GetOwnershipBurndown()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "ownership", "file_names": names, "matrices": matrices}, nil
	case "overwrites-matrix":
		people, matrix, err := reader.GetPeopleInteraction()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"type": "overwrites_matrix", "people": people, "matrix": matrix}, nil
	case "couples-files":
		names, matrix, err := reader.GetFileCooccurrence()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"file_names": names, "coupling_matrix": matrix}, nil
	case "couples-people":
		names, matrix, err := reader.GetPeopleCooccurrence()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"people_names": names, "coupling_matrix": matrix}, nil
	case "couples-shotness":
		names, matrix, err := reader.GetShotnessCooccurrence()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"entity_names": names, "coupling_matrix": matrix}, nil
	case "shotness":
		records, err := reader.GetShotnessRecords()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"shotness_records": records}, nil
	case "run-times":
		stats, err := reader.GetRuntimeStats()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"runtime_stats": stats}, nil
	case "languages":
		stats, err := reader.GetLanguageStats()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"language_stats": stats}, nil
	case "sentiment":
		sentimentReader, ok := reader.(readers.SentimentReader)
		if !ok {
			return nil, fmt.Errorf("%w: sentiment", readers.ErrAnalysisMissing)
		}
		data, err := sentimentReader.GetSentimentByTick()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"sentiment_by_tick": data}, nil
	case "temporal-activity":
		temporalReader, ok := reader.(readers.TemporalActivityReader)
		if !ok {
			return nil, fmt.Errorf("%w: temporal activity", readers.ErrAnalysisMissing)
		}
		data, err := temporalReader.GetTemporalActivity()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"temporal_activity": data}, nil
	case "bus-factor":
		busFactorReader, ok := reader.(readers.BusFactorReader)
		if !ok {
			return nil, fmt.Errorf("%w: bus factor", readers.ErrAnalysisMissing)
		}
		data, err := busFactorReader.GetBusFactor()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"bus_factor": data}, nil
	case "ownership-concentration":
		ownershipReader, ok := reader.(readers.OwnershipConcentrationReader)
		if !ok {
			return nil, fmt.Errorf("%w: ownership concentration", readers.ErrAnalysisMissing)
		}
		data, err := ownershipReader.GetOwnershipConcentration()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"ownership_concentration": data}, nil
	case "knowledge-diffusion":
		diffusionReader, ok := reader.(readers.KnowledgeDiffusionReader)
		if !ok {
			return nil, fmt.Errorf("%w: knowledge diffusion", readers.ErrAnalysisMissing)
		}
		data, err := diffusionReader.GetKnowledgeDiffusion()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"knowledge_diffusion": data}, nil
	case "hotspot-risk":
		hotspotReader, ok := reader.(readers.HotspotRiskReader)
		if !ok {
			return nil, fmt.Errorf("%w: hotspot risk", readers.ErrAnalysisMissing)
		}
		data, err := hotspotReader.GetHotspotRisk()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"hotspot_risk": data}, nil
	case "refactoring-proxy":
		refactoringReader, ok := reader.(readers.RefactoringProxyReader)
		if !ok {
			return nil, fmt.Errorf("%w: refactoring proxy", readers.ErrAnalysisMissing)
		}
		data, err := refactoringReader.GetRefactoringProxy()
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"refactoring_proxy": data}, nil
	}

	return nil, fmt.Errorf("JSON output is not implemented for mode %s", mode)
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
