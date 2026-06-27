#!/usr/bin/env bash
# bench-large-repo.sh — measure hercules runtime and peak RSS against a real
# repository, for a small set of representative analysis configurations.
#
# Usage:   scripts/bench-large-repo.sh <repo-path> [output-dir]
# Example: scripts/bench-large-repo.sh /mnt/projekte/Code/cpython results/
#
# Output:
#   <output-dir>/<config>.time   — full /usr/bin/time -v capture
#   <output-dir>/<config>.yml    — hercules YAML output (for sanity, can be deleted)
#   <output-dir>/summary.tsv     — parseable: config, wall_seconds, peak_rss_kb, status
#
# The script uses /usr/bin/time (GNU time), not the shell builtin. On debian-likes:
#   sudo apt-get install time
#
# Acceptance for ROADMAP Milestone 2: a documented command set completes
# without OOM and produces reproducible results.

set -euo pipefail

PROG=$(basename "$0")
HERE=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$HERE/.." && pwd)

die() { printf '%s: %s\n' "$PROG" "$*" >&2; exit 1; }

[[ $# -ge 1 ]] || die "missing <repo-path>; usage: $PROG <repo-path> [output-dir]"

TARGET=$(cd "$1" && pwd)
[[ -d "$TARGET/.git" ]] || die "not a git repository: $TARGET"

OUT_DIR=${2:-"$REPO_ROOT/bench-results"}
mkdir -p "$OUT_DIR"

# Locate /usr/bin/time, *not* the shell builtin.
TIME_BIN=$(command -v /usr/bin/time || true)
[[ -x "$TIME_BIN" ]] || die "/usr/bin/time not found; install GNU time (apt: 'time' package)"

# Build hercules if not present alongside this script.
HERCULES=${HERCULES:-"$REPO_ROOT/hercules"}
if [[ ! -x "$HERCULES" ]]; then
    printf '%s: building hercules…\n' "$PROG" >&2
    (cd "$REPO_ROOT" && just hercules >&2)
fi

# Configurations to time. Each entry: "name|flags".
# --pb keeps serialization cost low; we don't care about output content here.
CONFIGS=(
    "quick|--preset quick --burndown --pb"
    "burndown-fp|--burndown --first-parent --pb"
    "burndown-lr|--burndown --preset large-repo --pb"
    "devs-fp|--devs --first-parent --pb"
)

SUMMARY="$OUT_DIR/summary.tsv"
{
    printf 'config\twall_seconds\tpeak_rss_kb\tstatus\n'
} > "$SUMMARY"

# Capture system info for reproducibility context.
{
    printf '# bench-large-repo.sh run on %s\n' "$(date -Iseconds)"
    printf '# repo: %s\n' "$TARGET"
    printf '# commits: %s\n' "$(git -C "$TARGET" rev-list --count HEAD)"
    printf '# hercules: %s\n' "$("$HERCULES" version 2>/dev/null || echo unknown)"
    printf '# host: %s %s\n' "$(uname -srm)" "$(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | sed 's/^[^:]*: //')"
    printf '# memory: %s\n' "$(grep -m1 MemTotal /proc/meminfo 2>/dev/null || true)"
} > "$OUT_DIR/environment.txt"

run_one() {
    local name=$1
    local flags=$2
    local time_file="$OUT_DIR/$name.time"
    local out_file="$OUT_DIR/$name.yml"
    local status="ok"

    printf '%s: running config %s …\n' "$PROG" "$name" >&2

    # /usr/bin/time -v writes to stderr; redirect it to a dedicated file so we
    # don't trip over hercules' own stderr noise.
    if ! "$TIME_BIN" -v -o "$time_file" \
            "$HERCULES" $flags "$TARGET" > "$out_file" 2>"$time_file.stderr"; then
        status="failed"
    fi

    # Peak resident set size, KB. GNU time labels it "Maximum resident set size".
    local rss
    rss=$(awk -F': ' '/Maximum resident set size/{print $2}' "$time_file" | tr -d '[:space:]')
    rss=${rss:-0}

    # Wall-clock time, format h:mm:ss.s OR m:ss.s — convert to seconds.
    local wall_raw
    wall_raw=$(awk -F': ' '/Elapsed \(wall clock\) time/{print $NF}' "$time_file" | tr -d '[:space:]')
    local wall_sec
    wall_sec=$(awk -v t="$wall_raw" 'BEGIN{
        n = split(t, a, ":");
        s = 0;
        for (i = 1; i <= n; i++) s = s * 60 + a[i] + 0;
        printf "%.1f", s;
    }')

    printf '%s\t%s\t%s\t%s\n' "$name" "$wall_sec" "$rss" "$status" >> "$SUMMARY"
}

for cfg in "${CONFIGS[@]}"; do
    name=${cfg%%|*}
    flags=${cfg#*|}
    run_one "$name" "$flags"
done

printf '\n%s: done. Summary:\n\n' "$PROG" >&2
column -t -s$'\t' "$SUMMARY"
