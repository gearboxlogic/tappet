# CapScope structure generator

`capscope-structure-generator` connects to each stdio, SSE, or Streamable HTTP provider in a CapScope configuration, lists its tools, and writes the JSON hierarchy consumed by the current broker. It expands environment placeholders in provider commands, arguments, environment values, URLs, and header values.

```bash
make build
./build/capscope-structure-generator \
  --config config.json \
  --output testdata/mcp_hierarchy
```

The generator gives each provider an independent 30-second inventory timeout, performs the legacy MCP initialize handshake, and follows every `tools/list` cursor. Generation fails if any configured provider cannot return a complete inventory. It rejects provider and tool names that collide under Unicode case folding or canonical normalization, so a case-insensitive or normalization-insensitive filesystem cannot silently replace one generated entry with another. It builds the hierarchy in a fresh staging directory and replaces the previous generated tree only after generation succeeds, so removed providers and tools do not remain as stale leaves. Existing non-empty output must be recognizable as a generated hierarchy; working-directory ancestors and the system temporary directory are rejected. It never reports success with a partial hierarchy. It writes:

```text
root.json
<provider>/<provider>.json
<provider>/<tool>.json
```

Each tool leaf keeps its input schema, optional output schema, annotations, downstream provider name, and `maps_to` name. Live inventory reads each raw `tools/list` result before the pinned SDK converts it to typed tool structures, preserving valid schema keywords that `mcp-go v0.43.2` does not model. `--regenerate` rebuilds branch overviews after files are moved without changing leaf definitions.

Package use:

```go
import generator "github.com/gearboxlogic/capscope/structure_generator"
```

This generator produces the inherited hierarchy format. It does not produce capability packages.
