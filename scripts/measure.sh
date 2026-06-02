#!/usr/bin/env bash
# chaosbot performance baseline (docs/performance.md).
# Portable across macOS and Linux. Steps whose subcommand is not yet
# built are skipped with a clear "not yet implemented" note.

set -uo pipefail

BIN_DIR=${BIN_DIR:-bin}
BIN=${BIN:-$BIN_DIR/chaosbot}
GO=${GO:-go}

# Limits from docs/performance.md §1 (MB)
LIM_BIN=${LIM_BIN:-25}
LIM_COLD=${LIM_COLD:-30}
LIM_STEADY=${LIM_STEADY:-40}
LIM_PEAK=${LIM_PEAK:-80}

bytes_to_mb() { awk "BEGIN { printf \"%.2f\", $1/1024/1024 }"; }
kb_to_mb()    { awk "BEGIN { printf \"%.2f\", $1/1024 }"; }

# sample_rss_kb <command...>: report max RSS in KB. The measured
# command's own stdout/stderr is discarded — only the max RSS echoes.
# Linux: uses /usr/bin/time -v (true peak via getrusage).
# macOS / fallback: tight ps polling.
sample_rss_kb() {
    if [[ -x /usr/bin/time ]] && /usr/bin/time -v true 2>&1 | grep -q "Maximum resident set size"; then
        local out m
        out=$(/usr/bin/time -v "$@" 2>&1) || true
        m=$(echo "$out" | awk -F': ' '/Maximum resident set size/ {gsub(/[^0-9]/,"",$2); print $2; exit}')
        if [[ -n "$m" && "$m" -gt 0 ]]; then
            echo "$m"
            return
        fi
    fi
    local max=0
    "$@" >/dev/null 2>&1 &
    local pid=$!
    while kill -0 "$pid" 2>/dev/null; do
        local rss
        rss=$(ps -o rss= -p "$pid" 2>/dev/null | tr -d ' ')
        [[ -n "$rss" && "$rss" -gt "$max" ]] && max=$rss
        sleep 0
    done
    wait "$pid" 2>/dev/null || true
    echo "$max"
}

has_subcmd() { "$BIN" "$1" --help >/dev/null 2>&1; }

echo "==> Building..."
"$GO" build -trimpath -ldflags "-s -w" -o "$BIN" ./... || { echo "build failed"; exit 1; }

size_b=$(stat -f %z "$BIN" 2>/dev/null || stat -c %s "$BIN")
echo "==> Binary size : $(bytes_to_mb "$size_b") MB  (limit ${LIM_BIN} MB)"

echo "==> Cold-start  : $(kb_to_mb "$(sample_rss_kb "$BIN" version)") MB  (limit ${LIM_COLD} MB)"

if has_subcmd repl; then
    echo "==> Steady-state: $(kb_to_mb "$(sample_rss_kb "$BIN" repl < /dev/null)") MB  (limit ${LIM_STEADY} MB)"
else
    echo "==> Steady-state: skipped (REPL not yet implemented; Phase 07-4)"
fi

if has_subcmd bench; then
    echo "==> Peak / Run  : $(kb_to_mb "$(sample_rss_kb "$BIN" bench)") MB  (limit ${LIM_PEAK} MB)"
else
    echo "==> Peak / Run  : skipped (synthetic bench not yet implemented; Phase 08-2)"
fi

echo "==> Done."
