# Configuration

Tappet reads JSON from a local file or an HTTP(S) URL. Environment expansion is enabled by default.
Pin provider package versions when checked-in capability operation targets
depend on their tool names. Review the affected packages when the pin changes.

```json
{
  "mcpProxy": {
    "baseURL": "http://localhost:8080",
    "addr": ":8080",
    "name": "Tappet",
    "version": "0.1.0",
    "type": "streamable-http",
    "capabilityPath": "testdata/capabilities",
    "options": {
      "logEnabled": true,
      "authTokens": []
    }
  },
  "mcpServers": {
    "everything": {
      "transportType": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-everything@2025.9.25"],
      "env": {},
      "options": {}
    }
  }
}
```

The `mcpProxy` JSON key is retained for configuration compatibility. It configures the outward Tappet server.

## Outward server

- `baseURL` is the public base URL used by SSE mode.
- `addr` is the HTTP bind address.
- `name` and `version` become MCP server identity metadata.
- `type` accepts `stdio`, `sse`, or `streamable-http`.
- `capabilityPath` names the directory containing one subdirectory per
  V1-alpha capability package. It is the primary runtime domain model.
- `hierarchyPath` names an inherited generated hierarchy directory. Tappet uses
  it only when `capabilityPath` is absent.
- `options.logEnabled` enables MCP and HTTP request logging.
- `options.authTokens` accepts bearer tokens for HTTP transports.

## Downstream providers

Each `mcpServers` entry supports one transport:

- `stdio`: `command`, `args`, and `env`
- `sse`: `url` and `headers`
- `streamable-http`: `url`, `headers`, and `timeout`

After starting a provider, the client first attempts modern `server/discover`.
It uses stateless 2026-07-28 requests when discovery succeeds and falls back to
the legacy initialize handshake for providers through 2025-11-25.

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
./build/tappet --config config.json
```

## Capability packages

Each immediate subdirectory of `capabilityPath` must be named for its exact
capability ID and contain `tappet.yaml`. Every listed `SKILL.md`, skill
resource, and context reference is opened without following links, copied into
a bounded private staging transaction, validated there, and retained in an
immutable process-local snapshot. A failure in any package aborts the complete
startup load.

Provider bindings contain only `id`, `type`, and `serverRef`. Commands, URLs,
headers, environment variables, tokens, and other provider credentials remain
in `mcpServers`; the closed manifest schema rejects them in packages.

Local package ingestion is supported on Linux and macOS through
descriptor-relative, no-follow, nonblocking file opens. Other platforms reject
local ingestion unless an equivalent safe adapter is implemented. The legacy
hierarchy backend remains available on those platforms.

The command-line `--capabilities` flag selects package mode and overrides the
configured path. `--hierarchy` selects the compatibility backend when
`--capabilities` is not also supplied. If both flags are supplied,
`--capabilities` wins.

## Candidate generation

Convert the inherited hierarchy into deterministic review-required package
candidates with:

```bash
./build/tappet-structure-generator \
  --capability-candidates-from testdata/mcp_hierarchy \
  --output /tmp/tappet-capability-candidates
```

The generator preserves provider and downstream target mappings, but excludes
provider configuration, credentials, and tool schemas. It marks every output
as generated and refuses to overwrite a reviewed package. Review names,
descriptions, boundaries, skills, and context before installation.

## Compatibility hierarchy

`tappet-structure-generator` writes `root.json`, one provider overview, and one JSON file per tool. A leaf maps its public hierarchy name to its downstream provider and tool name:

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
