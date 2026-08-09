package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/cwbudde/hercules/internal/render"
	"github.com/cwbudde/hercules/internal/render/graphics"
)

var rootCmd = &cobra.Command{
	Use:           "labours",
	Short:         "Labours CLI for analyzing git repository data",
	Long:          "Labours CLI for analyzing git repository data, visualizing trends, and generating reports.",
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE:          runLaboursCommand,
}

// Theme names accepted by --theme and produced by the --style mapping below.
// These values are the user-facing CLI contract (they name the built-in
// graphics themes), so they must stay byte-identical.
const (
	themeDefault    = "default"
	themeDark       = "dark"
	themeMinimal    = "minimal"
	themeVibrant    = "vibrant"
	themeMatplotlib = "matplotlib"
)

// helpMaxRepos is hoisted out of the flag registration because it does not fit
// on a single source line. The concatenation does not change the text.
const helpMaxRepos = "Maximum repositories shown individually in burndown-repos-combined; " +
	"the rest are aggregated into an \"Other\" band. 0 disables the limit."

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)
	initializeFlags()
	bindFlagsToViper()
}

func initializeFlags() {
	initializeInputOutputFlags()
	initializePlotFlags()
	initializeModeFlags()
	initializeDetailFlags()
	initializeThemeFlags()
	initializeHerculesFlags()
}

func initializeInputOutputFlags() {
	rootCmd.PersistentFlags().StringP(
		"output", "o", "",
		"Path to output file/directory; burndown-file and burndown-person use a file path "+
			"as a fan-out basename with safe hashed suffixes. JSON extension saves data instead of images",
	)
	rootCmd.PersistentFlags().StringP("input", "i", "-", "Path to input file")
	rootCmd.PersistentFlags().StringP("input-format", "f", "auto", "Input format")
}

func initializePlotFlags() {
	rootCmd.PersistentFlags().Int("font-size", 12, "Size of labels and legend")
	rootCmd.PersistentFlags().String("style", "ggplot", "Plot style to use")
	rootCmd.PersistentFlags().String("backend", "", "Matplotlib backend")
	rootCmd.PersistentFlags().String("background", "white", "Plot's general color scheme")
	rootCmd.PersistentFlags().String("size", "", "Axes' size in inches, e.g. \"12,9\"")
	rootCmd.PersistentFlags().Bool("no-burndown-title", false, "Suppress titles on burndown and ownership charts")
	rootCmd.PersistentFlags().Bool("relative", false, "Occupy 100% height for every measurement")
	rootCmd.PersistentFlags().String("tmpdir", "", "Temporary directory for intermediate files")
}

func initializeModeFlags() {
	rootCmd.PersistentFlags().StringSliceP(
		"modes", "m", []string{},
		"What to plot, can be repeated; burndown-person fans out per contributor, "+
			"while ownership writes one combined developer stack",
	)
	rootCmd.PersistentFlags().StringSlice(
		"mode", []string{},
		"What to plot; Python-compatible alias for --modes (burndown-person fans out, ownership combines developers)",
	)
	rootCmd.PersistentFlags().String("resample", "year", "Resample time series method")
	rootCmd.PersistentFlags().String("start-date", "", "Start date for time-based plots")
	rootCmd.PersistentFlags().String("end-date", "", "End date for time-based plots")
	rootCmd.PersistentFlags().Bool("disable-projector", false, "Do not run Tensorflow Projector")
	rootCmd.PersistentFlags().Int("max-people", 20, "Maximum developers in matrix and people plots")
	rootCmd.PersistentFlags().Int("max-repos", 25, helpMaxRepos)
	rootCmd.PersistentFlags().Bool(
		"order-ownership-by-time", false,
		"Sort developers in the ownership plot by their first appearance in the history.",
	)
	rootCmd.PersistentFlags().Int(
		"temporal-legend-threshold", 32,
		"Maximum number of developers to show legend for in temporal activity charts. 0 disables the limit.",
	)
	rootCmd.PersistentFlags().Int(
		"temporal-legend-single-col-threshold", 10,
		"Maximum number of developers for single-column legend in temporal activity charts.",
	)
	rootCmd.PersistentFlags().Bool("sentiment", false, "Include sentiment analysis in the output (Python compatibility)")
	rootCmd.PersistentFlags().Bool(
		"sentiment-fallback", false,
		"Allow heuristic sentiment charts when collected sentiment data is missing",
	)
	rootCmd.PersistentFlags().Bool(
		"devs-parallel-fallback", false,
		"Allow synthetic devs-parallel charts when people burndown data is missing",
	)
}

// initializeDetailFlags registers the Go-only auxiliary plots. They are off by
// default so the rendered file set mirrors Python labours; enable them to
// render the extra Go-specific charts.
func initializeDetailFlags() {
	rootCmd.PersistentFlags().Bool(
		"run-times-detail", false,
		"Render the Go-only run-times breakdown chart (Python labours is text-only)",
	)
	rootCmd.PersistentFlags().Bool(
		"devs-efforts-detail", false,
		"Also render the Go-only developer productivity ranking chart",
	)
	rootCmd.PersistentFlags().Bool(
		"devs-parallel-detail", false,
		"Also render the Go-only devs-parallel concurrency timeline sibling chart",
	)
	rootCmd.PersistentFlags().Bool(
		"knowledge-diffusion-detail", false,
		"Also render the Go-only knowledge-diffusion trend chart",
	)
}

func initializeThemeFlags() {
	// Progress and output control flags
	rootCmd.PersistentFlags().BoolP("quiet", "q", false, "Disable progress bars and reduce output")
	rootCmd.PersistentFlags().Bool("verbose", false, "Enable verbose output with detailed progress information")

	// Theme-related flags
	rootCmd.PersistentFlags().String(
		"theme", themeDefault,
		"Theme to use for visualization (default, dark, minimal, vibrant, matplotlib)",
	)
	rootCmd.PersistentFlags().Bool("list-themes", false, "List all available themes and exit")
	rootCmd.PersistentFlags().String("export-theme", "", "Export a built-in theme to file for customization")
	rootCmd.PersistentFlags().String("load-theme", "", "Load custom theme from file")
	rootCmd.PersistentFlags().Bool(
		"matplotlib-colors", false,
		"Force matplotlib color scheme for burndown charts (Red #d62728 bottom, Blue #1f77b4 top)",
	)
}

func initializeHerculesFlags() {
	rootCmd.PersistentFlags().String("hercules", "", "Path to hercules binary (empty for auto-detection)")
	rootCmd.PersistentFlags().String("from-repo", "", "Analyze git repository directly using hercules")
	rootCmd.PersistentFlags().String("hercules-flags", "", "Additional flags to pass to hercules")
}

func bindFlagsToViper() {
	if err := viper.BindPFlags(rootCmd.PersistentFlags()); err != nil {
		fmt.Fprintf(os.Stderr, "Error binding flags: %v\n", err)
		os.Exit(1)
	}
}

func initConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("$HOME/.labours-go")

	if err := viper.ReadInConfig(); err == nil {
		if viper.GetBool("verbose") {
			fmt.Println("Using config file:", viper.ConfigFileUsed())
		}
	}

	// Load user themes from standard directories
	if err := graphics.LoadUserThemes(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load user themes: %v\n", err)
	}
}

func runLaboursCommand(cmd *cobra.Command, args []string) error {
	handled, err := handleImmediateLaboursCommand(cmd, args)
	if err != nil || handled {
		return err
	}

	themeName, err := configureLaboursTheme()
	if err != nil {
		return err
	}
	rendererOptions, err := renderOptionsFromViper(themeName)
	if err != nil {
		return err
	}
	rendererOptions.SentimentFallback = commandBoolFlag(cmd, "sentiment-fallback")
	rendererOptions.DevsParallelFallback = commandBoolFlag(cmd, "devs-parallel-fallback")

	if repoPath := viper.GetString("from-repo"); repoPath != "" {
		return handleHerculesIntegration(cmd.Context(), repoPath, rendererOptions)
	}
	return renderLaboursInput(rendererOptions)
}

func handleImmediateLaboursCommand(cmd *cobra.Command, args []string) (bool, error) {
	if viper.GetBool("version") {
		versionCmd.Run(cmd, args)
		return true, nil
	}
	if viper.GetBool("list-themes") {
		listThemes()
		return true, nil
	}
	if exportTheme := viper.GetString("export-theme"); exportTheme != "" {
		return true, handleExportTheme(exportTheme)
	}
	return false, nil
}

func configureLaboursTheme() (string, error) {
	if loadTheme := viper.GetString("load-theme"); loadTheme != "" {
		if err := graphics.GlobalThemeManager.LoadThemeFromFile(loadTheme); err != nil {
			return "", fmt.Errorf("load custom theme: %w", err)
		}
	}

	themeName := viper.GetString("theme")
	styleName := viper.GetString("style")
	if styleName != "ggplot" && styleName != "" {
		mappedTheme := mapStyleToTheme(styleName)
		if mappedTheme != "" {
			themeName = mappedTheme
			if !viper.GetBool("quiet") {
				fmt.Printf("Mapping matplotlib style '%s' to theme '%s'\n", styleName, mappedTheme)
			}
		}
	}
	if _, err := graphics.GetTheme(themeName); err != nil {
		return "", fmt.Errorf(
			"set theme %q (available: %v): %w",
			themeName, graphics.ListThemes(), err,
		)
	}
	if viper.GetBool("matplotlib-colors") {
		themeName = themeMatplotlib
		if !viper.GetBool("quiet") {
			fmt.Printf("Using matplotlib color scheme (Red #d62728 bottom, Blue #1f77b4 top)\n")
		}
	}
	return themeName, nil
}

func renderLaboursInput(rendererOptions render.Options) error {
	input, inputFormat := viper.GetString("input"), viper.GetString("input-format")
	inputFormat, err := render.NormalizeInputFormat(inputFormat)
	if err != nil {
		return err
	}
	startDate, endDate, err := parseDates()
	if err != nil {
		return err
	}
	if err := validateDateRange(startDate, endDate); err != nil {
		return err
	}
	modes, err := resolveModes()
	if err != nil {
		return err
	}
	if viper.GetBool("sentiment") {
		modes = append(modes, render.ModeSentiment)
		fmt.Println("Added sentiment analysis mode (--sentiment flag)")
	}
	reader, err := detectAndReadInput(input, inputFormat)
	if err != nil {
		return err
	}
	rendererOptions.Output = viper.GetString("output")
	rendererOptions.StartTime, rendererOptions.EndTime = startDate, endDate
	return render.Run(reader, modes, rendererOptions).Err()
}

func renderOptionsFromViper(themeName string) (render.Options, error) {
	theme, err := graphics.GetTheme(themeName)
	if err != nil {
		return render.Options{}, fmt.Errorf("get renderer theme %q: %w", themeName, err)
	}
	opts := render.DefaultOptions()
	opts.Quiet = viper.GetBool("quiet")
	opts.Relative = viper.GetBool("relative")
	opts.Resample = viper.GetString("resample")
	opts.MaxPeople = viper.GetInt("max-people")
	opts.MaxRepos = viper.GetInt("max-repos")
	if opts.MaxRepos == 0 {
		opts.MaxRepos = -1
	}
	opts.Backend = viper.GetString("backend")
	opts.Background = viper.GetString("background")
	opts.FontSize = viper.GetInt("font-size")
	opts.Size = viper.GetString("size")
	opts.TempDir = viper.GetString("tmpdir")
	opts.NoBurndownTitle = viper.GetBool("no-burndown-title")
	opts.TemporalLegendThreshold = viper.GetInt("temporal-legend-threshold")
	if opts.TemporalLegendThreshold == 0 {
		opts.TemporalLegendThreshold = -1
	}
	opts.TemporalLegendSingleColumn = viper.GetInt("temporal-legend-single-col-threshold")
	opts.OrderOwnershipByTime = viper.GetBool("order-ownership-by-time")
	opts.DisableProjector = viper.GetBool("disable-projector")
	opts.RunTimesDetail = viper.GetBool("run-times-detail")
	opts.DevsEffortsDetail = viper.GetBool("devs-efforts-detail")
	opts.DevsParallelDetail = viper.GetBool("devs-parallel-detail")
	opts.KnowledgeDiffusionDetail = viper.GetBool("knowledge-diffusion-detail")
	opts.SentimentFallback = viper.GetBool("sentiment-fallback")
	opts.DevsParallelFallback = viper.GetBool("devs-parallel-fallback")
	opts.Theme = *theme
	return opts, nil
}

func commandBoolFlag(cmd *cobra.Command, name string) bool {
	flag := cmd.Flag(name)
	if flag == nil && cmd.Root() != nil {
		flag = cmd.Root().PersistentFlags().Lookup(name)
	}
	if flag != nil && flag.Changed {
		return flag.Value.String() == "true"
	}
	return viper.GetBool(name)
}

func listThemes() {
	fmt.Println("Available themes:")
	for _, theme := range graphics.ListThemes() {
		fmt.Printf("  - %s\n", theme)
	}
}

func handleExportTheme(themeName string) error {
	outputPath := themeName + "-theme.yaml"
	if err := graphics.GlobalThemeManager.ExportTheme(themeName, outputPath); err != nil {
		return fmt.Errorf("export theme %q: %w", themeName, err)
	}
	fmt.Printf("Theme '%s' exported to %s\n", themeName, outputPath)
	return nil
}

func handleHerculesIntegration(
	ctx context.Context, repoPath string, rendererOptions ...render.Options,
) error {
	modes, err := resolveModes()
	if err != nil {
		return err
	}
	if len(modes) == 0 {
		modes = []string{render.ModeBurndownProject, render.ModeDevs} // default modes
	}
	if viper.GetBool("sentiment") {
		modes = append(modes, render.ModeSentiment)
	}

	analyses, err := requiredAnalysesForModes(modes)
	if err != nil {
		return err
	}

	herculesName := viper.GetString("hercules")
	if herculesName == "" {
		herculesName = "hercules"
	}
	herculesPath, err := lookPath(herculesName)
	if err != nil {
		return fmt.Errorf(
			"find Hercules executable %q: %w; install Hercules or specify --hercules",
			herculesName, err,
		)
	}

	discoveredRepo, err := discoverRepository(ctx, repoPath)
	if err != nil {
		return err
	}

	if !viper.GetBool("quiet") {
		fmt.Printf("Using hercules: %s\n", herculesPath)
		fmt.Printf("Analyzing repository: %s\n", discoveredRepo)
		fmt.Printf("Running Hercules once with analyses: %s\n", strings.Join(analyses, ", "))
	}
	return runHerculesAndVisualize(
		ctx, herculesPath, discoveredRepo, modes, analyses, rendererOptions...,
	)
}

// mapStyleToTheme maps matplotlib style names to labours-go theme names.
func mapStyleToTheme(style string) string {
	styleToTheme := map[string]string{
		// Core matplotlib built-in styles
		"default":         themeDefault, // matplotlib default
		"classic":         themeDefault, // classic matplotlib -> default
		"ggplot":          themeDefault, // ggplot is our default
		"dark_background": themeDark,    // dark background -> dark theme
		"grayscale":       themeMinimal, // grayscale -> minimal
		"bmh":             themeVibrant, // Bayesian Methods for Hackers -> vibrant
		"fivethirtyeight": themeVibrant, // FiveThirtyEight -> vibrant
		"fast":            themeDefault, // fast style -> default

		// Seaborn styles (original and v0.8+ variants)
		"seaborn":            themeMinimal, // seaborn-like -> minimal
		"seaborn-v0_8":       themeMinimal, // newer seaborn -> minimal
		"seaborn-bright":     themeVibrant, // seaborn bright -> vibrant
		"seaborn-colorblind": themeDefault, // seaborn colorblind -> default
		"seaborn-dark":       themeDark,    // seaborn dark -> dark
		"seaborn-darkgrid":   themeDark,    // seaborn dark grid -> dark
		"seaborn-pastel":     themeMinimal, // seaborn pastel -> minimal
		"seaborn-white":      themeMinimal, // seaborn white -> minimal
		"seaborn-whitegrid":  themeDefault, // seaborn white grid -> default
		"seaborn-paper":      themeMinimal, // seaborn paper -> minimal
		"seaborn-poster":     themeVibrant, // seaborn poster -> vibrant
		"seaborn-talk":       themeDefault, // seaborn talk -> default
		"seaborn-notebook":   themeDefault, // seaborn notebook -> default
		"seaborn-muted":      themeMinimal, // seaborn muted -> minimal
		"seaborn-deep":       themeDark,    // seaborn deep -> dark
		"seaborn-ticks":      themeDefault, // seaborn ticks -> default

		// Tableau styles
		"tableau-colorblind10": themeDefault, // tableau -> default
		"tab10":                themeDefault, // tableau 10 colors -> default
		"tab20":                themeVibrant, // tableau 20 colors -> vibrant
		"tab20b":               themeVibrant, // tableau 20b -> vibrant
		"tab20c":               themeMinimal, // tableau 20c -> minimal

		// Solarized styles
		"Solarize_Light2": themeMinimal, // Solarized light -> minimal
		"solarized":       themeMinimal, // general solarized -> minimal
		"solarized-light": themeMinimal, // solarized light -> minimal
		"solarized-dark":  themeDark,    // solarized dark -> dark

		// Additional matplotlib styles
		"cyberpunk": themeDark,    // cyberpunk style -> dark
		"science":   themeMinimal, // science style -> minimal
		"ieee":      themeMinimal, // IEEE format -> minimal
		"nature":    themeDefault, // nature format -> default
		"grid":      themeDefault, // with grid -> default
		"no-latex":  themeDefault, // no LaTeX -> default

		// Common style variants and aliases (case-insensitive)
		"dark":       themeDark,
		"light":      themeDefault,
		"minimal":    themeMinimal,
		"vibrant":    themeVibrant,
		"colorful":   themeVibrant,
		"monochrome": themeMinimal,
		"black":      themeDark,
		"white":      themeMinimal,
		"bright":     themeVibrant,
		"muted":      themeMinimal,
		"pastel":     themeMinimal,
		"deep":       themeDark,
		"paper":      themeMinimal,
		"poster":     themeVibrant,
		"talk":       themeDefault,
		"notebook":   themeDefault,
		"whitegrid":  themeDefault,
		"darkgrid":   themeDark,
		"ticks":      themeDefault,

		// Color scheme aliases
		"blues":   themeMinimal,
		"greens":  themeMinimal,
		"greys":   themeMinimal,
		"oranges": themeVibrant,
		"purples": themeVibrant,
		"reds":    themeVibrant,
	}

	return styleToTheme[strings.ToLower(style)]
}
