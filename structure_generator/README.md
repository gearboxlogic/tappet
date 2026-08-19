# CapScope structure generator

`capscope-structure-generator` connects to each provider in a CapScope configuration, lists its tools, and writes the JSON hierarchy consumed by the current broker.

```bash
make build
./build/capscope-structure-generator \
  --config config.json \
  --output testdata/mcp_hierarchy
```

The generator performs the legacy MCP initialize handshake before listing tools. It writes:

```text
root.json
<provider>/<provider>.json
<provider>/<tool>.json
```

Each tool leaf keeps its input schema, optional output schema, annotations, downstream provider name, and `maps_to` name. `--regenerate` rebuilds branch overviews after files are moved without changing leaf definitions.

Package use:

```go
import generator "github.com/gearboxlogic/capscope/structure_generator"
```

This generator produces the inherited hierarchy format. It does not produce capability packages.
