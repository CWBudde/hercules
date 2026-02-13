"""Knowledge diffusion visualization for hercules analysis."""

import os
from argparse import Namespace
from typing import Dict, List

import numpy as np

from labours.plotting import apply_plot_style, deploy_plot, get_plot_path, import_pyplot


def show_knowledge_diffusion(
    args: Namespace,
    name: str,
    files: Dict[str, Dict],
    distribution: Dict[int, int],
    people: List[str],
    window_months: int,
    tick_size: int,
    header_start_date: int,
) -> None:
    """Generate knowledge diffusion visualizations.

    Produces:
      1. Distribution histogram of files by unique editor count
      2. Top-N knowledge silos (files with fewest editors)
      3. Lorenz curve of editor distribution across files

    Args:
        args: Command line arguments
        name: Repository name
        files: file_path -> {unique_editors, recent_editors, editors_over_time}
        distribution: editor_count -> number_of_files
        people: List of developer names
        window_months: Sliding window in months for recent editors
        tick_size: Duration of each tick in nanoseconds
        header_start_date: Unix timestamp of first commit
    """
    matplotlib, pyplot = import_pyplot(args.backend, args.style)

    if not files:
        print("No knowledge diffusion data available.")
        return

    # --- 1. Distribution histogram ---
    _plot_distribution(args, name, distribution, matplotlib, pyplot)

    # --- 2. Top-N knowledge silos ---
    _plot_silos(args, name, files, people, window_months, matplotlib, pyplot)

    # --- 3. Lorenz curve ---
    _plot_lorenz(args, name, files, matplotlib, pyplot)


def _plot_distribution(args, name, distribution, matplotlib, pyplot):
    """Plot histogram of files by number of unique editors."""
    if args.size is None:
        figsize = (12, 6)
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, ax = pyplot.subplots(figsize=figsize)

    editor_counts = sorted(distribution.keys())
    file_counts = [distribution[c] for c in editor_counts]

    colors = []
    for c in editor_counts:
        if c <= 1:
            colors.append("#F44336")  # red - single editor risk
        elif c <= 2:
            colors.append("#FF9800")  # orange
        elif c <= 3:
            colors.append("#FFC107")  # yellow
        else:
            colors.append("#4CAF50")  # green - well shared

    ax.bar(editor_counts, file_counts, color=colors, edgecolor="white", linewidth=0.5)

    ax.set_xlabel("Number of Unique Editors")
    ax.set_ylabel("Number of Files")
    ax.set_title(f"{name} - Knowledge Diffusion: Files by Editor Count")
    ax.xaxis.set_major_locator(matplotlib.ticker.MaxNLocator(integer=True))
    ax.yaxis.set_major_locator(matplotlib.ticker.MaxNLocator(integer=True))

    # Add value labels on bars
    for x, y in zip(editor_counts, file_counts):
        ax.text(x, y + 0.3, str(y), ha="center", va="bottom",
                fontsize=args.font_size * 0.8)

    # Annotate risk zones
    total_files = sum(file_counts)
    single_editor = distribution.get(1, 0)
    if total_files > 0:
        pct = single_editor / total_files * 100
        ax.text(
            0.98, 0.95,
            f"Single-editor files: {single_editor} ({pct:.0f}%)",
            transform=ax.transAxes, ha="right", va="top",
            fontsize=args.font_size * 0.9,
            color="#F44336" if pct > 30 else "gray",
            bbox=dict(boxstyle="round,pad=0.3", facecolor="white", alpha=0.8),
        )

    apply_plot_style(fig, ax, None, args.background, args.font_size,
                     args.size or "12,6")

    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, "knowledge_diffusion_distribution")
    elif args.output:
        base, ext = os.path.splitext(args.output)
        output = f"{base}_distribution{ext}"
    else:
        output = None

    deploy_plot(f"{name} - Knowledge Diffusion Distribution", output, args.background)
    pyplot.close(fig)


def _plot_silos(args, name, files, people, window_months, matplotlib, pyplot):
    """Plot top-N knowledge silos: files with fewest unique editors."""
    # Sort files by unique editor count ascending, then by name
    sorted_files = sorted(
        files.items(),
        key=lambda x: (x[1]["unique_editors"], x[0]),
    )

    # Show top 30 silos (fewest editors)
    max_show = 30
    silos = sorted_files[:max_show]

    if not silos:
        return

    if args.size is None:
        height = max(5, len(silos) * 0.35 + 2)
        figsize = (14, height)
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, ax = pyplot.subplots(figsize=figsize)

    file_names = [s[0] for s in silos]
    unique_counts = [s[1]["unique_editors"] for s in silos]
    recent_counts = [s[1]["recent_editors"] for s in silos]

    y_pos = np.arange(len(file_names))
    bar_height = 0.35

    # Total unique editors (full bar)
    bars_total = ax.barh(
        y_pos - bar_height / 2, unique_counts, bar_height,
        color="#90CAF9", label="Total unique editors",
    )
    # Recent editors (overlay)
    bars_recent = ax.barh(
        y_pos + bar_height / 2, recent_counts, bar_height,
        color="#1565C0", label=f"Active in last {window_months} months",
    )

    # Truncate long paths for display
    display_names = []
    for f in file_names:
        if len(f) > 60:
            display_names.append("..." + f[-57:])
        else:
            display_names.append(f)

    ax.set_yticks(y_pos)
    ax.set_yticklabels(display_names, fontsize=args.font_size * 0.7, family="monospace")
    ax.set_xlabel("Number of Editors")
    ax.set_title(f"{name} - Top {len(silos)} Knowledge Silos")
    ax.xaxis.set_major_locator(matplotlib.ticker.MaxNLocator(integer=True))
    ax.invert_yaxis()  # worst at top
    ax.legend(fontsize=args.font_size * 0.8, loc="lower right")

    # Value labels
    for i, (total, recent) in enumerate(zip(unique_counts, recent_counts)):
        ax.text(total + 0.1, i - bar_height / 2, str(total),
                va="center", fontsize=args.font_size * 0.7)
        ax.text(recent + 0.1, i + bar_height / 2, str(recent),
                va="center", fontsize=args.font_size * 0.7)

    apply_plot_style(
        fig, ax, None, args.background, args.font_size,
        args.size or f"14,{figsize[1]}",
    )

    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, "knowledge_diffusion_silos")
    elif args.output:
        base, ext = os.path.splitext(args.output)
        output = f"{base}_silos{ext}"
    else:
        output = None

    deploy_plot(f"{name} - Knowledge Silos", output, args.background)
    pyplot.close(fig)


def _plot_lorenz(args, name, files, matplotlib, pyplot):
    """Plot Lorenz curve of editor distribution across files.

    X-axis: cumulative fraction of files (sorted by editor count ascending).
    Y-axis: cumulative fraction of total unique-editor slots.
    The diagonal represents perfect equality (all files have the same number
    of editors). A curve bowing far below indicates concentration (many files
    have few editors while some files have many).
    """
    if args.size is None:
        figsize = (8, 8)
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, ax = pyplot.subplots(figsize=figsize)

    # Sort files by editor count ascending
    editor_counts = sorted(f["unique_editors"] for f in files.values())
    n = len(editor_counts)
    total_editors = sum(editor_counts)

    if total_editors == 0 or n == 0:
        return

    # Build Lorenz curve points
    cum_files = np.arange(1, n + 1) / n
    cum_editors = np.cumsum(editor_counts) / total_editors

    # Prepend origin
    cum_files = np.insert(cum_files, 0, 0)
    cum_editors = np.insert(cum_editors, 0, 0)

    # Compute Gini coefficient from Lorenz curve
    gini = 1 - 2 * np.trapz(cum_editors, cum_files)

    # Plot
    ax.plot(cum_files, cum_editors, linewidth=2, color="#1565C0",
            label=f"Lorenz curve (Gini = {gini:.3f})")
    ax.fill_between(cum_files, cum_editors, alpha=0.1, color="#1565C0")
    ax.plot([0, 1], [0, 1], linewidth=1, color="gray", linestyle="--",
            label="Perfect equality")

    ax.set_xlabel("Cumulative Fraction of Files")
    ax.set_ylabel("Cumulative Fraction of Editors")
    ax.set_title(f"{name} - Editor Distribution (Lorenz Curve)")
    ax.set_xlim(0, 1)
    ax.set_ylim(0, 1)
    ax.set_aspect("equal")
    ax.legend(fontsize=args.font_size * 0.9, loc="upper left")

    # Annotate
    ax.text(
        0.95, 0.05,
        f"Gini = {gini:.3f}\n{n} files, {total_editors} editor-slots",
        transform=ax.transAxes, ha="right", va="bottom",
        fontsize=args.font_size * 0.85,
        bbox=dict(boxstyle="round,pad=0.3", facecolor="white", alpha=0.8),
    )

    apply_plot_style(fig, ax, None, args.background, args.font_size,
                     args.size or "8,8")

    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, "knowledge_diffusion_lorenz")
    elif args.output:
        base, ext = os.path.splitext(args.output)
        output = f"{base}_lorenz{ext}"
    else:
        output = None

    deploy_plot(f"{name} - Knowledge Diffusion Lorenz", output, args.background)
    pyplot.close(fig)
