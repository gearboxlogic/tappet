#!/usr/bin/env bash

set -euo pipefail

image=${1:-capscope:smoke}
container_engine=${CONTAINER_ENGINE:-docker}
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)
response_dir=$(mktemp -d)
container_id=

cleanup() {
  if [[ -n "$container_id" ]]; then
    "$container_engine" rm --force "$container_id" >/dev/null 2>&1 || true
  fi
  rm -rf "$response_dir"
}
trap cleanup EXIT

container_id=$("$container_engine" run --detach \
  --publish 127.0.0.1::9090 \
  --volume "$repo_root/config.docker.json:/config/config.json:ro,Z" \
  --volume "$repo_root/testdata/mcp_hierarchy:/config/hierarchy:ro,Z" \
  "$image")

host_port=$("$container_engine" port "$container_id" 9090/tcp | awk -F: 'NR == 1 { print $NF }')
if [[ -z "$host_port" ]]; then
  "$container_engine" logs "$container_id"
  echo "container did not publish port 9090" >&2
  exit 1
fi

initialize_response="$response_dir/initialize.json"
initialized=false
for _ in $(seq 1 30); do
  if curl --fail --silent --show-error --max-time 2 \
    --header 'Content-Type: application/json' \
    --header 'Accept: application/json, text/event-stream' \
    --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"container-smoke","version":"1.0.0"}}}' \
    "http://127.0.0.1:$host_port/" >"$initialize_response"; then
    initialized=true
    break
  fi

  if [[ $("$container_engine" inspect --format '{{.State.Running}}' "$container_id") != "true" ]]; then
    "$container_engine" logs "$container_id"
    echo "container exited before initialization" >&2
    exit 1
  fi
  sleep 1
done

if [[ "$initialized" != "true" ]]; then
  "$container_engine" logs "$container_id"
  echo "container did not accept an MCP initialize request" >&2
  exit 1
fi
grep -q '"name":"CapScope"' "$initialize_response"

tools_response="$response_dir/tools.json"
curl --fail --silent --show-error --max-time 5 \
  --header 'Content-Type: application/json' \
  --header 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  "http://127.0.0.1:$host_port/" >"$tools_response"

grep -q '"name":"get_tools_in_category"' "$tools_response"
grep -q '"name":"execute_tool"' "$tools_response"
