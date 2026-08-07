#!/usr/bin/env bash
set -Eeuo pipefail

# Scan only caller-selected public artifact roots. This command never walks
# the repository, authored source, or the ExecPlan.
LC_ALL=C
export LC_ALL

readonly SCRIPT_NAME="$(basename "$0")"
readonly MAX_FILES=10000
readonly MAX_FILE_BYTES=$((16 * 1024 * 1024))
readonly MAX_TOTAL_BYTES=$((128 * 1024 * 1024))

die() {
  printf '%s: %s\n' "$SCRIPT_NAME" "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage:
  check-assistant-public-surface.sh [--root PATH]... [--http PATH]...
  check-assistant-public-surface.sh APP_ROOT [PUBLIC_ROOT ...]

--root and --http name explicit roots containing public generated artifacts or
captured HTTP fixtures. Positional directories are scanned directly unless
they look like a Scenery app root; an app root is limited to named public
capture directories beneath .scenery/harness/assistant-acceptance/.
EOF
}

if [[ "${1:-}" == "--help" || "${1:-}" == "-h" ]]; then
  usage
  exit 0
fi

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/scenery-assistant-public-surface.XXXXXX")" || die "cannot create temporary directory"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

roots=()

append_root() {
  local path="$1"
  local i
  for i in "${!roots[@]}"; do
    if [[ "${roots[$i]}" == "$path" ]]; then
      return 0
    fi
  done
  roots+=("$path")
}

require_existing_path() {
  local path="$1"
  [[ -n "$path" ]] || die "empty input path"
  [[ ! -L "$path" ]] || die "symlink input is not allowed: $path"
  [[ -e "$path" || -d "$path" ]] || die "input path does not exist: $path"
  [[ -r "$path" ]] || die "input path is not readable: $path"
}

# Positional app roots are restricted to these public-only directories. The
# acceptance directory can also be passed directly; private logs/descriptors
# are never included by discovery.
discover_app_root() {
  local app="$1"
  local candidate
  local found=0
  local acceptance_root

  require_existing_path "$app"
  [[ -d "$app" ]] || die "app-root input is not a directory: $app"

  if [[ -f "$app/.scenery.json" ]]; then
    acceptance_root="$app/.scenery/harness/assistant-acceptance"
    for candidate in \
      "$acceptance_root/public" "$acceptance_root/artifacts" \
      "$acceptance_root/http" "$acceptance_root/http-fixtures" \
      "$acceptance_root/responses" "$acceptance_root/browser" \
      "$acceptance_root/openapi" "$acceptance_root/schemas" \
      "$acceptance_root/routes" "$acceptance_root/docs" \
      "$app/assistant-acceptance/public" \
      "$app/assistant-acceptance/artifacts" \
      "$app/assistant-acceptance/http" \
      "$app/assistant-acceptance/responses"; do
      if [[ -d "$candidate" ]]; then
        append_root "$candidate"
        found=1
      fi
    done
    (( found == 1 )) || die "app root has no recognized public capture directories: $app"
    return 0
  fi

  case "$(basename "$app")" in
    assistant-acceptance|public-capture|public-artifacts)
      for candidate in \
        "$app/public" "$app/artifacts" "$app/http" "$app/http-fixtures" \
        "$app/responses" "$app/browser" "$app/openapi" "$app/schemas" \
        "$app/routes" "$app/docs"; do
        if [[ -d "$candidate" ]]; then
          append_root "$candidate"
          found=1
        fi
      done
      (( found == 1 )) || die "capture root has no recognized public directories: $app"
      return 0
      ;;
  esac

  # A non-app positional path is an explicit artifact root.
  append_root "$app"
}

if (( $# == 0 )); then
  usage >&2
  exit 2
fi

while (( $# > 0 )); do
  case "$1" in
    --root|--http)
      (( $# >= 2 )) || die "$1 requires a path"
      require_existing_path "$2"
      append_root "$2"
      shift 2
      ;;
    --)
      shift
      while (( $# > 0 )); do
        discover_app_root "$1"
        shift
      done
      ;;
    -*)
      die "unknown option: $1"
      ;;
    *)
      discover_app_root "$1"
      shift
      ;;
  esac
done

(( ${#roots[@]} > 0 )) || die "no public artifact roots were supplied"

file_size() {
  local file="$1"
  local size
  if size="$(stat -f '%z' "$file" 2>/dev/null)" && [[ "$size" =~ ^[0-9]+$ ]]; then
    printf '%s' "$size"
    return 0
  fi
  if size="$(stat -c '%s' "$file" 2>/dev/null)" && [[ "$size" =~ ^[0-9]+$ ]]; then
    printf '%s' "$size"
    return 0
  fi
  return 1
}

files=()
for root in "${roots[@]}"; do
  require_existing_path "$root"
  if [[ -f "$root" ]]; then
    files+=("$root")
    continue
  fi
  [[ -d "$root" ]] || die "public root is neither regular file nor directory: $root"
  symlink="$(find -P "$root" -type l -print -quit 2>/dev/null || true)"
  [[ -z "$symlink" ]] || die "symlink input is not allowed: $symlink"
  special="$(find -P "$root" ! -type d ! -type f ! -type l -print -quit 2>/dev/null || true)"
  [[ -z "$special" ]] || die "special file input is not allowed: $special"
  while IFS= read -r -d '' file; do
    case "$file" in
      *$'\n'*|*$'\r'*) die "input path contains a newline or carriage return: $file" ;;
    esac
    files+=("$file")
  done < <(find -P "$root" -type f -print0 2>/dev/null)
done

(( ${#files[@]} > 0 )) || die "public roots contain no regular files"
sorted_manifest="$tmp_dir/sorted-manifest"
printf '%s\n' "${files[@]}" | LC_ALL=C sort -u > "$sorted_manifest"
manifest="$tmp_dir/manifest"
: > "$manifest"
while IFS= read -r file; do
  [[ -n "$file" ]] || continue
  case "$file" in
    *$'\n'*|*$'\r'*) die "input path contains a newline or carriage return: $file" ;;
  esac
  printf '%s\n' "$file" >> "$manifest"
done < "$sorted_manifest"

file_count=0
total_bytes=0
raw="$tmp_dir/raw"
normalized="$tmp_dir/normalized"
: > "$raw"

# The exact provider token is checked only at lexical boundaries. In
# particular, "event" and "revisions" are ordinary allowed public words.
token_re='(^|[^[:alnum:]_])[Ee][Vv][Ee]([^[:alnum:]_]|$)'
# These are known provider-specific spellings that are not caught by the
# standalone token rule (camelCase and underscore namespaces).
known_re='(^|[^[:alnum:]_])([Uu][Ss][Ee][Ee][Vv][Ee][Aa][Gg][Ee][Nn][Tt]|[Ee][Vv][Ee][Aa][Gg][Ee][Nn][Tt]|[Ee][Vv][Ee][Cc][Hh][Aa][Nn][Nn][Ee][Ll]|[Ee][Vv][Ee][Rr][Uu][Nn][Tt][Ii][Mm][Ee]|[Ee][Vv][Ee][Ss][Ee][Ss][Ss][Ii][Oo][Nn]|[Ee][Vv][Ee][Ww][Oo][Rr][Kk][Ff][Ll][Oo][Ww]|[Ee][Vv][Ee]_[[:alnum:]_-]+|[Xx]-[Ee][Vv][Ee]-|@[Ee][Vv][Ee]/|/[_-][Ee][Vv][Ee]/|[Ee][Vv][Ee]-[[:alnum:]_-]+)([^[:alnum:]_]|$)'
violations=0

report_hit() {
  local label="$1"
  local path="$2"
  printf '%s: forbidden %s in %s\n' "$SCRIPT_NAME" "$label" "$path" >&2
  violations=1
}

scan_text() {
  local file="$1"
  local display="$2"
  local label="$3"
  local pattern="$4"
  local hit_file="$tmp_dir/hit"
  local rc
  if LC_ALL=C grep -Eni "$pattern" "$file" > "$hit_file" 2>/dev/null; then
    report_hit "$label" "$display"
  else
    rc=$?
    (( rc == 1 )) || die "cannot scan input: $display"
  fi
}

public_path() {
  local file="$1"
  local root
  for root in "${roots[@]}"; do
    case "$file" in
      "$root"/*)
        printf '%s' "${file#"$root"/}"
        return 0
        ;;
      "$root")
        printf '%s' "$(basename "$file")"
        return 0
        ;;
    esac
  done
  printf '%s' "$(basename "$file")"
}

while IFS= read -r file; do
  [[ -n "$file" ]] || continue
  [[ -f "$file" && ! -L "$file" ]] || die "manifest input changed or is unsafe: $file"
  size="$(file_size "$file")" || die "cannot determine size: $file"
  (( size <= MAX_FILE_BYTES )) || die "input file exceeds ${MAX_FILE_BYTES} bytes: $file"
  (( file_count < MAX_FILES )) || die "too many public input files (limit ${MAX_FILES})"
  total_bytes=$((total_bytes + size))
  (( total_bytes <= MAX_TOTAL_BYTES )) || die "public input exceeds ${MAX_TOTAL_BYTES} total bytes"

  if (( size > 0 )); then
    if ! LC_ALL=C grep -Iq . "$file" >/dev/null 2>&1; then
      die "binary or unreadable public input: $file"
    fi
  fi

  scan_text "$file" "$file" "provider token" "$token_re"
  scan_text "$file" "$file" "provider signature" "$known_re"
  path_text="$(public_path "$file")"
  scan_text <(printf '%s\n' "$path_text") "$path_text" "provider token in public filename" "$token_re"
  scan_text <(printf '%s\n' "$path_text") "$path_text" "provider signature in public filename" "$known_re"
  cat "$file" >> "$raw" || die "cannot read public input: $file"
  file_count=$((file_count + 1))
done < "$manifest"

# Concatenation catches a split E + ve chunk. Removing whitespace additionally
# catches chunks split across line/JSON separators, without matching "event".
tr -d '[:space:]' < "$raw" > "$normalized" || die "cannot normalize public input"
scan_text "$raw" "<reconstructed public chunks>" "provider token after reconstruction" "$token_re"
scan_text "$raw" "<reconstructed public chunks>" "provider signature after reconstruction" "$known_re"
scan_text "$normalized" "<normalized public chunks>" "provider token after normalization" "$token_re"
scan_text "$normalized" "<normalized public chunks>" "provider signature after normalization" "$known_re"

if (( violations != 0 )); then
  exit 1
fi

printf '%s: clean (%d files, %d bytes, %d roots)\n' "$SCRIPT_NAME" "$file_count" "$total_bytes" "${#roots[@]}"
