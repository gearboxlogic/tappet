# Configuration

CapScope reads JSON from a local file or an HTTP(S) URL. Environment expansion is enabled by default.

```json
{
  "mcpProxy": {
    "baseURL": "http://localhost:8080",
    "addr": ":8080",
    "name": "CapScope",
    "version": "0.1.0",
    "type": "streamable-http",
    "hierarchyPath": "testdata/mcp_hierarchy",
    "options": {
      "logEnabled": true,
      "authTokens": []
    }
  },
  "mcpServers": {
    "everything": {
      "transportType": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything"],
      "env": {},
      "options": {}
    }
  }
}
```

The `mcpProxy` JSON key is retained for configuration compatibility. It configures the outward CapScope server.

## Outward server

- `baseURL` is the public base URL used by SSE mode.
- `addr` is the HTTP bind address.
- `name` and `version` become MCP server identity metadata.
- `type` accepts `stdio`, `sse`, or `streamable-http`.
- `hierarchyPath` names the generated hierarchy directory.
- `options.logEnabled` enables MCP and HTTP request logging.
- `options.authTokens` accepts bearer tokens for HTTP transports.

## Downstream providers

Each `mcpServers` entry supports one transport:

- `stdio`: `command`, `args`, and `env`
- `sse`: `url` and `headers`
- `streamable-http`: `url`, `headers`, and `timeout`

The current client explicitly performs the legacy MCP initialize handshake after starting a provider.

Environment values use `${NAME}` syntax:

```json
{
  "mcpServers": {
    "example": {
      "transportType": "stdio",
      "command": "${EXAMPLE_SERVER}",
      "args": ["serve"],
      "env": {}
    }
  }
}
```

```bash
export EXAMPLE_SERVER=/path/to/example-server
./build/capscope --config config.json
```

## Generated hierarchy

`capscope-structure-generator` writes `root.json`, one provider overview, and one JSON file per tool. A leaf maps its public hierarchy name to its downstream provider and tool name:

```json
{
  "tools": {
    "echo": {
      "description": "Echoes the input",
      "maps_to": "echo",
      "server": "everything",
      "inputSchema": {
        "type": "object"
      }
    }
  }
}
```

The path follows the file location. For example, `everything/echo.json` resolves as `everything.echo`. `maps_to` may differ from the hierarchy name.
