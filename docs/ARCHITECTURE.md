# CapScope Architecture

Status: **accepted direction; interfaces remain V1-alpha proposals**

Last researched: **2026-08-23**

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

Package loading enforces the card field and aggregate byte limits in
`CAPABILITY_PACKAGE.md`. Runtime match metadata is also bounded to 512 encoded
JSON bytes per result, and the whole search response remains subject to the
broker response limit. Search fails explicitly rather than truncating a card or
match explanation.

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
- `describe`: return one capability's compact structure and explicitly selected operation schemas
- `read`: return one schema, skill body, reference, or bounded chunk of an artifact or spilled invocation result
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

#### 6.5.1 Bounded materialization and invocation results

Every broker response has a finite encoded-size limit. CapScope must never
silently truncate or flatten a schema, skill, reference, or provider result.

Skills, references, and cached operation schemas have stable artifact
references. `describe` returns a selected schema inline only when it fits the
response limit; otherwise it returns that schema's artifact reference, media
type, byte count, and digest. `read` accepts any artifact reference plus
`offset` and `max_bytes`, so the complete schema, skill body, or reference can
be fetched in bounded chunks. Package validation and provider metadata loading
also enforce finite per-artifact limits. Content beyond those accepted limits
is rejected explicitly rather than indexed or partially returned.
Package artifact references include the package version and content digest.
Schema references include the provider fingerprint and schema digest; a cache
refresh invalidates references whose digest changed.

When the first artifact `read` is incomplete, it atomically acquires a lease on
the resolved immutable snapshot and returns an opaque `continuation_ref` plus
its `expires_at`. Every subsequent chunk passes that continuation reference as
`ref`; it does not resolve the installed-package or metadata registry again.
The lease survives reinstall, uninstall, and metadata refresh, and is released
on the final chunk, a five-minute idle timeout, or a one-hour absolute lifetime.
Each successful non-final chunk renews the idle deadline without extending the
absolute deadline, and each response reports the effective expiry. Live-handle
and retained-byte quotas bound this state, and admission fails before the first
chunk is promised if a continuation cannot be reserved. These are explicit
application handles independent of the broker transport or MCP protocol
session.

An inline `invoke` result keeps the downstream MCP result at the outer protocol
level. CapScope forwards every provider content block in the outer
`CallToolResult.Content`; text, image, audio, and embedded-resource blocks remain
unchanged. A resource-link block keeps its name, title, description, media type,
and annotations, but its provider-scoped URI is rewritten to the resolvable
broker URI defined in section 6.5.3. CapScope also preserves
`StructuredContent` and `IsError`.
For inline results, CapScope copies every provider-defined `_meta` entry to the
same top-level key so unmodified consumers keep seeing the metadata paths they
expect. Broker fields use the reserved envelope
`_meta["io.capscope.proxy"]`. If and only if the provider already used that
exact reserved key, CapScope stores its original value unchanged under
`downstream_reserved_value` inside the proxy envelope; no other downstream key
is moved or wrapped.

```json
{
  "content": [{"type": "image", "data": "...", "mimeType": "image/png"}],
  "structuredContent": {"provider": "value"},
  "isError": false,
  "_meta": {
    "capscope": {"provider_owned": true},
    "io.capscope.proxy": {
      "invocation": {
        "invocation_id": "invocation:opaque-id",
        "disposition": "inline"
      },
      "downstream_reserved_value": {"provider_owned_collision": true}
    }
  }
}
```

The `downstream_reserved_value` member is omitted when there was no collision.
This one reserved-key exception is explicit because a JSON object cannot retain
two different values at the same key; all noncolliding provider metadata keeps
its original protocol-visible path.

If the complete typed provider result exceeds the inline limit but remains
within the accepted result limit, CapScope stores a lossless UTF-8 JSON encoding
in a temporary spill object. `invoke` preserves the provider's outer `IsError`
classification and returns compact spill metadata in
`_meta["io.capscope.proxy"].invocation` and structured content. The complete
complete broker-adapted typed result, including the provider `_meta` object at
its original keys and any scoped resource-link rewrites, exists only inside the
lossless spill. No provider metadata is copied into the spilled outer response
because it may itself be what exceeded the inline limit. The outer content
contains only a bounded notice directing the caller to `capscope.read`; it never
presents a partial provider content array as complete:

```json
{
  "invocation_id": "invocation:opaque-id",
  "disposition": "spilled",
  "provider_is_error": false,
  "result_ref": "result:opaque-handle",
  "media_type": "application/json",
  "chunk_encoding": "base64",
  "stored_bytes": 1048576,
  "sha256": "hex-digest",
  "expires_at": "RFC3339 timestamp"
}
```

Each result or error spill reference is an opaque application handle. A caller
retrieves it through bounded `read` calls using an offset and maximum decoded
byte count. Each byte range is base64 encoded, and each response reports the
next offset and whether the spill is complete. Reassembly preserves the complete
broker-adapted typed MCP result or original JSON-RPC error. If storage fails,
`invoke` returns an explicit bounded-output error instead of a partial value.

A downstream JSON-RPC error is not an MCP `CallToolResult` and follows the same
bounded-preservation rule separately. When the complete encoded error object
fits the broker response limit, CapScope forwards its numeric `code`, `message`,
and structured `data` unchanged. When it exceeds the inline limit but fits the
accepted downstream frame and spill limits, CapScope stores a lossless encoding
of those three original fields in an error spill. The bounded outward JSON-RPC
error retains the original numeric code, uses the fixed message
`Downstream error payload spilled`, and puts only this broker envelope in
`data`:

```json
{
  "io.capscope.proxy": {
    "disposition": "spilled_error",
    "error_ref": "error:opaque-handle",
    "media_type": "application/json",
    "stored_bytes": 1048576,
    "sha256": "hex-digest",
    "expires_at": "RFC3339 timestamp"
  }
}
```

`capscope.read` retrieves that error reference in bounded chunks; reassembly
restores the exact original code, message, and data. The outer envelope never
copies a partial provider `data` value or truncates the provider message. If
error-spill admission fails, CapScope returns the distinct bounded
`downstream_error_spill_failed` proxy error and does not pretend the original
error was preserved. MCP results with `isError: true` remain typed
`CallToolResult` values and use the ordinary result path above.

The spill store must enforce finite, operator-configurable limits for incoming
provider-result or error bytes, bytes per spill object, aggregate live bytes,
live object count, object lifetime, and bytes per `read`. Provider adapters
enforce the incoming limit while reading, before unbounded decoding or
allocation. Spill admission reserves aggregate bytes and an object slot
atomically. It may reclaim expired objects, but it must not evict an unexpired
object promised to a caller. An oversized value or exhausted store returns a
distinct typed error and does not create a partial handle. V1 must ship safe
finite defaults and expose quota failures through metrics and logs.

Process-local spill objects expire after their configured lifetime and are
removed at shutdown. Their handles are bearer secrets: they must be unguessable
and must never appear raw in logs, trace attributes or events, metric labels,
error messages, panic reports, or audit payloads. Telemetry may correlate a
handle only through an irreversible keyed digest produced with a
telemetry-specific secret; that digest is never accepted by `read`. Result
handles are not MCP protocol sessions and must not carry hidden capability
activation state. A single-process V1 may use local storage. A replicated
deployment must use a shared bounded spill store addressable by result reference
or explicitly disable spill retrieval; process-local handles alone are not a
stateless horizontal-scaling design.

#### 6.5.2 Bounded broker requests

Broker inputs are bounded before JSON decoding. V1 accepts at most 1 MiB of
encoded JSON-RPC request data per message. HTTP rejects an oversized declared
body and applies a limited reader to chunked or undeclared bodies. Stdio and
other framed transports stop reading once the frame limit is reached; they must
not accumulate an unbounded line or frame first. A transport or SDK integration
that cannot enforce this pre-decode boundary is not supported on an untrusted
broker endpoint.

An oversized inbound message invalidates the affected transport. CapScope may
emit a bounded `broker_frame_limit_exceeded` response when the framing still
permits it, but then closes the connection or session before accepting another
request. In particular, a newline-delimited stdio server exits or closes its
input instead of interpreting the unread remainder as a second message. HTTP
returns a bounded 413 response and forces the request connection closed;
streaming transports close the stream. V1 does not perform unbounded draining
or attempt in-place resynchronization after the frame boundary is lost. A client
may continue only through a fresh transport connection.

After bounded decoding, V1 applies these tool-input limits before dispatch:

| Input | Limit |
| --- | ---: |
| JSON nesting depth | 64 |
| `query` | 4,096 normalized UTF-8 bytes |
| capability or operation ID | 128 bytes |
| hierarchy `path` | 256 bytes |
| cursor or artifact `ref` | 2,048 bytes |
| `include` values | 8 entries, 64 bytes each |
| `operation_schemas` | 32 exact IDs |
| `limit` or `child_limit` | 100 items each |
| `max_bytes` requested from `read` | 64 KiB decoded |
| normalized `invoke.arguments` | 256 KiB encoded JSON |

Invalid UTF-8, excess nesting, or an exceeded field, collection, or aggregate
limit returns a typed bounded-request error before registry lookup, cache
access, provider startup, or spill allocation. The outer 1 MiB limit remains
authoritative even when individual fields are below their limits. Deployments
may set smaller limits, but changing these V1-alpha maxima requires an explicit
architecture and benchmark update.

#### 6.5.3 Scoped provider resource links

A downstream `resource-link` URI is scoped to the provider connection that
returned it; forwarding that URI without a read route would create a dangling
link. Before publishing an invocation result containing such a block, CapScope
uses that same initialized provider session to call downstream
`resources/read`. It accepts at most 32 links per result, snapshots each complete
typed resource response under finite per-resource and aggregate byte quotas,
and stages all snapshot capacity before returning the tool result.
The block URI is then rewritten to an unguessable
`capscope-resource://<opaque-handle>` URI. All other block fields remain
unchanged, and the snapshot record retains the original provider URI, provider
fingerprint, content digest, and invocation ID for diagnostics and stale checks.

Resource snapshots, the fully adapted result encoding, and any required result
or resource-error spill are one publication transaction. CapScope commits the
resource handles and spill handle together only after every link has been
materialized and the final bounded response, preservation error, or spill has
been prepared. Any failure before that commit releases every staged snapshot,
object slot, and byte reservation; no unreachable resource object remains
charged against quota. A transport failure after commit cannot be rolled back
safely, so the ordinary finite handle expiry reclaims those published objects.

The outward broker advertises MCP resource support for a scoped
`resources/read` route. Reading the rewritten URI returns the exact snapshotted
typed resource contents without restarting or consulting the provider.
`resources/list` does not enumerate ephemeral handles, and arbitrary provider
URIs are rejected; possession of the broker-issued URI is required. A snapshot
is admitted only when its complete `resources/read` response fits the broker
resource-response limit, so this route never truncates or flattens resource
contents. Handles expire under the same bounded temporary-store rules as spill
objects and are bearer secrets excluded from telemetry.

If the provider does not support `resources/read`, the link cannot be read in
the current session, any link or byte quota is exceeded, or snapshot admission
fails, `invoke` returns a bounded `resource_link_unmaterializable` preservation
error. It does not emit a dangling rewritten link, a partial resource snapshot,
or a provider URI that the CapScope-connected harness cannot resolve. This
scoped proxy does not expose a provider-wide resource catalog and does not add
dynamic tools; implementing it belongs to the portable broker milestone, not
Milestone 0.

When downstream `resources/read` returns a structured JSON-RPC error, the
preservation error retains the original numeric code, message, and structured
data unchanged in a bounded `cause`. If that cause exceeds the inline limit,
the ordinary lossless error-spill path in section 6.5.1 stores it and the cause
contains the resulting opaque error reference. Local unsupported-resource and
quota failures remain separately classified; they are never presented as a
provider authorization error.

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

#### 6.6.1 Ambiguous delivery and retry safety

V1 never automatically retries a downstream `tools/call` after any request byte
has been handed to the transport. A timeout, cancellation, connection loss, or
frame failure after that point can mean the provider executed the operation but
its result was lost. CapScope closes or repairs the connection for future work
and returns a typed `provider_outcome_unknown` error containing bounded provider
and invocation diagnostics; it does not replay the call. A call may be retried
automatically only when failure is proven to have occurred before transmission.
Any future post-transmission retry requires an explicit provider contract for
idempotency and a replay token; capability packages do not imply either.

#### 6.6.2 Downstream transport frame limits

Every message received from a downstream provider has a finite pre-decode
limit, regardless of method or direction of the JSON-RPC exchange. V1 permits
at most 16 MiB of encoded data per downstream protocol message. The limit
applies to initialize and discovery responses, invocation results, errors,
notifications, ping responses, progress events, and unsolicited requests or
callbacks. The stricter metadata and result-ingestion quotas still apply after
this transport boundary.

Stdio adapters enforce the ceiling while reading a frame and never accumulate
an oversized line first. HTTP adapters use bounded body readers even without a
valid `Content-Length`; streaming transports enforce the limit independently
for each event. The bound must sit below the SDK decoder. An SDK or transport
that cannot expose a pre-decode boundary is unsupported for downstream V1 use.

An exceeded frame returns `provider_frame_limit_exceeded`, fails every affected
in-flight request, and closes the protocol connection. CapScope terminates a
stdio child whose stream can no longer be trusted and closes an HTTP or event
stream session; it does not skip bytes or attempt in-place resynchronization.
The prior atomic metadata-cache snapshot remains intact, but no further request
is accepted until the provider manager establishes and initializes a fresh
connection. Repeated violations feed normal failure backoff and bounded
telemetry.

### 6.7 Metadata cache

Cache:

- provider identity and protocol support
- tool names, descriptions, annotations, input schemas, output schemas
- optional prompts and resource metadata when exposed by a capability
- cache timestamp, provider fingerprint, and schema digest

The cache should permit discovery while a lazy provider is stopped.

A missing cache entry is not silently populated by search or ordinary
`describe`. Capability installation and the explicit operator refresh command
are the normal cache-prime paths and may start the selected provider. If neither
has run, operation cards report `metadata_state: "unavailable"` and omit schema
references and digests. An exact schema selection returns a typed
`metadata_unavailable` result with the provider ID and refresh instruction.
First invocation may connect and populate the cache, but it must refresh
metadata and validate arguments before calling the tool. This behavior keeps
browsing lazy while making an empty or deleted cache observable.

Cached schemas are snapshots. Search and describe responses sourced from a
stopped provider must report the observation time, provider fingerprint, schema
digest, and cached freshness state. Every provider connection or reconnection
must refresh current metadata and schema digests before the first invocation is
accepted, even when configuration and package bindings are unchanged. On a
long-lived connection, CapScope may reuse that invocation contract only when
the provider advertised reliable tool-list change notifications on the active
connection. The provider manager serializes notification handling with schema
selection: a dirty notification before dispatch forces a bounded refresh and
revalidation. Without that negotiated invalidation contract, CapScope performs
a bounded metadata refresh immediately before every invocation, then validates
arguments against that result. A provider that advertises notifications but
changes schemas without sending them violates its negotiated contract; the
reported freshness state records which mode was used. If a digest changed,
CapScope atomically replaces the snapshot and invalidates old schema references.
If refresh fails, invocation fails without calling the provider; known-stale
metadata is never used as an invocation contract.

Metadata ingestion is bounded independently of individual schemas. V1-alpha
hard limits per provider refresh are 4 MiB per encoded list response, 128 list
pages, 4,096 total metadata items, 24 MiB of aggregate schema bytes, and 32 MiB
of aggregate normalized metadata. The adapter enforces response bytes while
reading and item, page, and aggregate counters before accumulation or cache
writes. A repeated or non-advancing cursor is an error. Exceeding any quota
fails the refresh with a distinct `metadata_limit_exceeded` classification and
does not replace the previous atomic cache snapshot. Deployments also configure
a finite total metadata-cache quota across providers; reaching it fails refresh
without unbounded disk growth. Changing these V1-alpha hard limits requires an
explicit architecture and benchmark update.

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

Record non-secret identifiers, versions, durations, result classifications,
cache hits, and provider lifecycle events. Do not record secrets, unrestricted
model prompts, or raw large outputs by default. Spill `result_ref` values are
bearer secrets and are excluded from logs, traces, metrics, errors, and audit
payloads; only the irreversible telemetry correlation digest described in
section 6.5.1 may be recorded.

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
- explicit handles for temporary spilled results
- explicit continuation handles for multi-call artifact reads

### 7.4 Explicit application handles

MCP 2026-07-28 removes protocol-level sessions. Any cross-call scope, lease,
task, or activation must use an explicit application handle passed by the
caller. Spill references, resource handles, and artifact continuation handles
follow this rule.

Do not map an implicit client connection to hidden capability state.

The first broker-mode vertical slice should remain stateless across CapScope
calls except for provider lifecycle, cache lifecycle, and bounded temporary
retrieval through the explicit handles defined above. Introduce activation
handles only after a tested requirement demonstrates their value.

## 8. Authorization boundary

Capability discovery and materialization are not authorization.

```text
model visibility
      !=
provider permission
```

CapScope may support an optional external filter hook that removes unavailable capabilities from a projection, but the hook is an integration point, not a policy engine.

Every invocation still reaches the provider's normal authorization boundary. CapScope must propagate permission failures accurately.

### 8.1 Broker transport access

The inherited HTTP entrypoint optionally wraps the entire MCP endpoint with
`internal/server.newAuthMiddleware`, using static bearer tokens from
`mcpProxy.options.authTokens`. This is a transport access gate retained for
baseline compatibility. It does not identify a user, grant access to a
capability or operation, authorize a downstream action, or replace provider
authentication.

The catalog, resolver, materializer, and provider interfaces must remain
independent of this middleware. Provider configuration may contain credential
references or runtime injection settings, but never secret values. Actual
credentials come from the environment or an external secret mechanism and are
not persisted in packages, ordinary configuration, caches, or logs. Removing or
replacing the gate requires a separate compatibility decision, tests for
existing HTTP deployments, and migration guidance. CapScope does not grow this
middleware into an IAM or policy subsystem.

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

### 9.1 Multi-round-trip requests

CapScope must advertise downstream client capabilities only when its provider
adapter can service them. The portable V1 broker does not advertise sampling,
elicitation, roots, or other provider-to-client callback capabilities and does
not service them locally. If a provider sends an unnegotiated callback anyway,
the adapter returns a method-not-supported or provider-incompatible error
within the invocation deadline. It must not wait for a callback that cannot
arrive, invoke an LLM, or route to a model on its own.

A later relay path is permitted only when the outward harness negotiated the
same capability and the transport and SDK expose a tested correlation
mechanism. Such a relay must bind the callback to one explicit invocation,
propagate cancellation, enforce round-trip, byte, and time limits, and preserve
the harness response without crossing callers. Capability packages cannot
enable unsupported callbacks or override this negotiation policy.

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
  "limit": 8,
  "child_limit": 50,
  "child_cursor": null
}
```

Output: at most `limit` compact capability cards with exact-match indicators and
scores, plus one lexically ordered page of direct child paths beneath `path`.
The child collection reports `total_children` and an opaque
`next_child_cursor`; `child_limit` has a finite maximum of 100 and may be zero
when the caller does not need hierarchy browsing. The cursor binds the catalog
generation and requested path, so a changed catalog or path returns an explicit
stale-cursor error instead of skipping or duplicating branches. Card ranking and
child pagination are independent, and the combined response remains subject to
the broker response-size limit.

### `capscope.describe`

Input:

```json
{
  "capability_id": "software.github.ci-debugging",
  "include": ["skills", "operations"],
  "operation_schemas": ["inspect-failed-checks"],
  "limit": 50,
  "cursor": null
}
```

Output: one deterministic page of skill metadata, operation cards, and
reference metadata, plus either the full input and output schemas or an exact
schema artifact reference for each operation ID named in `operation_schemas`.
Page items are ordered by section and stable ID. The response includes total
counts and an opaque `next_cursor`; `limit` has a finite maximum. At the first
page, `describe` atomically captures one projection generation containing the
capability version and digest plus the ordered metadata-cache generation IDs,
including the explicit empty-cache generation, for every provider referenced by
the capability. It captures all capability providers even when the initial
`include` set contains only provider-independent structure. The cursor binds
that projection generation and the requested `include` set. A package change,
provider metadata refresh, empty-cache transition, or structure-selector change
invalidates the cursor and returns an explicit stale-cursor error instead of
mixing package or operation metadata snapshots. `operation_schemas` is an
independent exact selector and may first appear on any page because every
possible operation provider is already in the bound projection; the selector is
resolved against that generation. Selected schema content counts toward the
response's size bound without changing the structure cursor. Individual
metadata entries are subject to package validation limits.

Each operation card with cached metadata includes its schema reference, digest,
observation time, and freshness state. With an empty cache it instead reports
`metadata_state: "unavailable"`; an exact schema selector then returns the typed
cache-miss result defined in section 6.7. Full skill bodies remain deferred to
`read`. Schemas for unselected operations are never returned. Unknown operation
IDs fail explicitly, and the schema selector count and total response size are
bounded independently of structure pagination.

### `capscope.read`

Input:

```json
{
  "capability_id": "software.github.ci-debugging",
  "ref": "schema:v1:provider-fingerprint:schema-digest:inspect-failed-checks",
  "offset": 0,
  "max_bytes": 65536
}
```

The `ref` above is illustrative but not mutable: callers copy the exact opaque
schema reference returned by `describe`. Its encoded identity binds the
operation ID, provider configuration fingerprint, and schema digest. `read`
must return a typed stale-reference error if any binding no longer matches; it
must never resolve the token to whichever schema is current at read time.

Output: one operation schema, skill body, permitted reference resource, or
bounded chunk of one of those artifacts. A complete artifact is returned inline
only when it fits the response limit. Larger accepted artifacts use the same
`offset`, `max_bytes`, base64 chunk, byte-count, and digest fields shown below,
with their stable artifact reference on the first request. If the first chunk
is incomplete, its response also returns `continuation_ref` and `expires_at`;
every later chunk uses that continuation reference as `ref`, preserving the
same immutable snapshot across reinstall, uninstall, or cache refresh.

For an oversized invocation result or downstream JSON-RPC error, the same tool
accepts the corresponding spill reference:

```json
{
  "ref": "result:opaque-handle",
  "offset": 0,
  "max_bytes": 65536
}
```

Output:

```json
{
  "ref": "result:opaque-handle",
  "offset": 0,
  "data_base64": "encoded byte range",
  "next_offset": 65536,
  "complete": false,
  "media_type": "application/json",
  "stored_bytes": 1048576,
  "sha256": "hex-digest",
  "expires_at": "RFC3339 timestamp"
}
```

Reassembling the decoded byte ranges yields the complete broker-adapted typed
provider result or original JSON-RPC error represented by the spill object. The
digest verifies the reassembled value.
`offset`, `next_offset`, `max_bytes`, and `stored_bytes` count decoded bytes in
the stored UTF-8 JSON representation, not base64 characters.

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

Output: the provider's native MCP content blocks, structured content, and
`isError` at the outer `CallToolResult` level when inline, with CapScope metadata
under `_meta["io.capscope.proxy"].invocation` and all noncolliding provider
metadata at its original top-level `_meta` keys. An oversized accepted result
returns only bounded proxy metadata plus the spill reference defined in section
6.5.1; retrieving and decoding the spill restores the complete original result,
including provider metadata, without partial provider content.

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
- oversized structured results can be retrieved completely without silent truncation
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
- How clients preserve structured output through a generic broker
- How modern MCP list caching should interact with the internal catalog cache
- Whether explicit activation handles provide enough value to justify state
- Which harness adapters can truly withdraw tools or skill text from future requests
