package leaves

import (
	"fmt"
	"slices"
	"sort"

	"github.com/cwbudde/hercules/internal/core"
	"github.com/cwbudde/hercules/internal/join"
)

// MergeResults combines two OnboardingResult-s together.
//
// Onboarding snapshots are anchored at each repository's own first commit by an author, so summing
// them across repositories counts a 2023 first touch of repository B inside the 30-day ramp-up of
// somebody who actually joined in 2020 via repository A. That is a wrong chart, not a rounding
// error. The merge therefore re-anchors: it takes the earliest first commit either side saw,
// concatenates the retained trails, truncates them back to the retention horizon, and recomputes
// both the snapshots and the cohorts from the result. Cohorts are never merged from the parts —
// re-anchoring an author can move them into a different month, so membership and averages change.
//
// The merge reads no receiver state: `hercules combine` summons the analysis zero-valued and calls
// neither Configure nor Initialize, so every configuration value comes off the two results.
//
// There is deliberately no QualifyPaths counterpart: OnboardingResult exposes no path anywhere,
// and the trail's file counters are computed per repository and then summed. Repository A's
// README.md and repository B's README.md therefore count as two files, which is exactly what an
// unqualified path-keyed merge would get wrong.
func (oa *OnboardingAnalysis) MergeResults(
	result1, result2 any, common1, common2 *core.CommonAnalysisResult,
) any {
	first, err := requiredResult[OnboardingResult](result1)
	if err != nil {
		return fmt.Errorf("merge onboarding first result: %w", err)
	}

	second, err := requiredResult[OnboardingResult](result2)
	if err != nil {
		return fmt.Errorf("merge onboarding second result: %w", err)
	}

	err = validateOnboardingMergeInputs(&first, &second)
	if err != nil {
		return err
	}

	people, mergedPeopleDict := join.PeopleIdentities(
		first.reversedPeopleDict, second.reversedPeopleDict,
	)
	offset1, offset2 := mergedTickOffsets(common1, common2, first.tickSize)

	windowDays := slices.Clone(first.WindowDays)
	trailDays := onboardingTrailDays(windowDays)

	authors := make(map[int]*AuthorOnboardingData, len(first.Authors)+len(second.Authors))
	foldOnboardingAuthors(authors, first.Authors, people.First, offset1)
	foldOnboardingAuthors(authors, second.Authors, people.Second, offset2)

	for _, author := range authors {
		sortOnboardingTrail(author.Trail)

		author.Trail = truncateOnboardingTrail(author.Trail, author.FirstCommitUnix, trailDays)
		author.Snapshots = snapshotsFromTrail(author.Trail, author.FirstCommitUnix, windowDays)
	}

	return OnboardingResult{
		Authors:             authors,
		Cohorts:             recomputeOnboardingCohorts(authors),
		WindowDays:          windowDays,
		MeaningfulThreshold: first.MeaningfulThreshold,
		TrailDays:           trailDays,
		reversedPeopleDict:  mergedPeopleDict,
		tickSize:            first.tickSize,
	}
}

// validateOnboardingMergeInputs rejects pairs which cannot be combined at all.
//
// The tick size, the meaningful threshold and the windows all come from global flags and none of
// them is comparable across repositories. The threshold is unrecoverable in particular:
// reclassifying commits needs the per-file line statistics, which the trail deliberately drops.
func validateOnboardingMergeInputs(first, second *OnboardingResult) error {
	if first.tickSize != second.tickSize {
		return fmt.Errorf("%w (r1: %s, r2: %s) received",
			errOnboardingMismatchingTickSizes, first.tickSize, second.tickSize)
	}

	if first.MeaningfulThreshold != second.MeaningfulThreshold {
		return fmt.Errorf("%w (r1: %d, r2: %d) received",
			errOnboardingMismatchingThresholds,
			first.MeaningfulThreshold, second.MeaningfulThreshold)
	}

	if !slices.Equal(first.WindowDays, second.WindowDays) {
		return fmt.Errorf("%w (r1: %v, r2: %v) received",
			errOnboardingMismatchingWindows, first.WindowDays, second.WindowDays)
	}

	err := validateOnboardingTrail(first)
	if err != nil {
		return err
	}

	return validateOnboardingTrail(second)
}

// validateOnboardingTrail rejects a result whose trail cannot answer the windows asked of it —
// above all one written before the trail existed. Falling back silently would produce the wrong
// chart, which is the whole point of storing the trail; erroring is safe because combine reports
// per input, so one stale file excludes itself rather than killing the run.
func validateOnboardingTrail(result *OnboardingResult) error {
	if len(result.Authors) == 0 {
		return nil
	}

	required := onboardingTrailDays(result.WindowDays)
	if result.TrailDays < required {
		return fmt.Errorf("%w (trail_days: %d, windows need %d)",
			errOnboardingMissingTrail, result.TrailDays, required)
	}

	for authorID, author := range result.Authors {
		if author == nil {
			continue
		}

		if len(author.Snapshots) > 0 && len(author.Trail) == 0 {
			return fmt.Errorf("%w (author %d has snapshots but no trail)",
				errOnboardingMissingTrail, authorID)
		}
	}

	return nil
}

// foldOnboardingAuthors folds one side into the merged author map, rebasing its tick axis and
// translating its author indices.
//
// Two source authors can map onto the same merged author when the identity join collapses
// aliases, so the trails are concatenated rather than overwritten.
func foldOnboardingAuthors(
	target, source map[int]*AuthorOnboardingData, identities []int, offset int,
) {
	for authorID, author := range source {
		if author == nil {
			continue
		}

		mergedID := mapOwnershipAuthor(authorID, identities)

		merged := target[mergedID]
		if merged == nil {
			merged = &AuthorOnboardingData{}
			target[mergedID] = merged

			setOnboardingAnchor(merged, author, offset)
		} else if onboardingAnchorWins(author, merged, offset) {
			setOnboardingAnchor(merged, author, offset)
		}

		merged.Trail = append(merged.Trail, author.Trail...)
	}
}

// setOnboardingAnchor copies one side's anchor onto the merged author.
//
// JoinCohort is carried over verbatim instead of being recomputed from FirstCommitUnix: the cohort
// is the calendar month in that commit's own UTC offset, so a commit at 2025-01-01T00:30+02:00
// belongs to 2025-01 while its Unix seconds read as 2024-12-31T22:30Z. FirstCommitTick is rebased
// rather than derived from FirstCommitUnix, because it is tick-quantised and committer-time
// derived; deriving it would make merged and single-repository values disagree for every rebased
// commit.
func setOnboardingAnchor(merged, source *AuthorOnboardingData, offset int) {
	merged.FirstCommitTick = source.FirstCommitTick + offset
	merged.FirstCommitUnix = source.FirstCommitUnix
	merged.JoinCohort = source.JoinCohort
}

// onboardingAnchorWins reports whether the candidate anchors the merged author. It is a strict
// total order — earliest first commit, then the lexicographically smaller cohort, then the earlier
// rebased tick — so the outcome does not depend on the order the sides are folded in.
func onboardingAnchorWins(candidate, current *AuthorOnboardingData, offset int) bool {
	if candidate.FirstCommitUnix != current.FirstCommitUnix {
		return candidate.FirstCommitUnix < current.FirstCommitUnix
	}

	if candidate.JoinCohort != current.JoinCohort {
		return candidate.JoinCohort < current.JoinCohort
	}

	return candidate.FirstCommitTick+offset < current.FirstCommitTick
}

// sortOnboardingTrail orders a concatenated trail ascending by timestamp. The counters break ties
// so that merging two sides in either order yields the identical trail; entries sharing a
// timestamp are in any case either all inside a window or all outside it, so the snapshots do not
// depend on the tie-break.
func sortOnboardingTrail(trail []OnboardingTrailEntry) {
	sort.SliceStable(trail, func(i, j int) bool {
		left, right := trail[i], trail[j]
		if left.UnixTime != right.UnixTime {
			return left.UnixTime < right.UnixTime
		}

		leftCounters := [...]int{
			left.Commits, left.NewFiles, left.Lines,
			left.MeaningfulCommits, left.NewMeaningfulFiles, left.MeaningfulLines,
		}
		rightCounters := [...]int{
			right.Commits, right.NewFiles, right.Lines,
			right.MeaningfulCommits, right.NewMeaningfulFiles, right.MeaningfulLines,
		}

		return slices.Compare(leftCounters[:], rightCounters[:]) < 0
	})
}

// truncateOnboardingTrail drops everything past the retention horizon, inclusively.
//
// This is what keeps trail growth bounded across a large combine and it is exact: a later merge
// can only move the anchor earlier, which shrinks the upper end of every window, so the set any
// future merge needs is always a prefix of what is kept here.
func truncateOnboardingTrail(
	trail []OnboardingTrailEntry, anchorUnix int64, trailDays int,
) []OnboardingTrailEntry {
	if len(trail) == 0 {
		return nil
	}

	horizon, err := onboardingWindowBoundary(anchorUnix, trailDays)
	if err != nil {
		return nil
	}

	for index, entry := range trail {
		if entry.UnixTime > horizon {
			return trail[:index:index]
		}
	}

	return trail
}
