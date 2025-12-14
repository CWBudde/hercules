from argparse import Namespace
from collections import defaultdict
from datetime import datetime, timedelta
from typing import Dict, List

import numpy

from labours.objects import DevDay
from labours.plotting import apply_plot_style, deploy_plot, get_plot_path, import_pyplot
from labours.utils import import_pandas


def show_languages(
    args: Namespace,
    name: str,
    start_date: int,
    end_date: int,
    people: List[str],
    days: Dict[int, Dict[int, DevDay]],
) -> None:
    devlangs = defaultdict(lambda: defaultdict(lambda: numpy.zeros(3, dtype=int)))
    for day, devs in days.items():
        for dev, stats in devs.items():
            for lang, vals in stats.Languages.items():
                devlangs[dev][lang] += vals
    devlangs = sorted(
        devlangs.items(), key=lambda p: -sum(x.sum() for x in p[1].values())
    )

    # Print text output
    for dev, ls in devlangs:
        print()
        print("#", people[dev])
        ls = sorted(((vals.sum(), lang) for lang, vals in ls.items()), reverse=True)
        for vals, lang in ls:
            if lang:
                print("%s: %d" % (lang, vals))

    # Generate chart if output is specified
    if args.output:
        _plot_languages_chart(args, devlangs, people, days, start_date, end_date)


def _resample_language_data(
    daily_matrix: numpy.ndarray,
    start_datetime: datetime,
    end_datetime: datetime,
    resample: str,
) -> tuple:
    """
    Resample daily language data using the same algorithm as burndown.py.
    This ensures consistent time bucketing across all chart types.
    """
    pandas = import_pandas()

    # Handle resample aliases (matching burndown.py exactly)
    aliases = {"year": "YE", "month": "ME", "day": "D", "week": "W"}
    resample_freq = aliases.get(resample, resample)

    # Calculate finish date (similar to burndown's finish variable)
    finish = end_datetime
    start = start_datetime

    # Create resampling periods (matching burndown.py logic at lines 343-349)
    periods = 0
    date_granularity_sampling = [start]
    while date_granularity_sampling[-1] < finish:
        periods += 1
        date_granularity_sampling = pandas.date_range(
            start, periods=periods, freq=resample_freq
        )

    # Check if resampling is too coarse (matching burndown.py lines 350-362)
    if len(date_granularity_sampling) > 0 and date_granularity_sampling[0] > finish:
        if resample_freq in ("A", "YE"):
            print("too loose resampling - by year, trying by month")
            return _resample_language_data(daily_matrix, start_datetime, end_datetime, "month")
        elif resample_freq in ("M", "ME"):
            print("too loose resampling - by month, trying by week")
            return _resample_language_data(daily_matrix, start_datetime, end_datetime, "W")
        else:
            raise ValueError(f"Too loose resampling: {resample}. Try finer.")

    # Aggregate values within each resampling period
    # This properly handles cumulative data and prevents cut-off issues
    resampled_matrix = numpy.zeros((len(date_granularity_sampling), daily_matrix.shape[1]), dtype=numpy.float32)

    for i, gdt in enumerate(date_granularity_sampling):
        # Calculate day indices for this period (matching burndown.py lines 374-375)
        istart = (date_granularity_sampling[i - 1] - start).days if i > 0 else 0
        ifinish = min((gdt - start).days, daily_matrix.shape[0])

        # Take the last value in the period for cumulative data
        # Use min() to ensure we don't go out of bounds
        if ifinish > istart and istart < daily_matrix.shape[0]:
            # Take last value in this period (ifinish - 1 is the last valid index)
            resampled_matrix[i] = daily_matrix[ifinish - 1]
        elif istart < daily_matrix.shape[0]:
            # Period extends beyond data, use the last available value
            resampled_matrix[i] = daily_matrix[-1]

    return resampled_matrix, date_granularity_sampling


def _plot_languages_chart(
    args: Namespace,
    devlangs: List,
    people: List[str],
    days: Dict[int, Dict[int, DevDay]],
    start_date: int,
    end_date: int,
) -> None:
    """Generate a temporal burndown chart showing language evolution over time."""
    pandas = import_pandas()

    # First, determine top languages overall to limit the number of series
    total_langs = defaultdict(int)
    for day, devs in days.items():
        for dev, stats in devs.items():
            for lang, vals in stats.Languages.items():
                if lang:  # Skip empty language names
                    total_langs[lang] += sum(vals)

    # Sort by total lines and get top languages
    sorted_langs = sorted(total_langs.items(), key=lambda x: -x[1])

    # Take top 10 languages and group the rest as "Other"
    top_n = 10
    if len(sorted_langs) > top_n:
        top_languages = {lang for lang, _ in sorted_langs[:top_n]}
    else:
        top_languages = {lang for lang, _ in sorted_langs}

    if not top_languages:
        print("No language data to plot")
        return

    # Create time series data - aggregate language lines per day
    sorted_days = sorted(days.keys())
    if not sorted_days:
        print("No temporal data to plot")
        return

    # Build a matrix: rows = days, columns = languages
    language_list = sorted(top_languages)
    if len(sorted_langs) > top_n:
        language_list.append("Other")

    # Initialize matrix to store cumulative lines per language per day
    matrix = numpy.zeros((len(sorted_days), len(language_list)), dtype=int)
    cumulative_langs = defaultdict(int)

    for day_idx, day in enumerate(sorted_days):
        devs = days[day]

        # Aggregate all developers for this day
        for dev, stats in devs.items():
            for lang, vals in stats.Languages.items():
                # vals is [added, removed, changed]
                # For cumulative count: added - removed
                delta = vals[0] - vals[1]
                if lang in top_languages:
                    cumulative_langs[lang] += delta
                elif lang:  # "Other" category
                    if "Other" in language_list:
                        cumulative_langs["Other"] += delta

        # Fill matrix row with cumulative values
        for lang_idx, lang in enumerate(language_list):
            matrix[day_idx, lang_idx] = max(0, cumulative_langs.get(lang, 0))

    # Create initial date range (daily)
    start_datetime = datetime.fromtimestamp(start_date)
    end_datetime = datetime.fromtimestamp(end_date)

    # Apply resampling if specified (matching burndown.py)
    resample = args.resample
    if resample not in ("no", "raw"):
        # Resample for smoother visualization
        matrix, date_range = _resample_language_data(
            matrix, start_datetime, end_datetime, resample
        )
    else:
        # No resampling - use daily data
        date_range = pandas.date_range(
            start=start_datetime,
            periods=len(sorted_days),
            freq='D'
        )

    # Create the plot
    matplotlib, pyplot = import_pyplot(args.backend, args.style)

    # Transpose matrix for stackplot (expects series as rows)
    matrix_transposed = matrix.T

    # Create stacked area chart
    pyplot.stackplot(date_range, matrix_transposed, labels=language_list, alpha=0.8)

    # Customize the plot
    legend = pyplot.legend(loc='upper left', fontsize=args.font_size)
    pyplot.ylabel("Lines of Code", fontsize=args.font_size)
    pyplot.xlabel("Time", fontsize=args.font_size)

    # Apply styling
    apply_plot_style(
        pyplot.gcf(), pyplot.gca(), legend, args.background, args.font_size, args.size
    )

    # Set date formatting - matching burndown.py exactly (lines 66-75)
    locator = pyplot.gca().xaxis.get_major_locator()
    # set the optimal xticks locator
    if "M" not in resample:
        pyplot.gca().xaxis.set_major_locator(matplotlib.dates.YearLocator())
    locs = pyplot.gca().get_xticks().tolist()
    if len(locs) >= 16:
        pyplot.gca().xaxis.set_major_locator(matplotlib.dates.YearLocator())
        locs = pyplot.gca().get_xticks().tolist()
        if len(locs) >= 16:
            pyplot.gca().xaxis.set_major_locator(locator)

    pyplot.gcf().autofmt_xdate()

    # Set title
    total_lines = matrix.sum()
    title = f'Language Evolution Over Time\n(Total: {total_lines:,} lines)'

    # Determine output path
    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, "languages")
    else:
        output = args.output

    deploy_plot(title, output, args.background)
