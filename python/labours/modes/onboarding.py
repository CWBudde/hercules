"""Onboarding ramp visualization for hercules analysis."""

import os
from argparse import Namespace
from typing import Dict, List, Sequence, Tuple

import numpy as np

from labours.plotting import apply_plot_style, deploy_plot, get_plot_path, import_pyplot


def build_cohort_heatmap(
    cohorts: Dict[str, Dict], window_days: Sequence[int], metric: str
) -> Tuple[List[str], List[str], np.ndarray]:
    """Build a cohort x window matrix from normalized onboarding data."""
    ordered_cohorts = sorted(cohorts.items())
    matrix = np.zeros((len(ordered_cohorts), len(window_days)), dtype=float)
    labels = []

    for row, (cohort_name, cohort) in enumerate(ordered_cohorts):
        labels.append("%s (n=%d)" % (cohort_name, cohort.get("author_count", 0)))
        snapshots = cohort.get("average_snapshots", {})
        for col, days in enumerate(window_days):
            matrix[row, col] = float(snapshots.get(days, {}).get(metric, 0.0))

    return labels, ["%dd" % days for days in window_days], matrix


def build_author_ramp_series(
    authors: Dict[int, Dict], people: Sequence[str], window_days: Sequence[int], metric: str
) -> List[Dict]:
    """Build per-author ramp series sorted by the latest available value."""
    series = []
    for author_id, author in authors.items():
        snapshots = author.get("snapshots", {})
        values = [float(snapshots.get(days, {}).get(metric, 0.0)) for days in window_days]
        if author_id >= 0 and author_id < len(people):
            author_name = _short_author_name(people[author_id])
        else:
            author_name = "author %d" % author_id
        series.append(
            {
                "author": author_name,
                "cohort": author.get("join_cohort", ""),
                "values": values,
            }
        )

    return sorted(series, key=lambda item: (-item["values"][-1], item["author"]))


def _short_author_name(identity: str) -> str:
    return identity.split("|", 1)[0] or identity


def _display_repo_name(name: str) -> str:
    return "Repository" if name in ("", ".") else name


def show_onboarding(
    args: Namespace,
    name: str,
    authors: Dict[int, Dict],
    cohorts: Dict[str, Dict],
    people: List[str],
    window_days: List[int],
    meaningful_threshold: int,
    tick_size: int,
) -> None:
    """Generate onboarding cohort and per-author ramp visualizations."""
    matplotlib, pyplot = import_pyplot(args.backend, args.style)

    if not cohorts and not authors:
        print("No onboarding data available.")
        return

    metric = "meaningful_lines"
    display_name = _display_repo_name(name)
    if cohorts:
        _plot_cohort_heatmap(
            args,
            display_name,
            cohorts,
            window_days,
            metric,
            meaningful_threshold,
            matplotlib,
            pyplot,
        )
    if authors:
        _plot_author_ramps(
            args,
            display_name,
            authors,
            people,
            window_days,
            metric,
            meaningful_threshold,
            matplotlib,
            pyplot,
        )


def _plot_cohort_heatmap(
    args, name, cohorts, window_days, metric, meaningful_threshold, matplotlib, pyplot
):
    cohort_labels, day_labels, matrix = build_cohort_heatmap(cohorts, window_days, metric)
    if matrix.size == 0:
        return

    if args.size is None:
        figsize = (max(8, len(day_labels) * 1.4 + 3), max(4, len(cohort_labels) * 0.45 + 2))
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, ax = pyplot.subplots(figsize=figsize)
    image = ax.imshow(matrix, aspect="auto", cmap="YlGnBu")
    cbar = pyplot.colorbar(image, ax=ax)
    cbar.set_label("Meaningful lines", rotation=270, labelpad=20)

    ax.set_xticks(np.arange(len(day_labels)))
    ax.set_xticklabels(day_labels)
    ax.set_yticks(np.arange(len(cohort_labels)))
    ax.set_yticklabels(cohort_labels)
    ax.set_xlabel("Days since first commit")
    ax.set_ylabel("Join cohort")
    ax.set_title(
        "%s - Onboarding Cohort Ramp\nmeaningful commit threshold: %d lines"
        % (name, meaningful_threshold)
    )

    max_value = matrix.max() if matrix.size else 0
    text_color_threshold = max_value / 2 if max_value else 0
    for row in range(matrix.shape[0]):
        for col in range(matrix.shape[1]):
            value = matrix[row, col]
            color = "white" if value > text_color_threshold else "black"
            ax.text(col, row, "%.0f" % value, ha="center", va="center", color=color)

    apply_plot_style(fig, ax, None, args.background, args.font_size, args.size or "%g,%g" % figsize)
    output = _mode_output(args, "onboarding_cohorts", "_cohorts")
    deploy_plot("%s - Onboarding Cohorts" % name, output, args.background)
    pyplot.close(fig)


def _plot_author_ramps(
    args, name, authors, people, window_days, metric, meaningful_threshold, matplotlib, pyplot
):
    series = build_author_ramp_series(authors, people, window_days, metric)
    if not series:
        return

    max_authors = min(len(series), getattr(args, "max_people", 20))
    series = series[:max_authors]

    if args.size is None:
        figsize = (12, max(5, max_authors * 0.35 + 2))
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, ax = pyplot.subplots(figsize=figsize)
    x_values = np.array(window_days)
    colors = matplotlib.cm.get_cmap("tab20", max(1, len(series)))

    for idx, item in enumerate(series):
        label = "%s (%s)" % (item["author"], item["cohort"])
        ax.plot(x_values, item["values"], marker="o", linewidth=1.6, label=label, color=colors(idx))

    ax.set_xlabel("Days since first commit")
    ax.set_ylabel("Meaningful lines")
    ax.set_title(
        "%s - Per-Author Onboarding Ramp\nmeaningful commit threshold: %d lines"
        % (name, meaningful_threshold)
    )
    ax.xaxis.set_major_locator(matplotlib.ticker.MaxNLocator(integer=True))
    legend = ax.legend(fontsize=max(6, args.font_size * 0.7), loc="best")

    apply_plot_style(fig, ax, legend, args.background, args.font_size, args.size or "%g,%g" % figsize)
    output = _mode_output(args, "onboarding_authors", "_authors")
    deploy_plot("%s - Onboarding Authors" % name, output, args.background)
    pyplot.close(fig)


def _mode_output(args, all_mode_name: str, suffix: str):
    if args.mode == "all" and args.output:
        return get_plot_path(args.output, all_mode_name)
    if args.output:
        base, ext = os.path.splitext(args.output)
        return "%s%s%s" % (base, suffix, ext or ".png")
    return None
