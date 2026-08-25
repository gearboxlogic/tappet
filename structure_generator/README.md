# Tappet structure generator

`tappet-structure-generator` connects to each stdio, SSE, or Streamable HTTP
provider in a Tappet configuration, lists its tools, and writes the inherited
JSON hierarchy format. It can then convert that hierarchy into review-required
capability-package candidates. It expands environment placeholders in provider
commands, arguments, environment values, URLs, and header values.

```bash
make build
./build/tappet-structure-generator \
  --config config.json \
  --output testdata/mcp_hierarchy
```

The generator gives each provider an independent 30-second inventory timeout,
negotiates modern `server/discover` or the legacy initialize fallback, and
follows every `tools/list` cursor. Generation fails if any configured provider
cannot return a complete inventory. It rejects provider and tool names that
collide under Unicode case folding or canonical normalization, so a
case-insensitive or normalization-insensitive filesystem cannot silently
replace one generated entry with another. It builds the hierarchy in a fresh
staging directory and replaces the previous generated tree only after
generation succeeds, so removed providers and tools do not remain as stale
leaves. Existing non-empty output must be recognizable as a generated
hierarchy; working-directory ancestors and the system temporary directory are
rejected. It never reports success with a partial hierarchy. It writes:

```text
root.json
<provider>/<provider>.json
<provider>/<tool>.json
```

Each tool leaf keeps its input schema, optional output schema, annotations,
downstream provider name, and `maps_to` name. Live inventory reads each raw
`tools/list` result before the pinned SDK converts it to typed tool structures,
preserving schema keywords and exact JSON number lexemes. Invalid
`x-mcp-header` annotations reject the inventory instead of creating a callable
unsafe tool. `--regenerate` rebuilds branch overviews after files are moved
without changing leaf definitions.

Package use:

```go
import generator "github.com/gearboxlogic/tappet/structure_generator"
```

## Capability-package candidates

An existing hierarchy can be converted without contacting providers:

```bash
./build/tappet-structure-generator \
  --capability-candidates-from testdata/mcp_hierarchy \
  --output /tmp/tappet-capability-candidates
```

Each hierarchy leaf becomes one deterministic V1-alpha candidate package. The
conversion preserves the downstream provider and `maps_to` target, normalizes
the capability and operation IDs, and omits provider configuration, tool
schemas, and annotations. Every manifest carries a generated-review marker.

The generator refuses to replace an output tree that contains a reviewed or
unrecognized package. Remove the marker only after reviewing capability
boundaries, names, descriptions, provider bindings, and any manually added
skills or context. Generated output is a migration aid, not authoritative
semantic grouping.
