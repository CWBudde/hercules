"""Ownership concentration (Gini/HHI) visualization for hercules analysis."""

import os
from argparse import Namespace
from typing import Dict, List, Optional

import numpy as np

from labours.plotting import apply_plot_style, deploy_plot, get_plot_path, import_pyplot


def show_ownership_concentration(
    args: Namespace,
    name: str,
    snapshots: Dict[int, Dict],
    people: List[str],
    subsystem_gini: Dict[str, float],
    subsystem_hhi: Dict[str, float],
    tick_size: int,
    header_start_date: int,
) -> None:
    """Generate ownership concentration visualizations.

    Produces:
      1. Time series of Gini coefficient and HHI over project lifetime
      2. Per-subsystem horizontal bar chart (if subsystem data is present)

    Args:
        args: Command line arguments
        name: Repository name
        snapshots: tick -> {gini, hhi, total_lines}
        people: List of developer names
        subsystem_gini: directory -> Gini coefficient
        subsystem_hhi: directory -> HHI value
        tick_size: Duration of each tick in nanoseconds
        header_start_date: Unix timestamp of first commit
    """
    matplotlib, pyplot = import_pyplot(args.backend, args.style)

    if not snapshots:
        print("No ownership concentration data available.")
        return

    ticks = sorted(snapshots.keys())
    gini_values = [snapshots[t]["gini"] for t in ticks]
    hhi_values = [snapshots[t]["hhi"] for t in ticks]

    # Convert ticks to dates if possible
    nanoseconds_per_day = 24 * 60 * 60 * 1_000_000_000
    if tick_size > 0 and header_start_date > 0:
        from datetime import datetime, timedelta

        tick_days = tick_size / nanoseconds_per_day
        start = datetime.fromtimestamp(header_start_date)
        dates = [start + timedelta(days=t * tick_days) for t in ticks]
        use_dates = True
    else:
        dates = ticks
        use_dates = False

    # --- 1. Time series plot ---
    _plot_time_series(
        args, name, dates, gini_values, hhi_values, use_dates, matplotlib, pyplot
    )

    # --- 2. Per-subsystem bar chart ---
    if subsystem_gini:
        _plot_subsystems(
            args, name, subsystem_gini, subsystem_hhi, matplotlib, pyplot
        )


def _plot_time_series(
    args, name, dates, gini_values, hhi_values, use_dates, matplotlib, pyplot
):
    """Plot Gini and HHI over time as dual-axis line charts."""
    if args.size is None:
        figsize = (14, 6)
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, ax1 = pyplot.subplots(figsize=figsize)

    # Gini on primary y-axis
    color_gini = "#E91E63"  # pink/red
    ax1.step(dates, gini_values, where="post", linewidth=2, color=color_gini,
             label="Gini coefficient")
    ax1.fill_between(dates, gini_values, step="post", alpha=0.1, color=color_gini)
    ax1.set_ylabel("Gini Coefficient", color=color_gini)
    ax1.tick_params(axis="y", labelcolor=color_gini)
    ax1.set_ylim(0, 1.05)

    # HHI on secondary y-axis
    ax2 = ax1.twinx()
    color_hhi = "#3F51B5"  # indigo/blue
    ax2.step(dates, hhi_values, where="post", linewidth=2, color=color_hhi,
             linestyle="--", label="HHI")
    ax2.set_ylabel("HHI", color=color_hhi)
    ax2.tick_params(axis="y", labelcolor=color_hhi)
    ax2.set_ylim(0, 1.05)

    ax1.set_title(f"{name} - Ownership Concentration Over Time")

    if use_dates:
        ax1.set_xlabel("Date")
        fig.autofmt_xdate()
    else:
        ax1.set_xlabel("Tick")

    # Reference lines for interpretation
    ax1.axhline(y=0.4, color=color_gini, linestyle=":", alpha=0.3,
                label="Gini 0.4 (moderate)")
    ax1.axhline(y=0.6, color=color_gini, linestyle=":", alpha=0.3,
                label="Gini 0.6 (high)")

    # Combined legend
    lines1, labels1 = ax1.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax1.legend(lines1 + lines2, labels1 + labels2,
               fontsize=args.font_size * 0.8, loc="upper left")

    apply_plot_style(fig, ax1, None, args.background, args.font_size,
                     args.size or "14,6")

    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, "ownership_concentration_timeline")
    elif args.output:
        base, ext = os.path.splitext(args.output)
        output = f"{base}_timeline{ext}"
    else:
        output = None

    deploy_plot(f"{name} - Ownership Concentration Timeline", output, args.background)
    pyplot.close(fig)


def _plot_subsystems(args, name, subsystem_gini, subsystem_hhi, matplotlib, pyplot):
    """Plot per-subsystem Gini and HHI as a grouped horizontal bar chart."""
    if args.size is None:
        height = max(4, len(subsystem_gini) * 0.5 + 2)
        figsize = (12, height)
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, ax = pyplot.subplots(figsize=figsize)

    dirs = sorted(subsystem_gini.keys())
    gini_vals = [subsystem_gini[d] for d in dirs]
    hhi_vals = [subsystem_hhi.get(d, 0) for d in dirs]

    y_pos = np.arange(len(dirs))
    bar_height = 0.35

    bars_gini = ax.barh(y_pos - bar_height / 2, gini_vals, bar_height,
                        color="#E91E63", alpha=0.8, label="Gini")
    bars_hhi = ax.barh(y_pos + bar_height / 2, hhi_vals, bar_height,
                       color="#3F51B5", alpha=0.8, label="HHI")

    ax.set_yticks(y_pos)
    ax.set_yticklabels(dirs, fontsize=args.font_size * 0.8)
    ax.set_xlabel("Concentration Index")
    ax.set_title(f"{name} - Ownership Concentration by Subsystem")
    ax.set_xlim(0, 1.1)
    ax.legend(fontsize=args.font_size * 0.8)

    # Value labels on bars
    for bar in bars_gini:
        w = bar.get_width()
        ax.text(w + 0.02, bar.get_y() + bar.get_height() / 2,
                f"{w:.2f}", va="center", fontsize=args.font_size * 0.7)
    for bar in bars_hhi:
        w = bar.get_width()
        ax.text(w + 0.02, bar.get_y() + bar.get_height() / 2,
                f"{w:.2f}", va="center", fontsize=args.font_size * 0.7)

    apply_plot_style(
        fig, ax, None, args.background, args.font_size,
        args.size or f"12,{figsize[1]}"
    )

    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, "ownership_concentration_subsystems")
    elif args.output:
        base, ext = os.path.splitext(args.output)
        output = f"{base}_subsystems{ext}"
    else:
        output = None

    deploy_plot(f"{name} - Ownership Concentration Subsystems", output, args.background)
    pyplot.close(fig)
