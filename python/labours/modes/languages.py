from argparse import Namespace
from collections import defaultdict
from typing import Dict, List

import numpy

from labours.objects import DevDay
from labours.plotting import deploy_plot, get_plot_path, import_pyplot


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
        _plot_languages_chart(args, devlangs, people)


def _plot_languages_chart(args: Namespace, devlangs: List, people: List[str]) -> None:
    """Generate a pie chart showing language distribution across all developers."""
    # Aggregate all languages across all developers
    total_langs = defaultdict(int)
    for dev, ls in devlangs:
        for lang, vals in ls.items():
            if lang:  # Skip empty language names
                total_langs[lang] += vals.sum()

    # Sort by total lines and get top languages
    sorted_langs = sorted(total_langs.items(), key=lambda x: -x[1])

    # Take top 15 languages and group the rest as "Other"
    top_n = 15
    if len(sorted_langs) > top_n:
        top_langs = sorted_langs[:top_n]
        other_total = sum(count for _, count in sorted_langs[top_n:])
        if other_total > 0:
            top_langs.append(("Other", other_total))
    else:
        top_langs = sorted_langs

    if not top_langs:
        print("No language data to plot")
        return

    # Prepare data for pie chart
    languages = [lang for lang, _ in top_langs]
    counts = [count for _, count in top_langs]

    # Create the plot
    matplotlib, pyplot = import_pyplot(args.backend, args.style)

    # Create a figure with better size for pie chart
    fig, ax = pyplot.subplots(figsize=(12, 8))

    # Create pie chart
    wedges, texts, autotexts = ax.pie(
        counts,
        labels=languages,
        autopct=lambda pct: f'{pct:.1f}%' if pct > 2 else '',
        startangle=90,
        textprops={'fontsize': args.font_size}
    )

    # Enhance the text
    for autotext in autotexts:
        autotext.set_color('white')
        autotext.set_fontsize(args.font_size - 2)
        autotext.set_weight('bold')

    # Equal aspect ratio ensures that pie is drawn as a circle
    ax.axis('equal')

    # Set title
    total_lines = sum(counts)
    title = f'Language Distribution\n(Total: {total_lines:,} lines)'

    # Determine output path
    if args.mode == "all" and args.output:
        output = get_plot_path(args.output, "languages")
    else:
        output = args.output

    deploy_plot(title, output, args.background, tight=True)
