package graphics

import "errors"

// Sentinel errors returned by this package. They are unexported because no
// caller outside the package matches on them; the plotting entry points only
// surface them as messages.

// Theme validation sentinels.
var (
	errThemeEmptyPalette = errors.New("theme must have at least one color in palette")
	errThemeMissingName  = errors.New("theme must have a name")
	errThemeTextSize     = errors.New("text size must be positive")
	errThemeFillOpacity  = errors.New("fill opacity must be between 0 and 1")
)

// errThemeNotFound reports a lookup for a theme the manager does not know.
var errThemeNotFound = errors.New("theme not found")

// Size parsing sentinels.
var (
	errInvalidSizeFormat     = errors.New("invalid size format")
	errNonPositiveDimensions = errors.New("dimensions must be positive")
	errDimensionsTooLarge    = errors.New("dimensions too large")
)

// Heatmap validation sentinels.
var (
	errHeatmapRowCountMismatch    = errors.New("heatmap row count mismatch")
	errHeatmapColumnCountMismatch = errors.New("heatmap column count mismatch")
)

// Burndown plotting sentinels.
var (
	errEmptyMatrixOrDateRange = errors.New("empty matrix or date range")
	errEmptyBurndownData      = errors.New("empty burndown data")
	errEmptyMatrix            = errors.New("empty matrix")
	errCreateBurndownAxes     = errors.New("failed to create burndown axes")
)

// Matplotlib plotting sentinels.
var (
	errCreateAxes                  = errors.New("failed to create axes")
	errNoDatesToPlot               = errors.New("no dates to plot")
	errNoSeriesToPlot              = errors.New("no series to plot")
	errNoLineDataToPlot            = errors.New("no line data to plot")
	errNoBarDataToPlot             = errors.New("no bar data to plot")
	errNoGroupedBarDataToPlot      = errors.New("no grouped bar data to plot")
	errNoScatterDataToPlot         = errors.New("no scatter data to plot")
	errNoStackedBarDataToPlot      = errors.New("no stacked bar data to plot")
	errNoEffortLayersToPlot        = errors.New("no effort layers to plot")
	errNotEnoughDates              = errors.New("not enough dates to plot devs-efforts time series")
	errCreateHeatmapImage          = errors.New("failed to create heatmap image")
	errSeriesValueCountMismatch    = errors.New("series value count mismatch")
	errBaselineValueCountMismatch  = errors.New("baseline value count mismatch")
	errLineSeriesLengthMismatch    = errors.New("line series x/y length mismatch")
	errBarLabelValueCountMismatch  = errors.New("bar labels and values length mismatch")
	errBarSeriesValueCountMismatch = errors.New("bar series value count mismatch")
)
