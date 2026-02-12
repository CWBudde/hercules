"""Bus factor visualization for hercules analysis."""

import os
from argparse import Namespace
from typing import Dict, List, Optional

import numpy as np

from labours.plotting import apply_plot_style, deploy_plot, get_plot_path, import_pyplot


def show_bus_factor(
    args: Namespace,
    name: str,
    snapshots: Dict[int, Dict],
    people: List[str],
    subsystem_bus_factor: Dict[str, int],
    threshold: float,
    tick_size: int,
    header_start_date: int,
) -> None:
    """Generate bus factor visualizations.

    Produces:
      1. Time series line chart of bus factor over project lifetime
      2. Gauge / summary of current bus factor value
      3. Per-subsystem bar chart (if subsystem data is present)

    Args:
        args: Command line arguments
        name: Repository name
        snapshots: tick -> {bus_factor, total_lines, author_lines}
        people: List of developer names
        subsystem_bus_factor: directory -> bus factor value
        threshold: Ownership threshold used (e.g. 0.8)
        tick_size: Duration of each tick in nanoseconds
        header_start_date: Unix timestamp of first commit
    """
    matplotlib, pyplot = import_pyplot(args.backend, args.style)

    if not snapshots:
        print("No bus factor data available.")
        return

    # Sort ticks
    ticks = sorted(snapshots.keys())
    bus_factors = [snapshots[t]["bus_factor"] for t in ticks]

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
        args, name, dates, bus_factors, threshold, use_dates, matplotlib, pyplot
    )

    # --- 2. Current value summary ---
    current_bf = bus_factors[-1] if bus_factors else 0
    current_total = snapshots[ticks[-1]]["total_lines"] if ticks else 0
    current_authors = snapshots[ticks[-1]].get("author_lines", {}) if ticks else {}
    _plot_gauge(
        args, name, current_bf, current_total, current_authors, people,
        threshold, matplotlib, pyplot
    )

    # --- 3. Per-subsystem bar chart ---
    if subsystem_bus_factor:
        _plot_subsystems(
            args, name, subsystem_bus_factor, threshold, matplotlib, pyplot
        )


def _plot_time_series(
    args, name, dates, bus_factors, threshold, use_dates, matplotlib, pyplot
):
    """Plot bus factor over time as a step line chart."""
    if args.size is None:
        figsize = (14, 6)
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, ax = pyplot.subplots(figsize=figsize)

    ax.step(dates, bus_factors, where="post", linewidth=2, color="#2196F3")
    ax.fill_between(dates, bus_factors, step="post", alpha=0.15, color="#2196F3")

    ax.set_ylabel("Bus Factor")
    ax.set_title(f"{name} - Bus Factor Over Time (threshold: {threshold:.0%})")
    ax.set_ylim(bottom=0)
    ax.yaxis.set_major_locator(matplotlib.ticker.MaxNLocator(integer=True))

    if use_dates:
        ax.set_xlabel("Date")
        fig.autofmt_xdate()
    else:
        ax.set_xlabel("Tick")

    # Add a danger zone highlight for bus factor = 1
    ax.axhline(y=1, color="red", linestyle="--", alpha=0.5, label="Critical (BF=1)")
    ax.legend(fontsize=args.font_size * 0.8)

    apply_plot_style(fig, ax, None, args.background, args.font_size, args.size or "14,6")

    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, "bus_factor_timeline")
    elif args.output:
        base, ext = os.path.splitext(args.output)
        output = f"{base}_timeline{ext}"
    else:
        output = None

    deploy_plot(f"{name} - Bus Factor Timeline", output, args.background)
    pyplot.close(fig)


def _plot_gauge(
    args, name, current_bf, total_lines, author_lines, people,
    threshold, matplotlib, pyplot
):
    """Plot a gauge-style summary showing current bus factor and top owners."""
    if args.size is None:
        figsize = (10, 6)
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, (ax_gauge, ax_pie) = pyplot.subplots(1, 2, figsize=figsize)

    # Left: gauge-like display using a large centered number
    ax_gauge.set_xlim(0, 1)
    ax_gauge.set_ylim(0, 1)
    ax_gauge.axis("off")

    # Color based on bus factor value
    if current_bf <= 1:
        color = "#F44336"  # red
        label = "CRITICAL"
    elif current_bf <= 3:
        color = "#FF9800"  # orange
        label = "LOW"
    elif current_bf <= 5:
        color = "#FFC107"  # yellow
        label = "MODERATE"
    else:
        color = "#4CAF50"  # green
        label = "HEALTHY"

    ax_gauge.text(
        0.5, 0.6, str(current_bf),
        ha="center", va="center", fontsize=72, fontweight="bold", color=color,
        transform=ax_gauge.transAxes,
    )
    ax_gauge.text(
        0.5, 0.35, label,
        ha="center", va="center", fontsize=18, color=color,
        transform=ax_gauge.transAxes,
    )
    ax_gauge.text(
        0.5, 0.2, f"Bus Factor @ {threshold:.0%}",
        ha="center", va="center", fontsize=12, color="gray",
        transform=ax_gauge.transAxes,
    )
    ax_gauge.text(
        0.5, 0.1, f"{total_lines:,} total lines",
        ha="center", va="center", fontsize=10, color="gray",
        transform=ax_gauge.transAxes,
    )

    # Right: pie chart of top owners
    if author_lines and total_lines > 0:
        # Sort authors by lines descending
        sorted_authors = sorted(author_lines.items(), key=lambda x: x[1], reverse=True)

        # Show top 8 authors, group the rest as "Others"
        max_slices = 8
        pie_labels = []
        pie_values = []
        others = 0

        for i, (author_id, lines) in enumerate(sorted_authors):
            if i < max_slices:
                if 0 <= author_id < len(people):
                    pie_labels.append(people[author_id])
                else:
                    pie_labels.append(f"Author {author_id}")
                pie_values.append(lines)
            else:
                others += lines

        if others > 0:
            pie_labels.append("Others")
            pie_values.append(others)

        colors = matplotlib.cm.get_cmap("tab20", len(pie_values))
        ax_pie.pie(
            pie_values,
            labels=pie_labels,
            autopct="%1.1f%%",
            colors=[colors(i) for i in range(len(pie_values))],
            startangle=90,
            textprops={"fontsize": args.font_size * 0.7},
        )
        ax_pie.set_title("Line Ownership", fontsize=args.font_size)
    else:
        ax_pie.axis("off")
        ax_pie.text(
            0.5, 0.5, "No ownership data",
            ha="center", va="center", fontsize=14,
            transform=ax_pie.transAxes,
        )

    fig.suptitle(f"{name} - Bus Factor Summary", fontsize=args.font_size * 1.2)
    fig.tight_layout()

    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, "bus_factor_gauge")
    elif args.output:
        base, ext = os.path.splitext(args.output)
        output = f"{base}_gauge{ext}"
    else:
        output = None

    deploy_plot(f"{name} - Bus Factor Gauge", output, args.background)
    pyplot.close(fig)


def _plot_subsystems(args, name, subsystem_bus_factor, threshold, matplotlib, pyplot):
    """Plot per-subsystem bus factor as a horizontal bar chart."""
    if args.size is None:
        # Scale height with number of subsystems
        height = max(4, len(subsystem_bus_factor) * 0.4 + 2)
        figsize = (12, height)
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, ax = pyplot.subplots(figsize=figsize)

    # Sort subsystems by bus factor ascending (worst at top)
    sorted_subs = sorted(subsystem_bus_factor.items(), key=lambda x: x[1])
    dirs = [s[0] for s in sorted_subs]
    values = [s[1] for s in sorted_subs]

    # Color bars by risk level
    colors = []
    for v in values:
        if v <= 1:
            colors.append("#F44336")  # red
        elif v <= 3:
            colors.append("#FF9800")  # orange
        elif v <= 5:
            colors.append("#FFC107")  # yellow
        else:
            colors.append("#4CAF50")  # green

    y_pos = np.arange(len(dirs))
    ax.barh(y_pos, values, color=colors, height=0.6)
    ax.set_yticks(y_pos)
    ax.set_yticklabels(dirs, fontsize=args.font_size * 0.8)
    ax.set_xlabel("Bus Factor")
    ax.set_title(f"{name} - Bus Factor by Subsystem (threshold: {threshold:.0%})")
    ax.xaxis.set_major_locator(matplotlib.ticker.MaxNLocator(integer=True))

    # Add value labels on bars
    for i, v in enumerate(values):
        ax.text(v + 0.1, i, str(v), va="center", fontsize=args.font_size * 0.8)

    # Critical line
    ax.axvline(x=1, color="red", linestyle="--", alpha=0.4)

    apply_plot_style(
        fig, ax, None, args.background, args.font_size,
        args.size or f"12,{figsize[1]}"
    )

    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, "bus_factor_subsystems")
    elif args.output:
        base, ext = os.path.splitext(args.output)
        output = f"{base}_subsystems{ext}"
    else:
        output = None

    deploy_plot(f"{name} - Bus Factor Subsystems", output, args.background)
    pyplot.close(fig)
