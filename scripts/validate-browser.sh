#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
project="${1:?Specify chromium-touch or webkit-ipad}"
shift
suite_args=("$@")
case "$project" in
  chromium-touch) default_port=8094 ;;
  webkit-ipad) default_port=8095 ;;
  *) echo "Unknown browser project: $project" >&2; exit 1 ;;
esac
port="${WEBFILE_E2E_PORT:-$default_port}"
if [[ ! "$port" =~ ^[0-9]+$ ]] || ((port < 1 || port > 65535)); then
  echo 'WEBFILE_E2E_PORT must be between 1 and 65535.' >&2
  exit 1
fi
work_dir="$ROOT/dist/validation/$project"
shard="${WEBFILE_E2E_SHARD:-}"
shard_args=()
if [[ -n "$shard" ]]; then
  if [[ ! "$shard" =~ ^([1-9][0-9]*)/([1-9][0-9]*)$ ]] ||
     ((BASH_REMATCH[1] > BASH_REMATCH[2])); then
    echo 'WEBFILE_E2E_SHARD must be INDEX/TOTAL with 1 <= INDEX <= TOTAL.' >&2
    exit 1
  fi
  work_dir="$work_dir/shard-${BASH_REMATCH[1]}-of-${BASH_REMATCH[2]}"
  shard_args=("--shard=$shard")
fi
mkdir -p "$work_dir"
runtime_dir="$(mktemp -d "/tmp/webfile-$project.XXXXXX")"
webfile_pid=''
stop_server() {
  if [[ -n "$webfile_pid" ]]; then
    kill "$webfile_pid" 2>/dev/null || true
    for attempt in {1..50}; do
      if ! kill -0 "$webfile_pid" 2>/dev/null; then break; fi
      sleep 0.2
    done
    kill -KILL "$webfile_pid" 2>/dev/null || true
    wait "$webfile_pid" 2>/dev/null || true
    webfile_pid=''
  fi
}
cleanup() {
  stop_server
  case "$runtime_dir" in
    /tmp/webfile-*) rm -rf -- "$runtime_dir" ;;
  esac
}
trap cleanup EXIT

python3 - "$ROOT/webfile.dev.json" "$runtime_dir" "$work_dir/config.json" "$port" <<'PY'
import json, pathlib, sys
config = json.loads(pathlib.Path(sys.argv[1]).read_text())
runtime = pathlib.Path(sys.argv[2])
files = runtime / 'files'
for index in range(8):
    folder = files / f'folder-{index}' / 'nested'
    folder.mkdir(parents=True)
    (folder / 'example.txt').write_text('Browser validation fixture\n')
(files / 'notes.txt').write_text('Browser validation fixture\n')
config['listen'] = '127.0.0.1:' + sys.argv[4]
config['roots'] = [{'name': 'Files', 'path': str(files)}]
config['runtime']['worker_dir'] = str(runtime / 'workers')
config['runtime']['data_dir'] = str(runtime / 'data')
config['thumbnails']['cache_dir'] = str(runtime / 'thumbnails')
config['thumbnails']['cache_socket'] = str(runtime / 'thumb.sock')
pathlib.Path(sys.argv[3]).write_text(json.dumps(config))
PY

cd "$ROOT"
# Dependencies are installed by production validation or the CI setup step.
npm --prefix web run build
go -C backend build -o "$work_dir/webfile-dev" ./cmd/webfile
export WEBFILE_E2E_BASE_URL="http://127.0.0.1:$port"
export WEBFILE_E2E_FIXTURE_PARENT=/Files
unset WEBFILE_E2E_PREFIX
wait_for_server() {
  local prefix="$1" log_file="$2"
  for attempt in {1..100}; do
    if curl --fail --silent --max-time 1 "$WEBFILE_E2E_BASE_URL$prefix/api/health" >/dev/null; then return; fi
    if ! kill -0 "$webfile_pid" 2>/dev/null; then break; fi
    sleep 0.2
  done
  cat "$log_file"
  return 1
}
"$work_dir/webfile-dev" serve -config "$work_dir/config.json" > "$work_dir/server.log" 2>&1 &
webfile_pid=$!
wait_for_server '' "$work_dir/server.log"
(
  cd web
  if ((${#suite_args[@]})); then
    printf 'Focused browser validation: %s\n' "${suite_args[*]}"
  else
    printf 'Full browser validation: %s\n' "$project"
  fi
  if [[ -n "$shard" ]]; then printf 'Browser shard: %s\n' "$shard"; fi
  PLAYWRIGHT_JSON_OUTPUT_FILE="$work_dir/results.json" \
    npx playwright test "${suite_args[@]}" "${shard_args[@]}" --project="$project" \
      --output="$work_dir/test-results" \
      --reporter=line,json --global-timeout=900000
)
stop_server
"$work_dir/webfile-dev" serve -config "$work_dir/config.json" \
  --public-url "$WEBFILE_E2E_BASE_URL/tools/webfile" > "$work_dir/server-prefix.log" 2>&1 &
webfile_pid=$!
wait_for_server /tools/webfile "$work_dir/server-prefix.log"
(
  cd web
  WEBFILE_E2E_PREFIX=/tools/webfile PLAYWRIGHT_JSON_OUTPUT_FILE="$work_dir/results-prefix.json" \
    npx playwright test tests/e2e/base-path.spec.ts \
      --project="$project" --output="$work_dir/test-results-prefix" \
      --reporter=line,json --global-timeout=120000
)
