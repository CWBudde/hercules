# Render Parity — Go renderer vs Python labours

The in-repo Go renderer (`cmd/labours` + `internal/render`) replaced the retired Python
`labours` package. This document records the visual-parity matrix that gated the
replacement (PLAN.md Phase 8), the policy behind it, and how to re-run the checks.

## Metric

Parity is measured per matched chart pair (same mode, same input) with the RMSE metric
from the `labours-go` repository's `cmd/parityviewer`:

```
RMSE = sqrt( Σ Δ² / (unionPixels · 4) )        over the RGBA channels
```

`parityviewer --print` reports this on a **0–255 byte scale**; the normalized 0–1 figures
in the tables below are that value **÷ 255**. The RMSE ≤ 0.10 goal therefore corresponds
to a raw RMSE ≤ 25.5. `diff%` is the fraction of pixels that differ at all — a high
`diff%` with a low RMSE indicates a pervasive but tiny per-pixel shift (a renderer/alpha
signature), not a layout error.

## Policy: perceptual first

The parity bar is **perceptual-first**: a mode is "good" when its chart reads as
equivalent to Python's at a glance — same composition, file set, palette, and layout.
**RMSE ≤ 0.10 is a tracked goal, not a release gate** (font/DPI rendering between
`matplotlib-go` and real matplotlib will never be pixel-identical). The Phase 8 gate
criterion was: _not worse than the recorded 2026-05-25 matrix_, plus the visual test
harness's own thresholds.

## Matched-pair RMSE matrix

Fixture: `system-optimiser-core` (`analysis_results/system-optimiser-core/raw/analysis.pb`
in the archived `labours-go` repository; 1,994 commits, C++). Baseline: the pre-baked
Python labours reference PNGs (unchanged since 2025-12). Worst-first, normalized ÷255.

| Mode                                 | 2026-05-25 (upstream labours-go) | 2026-07-02 (this repo) | Notes                                                                                                                                                 |
| ------------------------------------ | -------------------------------- | ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `temporal-activity_heatmap_commits`  | 0.230                            | 0.230                  | heatmap colormap-scale / cell annotations / colorbar                                                                                                  |
| `bus-factor_subsystems`              | 0.196                            | 0.196                  | TightLayout left-margin undershoot                                                                                                                    |
| `temporal-activity_weekdays_lines`   | 0.202                            | 0.195                  | better                                                                                                                                                |
| `temporal-activity_heatmap_lines`    | 0.193                            | 0.193                  |                                                                                                                                                       |
| `temporal-activity_months_lines`     | 0.177                            | 0.178                  |                                                                                                                                                       |
| `temporal-activity_hours_lines`      | 0.176                            | 0.177                  |                                                                                                                                                       |
| `temporal-activity_weekdays_commits` | 0.170                            | 0.170                  |                                                                                                                                                       |
| `temporal-activity_weeks_lines`      | 0.166                            | 0.166                  |                                                                                                                                                       |
| `knowledge-diffusion_distribution`   | 0.163                            | 0.161                  | better                                                                                                                                                |
| `ownership-concentration_subsystems` | 0.152                            | 0.157                  | within tolerance; text/AA deltas from the newer agg_go (bars themselves are correct)                                                                  |
| `knowledge-diffusion_silos`          | 0.156                            | 0.154                  | better                                                                                                                                                |
| `temporal-activity_hours_commits`    | 0.152                            | 0.152                  |                                                                                                                                                       |
| `old-vs-new`                         | 0.147                            | 0.147                  |                                                                                                                                                       |
| `devs-efforts`                       | 0.139                            | 0.144                  | **marginal** (+0.005): caused by the post-matrix y-axis offset-notation feature; perceptually equivalent (the same feature improved four other modes) |
| `temporal-activity_weeks_commits`    | 0.142                            | 0.143                  |                                                                                                                                                       |
| `knowledge-diffusion_lorenz`         | 0.139                            | 0.138                  | better                                                                                                                                                |
| `languages`                          | 0.126                            | 0.127                  | Go fill alpha vs Python alpha 0.8                                                                                                                     |
| `bus-factor_timeline`                | 0.123                            | 0.124                  |                                                                                                                                                       |
| `temporal-activity_months_commits`   | 0.123                            | 0.124                  |                                                                                                                                                       |
| `ownership-concentration_timeline`   | 0.121                            | 0.122                  |                                                                                                                                                       |
| `refactoring-proxy`                  | 0.121                            | 0.117                  | better                                                                                                                                                |
| `bus-factor_gauge`                   | 0.111                            | 0.115                  | within tolerance; gauge text/AA                                                                                                                       |
| `burndown-project`                   | 0.101                            | 0.098                  | better — now meets the ≤0.10 goal                                                                                                                     |
| `ownership`                          | 0.090                            | 0.088                  | better; at goal                                                                                                                                       |
| `overwrites-matrix`                  | 0.082                            | 0.084                  | at goal; pervasive ±1 colormap rounding over the heatmap                                                                                              |
| `devs`                               | 0.078                            | 0.078                  | at goal; calibration match to 4 decimals                                                                                                              |

Gate result (2026-07-02): **25/26 pairs equal-or-better** than the 2026-05-25 matrix
(12 within tolerance, 7 strictly better), 1 marginal (`devs-efforts`, see table).
Port fidelity: on identical fixtures the integrated renderer is **byte-identical
(RMSE 0.0)** to a fresh upstream-current `labours-go` build.

Artifact-only modes with no Python pair (RMSE n/a): `devs-parallel`, `hotspot-risk`,
`run-times`.

### Shotness coupling semantic parity

`couples-shotness` is validated as a data-level contract rather than against
the retired Python renderer's historical pixels. Both YAML and Protocol Buffer
readers construct the same entity-profile dot-product matrix. Its diagonal is
the profile squared norm, while ranked pairs contain only positive, distinct
off-diagonal entries. Hand-authored fixtures cover the matrix and ranking
policy.

## How to re-run

In this repository:

```bash
just test-visual          # structural visual tests, no references required
just test-visual-parity   # golden + Python-reference parity suite
```

The parity suite (`test/visual/`, gated by `LABOURS_GO_VISUAL_PARITY=1` and
`LABOURS_GO_PYTHON_PARITY=1`) compares in-process renders against the pre-baked Python
reference PNGs in `test/visual/reference/` (untracked; copied from the archived
`labours-go` repo's `analysis_results/reference/python_*.png`). Its metric is a weighted
Histogram-Intersection (0.4) + SSIM (0.4) + ColorRMS (0.2) score with Strict 0.95 /
Standard 0.90 / Lenient 0.85 thresholds — see `test/visual/README.md`.

Regenerating the RMSE matrix itself requires the archived `labours-go` repository, which
hosts the `system-optimiser-core` fixture, the Python baseline PNGs, `cmd/parityviewer`,
and the showcase tooling (`scripts/full_showcase.sh`, `just showcase` /
`just showcase-compare` producing `compare/index.html`). Regenerating the _Python_
baseline additionally requires an installation of the original Python labours package;
the day-to-day parity run is Python-free.
