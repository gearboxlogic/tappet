# Usage

## CLI

```text
-config string         path to a config file or HTTP(S) URL (default "config.json")
-expand-env            expand environment variables (default true)
-http-headers string   headers for a remote config URL
-http-timeout int      remote config timeout in seconds (default 10)
-capabilities string   path to capability package directory (overrides config)
-hierarchy string      path to hierarchy directory (overrides config)
-insecure              skip TLS verification for remote config
-port string           override the configured HTTP port
-version               print version and exit
-help                  print help and exit
```

## Outward tools

Tappet currently exposes exactly two tools.

### `get_tools_in_category`

Input:

```json
{"path": "everything"}
```

The result is JSON text with `path`, direct `children`, and available `tools`.
Each package-backed tool includes its description, exact compatibility
`tool_path`, `capability_id`, and `operation_id`. Use an empty path or `/` for
the root.

Browsing reads immutable capability records loaded at startup. It does not
connect to a downstream provider. When no `capabilityPath` is configured, the
same tool browses the inherited generated hierarchy compatibility backend.

### `execute_tool`

Input:

```json
{
  "tool_path": "everything.echo",
  "arguments": {
    "message": "hello"
  }
}
```

Tappet resolves the exact path to one immutable capability and provider-binding
generation, starts and initializes the mapped provider if needed, calls the
downstream target, and returns its MCP result. It reuses that provider until
shutdown.

Calls to one provider are serialized. Calls to different providers may overlap. Each invocation has a 30-second deadline covering lazy provider startup, initialization, time spent waiting for the same-provider lock, and the downstream call. A provider's connection and ping task use a separate registry lifecycle context, so canceling the request that first starts an SSE provider does not terminate the cached provider.

An MCP result with `isError: true` remains an MCP result, and structured content
is preserved. The compatibility hierarchy backend may append its stored input
schema to a downstream error as a diagnostic. Package manifests intentionally
contain no provider schemas; provider metadata caching and schema selection are
Milestone 4 work.

## HTTP authentication

When `options.authTokens` is non-empty, send:

```text
Authorization: Bearer <token>
```

In SSE mode, clients connect to `/sse`; downstream client messages use `/message`. In Streamable HTTP mode, clients connect to `/`.
