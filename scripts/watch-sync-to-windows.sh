#!/usr/bin/env bash
set -euo pipefail

source_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
sync_script="$source_dir/scripts/sync-to-windows.sh"

if ! command -v inotifywait >/dev/null 2>&1; then
    printf 'inotifywait is required. Install it with: sudo apt install inotify-tools\n' >&2
    exit 1
fi

printf 'Watching %s for changes. Press Ctrl+C to stop.\n' "$source_dir"
printf 'The initial synchronization is a dry run; review it before starting this watcher.\n'

while true; do
    inotifywait \
        --quiet \
        --recursive \
        --event close_write,create,delete,move \
        --exclude '(^|/)(node_modules|test-results|playwright-report)(/|$)' \
        "$source_dir"
    sleep 2
    "$sync_script"
done
