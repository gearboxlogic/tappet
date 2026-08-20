# Usage

## CLI

```text
-config string         path to a config file or HTTP(S) URL (default "config.json")
-expand-env            expand environment variables (default true)
-http-headers string   headers for a remote config URL
-http-timeout int      remote config timeout in seconds (default 10)
-hierarchy string      path to hierarchy directory (overrides config)
-insecure              skip TLS verification for remote config
-port string           override the configured HTTP port
-version               print version and exit
-help                  print help and exit
```

## Outward tools

CapScope currently exposes exactly two tools.

### `get_tools_in_category`

Input:

```json
{"path": "everything"}
```

The result is JSON text with `path`, an optional `overview`, direct `children`, and available `tools`. Each tool includes its description and exact `tool_path`. Use an empty path or `/` for the root.

Browsing reads the generated JSON loaded at startup. It does not connect to a downstream provider.

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

CapScope resolves the exact path, starts and initializes the mapped provider if needed, calls the mapped downstream tool, and returns its MCP result. It reuses that provider until shutdown.

Calls to one provider are serialized. Calls to different providers may overlap. Each invocation has a 30-second deadline covering lazy provider startup, initialization, time spent waiting for the same-provider lock, and the downstream call. A provider's connection and ping task use a separate registry lifecycle context, so canceling the request that first starts an SSE provider does not terminate the cached provider.

When a downstream call returns an error and the generated hierarchy contains an input schema, CapScope appends that schema as a diagnostic. An MCP result with `isError: true` remains an MCP result, and structured content is preserved.

## HTTP authentication

When `options.authTokens` is non-empty, send:

```text
Authorization: Bearer <token>
```

CapScope uses the root path for its HTTP handler in both SSE and Streamable HTTP modes.
