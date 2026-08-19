# CapScope Architecture

Status: **accepted direction; interfaces remain V1-alpha proposals**

Last researched: **2026-08-19**

## 1. Problem

Agent harnesses commonly treat configured capabilities as permanently model-visible. As the number of MCP tools and skills grows, this causes:

- repeated schema and instruction tokens
- weaker tool selection and naming collisions
- slower startup and larger prompt-cache prefixes
- unnecessary downstream processes
- more irrelevant authority appearing in the model's apparent action space
- architecture tied to one harness's tool-loading behavior

CapScope separates four states that most harnesses currently conflate:

```text
installed
  -> discoverable
  -> materialized
  -> invoked
```

Only the subset needed for the current work should advance through those states.

## 2. Scope

CapScope is a progressive capability catalog, materializer, and provider broker.

It is not:

- a general agent harness
- a planner
- an LLM router
- a memory service
- a durable workflow engine
- an IAM or approval system
- a replacement for provider-side authorization
- a subagent framework

The initial implementation supports MCP providers. The internal interfaces should permit additional provider adapters later, but V1 must not implement speculative providers.

## 3. Conceptual model

```text
Capability
├── compact discovery card
├── Skills
│   ├── SKILL.md metadata
│   ├── SKILL.md instructions
│   └── references / scripts / assets
├── Operations
│   ├── logical operation identifier
│   ├── compact description
│   └── provider binding
├── Context references
│   └── selectively readable supporting material
└── Providers
    └── MCP server references in V1
```

### 3.1 Capability

A capability represents a coherent class of work, such as:

```text
software.github.ci-debugging
embedded.yocto.kernel-module-debugging
data.postgres.query-analysis
```

A capability is not an MCP server. One capability may select tools from multiple providers, and one provider may support many capabilities.

### 3.2 Capability card

A card is the only representation returned during broad discovery. It should contain approximately:

```text
id
name
description
hierarchy path
tags
version
match reason
```

Cards must not contain complete operation schemas, full skill bodies, or large reference content.

### 3.3 Skill

A skill is procedural knowledge using the Agent Skills format. CapScope should index only the `name` and `description` for discovery, return the complete `SKILL.md` only when requested, and return supporting resources individually.

CapScope does not execute bundled scripts by default. It may expose them as references to a harness that already has an execution tool.

The Agent Skills `allowed-tools` field is experimental and must not be interpreted as an authorization grant.

### 3.4 Operation and tool

An operation is the capability-facing action. A tool is the provider-specific implementation.

```text
operation: software.github.ci.inspect-failed-checks
provider: github
target: get_check_runs
```

The distinction allows capability identity to remain stable when an operation is later backed by another MCP server or native adapter. V1 may initially implement a direct one-to-one binding, but the manifest must not make the MCP server name the capability identity.

### 3.5 Context reference

Context is supporting information, not procedural knowledge and not conversation memory.

Examples:

- repository conventions
- a schema reference
- a runbook appendix
- a project-specific glossary

V1 should support local package references and optionally MCP resources. It should not introduce embeddings, RAG, memory, freshness reconciliation, or arbitrary remote fetches into the core.

### 3.6 Provider

A provider supplies executable operations. V1 provider type:

```text
MCP
```

The provider manager owns process and connection lifecycle, cached metadata, transport adaptation, and result preservation. The provider remains responsible for authenticating and authorizing requests.

## 4. Two runtime modes

CapScope must distinguish portable broker behavior from native harness integration.

### 4.1 Broker mode: portable baseline

```text
Unmodified MCP harness
        |
        | fixed, small MCP tool surface
        v
CapScope broker
        |
        +-- catalog / resolver
        +-- materializer
        +-- provider manager
        |
        v
Downstream MCP providers
```

The model sees a small stable API, proposed as:

```text
capscope.search
capscope.describe
capscope.read
capscope.invoke
```

Exact names and schemas remain alpha until tested.

Suggested responsibilities:

- `search`: natural-language and exact-ID search, optionally constrained by hierarchy path
- `describe`: return one capability's compact structure and selected operation schemas
- `read`: return one skill body or referenced resource
- `invoke`: execute one operation with typed arguments through its provider

Broker mode does not depend on `tools/list_changed`.

Broker mode can keep irrelevant content out of the conversation. It cannot delete a previously returned skill body from an existing transcript.

### 4.2 Native adapter mode: later optimization

A harness adapter can use native APIs to:

- register selected tools directly
- inject selected skills into the next model request
- omit deactivated material from future requests
- create a fresh or subagent context for branch-local work
- react to capability changes using the harness's own refresh mechanism

```text
Harness adapter
     |
     +-- active projection
     +-- native tool binding
     +-- skill/context injection
     v
CapScope core library
```

Native adapter mode is optional and client-specific. It must reuse the same registry, resolver, package, provider, and observability contracts as broker mode.

## 5. The meaning of activation and unloading

These terms must be precise.

### Broker mode

Activation means that CapScope materializes and returns selected capability data. Unloading can release provider resources and stop repeating capability data, but it cannot remove tokens already present in the conversation.

### Native adapter mode

Activation may update the harness's future tool and context projection. Deactivation may omit that projection from future model requests if the harness supports context reconstruction.

### Fresh-context mode

A subagent or new context can provide the strongest isolation: capability material exists only in that context and the parent receives a compact result.

CapScope core should not claim literal context deletion unless a tested adapter provides it.

## 6. Components

### 6.1 Package loader and validator

Responsibilities:

- discover package manifests
- validate stable identifiers and versions
- validate Agent Skills using the official reference rules
- resolve local references safely
- reject duplicate capability and operation identifiers
- produce normalized immutable package records

The package manifest is the source of truth. The search index and provider metadata cache are derived.

### 6.2 Registry

The registry stores normalized capability records and supports:

- lookup by exact ID
- lookup by hierarchy path
- enumeration of direct children
- package version and provenance
- dependency diagnostics

V1 can be in-memory after loading manifests. A database is not required for correctness.

### 6.3 Catalog index and resolver

Resolution should combine hierarchy and search without requiring an LLM.

Recommended initial ranking:

1. exact capability or operation ID
2. exact normalized name
3. exact alias or tag
4. lexical match over name and description
5. lexical match over operation summaries and skill descriptions

Exact matches must be pinned ahead of top-K ranked results.

Start with deterministic lexical search. SQLite FTS5/BM25 is reasonable when catalog size warrants it; an in-memory index is sufficient for the first vertical slice. Embeddings should be an optional later index evaluated against a fixed retrieval corpus.

Search results should include:

```text
score
match kind
matched fields
hierarchy path
```

This makes routing debuggable.

### 6.4 Materializer

The materializer builds a projection at increasing levels of detail:

```text
CARD
  -> STRUCTURE
  -> SKILL INSTRUCTIONS
  -> OPERATION SCHEMA
  -> REFERENCED RESOURCE
```

It must never materialize an entire package merely because one operation is needed.

The materializer is deterministic and side-effect free.

### 6.5 MCP broker adapter

The broker adapter exposes the fixed CapScope tools and translates calls into core operations.

It should:

- return structured content where the client supports it
- preserve output schemas
- produce deterministic tool ordering and descriptions
- use bounded outputs
- avoid embedding a complete downstream catalog into its own tool descriptions
- remain usable by clients that ignore live tool-list changes

### 6.6 Provider manager

The provider manager adapts downstream MCP servers.

Responsibilities:

- lazy start on first required operation
- transport creation
- modern and legacy protocol negotiation
- metadata acquisition and refresh
- per-provider concurrency handling
- request timeouts and cancellation
- idle shutdown
- reconnect behavior
- faithful result and error propagation
- health and lifecycle telemetry

Recommended lifecycle modes, inspired by Pi MCP Adapter:

```text
lazy
eager
keep-alive
lazy-keep-alive
```

Only `lazy` is required for the first vertical slice. Idle timeout should be configurable and disabled for providers known to be expensive or stateful.

Process lifecycle is separate from MCP protocol session state. A stateless MCP request model does not require restarting a stdio provider for each call.

### 6.7 Metadata cache

Cache:

- provider identity and protocol support
- tool names, descriptions, annotations, input schemas, output schemas
- optional prompts and resource metadata when exposed by a capability
- cache timestamp, provider fingerprint, and schema digest

The cache should permit discovery while a lazy provider is stopped.

The cache must be invalidated by:

- explicit refresh
- changed provider configuration
- changed package binding
- changed schema digest
- applicable protocol cache hints
- provider list-change/subscription signals when the client path supports them

A stale cache must never silently alter arguments. Schema version or digest should be included in invocation diagnostics.

### 6.8 Observability

Use ordinary structured logs and OpenTelemetry-compatible traces rather than MCP logging as the primary operational channel.

Suggested spans:

```text
capscope.search
capscope.describe
capscope.read
capscope.invoke
provider.connect
provider.discover
provider.list_tools
provider.call_tool
provider.disconnect
cache.load
cache.refresh
```

Record identifiers, versions, durations, result classifications, cache hits, and provider lifecycle events. Do not record secrets, unrestricted model prompts, or raw large outputs by default.

## 7. State model

### 7.1 Durable source-of-truth state

- capability manifests
- Agent Skills packages
- referenced static context
- provider configuration without embedded secrets
- architecture decisions
- tests and benchmark corpora

### 7.2 Durable but derived state

- tool metadata cache
- search index
- benchmark results
- conformance baselines

Derived state must be rebuildable.

### 7.3 Ephemeral state

- open downstream connections and processes
- request cancellation state
- active provider leases
- transient search results
- harness-native active projections
- temporary output spill files

### 7.4 Explicit application handles

MCP 2026-07-28 removes protocol-level sessions. If CapScope later introduces a cross-call scope, lease, task, or activation, it must use an explicit application handle passed by the caller.

Do not map an implicit client connection to hidden capability state.

The first broker-mode vertical slice should remain stateless across CapScope calls except for provider and cache lifecycle. Introduce explicit activation handles only after a tested requirement demonstrates their value.

## 8. Authorization boundary

Capability discovery and materialization are not authorization.

```text
model visibility
      !=
provider permission
```

CapScope may support an optional external filter hook that removes unavailable capabilities from a projection, but the hook is an integration point, not a policy engine.

Every invocation still reaches the provider's normal authorization boundary. CapScope must propagate permission failures accurately.

## 9. Latest MCP strategy

The outward CapScope server should target the latest stable MCP revision and retain reasonable compatibility with legacy clients.

For MCP 2026-07-28, account for:

- stateless per-request metadata
- `server/discover`
- removal of the initialize handshake for modern requests
- removal of `Mcp-Session-Id` for modern HTTP requests
- standard method/name headers
- typed result forms
- deterministic and cacheable list results
- Multi Round-Trip Requests
- the current subscription mechanism
- JSON Schema 2020-12 behavior
- official conformance tests

Current code uses `mcp-go v0.43.2`. `mark3labs/mcp-go` merged 2026-07-28 support after that version. [The upgrade decision](MCP_UPGRADE_DECISION.md) selects `v1.0.0-beta.1` for an isolated Milestone 1 compatibility and conformance pull request. Upgrade work must remain separate from the capability model so SDK migration does not drive architecture.

Do not switch Go SDKs without a focused comparison. First test the current SDK line at a revision containing modern support.

## 10. Current implementation mapping

| Current code | Reusable idea | Required change |
| --- | --- | --- |
| `internal/hierarchy` | compact hierarchy and tool mapping | generalize nodes into capability records |
| `get_tools_in_category` | progressive browsing | add exact and lexical search; return capability cards |
| `execute_tool` | stable broker invocation | invoke logical capability operations and preserve typed results |
| `ServerRegistry` | lazy provider startup | add lifecycle modes, idle shutdown, metadata cache, modern negotiation |
| structure generator | provider inventory capture | generate capability-package candidates, not only hierarchy JSON |
| per-server mutex | bounded same-provider access | retain only where transport/client requires it |
| permission hooks | proof that broker surfaces obscure native permissions | keep as optional integration; do not expand into IAM |

Legacy `Client` lazy-tool activation and recursive hierarchy behavior overlap. They should be simplified behind one provider manager rather than maintained as separate architectures.

## 11. Initial API proposal

This proposal is intentionally small.

### `capscope.search`

Input:

```json
{
  "query": "debug failing GitHub Actions checks",
  "path": "software.github",
  "limit": 8
}
```

Output: compact capability cards, exact-match indicators, scores, and child paths.

### `capscope.describe`

Input:

```json
{
  "capability_id": "software.github.ci-debugging",
  "include": ["skills", "operations"]
}
```

Output: skill metadata, operation cards, and references. Full skill bodies and full operation schemas remain deferred unless explicitly requested.

### `capscope.read`

Input:

```json
{
  "capability_id": "software.github.ci-debugging",
  "ref": "skill:github-actions-debugging"
}
```

Output: one skill body or one permitted reference resource.

### `capscope.invoke`

Input:

```json
{
  "capability_id": "software.github.ci-debugging",
  "operation_id": "inspect-failed-checks",
  "arguments": {
    "repository": "gearboxlogic/capscope"
  }
}
```

Output: the provider's structured result plus CapScope invocation metadata.

The first implementation may use fewer meta-tools if one union schema proves materially cheaper without harming validation. Benchmark the interface rather than assuming that the smallest tool count is automatically best.

## 12. Thin vertical slice

The first proof should include:

- two downstream MCP providers
- at least three capabilities
- one Agent Skill
- one capability with tools from only one provider
- one capability that selects a subset of a larger provider
- a fixed broker surface
- provider lazy startup
- cached metadata available while providers are stopped
- deterministic lexical search
- exact identifier matching
- one real unmodified MCP harness
- a measured comparison against eager exposure

It should not include:

- embeddings
- native dynamic tool binding
- subagents
- memory
- policy engines
- approval flows
- non-MCP providers
- distributed activation state

## 13. Success measures

Correctness:

- unrelated skills and schemas never appear in broker responses
- exact identifiers always resolve
- operation arguments are validated against the current provider schema
- provider authorization failures remain intact
- structured results survive proxying
- provider processes start and stop predictably

Efficiency:

- initial broker schema tokens/bytes
- capability-card bytes returned per search
- schemas materialized per task
- total task tokens
- provider startups and idle lifetime
- search latency and invoke overhead

Retrieval:

- top-1 and top-5 capability recall
- exact-match recall
- ambiguous-query escalation rate
- wrong-operation selection rate

Operational:

- cache hit rate
- stale-schema detection
- timeout and cancellation behavior
- same-provider and cross-provider concurrency
- conformance results by MCP revision and transport

## 14. Open questions requiring experiments

- Four fixed broker tools versus a single union-style broker tool
- In-memory lexical index versus SQLite FTS5 at expected catalog sizes
- Whether hierarchy is explicit manifest data, a derived path, or both
- How much operation schema detail belongs in `describe`
- How clients preserve structured output through a generic broker
- How modern MCP list caching should interact with the internal catalog cache
- Whether explicit activation handles provide enough value to justify state
- Which harness adapters can truly withdraw tools or skill text from future requests
