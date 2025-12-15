"""Temporal activity visualization for hercules analysis."""

from argparse import Namespace
from datetime import datetime
from typing import Dict, List, Optional

import numpy as np

from labours.plotting import apply_plot_style, deploy_plot, get_plot_path, import_pyplot
from labours.utils import parse_date


WEEKDAY_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
MONTH_LABELS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun",
                "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]

# Nanoseconds per day (Go's time.Duration is in nanoseconds)
NANOSECONDS_PER_DAY = 24 * 60 * 60 * 1_000_000_000


def show_temporal_activity(
    args: Namespace,
    name: str,
    activities: Dict[int, Dict[str, List[int]]],
    people: List[str],
    ticks: Optional[Dict[int, Dict[int, Dict]]] = None,
    tick_size: int = 0,
    header_start_date: int = 0,
    header_end_date: int = 0,
) -> None:
    """Generate stacked bar charts for temporal activity dimensions.

    Args:
        args: Command line arguments
        name: Repository name
        activities: Map of developer index to activity data (contains both commits and lines)
        people: List of developer names
        ticks: Per-tick data for date range filtering (tick_id -> dev_id -> activity)
        tick_size: Duration of each tick in nanoseconds
        header_start_date: Unix timestamp of first commit in repo
        header_end_date: Unix timestamp of last commit in repo
    """
    matplotlib, pyplot = import_pyplot(args.backend, args.style)

    # Determine if we need to filter by date range
    use_filtered_data = False
    if ticks and tick_size > 0 and header_start_date > 0:
        # Parse user-provided date filters
        repo_start = datetime.fromtimestamp(header_start_date)
        repo_end = datetime.fromtimestamp(header_end_date)

        filter_start = parse_date(args.start_date, repo_start)
        filter_end = parse_date(args.end_date, repo_end)

        # Check if filtering is needed (user dates differ from repo dates)
        if filter_start > repo_start or filter_end < repo_end:
            use_filtered_data = True
            activities = _filter_activities_by_date_range(
                ticks, tick_size, header_start_date, people, filter_start, filter_end
            )
            print(f"Filtering temporal activity to {filter_start.date()} - {filter_end.date()}")

    # Generate charts for each dimension and mode (commits and lines)
    dimensions = [
        ("weekdays", WEEKDAY_LABELS, "Weekday"),
        ("hours", [f"{h:02d}:00" for h in range(24)], "Hour of Day"),
        ("months", MONTH_LABELS, "Month"),
        ("weeks", [f"W{w+1}" for w in range(53)], "ISO Week"),
    ]

    modes = ["commits", "lines"]

    for mode in modes:
        for dim_name, labels, title_suffix in dimensions:
            _create_temporal_chart(
                args,
                name,
                activities,
                people,
                mode,
                dim_name,
                labels,
                title_suffix,
                matplotlib,
                pyplot
            )

        # Generate weekday × hour heatmap for this mode
        _create_weekday_hour_heatmap(
            args,
            name,
            activities,
            people,
            mode,
            matplotlib,
            pyplot
        )


def _filter_activities_by_date_range(
    ticks: Dict[int, Dict[int, Dict]],
    tick_size: int,
    header_start_date: int,
    people: List[str],
    filter_start: datetime,
    filter_end: datetime,
) -> Dict[int, Dict[str, List[int]]]:
    """Filter temporal activity data by date range using per-tick data.

    Args:
        ticks: Per-tick data (tick_id -> dev_id -> activity dict)
        tick_size: Duration of each tick in nanoseconds
        header_start_date: Unix timestamp of first commit
        people: List of developer names
        filter_start: Start date for filtering
        filter_end: End date for filtering

    Returns:
        Filtered activities dict in same format as input activities
    """
    # Convert tick_size to days (tick_size is in nanoseconds)
    tick_days = tick_size / NANOSECONDS_PER_DAY if tick_size > 0 else 1

    # Calculate tick range to include
    repo_start = datetime.fromtimestamp(header_start_date)
    start_tick = int((filter_start - repo_start).days / tick_days)
    end_tick = int((filter_end - repo_start).days / tick_days)

    # Aggregate filtered tick data
    filtered_activities: Dict[int, Dict[str, List[int]]] = {}

    for tick_id, tick_devs in ticks.items():
        # Skip ticks outside date range
        if tick_id < start_tick or tick_id > end_tick:
            continue

        for dev_id, tick_data in tick_devs.items():
            if dev_id not in filtered_activities:
                filtered_activities[dev_id] = {
                    "weekdays_commits": [0] * 7,
                    "weekdays_lines": [0] * 7,
                    "hours_commits": [0] * 24,
                    "hours_lines": [0] * 24,
                    "months_commits": [0] * 12,
                    "months_lines": [0] * 12,
                    "weeks_commits": [0] * 53,
                    "weeks_lines": [0] * 53,
                }

            activity = filtered_activities[dev_id]
            commits = tick_data.get("commits", 0)
            lines = tick_data.get("lines", 0)
            weekday = tick_data.get("weekday", 0)
            hour = tick_data.get("hour", 0)
            month = tick_data.get("month", 0)
            week = tick_data.get("week", 0)

            # Accumulate counts
            activity["weekdays_commits"][weekday] += commits
            activity["weekdays_lines"][weekday] += lines
            activity["hours_commits"][hour] += commits
            activity["hours_lines"][hour] += lines
            activity["months_commits"][month] += commits
            activity["months_lines"][month] += lines
            activity["weeks_commits"][week] += commits
            activity["weeks_lines"][week] += lines

    return filtered_activities


def _create_temporal_chart(
    args: Namespace,
    name: str,
    activities: Dict[int, Dict[str, List[int]]],
    people: List[str],
    mode: str,
    dimension: str,
    labels: List[str],
    title_suffix: str,
    matplotlib,
    pyplot,
) -> None:
    """Create a single stacked bar chart for one temporal dimension."""

    # Extract data for this dimension and mode
    # The key format is now: "weekdays_commits", "weekdays_lines", etc.
    data_key = f"{dimension}_{mode}"

    devs = sorted(activities.keys())
    num_devs = len(devs)
    num_bins = len(labels)

    if num_devs == 0:
        print(f"No data for {dimension}")
        return

    # Build data matrix: rows = developers, cols = time bins
    data = np.zeros((num_devs, num_bins), dtype=np.int32)
    dev_names = []

    for i, dev in enumerate(devs):
        activity = activities[dev]
        if data_key in activity:
            values = activity[data_key]
            # Handle the case where values might be shorter than expected
            for j, val in enumerate(values):
                if j < num_bins:
                    data[i, j] = val

        # Get developer name
        if dev == -1 or dev >= len(people):
            dev_names.append("Unknown")
        else:
            dev_names.append(people[dev])

    # Parse size
    if args.size is None:
        figsize = (16, 10)
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    # Create figure
    fig, ax = pyplot.subplots(figsize=figsize)

    # Create stacked bar chart
    x = np.arange(num_bins)
    width = 0.8

    # Generate colors for developers
    colors = matplotlib.cm.get_cmap('tab20', num_devs)

    # Stack bars
    bottom = np.zeros(num_bins)
    for i in range(num_devs):
        ax.bar(x, data[i], width, bottom=bottom,
               label=dev_names[i], color=colors(i))
        bottom += data[i]

    # Customize chart
    ax.set_xlabel(title_suffix)
    ax.set_ylabel(f"Number of {mode}")
    ax.set_title(f"{name} - Activity by {title_suffix} ({mode})")

    # Set x-axis labels
    # For hours and weeks, show every nth label to avoid crowding
    if dimension == "hours":
        ax.set_xticks(x[::3])  # Show every 3rd hour
        ax.set_xticklabels(labels[::3], rotation=45, ha="right")
    elif dimension == "weeks":
        ax.set_xticks(x[::5])  # Show every 5th week
        ax.set_xticklabels(labels[::5], rotation=45, ha="right")
    else:
        ax.set_xticks(x)
        ax.set_xticklabels(labels)
        if dimension == "months":
            ax.tick_params(axis='x', rotation=45)

    # Add legend if there are multiple developers
    # Get thresholds from args (with defaults if not present for backward compatibility)
    legend_threshold = getattr(args, 'temporal_legend_threshold', 32)
    single_col_threshold = getattr(args, 'temporal_legend_single_col_threshold', 10)

    if num_devs > 1:
        if legend_threshold == 0 or num_devs < legend_threshold:
            # Determine number of columns based on developer count
            if num_devs <= single_col_threshold:
                ncol = 1
            elif num_devs < single_col_threshold * 2:
                ncol = 2
            else:
                ncol = 3
            legend = ax.legend(loc='upper right', fontsize=args.font_size * 0.8, ncol=ncol)
        else:
            # Too many developers, skip legend
            legend = None
    else:
        # Single developer, no legend needed
        legend = None

    # Apply plot style
    apply_plot_style(fig, ax, legend, args.background, args.font_size, args.size or "16,10")

    # Determine output path (include mode in filename)
    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, f"temporal_{dimension}_{mode}")
    else:
        if args.output:
            # Insert dimension and mode before extension
            import os
            base, ext = os.path.splitext(args.output)
            output = f"{base}_{dimension}_{mode}{ext}"
        else:
            output = None

    # Save plot
    deploy_plot(f"{name} - {title_suffix} ({mode})", output, args.background)
    pyplot.close(fig)


def _create_weekday_hour_heatmap(
    args: Namespace,
    name: str,
    activities: Dict[int, Dict[str, List[int]]],
    people: List[str],
    mode: str,
    matplotlib,
    pyplot,
) -> None:
    """Create a heatmap showing activity across weekdays and hours."""

    # Build 2D matrix: rows = weekdays (7), cols = hours (24)
    heatmap_data = np.zeros((7, 24), dtype=np.int32)

    # Data keys based on mode
    weekdays_key = f"weekdays_{mode}"
    hours_key = f"hours_{mode}"

    # Aggregate activity across all developers
    for dev, activity in activities.items():
        if weekdays_key in activity and hours_key in activity:
            weekday_data = activity[weekdays_key]
            hour_data = activity[hours_key]

            # For heatmap, we need to reconstruct weekday×hour from the marginal distributions
            # Since we only have marginals, we'll approximate by distributing proportionally
            # A better approach: store the full 2D matrix in Go and pass it through

            # Simple approach: create a synthetic 2D distribution
            # by assuming independence and using outer product
            total_weekday = sum(weekday_data) if weekday_data else 0
            total_hour = sum(hour_data) if hour_data else 0

            if total_weekday > 0 and total_hour > 0:
                # Normalize to create probability distributions
                weekday_prob = np.array(weekday_data, dtype=np.float64) / total_weekday
                hour_prob = np.array(hour_data, dtype=np.float64) / total_hour

                # Outer product gives joint distribution under independence assumption
                # Scale by total_weekday to get counts (using weekday total as reference)
                joint = np.outer(weekday_prob, hour_prob) * total_weekday
                heatmap_data += joint.astype(np.int32)

    # Check if we have any data
    if heatmap_data.sum() == 0:
        print("No data for weekday×hour heatmap")
        return

    # Parse size
    if args.size is None:
        base_figsize = (16, 10)
    else:
        base_figsize = tuple(float(p) for p in args.size.split(","))

    # Create figure with appropriate size for heatmap (wider for 24 hours)
    figsize = (base_figsize[0] * 1.2, base_figsize[1] * 0.8)
    fig, ax = pyplot.subplots(figsize=figsize)

    # Create heatmap using imshow
    im = ax.imshow(heatmap_data, cmap='YlOrRd', aspect='auto')

    # Set ticks and labels
    ax.set_xticks(np.arange(24))
    ax.set_yticks(np.arange(7))
    ax.set_xticklabels([f"{h:02d}" for h in range(24)])
    ax.set_yticklabels(WEEKDAY_LABELS)

    # Rotate hour labels for better readability
    pyplot.setp(ax.get_xticklabels(), rotation=45, ha="right", rotation_mode="anchor")

    # Add colorbar
    cbar = pyplot.colorbar(im, ax=ax)
    cbar.set_label(f"Number of {mode}", rotation=270, labelpad=20)

    # Add text annotations for each cell (optional, only if values aren't too small)
    max_value = heatmap_data.max()
    for i in range(7):
        for j in range(24):
            value = heatmap_data[i, j]
            # Only show text if value is significant (> 1% of max)
            if value > max_value * 0.01:
                text_color = "white" if value > max_value * 0.5 else "black"
                ax.text(j, i, int(value), ha="center", va="center",
                       color=text_color, fontsize=args.font_size * 0.6)

    # Labels and title
    ax.set_xlabel("Hour of Day")
    ax.set_ylabel("Day of Week")
    ax.set_title(f"{name} - Activity Heatmap: Weekday × Hour ({mode})")

    # Apply plot style
    apply_plot_style(fig, ax, None, args.background, args.font_size, args.size or "16,10")

    # Determine output path (include mode in filename)
    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, f"temporal_heatmap_{mode}")
    else:
        if args.output:
            import os
            base, ext = os.path.splitext(args.output)
            output = f"{base}_heatmap_{mode}{ext}"
        else:
            output = None

    # Save plot
    deploy_plot(f"{name} - Weekday×Hour Heatmap ({mode})", output, args.background)
    pyplot.close(fig)
