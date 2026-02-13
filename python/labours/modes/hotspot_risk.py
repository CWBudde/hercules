"""Hotspot risk score visualization for hercules analysis."""

import os
from argparse import Namespace
from typing import Dict, List

import numpy as np

from labours.plotting import apply_plot_style, deploy_plot, get_plot_path, import_pyplot


def show_hotspot_risk(
    args: Namespace,
    name: str,
    files: List[Dict],
    window_days: int,
) -> None:
    """Generate hotspot risk visualizations.

    Produces:
      1. Bubble chart: churn vs coupling, sized by file size, colored by ownership Gini
      2. Horizontal bar chart: ranked table of top-N risky files

    Args:
        args: Command line arguments
        name: Repository name
        files: List of file risk dicts with keys: path, risk_score, size, churn,
               coupling_degree, ownership_gini, and normalized values
        window_days: Time window in days used for churn calculation
    """
    matplotlib, pyplot = import_pyplot(args.backend, args.style)

    if not files:
        print("No hotspot risk data available.")
        return

    # Extract data for visualization
    paths = [f["path"] for f in files]
    risk_scores = np.array([f["risk_score"] for f in files])
    sizes = np.array([f["size"] for f in files])
    churns = np.array([f["churn"] for f in files])
    couplings = np.array([f["coupling_degree"] for f in files])
    ginis = np.array([f["ownership_gini"] for f in files])

    # --- 1. Bubble chart: churn vs coupling ---
    _plot_bubble_chart(
        args, name, paths, churns, couplings, sizes, ginis, window_days, matplotlib, pyplot
    )

    # --- 2. Ranked bar chart of top files ---
    _plot_ranked_bars(
        args, name, paths, risk_scores, sizes, churns, couplings, ginis, matplotlib, pyplot
    )

    # --- 3. Component breakdown table (text summary) ---
    if not args.output:
        print(f"\n{'='*80}")
        print(f"Top {len(files)} High-Risk Files (window: {window_days} days)")
        print(f"{'='*80}")
        print(f"{'Rank':<5} {'Risk':>8} {'Size':>6} {'Churn':>6} {'Coupling':>9} {'Gini':>6}  {'File':<40}")
        print(f"{'-'*80}")
        for i, f in enumerate(files[:20], 1):
            print(f"{i:<5} {f['risk_score']:>8.4f} {f['size']:>6} {f['churn']:>6} "
                  f"{f['coupling_degree']:>9} {f['ownership_gini']:>6.3f}  {f['path'][:40]}")


def _plot_bubble_chart(
    args, name, paths, churns, couplings, sizes, ginis, window_days, matplotlib, pyplot
):
    """Create bubble chart: x=churn, y=coupling, size=file size, color=ownership Gini."""
    if args.size is None:
        figsize = (14, 10)
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, ax = pyplot.subplots(figsize=figsize)
    apply_plot_style(pyplot, matplotlib, args.style, args.text_size, args.relative)

    # Scale bubble sizes (sqrt for better visual scaling)
    # Base size on file lines, with reasonable min/max
    bubble_sizes = np.sqrt(sizes) * 10
    bubble_sizes = np.clip(bubble_sizes, 20, 1000)

    # Create scatter plot
    scatter = ax.scatter(
        churns,
        couplings,
        s=bubble_sizes,
        c=ginis,
        cmap='YlOrRd',  # Yellow to Red colormap
        alpha=0.6,
        edgecolors='black',
        linewidth=0.5,
        vmin=0,
        vmax=1,
    )

    # Add colorbar
    cbar = pyplot.colorbar(scatter, ax=ax)
    cbar.set_label('Ownership Concentration (Gini)', rotation=270, labelpad=20)

    # Labels and title
    ax.set_xlabel(f'Churn (changes in last {window_days} days)', fontsize=12)
    ax.set_ylabel('Coupling Degree (# of co-changed files)', fontsize=12)
    ax.set_title(
        f'Hotspot Risk Landscape - {name}\n'
        f'(bubble size = file size, color = ownership concentration)',
        fontsize=14
    )

    # Add grid
    ax.grid(True, alpha=0.3, linestyle='--')

    # Annotate top 5 riskiest files (if any have non-zero risk)
    # Calculate risk scores inline
    max_churn = churns.max() if churns.max() > 0 else 1
    max_coupling = couplings.max() if couplings.max() > 0 else 1
    max_size = sizes.max() if sizes.max() > 0 else 1

    churn_norm = churns / max_churn
    coupling_norm = couplings / max_coupling
    size_norm = np.log(sizes + 1) / np.log(max_size + 1)

    risk_scores = size_norm * churn_norm * coupling_norm * ginis
    top_indices = np.argsort(risk_scores)[-5:][::-1]

    for idx in top_indices:
        if risk_scores[idx] > 0:
            # Get just the filename, not full path
            filename = paths[idx].split('/')[-1]
            ax.annotate(
                filename,
                xy=(churns[idx], couplings[idx]),
                xytext=(5, 5),
                textcoords='offset points',
                fontsize=8,
                bbox=dict(boxstyle='round,pad=0.3', facecolor='yellow', alpha=0.7),
                arrowprops=dict(arrowstyle='->', connectionstyle='arc3,rad=0')
            )

    pyplot.tight_layout()
    output_path = get_plot_path(args.output, "hotspot_risk_bubble")
    deploy_plot(name, output_path, args.output)


def _plot_ranked_bars(
    args, name, paths, risk_scores, sizes, churns, couplings, ginis, matplotlib, pyplot
):
    """Create horizontal bar chart showing ranked risk scores with breakdown."""
    if args.size is None:
        figsize = (12, max(8, len(paths) * 0.4))
    else:
        figsize = tuple(float(p) for p in args.size.split(","))

    fig, (ax1, ax2) = pyplot.subplots(1, 2, figsize=figsize, gridspec_kw={'width_ratios': [3, 2]})
    apply_plot_style(pyplot, matplotlib, args.style, args.text_size, args.relative)

    # Shorten file paths for display
    display_names = []
    for path in paths:
        parts = path.split('/')
        if len(parts) > 3:
            display_names.append('.../' + '/'.join(parts[-2:]))
        else:
            display_names.append(path)

    y_positions = np.arange(len(paths))

    # Left panel: Risk scores
    colors = pyplot.cm.RdYlGn_r(risk_scores / (risk_scores.max() + 0.001))  # Red for high risk
    ax1.barh(y_positions, risk_scores, color=colors, edgecolor='black', linewidth=0.5)
    ax1.set_yticks(y_positions)
    ax1.set_yticklabels(display_names, fontsize=9)
    ax1.set_xlabel('Composite Risk Score', fontsize=11)
    ax1.set_title(f'Top Risky Files - {name}', fontsize=12, fontweight='bold')
    ax1.grid(axis='x', alpha=0.3, linestyle='--')
    ax1.invert_yaxis()  # Highest risk at top

    # Right panel: Component breakdown (stacked bars with normalized values)
    # Show all four normalized factors
    bar_width = 0.8

    # Get normalized values (these should be in the data, but calculate as fallback)
    max_churn = churns.max() if churns.max() > 0 else 1
    max_coupling = couplings.max() if couplings.max() > 0 else 1
    max_size = sizes.max() if sizes.max() > 0 else 1

    churn_norm = churns / max_churn
    coupling_norm = couplings / max_coupling
    size_norm = np.log(sizes + 1) / np.log(max_size + 1)
    ownership_norm = ginis  # Already normalized

    # Create stacked horizontal bars
    ax2.barh(y_positions, size_norm, bar_width, label='Size (log)', color='#3498db', alpha=0.8)
    ax2.barh(y_positions, churn_norm, bar_width, left=size_norm, label='Churn', color='#e74c3c', alpha=0.8)
    ax2.barh(y_positions, coupling_norm, bar_width, left=size_norm + churn_norm, label='Coupling', color='#f39c12', alpha=0.8)
    ax2.barh(y_positions, ownership_norm, bar_width, left=size_norm + churn_norm + coupling_norm, label='Ownership', color='#9b59b6', alpha=0.8)

    ax2.set_yticks([])  # No labels on this side
    ax2.set_xlabel('Normalized Factors', fontsize=11)
    ax2.set_title('Risk Components', fontsize=12, fontweight='bold')
    ax2.legend(loc='lower right', fontsize=9)
    ax2.set_xlim(0, 4)
    ax2.invert_yaxis()

    pyplot.tight_layout()
    output_path = get_plot_path(args.output, "hotspot_risk_ranked")
    deploy_plot(name, output_path, args.output)
