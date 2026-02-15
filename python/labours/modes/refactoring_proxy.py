"""Refactoring proxy visualization for hercules analysis."""

from argparse import Namespace
from datetime import datetime, timedelta
from typing import Dict, List, Optional

import numpy as np

from labours.plotting import apply_plot_style, deploy_plot, get_plot_path, import_pyplot
from labours.utils import parse_date


def show_refactoring_proxy(
    args: Namespace,
    name: str,
    result: Dict,
) -> None:
    """Generate timeline plot and summary for refactoring proxy analysis.

    Args:
        args: Command line arguments
        name: Repository name
        result: Refactoring proxy results containing:
            - ticks: List of tick data with timestamps and metrics
            - threshold: Configured refactoring threshold (default 0.3)
            - tick_size_days: Size of each tick in days
            - start_date: Unix timestamp of first commit
            - end_date: Unix timestamp of last commit
    """
    # Print text summary first
    print_refactoring_summary(result)

    # Then generate visualization
    plot_refactoring_timeline(args, name, result)


def print_refactoring_summary(result: Dict) -> None:
    """Print a text summary of refactoring proxy analysis.

    Args:
        result: Refactoring proxy results
    """
    ticks = result.get("ticks", [])
    threshold = result.get("threshold", 0.3)
    tick_size_days = result.get("tick_size_days", 30)

    if not ticks:
        print("No refactoring proxy data available")
        return

    # Count refactoring vs feature phases
    refactoring_ticks = sum(1 for t in ticks if t.get("refactoring_rate", 0) >= threshold)
    feature_ticks = len(ticks) - refactoring_ticks

    # Calculate statistics
    rates = [t.get("refactoring_rate", 0) for t in ticks]
    avg_rate = np.mean(rates) if rates else 0
    max_rate = max(rates) if rates else 0

    # Find longest refactoring and feature streaks
    max_refactoring_streak = 0
    max_feature_streak = 0
    current_refactoring_streak = 0
    current_feature_streak = 0

    for tick in ticks:
        if tick.get("refactoring_rate", 0) >= threshold:
            current_refactoring_streak += 1
            current_feature_streak = 0
            max_refactoring_streak = max(max_refactoring_streak, current_refactoring_streak)
        else:
            current_feature_streak += 1
            current_refactoring_streak = 0
            max_feature_streak = max(max_feature_streak, current_feature_streak)

    print("\n=== Refactoring Proxy Analysis ===")
    print(f"Threshold: {threshold:.1%}")
    print(f"Tick size: {tick_size_days} days")
    print(f"Total ticks: {len(ticks)}")
    print(f"\nPhase distribution:")
    print(f"  Refactoring phases: {refactoring_ticks} ({refactoring_ticks/len(ticks):.1%})")
    print(f"  Feature phases: {feature_ticks} ({feature_ticks/len(ticks):.1%})")
    print(f"\nRefactoring rate statistics:")
    print(f"  Average: {avg_rate:.1%}")
    print(f"  Maximum: {max_rate:.1%}")
    print(f"\nLongest streaks:")
    print(f"  Refactoring: {max_refactoring_streak} ticks ({max_refactoring_streak * tick_size_days} days)")
    print(f"  Feature development: {max_feature_streak} ticks ({max_feature_streak * tick_size_days} days)")
    print()


def plot_refactoring_timeline(
    args: Namespace,
    name: str,
    result: Dict,
) -> None:
    """Generate timeline plot showing refactoring rate over time.

    Creates a line plot with:
    - Refactoring rate over time
    - Threshold line
    - Shaded regions for refactoring vs feature phases

    Args:
        args: Command line arguments
        name: Repository name
        result: Refactoring proxy results
    """
    matplotlib, pyplot = import_pyplot(args.backend, args.style)

    ticks = result.get("ticks", [])
    threshold = result.get("threshold", 0.3)
    tick_size_days = result.get("tick_size_days", 30)
    start_date = result.get("start_date", 0)
    end_date = result.get("end_date", 0)

    if not ticks:
        print("No refactoring proxy data to plot")
        return

    # Apply date filtering if specified
    if start_date > 0 and (args.start_date or args.end_date):
        repo_start = datetime.fromtimestamp(start_date)
        repo_end = datetime.fromtimestamp(end_date)
        filter_start = parse_date(args.start_date, repo_start)
        filter_end = parse_date(args.end_date, repo_end)

        # Filter ticks by date range
        filtered_ticks = []
        for tick in ticks:
            tick_date = datetime.fromtimestamp(tick.get("timestamp", 0))
            if filter_start <= tick_date <= filter_end:
                filtered_ticks.append(tick)

        if filtered_ticks:
            print(f"Filtering refactoring proxy to {filter_start.date()} - {filter_end.date()}")
            ticks = filtered_ticks
        else:
            print(f"No data in date range {filter_start.date()} - {filter_end.date()}")
            return

    # Extract data
    timestamps = [datetime.fromtimestamp(t.get("timestamp", 0)) for t in ticks]
    rates = [t.get("refactoring_rate", 0) for t in ticks]

    # Parse size
    if args.size is None:
        figsize = (16, 6)
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    # Create figure
    fig, ax = pyplot.subplots(figsize=figsize)

    # Plot refactoring rate line
    ax.plot(timestamps, rates, linewidth=2, label="Refactoring Rate", color="#2E86AB")

    # Plot threshold line
    ax.axhline(y=threshold, color="#E63946", linestyle="--", linewidth=1.5,
               label=f"Threshold ({threshold:.1%})")

    # Shade refactoring vs feature regions
    refactoring_regions = []
    current_start = None

    for i, tick in enumerate(ticks):
        is_refactoring = tick.get("refactoring_rate", 0) >= threshold

        if is_refactoring and current_start is None:
            # Start of refactoring region
            current_start = timestamps[i]
        elif not is_refactoring and current_start is not None:
            # End of refactoring region
            refactoring_regions.append((current_start, timestamps[i]))
            current_start = None

    # Close last region if still open
    if current_start is not None:
        refactoring_regions.append((current_start, timestamps[-1]))

    # Shade refactoring regions
    for start, end in refactoring_regions:
        ax.axvspan(start, end, alpha=0.2, color="#A8DADC", label="Refactoring Phase" if start == refactoring_regions[0][0] else "")

    # Customize chart
    ax.set_xlabel("Date")
    ax.set_ylabel("Refactoring Rate (Renames/Moves per Commit)")
    ax.set_title(f"{name} - Refactoring Proxy Timeline")

    # Format y-axis as percentage
    ax.yaxis.set_major_formatter(matplotlib.ticker.PercentFormatter(1.0))

    # Add legend
    legend = ax.legend(loc="upper right", fontsize=args.font_size * 0.9)

    # Apply plot style
    apply_plot_style(fig, ax, legend, args.background, args.font_size, args.size or "16,6")

    # Determine output path
    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, "refactoring_proxy")
    else:
        output = args.output

    # Save plot
    deploy_plot(f"{name} - Refactoring Proxy", output, args.background)
    pyplot.close(fig)
