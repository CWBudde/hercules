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

    # Build a matrix: rows = languages, columns = days
    language_list = sorted(top_languages)
    if len(sorted_langs) > top_n:
        language_list.append("Other")

    # Initialize matrix to store cumulative lines per language per day
    matrix = numpy.zeros((len(sorted_days), len(language_list)), dtype=int)

    for day_idx, day in enumerate(sorted_days):
        devs = days[day]
        day_langs = defaultdict(int)

        # Aggregate all developers for this day
        for dev, stats in devs.items():
            for lang, vals in stats.Languages.items():
                if lang in top_languages:
                    day_langs[lang] += sum(vals)
                elif lang:  # "Other" category
                    if "Other" in language_list:
                        day_langs["Other"] += sum(vals)

        # Fill matrix row
        for lang_idx, lang in enumerate(language_list):
            matrix[day_idx, lang_idx] = day_langs.get(lang, 0)

    # Create date range
    start_datetime = datetime.fromtimestamp(start_date)
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
    pyplot.figure(figsize=(14, 8))
    pyplot.stackplot(date_range, matrix_transposed, labels=language_list, alpha=0.8)

    # Customize the plot
    legend = pyplot.legend(loc='upper left', fontsize=args.font_size)
    pyplot.ylabel("Lines of Code", fontsize=args.font_size)
    pyplot.xlabel("Time", fontsize=args.font_size)

    # Apply styling
    apply_plot_style(
        pyplot.gcf(), pyplot.gca(), legend, args.background, args.font_size, args.size
    )

    # Set date formatting
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
