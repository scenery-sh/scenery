#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'EOF'
usage: snapshot-backup.sh --app-root <path> --output-dir <path> [--keep <count>] [--copy-to <rclone-destination>]

Creates and verifies a database-plus-storage snapshot, optionally copies it
off-machine with rclone, then retains the newest local snapshots. Run this
script from launchd, systemd, or cron; it does not install a scheduler.
EOF
}

app_root=""
output_dir=""
keep=14
copy_to=""

while (( $# > 0 )); do
  case "$1" in
    --app-root) app_root="${2:-}"; shift 2 ;;
    --output-dir) output_dir="${2:-}"; shift 2 ;;
    --keep) keep="${2:-}"; shift 2 ;;
    --copy-to) copy_to="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'snapshot backup: unknown argument %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done

[[ -n "$app_root" ]] || { printf 'snapshot backup: --app-root is required\n' >&2; exit 2; }
[[ -n "$output_dir" ]] || { printf 'snapshot backup: --output-dir is required\n' >&2; exit 2; }
[[ "$keep" =~ ^[1-9][0-9]*$ ]] || { printf 'snapshot backup: --keep must be a positive integer\n' >&2; exit 2; }
command -v scenery >/dev/null 2>&1 || { printf 'snapshot backup: scenery is not installed\n' >&2; exit 1; }
if [[ -n "$copy_to" ]]; then
  command -v rclone >/dev/null 2>&1 || { printf 'snapshot backup: rclone is required for --copy-to\n' >&2; exit 1; }
fi

mkdir -p "$output_dir"
lock_dir="$output_dir/.snapshot-backup.lock"
if ! mkdir "$lock_dir" 2>/dev/null; then
  lock_pid="$(cat "$lock_dir/pid" 2>/dev/null || true)"
  if [[ "$lock_pid" =~ ^[1-9][0-9]*$ ]] && kill -0 "$lock_pid" 2>/dev/null; then
    printf 'snapshot backup: another backup is running for %s (pid %s)\n' "$output_dir" "$lock_pid" >&2
    exit 1
  fi
  rm -rf -- "$lock_dir"
  mkdir "$lock_dir" 2>/dev/null || { printf 'snapshot backup: could not recover stale lock for %s\n' "$output_dir" >&2; exit 1; }
fi
printf '%s\n' "$$" > "$lock_dir/pid"
cleanup() { rm -rf -- "$lock_dir"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

archive="$output_dir/snapshot-$(date -u +%Y%m%dT%H%M%SZ).zip"
scenery snapshot save --db --storage --app-root "$app_root" --output "$archive" -o json
scenery snapshot verify --input "$archive" -o json

if [[ -n "$copy_to" ]]; then
  rclone copyto --checksum "$archive" "${copy_to%/}/$(basename "$archive")"
fi

index=0
while IFS= read -r file; do
  index=$((index + 1))
  if (( index > keep )); then
    rm -- "$file"
  fi
done < <(find "$output_dir" -maxdepth 1 -type f -name 'snapshot-*.zip' -print | LC_ALL=C sort -r)

printf '%s\n' "$archive"
