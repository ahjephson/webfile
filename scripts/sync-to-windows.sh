#!/usr/bin/env bash
set -euo pipefail

usage() {
    printf 'Usage: %s [--dry-run]\n' "$(basename "$0")"
}

dry_run=false
case "${1:-}" in
    "")
        ;;
    --dry-run)
        dry_run=true
        ;;
    -h|--help)
        usage
        exit 0
        ;;
    *)
        usage >&2
        exit 2
        ;;
esac

source_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
destination_dir="/mnt/d/webfile"
backup_root="/mnt/d/webfile-sync-backups"

if [[ "$source_dir" != "/home/"*"/src/webfile" ]]; then
    printf 'Refusing to sync from unexpected source: %s\n' "$source_dir" >&2
    printf 'Expected the authoritative copy at /home/<user>/src/webfile.\n' >&2
    exit 1
fi

if [[ ! -d /mnt/d ]] || ! mountpoint -q /mnt/d; then
    printf 'Windows drive D: is not mounted at /mnt/d.\n' >&2
    exit 1
fi

for marker in Makefile backend/go.mod web/package.json; do
    if [[ ! -f "$source_dir/$marker" ]]; then
        printf 'Source validation failed; missing %s.\n' "$source_dir/$marker" >&2
        exit 1
    fi
    if [[ ! -f "$destination_dir/$marker" ]]; then
        printf 'Destination validation failed; missing %s.\n' "$destination_dir/$marker" >&2
        exit 1
    fi
done

rsync_args=(
    --recursive
    --links
    --times
    --checksum
    --itemize-changes
    --human-readable
    --delete-delay
    --exclude=/.git/
    --exclude=/.gocache/
    --exclude=/web/node_modules/
    --exclude=/web/test-results/
    --exclude=/web/playwright-report/
    --exclude=/dist/
    --exclude=/webfile.7z
)

if [[ "$dry_run" == true ]]; then
    rsync_args+=(--dry-run)
    printf 'Previewing WSL -> Windows synchronization\n'
else
    backup_dir="$backup_root/$(date -u +%Y%m%dT%H%M%SZ)"
    mkdir -p "$backup_dir"
    rsync_args+=(--backup "--backup-dir=$backup_dir")
    printf 'Synchronizing WSL -> Windows\n'
    printf 'Replaced or deleted destination files will be saved under %s\n' "$backup_dir"
fi

printf '  source:      %s/\n' "$source_dir"
printf '  destination: %s/\n' "$destination_dir"

rsync "${rsync_args[@]}" "$source_dir/" "$destination_dir/"

if [[ "$dry_run" == true ]]; then
    printf 'Dry run complete; no files were changed.\n'
else
    rmdir "$backup_dir" 2>/dev/null || true
    printf 'Synchronization complete.\n'
fi
