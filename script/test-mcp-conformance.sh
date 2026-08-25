#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

port=${TAPPET_CONFORMANCE_PORT:-19091}
output_dir=${TAPPET_CONFORMANCE_OUTPUT:-build/conformance}
server_log="$output_dir/tappet.log"
mkdir -p "$output_dir"

go build -o build/tappet ./cmd/tappet
go build -o build/tappet-conformance-client ./conformance/client
./build/tappet \
  --config testdata/recursive_config_test.json \
  --port "$port" \
  --hierarchy testdata/mcp_hierarchy >"$server_log" 2>&1 &
server_pid=$!

cleanup() {
  kill "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
}
trap cleanup EXIT

probe_server() {
  curl --silent --max-time 1 --output /dev/null \
    --header 'Content-Type: application/json' \
    --data '{}' \
    "http://127.0.0.1:$port"
}

for _ in $(seq 1 100); do
  if probe_server; then
    break
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    cat "$server_log"
    exit 1
  fi
  sleep 0.1
done

if ! probe_server; then
  cat "$server_log"
  echo "Tappet did not become ready" >&2
  exit 1
fi

for revision in 2025-11-25 2026-07-28; do
  npx --yes @modelcontextprotocol/conformance@0.2.0-alpha.11 server \
    --url "http://127.0.0.1:$port" \
    --requirements "$revision" \
    --expected-failures "conformance/expected-failures-$revision.yml" \
    --output-dir "$output_dir/$revision/server"

  npx --yes @modelcontextprotocol/conformance@0.2.0-alpha.11 client \
    --command './build/tappet-conformance-client' \
    --requirements "$revision" \
    --expected-failures "conformance/expected-failures-$revision.yml" \
    --output-dir "$output_dir/$revision/client"
done
