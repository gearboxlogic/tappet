# CapScope

CapScope is an MCP broker that exposes two tools:

- `get_tools_in_category(path)` browses a generated JSON hierarchy.
- `execute_tool(tool_path, arguments)` starts the mapped downstream MCP provider on first use and forwards the call.

The current implementation keeps downstream providers open until CapScope shuts down. It serializes calls to the same provider and permits calls to different providers to run concurrently. The outward tool names are inherited compatibility contracts and have not changed in this baseline.

CapScope does not yet implement capability packages, Agent Skills loading, search, metadata caching, idle provider shutdown, or the planned broker API. See [the architecture](docs/ARCHITECTURE.md), [package proposal](docs/CAPABILITY_PACKAGE.md), and [roadmap](docs/ROADMAP.md) for accepted boundaries and planned work.

## Build

Go 1.24 or newer is required. CI, release artifacts, and the container image use Go 1.24.13.

```bash
make build
```

This creates:

```text
build/capscope
build/capscope-structure-generator
```

Both commands are shipped in release archives. The structure generator remains a supported companion command because the current broker loads its hierarchy from generated JSON.

Generate a hierarchy from the providers in `config.json`:

```bash
./build/capscope-structure-generator \
  --config config.json \
  --output testdata/mcp_hierarchy
```

Run CapScope over stdio:

```bash
./build/capscope --config config.json
```

For Claude Code, one possible registration is:

```bash
claude mcp add --transport stdio capscope build/capscope -- --config config.json
```

See [configuration](docs/CONFIGURATION.md), [usage](docs/USAGE.md), and [deployment](docs/DEPLOYMENT.md) for the current implementation.

## Example flow

```text
get_tools_in_category("")
get_tools_in_category("everything")
execute_tool("everything.echo", {"message": "hello"})
```

The first two calls read local hierarchy JSON and do not start the `everything` provider. The third call starts and initializes it, maps the hierarchy path to the downstream tool name, and returns the MCP result without flattening structured content.

## Permission-hook examples

The scripts under `examples/hooks` show how a harness can inspect `execute_tool` arguments. They are optional integrations, not authorization controls. A downstream provider must still enforce authorization for every invocation.

## Origin and license

CapScope began from [`voicetreelab/lazy-mcp`](https://github.com/voicetreelab/lazy-mcp). The original MIT license, authorship, and Git history are preserved. See [LICENSE](LICENSE) and [prior art](docs/PRIOR_ART.md).
