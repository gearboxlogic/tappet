# Tappet

Tappet is an MCP broker that exposes two tools:

- `get_tools_in_category(path)` browses validated capability packages.
- `execute_tool(tool_path, arguments)` starts the mapped downstream MCP provider on first use and forwards the call.

The current implementation keeps downstream providers open until Tappet shuts down. It serializes calls to the same provider and permits calls to different providers to run concurrently. The outward tool names are inherited compatibility contracts and have not changed in this baseline.

Tappet now loads closed V1-alpha capability manifests, validates listed Agent
Skills and references from private staged bytes, and publishes immutable
in-memory registry generations. Its registry now provides bounded,
deterministic `lexical-v1` catalog search without reading artifacts or starting
providers. The public MCP surface still exposes only hierarchy browsing and
execution; public search exposure, progressive artifact reads, metadata
caching, idle provider shutdown, and the planned four-tool broker API remain
later work.
See [the architecture](docs/ARCHITECTURE.md), [package format](docs/CAPABILITY_PACKAGE.md),
[MCP conformance matrix](docs/MCP_CONFORMANCE.md), and [roadmap](docs/ROADMAP.md)
for accepted boundaries, verified protocol behavior, and planned work.

## Build

Go 1.25.5 or newer is required. CI, release artifacts, and the container image use Go 1.25.5.

```bash
make build
```

This creates:

```text
build/tappet
build/tappet-structure-generator
```

Both commands are shipped in release archives. The structure generator remains
a supported companion command for provider inventory and for generating
review-required capability-package candidates from an inherited hierarchy.

Generate reviewable package candidates from an existing hierarchy:

```bash
./build/tappet-structure-generator \
  --capability-candidates-from testdata/mcp_hierarchy \
  --output /tmp/tappet-capability-candidates
```

Generated candidates are not authoritative capability boundaries. Review them
before moving them into the configured package root. The legacy hierarchy
generator and runtime backend remain available as a compatibility path.

Run Tappet over stdio:

```bash
./build/tappet --config config.json
```

For Claude Code, one possible registration is:

```bash
claude mcp add --transport stdio tappet build/tappet -- --config config.json
```

See [configuration](docs/CONFIGURATION.md), [usage](docs/USAGE.md), and [deployment](docs/DEPLOYMENT.md) for the current implementation.

## Example flow

```text
get_tools_in_category("")
get_tools_in_category("everything")
execute_tool("everything.echo", {"message": "hello"})
```

The first two calls browse immutable package records and do not start the
`everything` provider. The third call leases the package generation, starts and
initializes the provider, resolves the operation binding, and returns the MCP
result without flattening structured content.

## Permission-hook examples

The scripts under `examples/hooks` show how a harness can inspect `execute_tool` arguments. They are optional integrations, not authorization controls. A downstream provider must still enforce authorization for every invocation.

## Origin and license

Tappet began from [`voicetreelab/lazy-mcp`](https://github.com/voicetreelab/lazy-mcp). The original MIT license, authorship, and Git history are preserved. See [LICENSE](LICENSE) and [prior art](docs/PRIOR_ART.md).
