#!/usr/bin/env bash

set -euo pipefail

image=${1:-tappet:smoke}
container_engine=${CONTAINER_ENGINE:-docker}
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/.." && pwd)
response_dir=$(mktemp -d)
container_id=

cleanup() {

	status=$?
	if [[ "$status" -ne 0 && -n "$container_id" ]]; then
		"$container_engine" logs "$container_id" >&2 || true
		for response in "$response_dir"/*.json; do
			if [[ -f "$response" ]]; then
				echo "smoke response: $response" >&2
				sed -n '1,200p' "$response" >&2
			fi
		done
	fi
	if [[ -n "$container_id" ]]; then
		"$container_engine" rm --force "$container_id" >/dev/null 2>&1 || true
	fi
	rm -rf "$response_dir"
	return "$status"
}
trap cleanup EXIT

container_id=$("$container_engine" run --detach \
  --publish 127.0.0.1::9090 \
  --volume "$repo_root/config.docker.json:/config/config.json:ro,Z" \
  --volume "$repo_root/testdata/capabilities:/config/capabilities:ro,Z" \
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
jq -e '.result.serverInfo.name == "Tappet"' "$initialize_response" >/dev/null

curl --fail --silent --show-error --max-time 5 \
  --output /dev/null \
  --header 'Content-Type: application/json' \
  --header 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' \
  "http://127.0.0.1:$host_port/"

tools_response="$response_dir/tools.json"
curl --fail --silent --show-error --max-time 5 \
  --header 'Content-Type: application/json' \
  --header 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  "http://127.0.0.1:$host_port/" >"$tools_response"

jq -e '[.result.tools[].name] | sort == ["execute_tool", "get_tools_in_category"]' "$tools_response" >/dev/null

capabilities_response="$response_dir/capabilities.json"
curl --fail --silent --show-error --max-time 5 \
  --header 'Content-Type: application/json' \
  --header 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_tools_in_category","arguments":{"path":"everything"}}}' \
  "http://127.0.0.1:$host_port/" >"$capabilities_response"

jq -e '
  .result.content[]
  | select(.type == "text")
  | .text
  | fromjson
  | .tools.add.capability_id == "everything.add"
' "$capabilities_response" >/dev/null
