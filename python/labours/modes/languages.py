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
    Resample daily language data to coarser time periods.
    For cumulative data, takes the last value at each period boundary.
    """
    pandas = import_pandas()

    # Handle resample aliases (matching burndown.py)
    aliases = {"year": "YE", "month": "ME", "day": "D", "week": "W"}
    freq = aliases.get(resample, resample)

    # Generate period boundaries from start to end
    periods = pandas.date_range(start_datetime, end_datetime, freq=freq)

    # Handle edge case: if no periods generated, create at least one
    if len(periods) == 0:
        periods = pandas.date_range(start_datetime, periods=1, freq=freq)

    # Check if resampling is too coarse (first period already past end date)
    if len(periods) > 0 and periods[0] > end_datetime:
        if freq in ("A", "YE"):
            print("too loose resampling - by year, trying by month")
            return _resample_language_data(daily_matrix, start_datetime, end_datetime, "month")
        elif freq in ("M", "ME"):
            print("too loose resampling - by month, trying by week")
            return _resample_language_data(daily_matrix, start_datetime, end_datetime, "week")
        else:
            raise ValueError(f"Too loose resampling: {resample}. Try finer.")

    # For cumulative data, take the last daily value at each period boundary
    resampled_matrix = numpy.zeros((len(periods), daily_matrix.shape[1]), dtype=numpy.float32)

    for i, period_end in enumerate(periods):
        # Calculate day index for this period boundary
        day_idx = (period_end - start_datetime).days
        # Clamp to valid range
        day_idx = max(0, min(day_idx, daily_matrix.shape[0] - 1))
        resampled_matrix[i] = daily_matrix[day_idx]

    return resampled_matrix, periods


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

    # Convert timestamps to datetime
    start_datetime = datetime.fromtimestamp(start_date)
    end_datetime = datetime.fromtimestamp(end_date)
    total_days = (end_datetime - start_datetime).days + 1

    if total_days <= 0:
        print("No temporal data to plot")
        return

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

    # Build language list for matrix columns
    language_list = sorted(top_languages)
    if len(sorted_langs) > top_n:
        language_list.append("Other")

    # Build complete daily matrix: rows = all days from start to end, columns = languages
    # This ensures continuous time series with no gaps
    matrix = numpy.zeros((total_days, len(language_list)), dtype=numpy.int64)
    cumulative_langs = defaultdict(int)

    # Process days in chronological order (days keys are tick offsets from start)
    for day_tick in sorted(days.keys()):
        # day_tick is the offset in days from start_date
        if day_tick < 0 or day_tick >= total_days:
            continue

        # Accumulate language stats for this day
        for dev, stats in days[day_tick].items():
            for lang, vals in stats.Languages.items():
                # vals is [added, removed, changed]
                delta = vals[0] - vals[1]  # added - removed
                if lang in top_languages:
                    cumulative_langs[lang] += delta
                elif lang:  # "Other" category
                    if "Other" in language_list:
                        cumulative_langs["Other"] += delta

        # Store cumulative snapshot at this day
        for lang_idx, lang in enumerate(language_list):
            matrix[day_tick, lang_idx] = max(0, cumulative_langs.get(lang, 0))

    # Forward-fill: propagate values through days without commits
    # This ensures continuous stacked areas without gaps
    for day_idx in range(1, total_days):
        for lang_idx in range(len(language_list)):
            if matrix[day_idx, lang_idx] == 0 and matrix[day_idx - 1, lang_idx] > 0:
                matrix[day_idx, lang_idx] = matrix[day_idx - 1, lang_idx]

    # Apply resampling if specified (matching burndown.py)
    resample = args.resample
    if resample not in ("no", "raw"):
        # Resample for smoother visualization
        matrix, date_range = _resample_language_data(
            matrix, start_datetime, end_datetime, resample
        )
    else:
        # No resampling - use complete daily date range
        date_range = pandas.date_range(
            start=start_datetime,
            periods=total_days,
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
