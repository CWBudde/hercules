package leaves

import (
	"fmt"

	"github.com/gogo/protobuf/proto"

	"github.com/cwbudde/hercules/internal/analysisio"
)

var (
	ErrAnalysisMalformed          = analysisio.ErrAnalysisMalformed
	ErrAnalysisTooLarge           = analysisio.ErrAnalysisTooLarge
	ErrAnalysisVersionUnsupported = analysisio.ErrAnalysisVersionUnsupported
)

func unmarshalAnalysis(data []byte, message proto.Message) error {
	err := analysisio.Unmarshal(data, message, analysisio.DefaultLimits())
	if err != nil {
		return fmt.Errorf("validate analysis protobuf: %w", err)
	}

	return nil
}
