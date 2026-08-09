package render

// Mode names accepted by the `--mode` CLI flag. These strings are part of the
// public CLI contract and double as the per-mode keys of the JSON output, so
// their values must never change. Every mode table in this package
// (modeHandlers, jsonModeExtractors, validModeNames, modeOutputConventions)
// and the --from-repo requirement table in cmd/labours are keyed by these
// constants; declaring them once keeps the tables from drifting apart.
const (
	// Aliases resolved by ResolveModes into one or more concrete modes.

	// ModeAll expands to the Python labours "all" mode composition.
	ModeAll = "all"
	// ModeBurndown is the Python labours alias for ModeBurndownProject.
	ModeBurndown = "burndown"
	// ModeCouples is the Python labours alias fanning out to the three
	// coupling modes.
	ModeCouples = "couples"

	// Concrete modes.

	// ModeBurndownProject renders the project-wide line burndown.
	ModeBurndownProject = "burndown-project"
	// ModeBurndownFile renders one burndown chart per file.
	ModeBurndownFile = "burndown-file"
	// ModeBurndownPerson renders one burndown chart per developer.
	ModeBurndownPerson = "burndown-person"
	// ModeBurndownRepository renders one burndown chart per repository.
	ModeBurndownRepository = "burndown-repository"
	// ModeBurndownReposCombined renders the combined multi-repository burndown.
	ModeBurndownReposCombined = "burndown-repos-combined"
	// ModeOverwritesMatrix renders the code overwrites matrix.
	ModeOverwritesMatrix = "overwrites-matrix"
	// ModeOwnership renders the code ownership burndown.
	ModeOwnership = "ownership"
	// ModeCouplesFiles renders the file coupling projector assets.
	ModeCouplesFiles = "couples-files"
	// ModeCouplesPeople renders the developer coupling projector assets.
	ModeCouplesPeople = "couples-people"
	// ModeCouplesShotness renders the structural-hotness coupling charts.
	ModeCouplesShotness = "couples-shotness"
	// ModeShotness renders the structural hotness statistics and charts.
	ModeShotness = "shotness"
	// ModeDevs renders the developer activity chart.
	ModeDevs = "devs"
	// ModeDevsEfforts renders the developer efforts chart.
	ModeDevsEfforts = "devs-efforts"
	// ModeDevsParallel renders the parallel-coordinates developer chart.
	ModeDevsParallel = "devs-parallel"
	// ModeOldVsNew renders the additions-versus-changes chart.
	ModeOldVsNew = "old-vs-new"
	// ModeLanguages renders the per-language statistics chart.
	ModeLanguages = "languages"
	// ModeTemporalActivity renders the temporal activity chart set.
	ModeTemporalActivity = "temporal-activity"
	// ModeRunTimes renders the pipeline runtime summary.
	ModeRunTimes = "run-times"
	// ModeBusFactor renders the bus-factor charts.
	ModeBusFactor = "bus-factor"
	// ModeOwnershipConcentration renders the ownership concentration charts.
	ModeOwnershipConcentration = "ownership-concentration"
	// ModeKnowledgeDiffusion renders the knowledge diffusion charts.
	ModeKnowledgeDiffusion = "knowledge-diffusion"
	// ModeHotspotRisk renders the hotspot risk chart and table.
	ModeHotspotRisk = "hotspot-risk"
	// ModeSentiment renders the commit sentiment charts.
	ModeSentiment = "sentiment"
	// ModeRefactoringProxy renders the refactoring proxy chart.
	ModeRefactoringProxy = "refactoring-proxy"
	// ModeOnboarding renders the developer onboarding ramp-up charts.
	ModeOnboarding = "onboarding"
)
