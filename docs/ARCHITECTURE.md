# Tappet Architecture

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

Tappet separates four states that most harnesses currently conflate:

```text
installed
  -> discoverable
  -> materialized
  -> invoked
```

Only the subset needed for the current work should advance through those states.

## 2. Scope

Tappet is a progressive capability catalog, materializer, and provider broker.

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

A skill is procedural knowledge using the Agent Skills format. Tappet should index only the `name` and `description` for discovery, return the complete `SKILL.md` only when requested, and return supporting resources individually.

Tappet does not execute bundled scripts by default. It may expose them as references to a harness that already has an execution tool.

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

Tappet must distinguish portable broker behavior from native harness integration.

### 4.1 Broker mode: portable baseline

```text
Unmodified MCP harness
        |
        | fixed, small MCP tool surface
        v
Tappet broker
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
tappet.search
tappet.describe
tappet.read
tappet.invoke
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
Tappet core library
```

Native adapter mode is optional and client-specific. It must reuse the same registry, resolver, package, provider, and observability contracts as broker mode.

## 5. The meaning of activation and unloading

These terms must be precise.

### Broker mode

Activation means that Tappet materializes and returns selected capability data. Unloading can release provider resources and stop repeating capability data, but it cannot remove tokens already present in the conversation.

### Native adapter mode

Activation may update the harness's future tool and context projection. Deactivation may omit that projection from future model requests if the harness supports context reconstruction.

### Fresh-context mode

A subagent or new context can provide the strongest isolation: capability material exists only in that context and the parent receives a compact result.

Tappet core should not claim literal context deletion unless a tested adapter provides it.

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

The final order is deterministic: sort first by the match tier above, then by
lexical score descending within a tier, then by fully qualified capability ID
in ascending UTF-8 byte order. The ID tie-break is applied before `limit` or
cursor pagination, so map iteration, database rebuilds, and equal-score cards
cannot change which top-K results are returned.

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

The broker adapter exposes the fixed Tappet tools and translates calls into core operations.

It should:

- return structured content where the client supports it
- preserve output schemas
- produce deterministic tool ordering and descriptions
- use bounded outputs
- avoid embedding a complete downstream catalog into its own tool descriptions
- remain usable by clients that ignore live tool-list changes

#### 6.5.1 Bounded materialization and invocation results

Every broker response has a finite encoded-size limit. Tappet must never
silently truncate or flatten a schema, skill, reference, or provider result.

V1-alpha uses these encoded-byte ceilings. The defaults are also the hard
maxima unless this document and the associated benchmarks are updated:

| Value | Default | Hard maximum |
| --- | ---: | ---: |
| complete outward JSON-RPC response | 1 MiB | 1 MiB |
| inline materialization or typed invocation result | 256 KiB | 1 MiB |
| accepted broker-adapted invocation result | 16 MiB | 16 MiB |
| accepted downstream JSON-RPC error | 16 MiB | 16 MiB |
| complete encoded `resources/read` response for one snapshot | 1 MiB | 1 MiB |
| stored resource snapshot bytes for one invocation | 16 MiB | 16 MiB |
| decoded bytes in one `tappet.read` chunk | 64 KiB | 64 KiB |

Encoded response size means the exact compact UTF-8 JSON encoding of the
complete outward JSON-RPC response. It includes the outer envelope, content,
structured content, metadata, member names, escaping, and base64 expansion.
HTTP framing and content encoding do not change the accounting. The inline
decision uses a deterministic encoding of the whole candidate response. The
accepted-result limit uses the complete broker-adapted typed-result encoding
after resource URI rewriting, while the downstream frame limit applies to the
provider message before adaptation.

A 64 KiB decoded `read` chunk expands to 87,384 base64 bytes. Bounded handles
and fixed metadata keep its complete encoded response below 128 KiB and
therefore below the 1 MiB broker-response ceiling. A deployment may lower a
limit only when every dependent limit still fits. Startup validation constructs
the maximum-shaped `read` response and rejects a configuration whose response
ceiling cannot contain it; an operator that needs a smaller response ceiling
must also lower the decoded chunk maximum. Limits are checked against the
encoded candidate before publication, so no transport-specific encoder can
turn an accepted response into an oversized one.

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
The lease survives reinstall, uninstall, and metadata refresh. It has a
five-minute idle timeout and a one-hour absolute lifetime; each successful
non-final chunk renews only the idle deadline.

Before exposing any chunk, Tappet atomically commits an exact response replay
in the lease's single replaceable replay slot. Repeating the same input
reference, offset, and `max_bytes` returns that byte-identical response without
renewing its deadline. Every initial stable-reference read includes a
caller-generated `attempt_id` containing at least 128 bits of randomness and
unique to that logical read attempt. An atomic mapping from the authorized
requester scope, stable source reference, `attempt_id`, offset, and `max_bytes`
to the new lease makes a lost initial response return the same
`continuation_ref`. Independent readers use distinct attempt IDs and therefore
receive distinct leases even when every other input is identical. Caller
authorization is checked before this lookup, but a committed replay takes
precedence over later registry staleness. The attempt ID is bounded, is not
itself a read route, and is excluded from raw telemetry. A request containing
the returned continuation and exact
`next_offset` proves receipt of the preceding non-final response, so preparing
that next chunk atomically replaces the prior replay record. Thus one replay
record per lease protects every prepared chunk without retaining an unbounded
history.

Advancement is serialized per lease and guarded by a monotonic lease-generation
compare-and-swap. For one expected offset, the first request tuple containing
`ref`, `offset`, and `max_bytes` to commit wins. An identical concurrent or
later tuple receives the committed byte-identical replay. A competing tuple
with a different `max_bytes` receives bounded `continuation_conflict` without
preparing or exposing another chunk and without changing the replay slot. Only
a request at the committed response's `next_offset` may advance the generation
and replace that replay. A losing request can therefore never expose bytes that
the lease no longer knows how to replay.

A non-final replay remains available until it is replaced, the five-minute idle
deadline passes, or the one-hour absolute deadline passes. Preparing a final
chunk retains the immutable object and its exact response replay for five
additional minutes rather than releasing immediately. This also applies to a
one-chunk artifact read whose original stable reference becomes stale after
response preparation. Each response reports its effective expiry.
Live-handle, replay-record, and retained-byte quotas bound this state, and
admission fails before the first chunk is promised if continuation and replay
capacity cannot be reserved. These are explicit application handles independent
of the broker transport or MCP protocol session.

An inline `invoke` result keeps the downstream MCP result at the outer protocol
level. Tappet forwards every provider content block in the outer
`CallToolResult.Content`; text, image, and audio blocks remain unchanged. An
embedded-resource block keeps its typed contents and all non-URI fields, but
each standard `ResourceContents.uri` is rewritten to a resolvable broker URI
backed by the snapshot defined in section 6.5.3. A resource-link block keeps its
name, title, description, media type, and annotations, but its provider-scoped
URI is rewritten through the same route. Tappet also preserves
`StructuredContent` and `IsError`.
For inline results, Tappet copies every provider-defined `_meta` entry to the
same top-level key so unmodified consumers keep seeing the metadata paths they
expect. Broker fields use the reserved envelope
`_meta["io.tappet.proxy"]`. If and only if the provider already used that
exact reserved key, Tappet stores its original value unchanged under
`downstream_reserved_value` inside the proxy envelope; no other downstream key
is moved or wrapped.

```json
{
  "content": [{"type": "image", "data": "...", "mimeType": "image/png"}],
  "structuredContent": {"provider": "value"},
  "isError": false,
  "_meta": {
    "tappet": {"provider_owned": true},
    "io.tappet.proxy": {
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
within the accepted result limit, Tappet stores a lossless UTF-8 JSON encoding
in a temporary spill object. `invoke` preserves the provider's outer `IsError`
classification and returns compact spill metadata in
`_meta["io.tappet.proxy"].invocation` and structured content. The complete
broker-adapted typed result, including the provider `_meta` object at its
original keys and any scoped resource-link rewrites, exists only inside the
lossless spill. No provider metadata is copied into the spilled outer response
because it may itself be what exceeded the inline limit. The outer content
contains only a bounded notice directing the caller to `tappet.read`; it never
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

Each result or error spill reference is an opaque application handle with a
published `expires_at` by which retrieval must begin. The first `read` uses the
spill reference at offset zero and atomically activates its reserved retrieval
lease. If the value is incomplete, that response returns a distinct opaque
`continuation_ref`; later chunks must use that reference and the exact reported
`next_offset`. The active lease has a five-minute idle timeout renewed by each
successful non-final chunk and a one-hour absolute lifetime from the first
chunk. Every prepared spill chunk uses the single-slot replay and replacement
rules above. The final response reports its fixed five-minute replay deadline as
`expires_at`, after which the object and lease are released. A transport loss
after response preparation therefore cannot make an initial, intermediate, or
final chunk unrecoverable.
A read started before the published deadline cannot be reclaimed between chunks
merely because the original spill expiry passes.

Each byte range is base64 encoded, and reassembly preserves the complete
broker-adapted typed MCP result or original JSON-RPC error. Spill admission
reserves the object bytes, handle slot, one potential retrieval lease, and the
bounded final-replay grace atomically; live retrieval leases and retained bytes
have finite quotas. An
inactive spill expires at its published deadline, while an active lease is not
evicted to admit another spill. If storage or lease reservation fails after a
provider `CallToolResult` was received, `invoke` returns a bounded preservation
`CallToolResult` with `IsError: true` and the completed-call envelope:

```json
{
  "provider_call_state": "completed",
  "provider_is_error": false,
  "provider_result_published": false,
  "retry_safe": false,
  "invocation_id": "invocation:opaque-id",
  "failure": "result_spill_failed"
}
```

The original `provider_is_error` value is retained. This local preservation
failure never reports the provider operation as failed or outcome-unknown and
never returns a partial provider result. The same completed-call envelope is
required for every broker-local adaptation, encoding, resource-materialization,
or publication failure after a provider `CallToolResult` has arrived.

A downstream JSON-RPC error is not an MCP `CallToolResult` and follows the same
bounded-preservation rule separately. When the complete encoded error object
fits the broker response limit, Tappet forwards its numeric `code`, `message`,
and structured `data` unchanged. When it exceeds the inline limit but fits the
accepted downstream frame and spill limits, Tappet stores a lossless encoding
of those three original fields in an error spill. The bounded outward JSON-RPC
error uses the broker-owned server-error code `-32099` and the fixed message
`Downstream error payload spilled`, and puts only this broker envelope in
`data`. The exact original numeric code is retained inside the spill rather than
copied into the bounded outer response:

```json
{
  "io.tappet.proxy": {
    "disposition": "spilled_error",
    "error_ref": "error:opaque-handle",
    "media_type": "application/json",
    "stored_bytes": 1048576,
    "sha256": "hex-digest",
    "expires_at": "RFC3339 timestamp"
  }
}
```

`tappet.read` retrieves that error reference in bounded chunks; reassembly
restores the exact original code, message, and data. The outer envelope never
copies the provider code, a partial provider `data` value, or a truncated
provider message. The fixed outer code remains bounded even when the accepted
provider code has an unusually long numeric lexeme.
Before transmitting any invocation byte, admission reserves one terminal-error
object, its potential retrieval lease, and enough bytes for the maximum accepted
downstream JSON-RPC error. If that reservation is unavailable, invocation fails
with typed `provider_error_capacity_exhausted`,
`provider_call_state: "not_started"`, and `retry_safe: true`. An inline error
releases the unused reservation; an oversized accepted error commits its spill
from the reservation.
Quota exhaustion therefore cannot replace a received authorization or other
provider error. A storage-integrity failure despite a committed reservation is
reported as `downstream_error_preservation_failed`, with the completed-call and
unsafe-retry fields, and is an operational fault rather than an admission path.
MCP results with `isError: true` remain typed `CallToolResult` values and use the
ordinary result path above.

The spill store must enforce finite, operator-configurable limits for incoming
provider-result or error bytes, bytes per spill object, aggregate live bytes,
live object count, object lifetime, and bytes per `read`. Provider adapters
enforce the incoming limit while reading, before unbounded decoding or
allocation. Spill admission reserves aggregate bytes, an object slot, and one
potential retrieval-lease slot atomically. Terminal-error reservations are
charged to the same finite aggregate budgets before provider transmission and
cannot be borrowed by ordinary result spills. The store may reclaim an inactive
object
after its published retrieval-start deadline or an active object after its idle
or absolute lease expires, but it must not evict an active object promised to a
reader. An oversized value or exhausted store returns a distinct typed error
and does not create a partial handle. V1 must ship safe finite defaults and
expose quota failures through metrics and logs.

Every opaque handle that grants access to retained bytes or replay state is a
bearer secret. This includes result and error spill references, every artifact
or spill `continuation_ref`, rewritten resource handles, and final-response
replay handles. Each handle must be unguessable and must never appear raw in
logs, trace attributes or events, metric labels, error messages, panic reports,
or audit payloads. Telemetry may correlate a handle only through an irreversible
keyed digest produced with a telemetry-specific secret; that digest is never
accepted by `read` or `resources/read`. A stable package or schema reference
that is reauthorized against the current registry is not a continuation handle.
Once an incomplete read returns a continuation, that bearer handle alone selects
the retained snapshot and receives these protections even after reinstall or
uninstall.

Process-local retained objects follow the inactive and active deadlines above
and are removed at shutdown. Application handles are not MCP protocol sessions
and must not carry hidden capability activation state. A single-process V1 may
use local storage. A replicated
deployment must make every handle-backed object and its atomic lease/replay
state resolvable across requests. This includes result and error spills,
rewritten provider resources, artifact and spill continuation handles, and final
response replay records. It may use a shared bounded store addressable by opaque
handle, or encode authenticated owner-replica routing in the handle and enforce
explicit affinity for the handle's full published lifetime. Affinity is derived
from the application handle, never an implicit MCP session or client connection.
Shared-store lease and replay records are durable for their published lifetime
and are garbage-collection roots. Collection uses a transaction, epoch, or
equivalent fence so startup or quota-driven sweeping on one replica cannot
reclaim an object referenced by an unexpired lease owned by another replica.
An owner-routed store confines collection to the durable owner namespace and
retains its lease records across owner restart.

If neither shared storage nor handle-routed affinity is available, the
deployment must disable every path that can emit an affected handle and return
a typed configuration or materialization error instead. Disabling spill
retrieval alone is insufficient because inline results may emit resource handles
and `read` may emit continuation or replay state. Startup validation rejects a
multi-replica configuration whose enabled handle-producing paths have no shared
or routable store. Process-local handles alone are not a stateless
horizontal-scaling design.

#### 6.5.2 Bounded broker requests

Broker inputs are bounded before constructing a JSON object graph. V1 accepts
at most 1 MiB of encoded JSON-RPC request data per message. HTTP rejects an
oversized declared body and applies a limited reader to chunked or undeclared
bodies. Stdio and other framed transports stop reading once the frame limit is
reached; they must not accumulate an unbounded line or frame first. A
depth-aware tokenizing decoder then rejects invalid UTF-8 or syntax and stops as
soon as object or array nesting would exceed 64 or the message would exceed
131,072 JSON syntax nodes. Each object member name, scalar value, object, and
array counts as a node. The scanner never materializes a node beyond either
budget. A transport or SDK integration that cannot enforce the byte, depth, and
node-count boundaries below its general object decoder is not supported on an
untrusted broker endpoint.

During tokenization, every object keeps a bounded set of its decoded member
names. A repeated name, including an escape-equivalent spelling such as `id`
and `\u0069d`, rejects the whole request with
`broker_message_duplicate_key` before general decoding, schema validation,
provider startup, or transmission. This applies to the outer JSON-RPC object
and every nested object, especially `invoke.arguments`, so Tappet and a provider
cannot select different occurrences. The byte and node budgets above bound the
temporary key sets.

The tokenizer retains each JSON number's original validated lexeme. Invocation
arguments remain raw numeric tokens or `json.Number`-equivalent decimal strings
through schema validation and downstream encoding; validation uses exact
integer or decimal arithmetic and never converts through binary `float64`.
Forwarding preserves the accepted numeric lexeme. The same rule applies at the
downstream transport boundary to structured result content, metadata, and
JSON-RPC error data before any typed SDK model can round a value. An adapter
that can expose these values only through `map[string]interface{}` values
decoded as `float64` cannot provide the V1 lossless invocation path.

An oversized inbound message invalidates the affected transport. Tappet may
emit a bounded `broker_frame_limit_exceeded` response when the framing still
permits it, but then closes the connection or session before accepting another
request. In particular, a newline-delimited stdio server exits or closes its
input instead of interpreting the unread remainder as a second message. HTTP
returns a bounded 413 response and forces the request connection closed;
streaming transports close the stream. V1 does not perform unbounded draining
or attempt in-place resynchronization after the frame boundary is lost. A client
may continue only through a fresh transport connection.

The depth limit is enforced during tokenization. After bounded, depth-checked
decoding, V1 applies the remaining tool-input limits before dispatch:

| Input | Limit |
| --- | ---: |
| JSON nesting depth | 64 |
| JSON syntax nodes | 131,072 |
| encoded outer JSON-RPC `id` token | 256 bytes |
| `query` | 4,096 normalized UTF-8 bytes |
| capability or operation ID | 128 bytes |
| hierarchy `path` | 256 bytes |
| cursor or artifact `ref` | 2,048 bytes |
| initial read `attempt_id` | 128 bytes |
| `include` values | 8 entries, 64 bytes each |
| `operation_schemas` | 32 exact IDs |
| `limit` or `child_limit` | 100 items each |
| `max_bytes` requested from `read` | 64 KiB decoded |
| normalized `invoke.arguments` | 256 KiB encoded JSON |

Invalid UTF-8 or syntax returns `broker_message_invalid`; excess nesting returns
`broker_message_depth_exceeded`; and excess node count returns
`broker_message_node_limit_exceeded`. An exceeded field, collection, or
aggregate limit returns its typed bounded-request error. Each failure occurs
before registry lookup, cache access, provider startup, or spill allocation.
The ID limit applies to string and numeric IDs using their exact encoded token.
An over-limit ID returns a bounded invalid-request response with `id: null` and
cannot reach dispatch; Tappet never executes a request whose ID cannot fit in
the smallest terminal response.
The outer 1 MiB limit remains authoritative even when individual fields are
below their limits. Deployments may set smaller limits, but changing these
V1-alpha maxima requires an explicit architecture and benchmark update.

#### 6.5.3 Scoped provider resources and links

A downstream `resource-link` URI is scoped to the provider connection that
returned it; forwarding that URI without a read route would create a dangling
link. Before publishing an invocation result containing such a block, Tappet
uses that same initialized provider session to call downstream
`resources/read`. Resource links and embedded resources together are limited to
32 resource-bearing blocks per result. Tappet snapshots each complete typed
resource response under finite per-resource and aggregate byte quotas and
stages all snapshot capacity before returning the tool result.
The block URI is then rewritten to an unguessable
`tappet-resource://<opaque-handle>` URI. All other block fields remain
unchanged. Before computing the snapshot encoding or digest, Tappet also
rewrites every standard `ResourceContents.uri` field in the downstream
`resources/read` response to that broker URI, or to an opaque child URI backed
by the same snapshot. Other typed resource-content fields remain unchanged.

An embedded-resource block already contains its typed resource content, so it
does not require a second provider read. Before sizing, spilling, or publishing
the invocation result, Tappet snapshots that content, replaces every standard
`ResourceContents.uri` with a distinct opaque broker URI backed by the
snapshot, and retains every other typed field unchanged. Resource links and
embedded resources share that count limit, the aggregate snapshot-byte quota,
publication transaction, handle lifetime, and scoped `resources/read` route. An
embedded resource that would exceed those bounds fails materialization rather
than exposing a provider URI.

The original provider URI and any provider-originated nested content URI exist
only in bounded transient call state while Tappet adapts an embedded resource or
performs `resources/read`; they are treated as possible signed or
credential-bearing secrets and are excluded from telemetry. After the read
completes or fails, Tappet discards every plaintext provider URI. Neither the
typed snapshot nor its stored encoding retains one. The snapshot record retains
only an irreversible keyed digest of the original provider URI for correlation,
plus the provider fingerprint, content digest, and invocation ID. The digest is
never accepted as a read route and stale checks do not require the original URI.

On the successful result path, resource snapshots, the fully adapted result
encoding, and any required result spill are one publication transaction.
Tappet commits the resource handles and result-spill handle together only
after every link has been materialized and the final bounded response or spill
has been prepared. Any failure before that commit aborts the success transaction
and releases every staged resource snapshot, object slot, and byte reservation;
no unreachable resource object remains charged against quota.

If a downstream `resources/read` failure needs an error spill for its structured
cause, Tappet first aborts that success transaction. It then admits and commits
an independent error-only spill transaction and returns its `error_ref` only
after that commit succeeds. The error transaction never commits staged resource
handles or a result spill. If error-spill admission fails, the preservation
error returns the completed-call failure envelope without an `error_ref`. A
transport failure after either publication commit cannot be rolled back safely,
so ordinary finite handle expiry reclaims the published object.

The outward broker advertises MCP resource support for a scoped
`resources/read` route. Reading the rewritten URI returns the exact snapshotted
broker-adapted typed resource contents without restarting or consulting the
provider; only the standard URI fields differ from the downstream response.
`resources/list` does not enumerate ephemeral handles, and arbitrary provider
URIs are rejected; possession of the broker-issued URI is required. A snapshot
is admitted only when the exact compact encoding of its complete outward
JSON-RPC `resources/read` response, including the broker-adapted typed content,
URI rewrites, and outer envelope, fits the 1 MiB broker-response ceiling. Stored
content bytes alone are not the admission measurement. This route does not
chunk, truncate, or flatten resource contents, so any snapshot whose complete
read response would exceed the ceiling fails before a handle is published.
Handles are bearer secrets excluded from telemetry and have bounded lifetimes
independent of provider connections.

For an inline result, every associated resource handle remains live for at
least five minutes after result publication. For a spilled result, its resource
snapshots are associated with the parent retrieval lease and remain pinned
through its final-chunk replay window plus a five-minute link-use grace period.
Preparing the first final spill response atomically sets each associated
resource expiry to the fixed final-replay deadline plus five minutes; an exact
final-chunk retry does not extend either deadline. The publication transaction
reserves retained bytes and object slots through the latest possible bound: the
spill's published retrieval-start deadline, plus its one-hour maximum active
lease, plus the five-minute final replay and five-minute link-use grace periods.
It releases unused retention after completion or abandonment. If it cannot
reserve that linked lifetime, invocation fails before any handle is published.
Reading a resource does not renew it indefinitely.

If the provider does not support `resources/read`, a link cannot be read in the
current session, any resource count or byte quota is exceeded, or snapshot
admission fails, `invoke` returns a bounded `resource_link_unmaterializable` or
`embedded_resource_unmaterializable` preservation error matching the offending
block. Because the downstream `tools/call` has already returned at this point,
the error explicitly reports `provider_call_state: "completed"`, the original
`provider_is_error` value, `provider_result_published: false`, and
`retry_safe: false`, along with the invocation ID. The broker-generated
preservation `CallToolResult` has `IsError: true`, but that broker classification
does not replace the separately recorded provider value or describe the
provider operation as failed or outcome-unknown. It does not emit a dangling
rewritten link, a partial resource snapshot, or a provider URI that the
Tappet-connected harness cannot resolve. This scoped proxy does not expose a
provider-wide resource catalog and does not add dynamic tools; implementing it
belongs to the portable broker milestone, not Milestone 0.

When downstream `resources/read` returns a structured JSON-RPC error, URI
secrecy is the narrow exception to byte-for-byte provider-error preservation.
Before sizing, spilling, logging, tracing, or publication, a bounded recursive
sanitizer replaces every exact occurrence of each plaintext provider URI known
to that read attempt in the message and in every JSON string token, including
decoded object member names, with
`tappet-redacted-uri:<irreversible-keyed-digest>`. The token is not accepted by
`read` or `resources/read`. The sanitizer retains the original numeric code,
provider error classification, all non-URI message text, and all other
structured data. The sanitized cause then uses the ordinary inline or
error-spill path in section 6.5.1.

Sanitization runs within the accepted frame, depth, node, and encoded-output
budgets. If it exceeds a budget, creates duplicate member names after
replacement, or cannot prove that the known plaintext URIs are absent, Tappet
returns `resource_link_unmaterializable` with the original numeric code,
provider classification, and completed-call state, but uses the fixed message
`Downstream resource error payload withheld` and omits the unsafe provider
message and data. Tappet never stores, returns, or emits telemetry containing
the unsanitized payload. Local unsupported-resource and quota failures remain
separately classified; they are never presented as a provider authorization
error.

#### 6.5.4 Broker admission and queues

Per-message limits do not bound aggregate broker work. Every outward transport
therefore uses a cancellation-aware ingress gate before it retains or decodes a
complete request. V1-alpha permits at most 128 active broker requests and 256
queued requests globally, with at most 64 active and 128 queued for any one
broker tool or resource method. Queued encoded request bytes have a separate
64 MiB global ceiling. Deployments may lower these values; raising a V1-alpha
maximum requires an architecture and benchmark update.

The transport reserves global request-count and encoded-byte capacity before
reading or retaining the full bounded frame. HTTP uses the declared length when
valid and pessimistically reserves the 1 MiB message maximum otherwise; framed
transports pause ordinary request admission when no ordinary ingress slot
exists, but continue reading through the bounded control lane below.

V1-alpha reserves eight global control-decode slots and 8 MiB of encoded-byte
capacity outside the ordinary request queues. A framed reader acquires one slot
and pessimistically reserves the 1 MiB frame maximum before reading the next
otherwise-blocked frame. Bounded tokenization identifies
`notifications/cancelled` without constructing a general object graph. A valid
cancellation is limited to 16 KiB encoded and runs synchronously in the control
lane, where it removes the matching queued request or cancels the active request
before the slot is released. An ordinary request encountered in this lane is
rejected as overloaded and never queued or dispatched. Other notifications are
either admitted to their ordinary bounded method queue or rejected without
retention. Control slots are assigned fairly across connections, and one
connection may hold at most one, so a saturated or hostile peer cannot prevent
another connection from delivering cancellation. The lane has no provider
startup, registry lookup, or tool execution path.

Capacity reservation starts a non-renewable 30-second ingress deadline covering
the complete body or frame read, bounded tokenization, and method admission;
progress does not extend it. HTTP additionally sets a five-second connection
header-read deadline before handler admission. The HTTP listener also acquires
one of at most 256 global open-connection slots before calling its underlying
`Accept` and returning a connection to `net/http`; the slot remains charged
through keep-alive or streaming use and is released only when the connection
closes. At capacity, the listener backpressures acceptance or closes a newly
accepted socket without starting a per-connection goroutine. HTTP keep-alive
connections have a finite 60-second idle timeout. Expiry releases every count and
byte reservation, cancels decoding, and closes or invalidates the affected
request transport; HTTP may return a bounded 408 when safe. The ordinary
request deadline begins no later than ingress reservation, so method decoding
cannot reset its elapsed time. Deployments may lower these values, but disabling
them is unsupported on an untrusted broker endpoint.

HTTP also applies a non-renewable 30-second terminal-publication deadline to
each response. The adapter sets the write deadline immediately before the first
response header or body byte, and the deadline covers the complete encoded
response, flush, and terminal transport publication. It is separate from
handler execution time so a valid long-running provider call does not consume
the response-write budget before publication begins. A streaming transport
applies the same 30-second bound independently to each complete outbound frame;
partial write progress does not renew it. Expiry cancels the request, releases
its broker and connection reservations, and force-closes the transport so a
client that stops reading cannot retain a request slot indefinitely. The HTTP
implementation must use a per-response write deadline or an equivalent
mechanism; `IdleTimeout` alone does not bound an active response write.

After the bounded envelope identifies the method, admission atomically moves the
request to that method's queue or rejects it without starting handler work.
Unknown and malformed methods use a finite control-method bucket within the same
global limits. A transport that cannot apply bounded ingress backpressure must
reject overload and close the affected connection rather than accumulate
requests outside the gate.

Global active slots are assigned round-robin across nonempty method queues.
Queue waiting counts against the request deadline, and cancellation removes the
entry immediately. A full method queue returns bounded `broker_overloaded` when
a valid JSON-RPC ID is available; HTTP may return a bounded 503 before decoding
when ingress capacity is already exhausted. An admitted `invoke` retains its
broker active slot while it waits in or executes through the separate provider
admission gate, so downstream saturation cannot bypass the broker-wide bound.
Metrics expose only bounded counts, byte totals, wait durations, and method
names, never request payloads or bearer handles.

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

#### 6.6.1 Provider residency and invocation admission

Provider call limits do not bound the number of completed calls that leave
processes, connections, SDK clients, and event queues resident. V1-alpha
therefore permits at most 64 live or starting provider instances globally and
at most 32 live stdio child providers. One provider-residency slot covers its
process or connection, SDK client, event queue, and lifecycle tasks. These
ceilings cannot be disabled, though deployments may lower them. A configured
provider whose idle shutdown is disabled is non-evictable and reserves its
residency capacity during startup validation; an impossible reservation set
fails startup with `provider_capacity_invalid`.

Before starting another provider, the manager reserves capacity atomically. If
the limit is full, it may close the least-recently-used idle evictable provider
that has no active or queued call and no startup or shutdown in progress. The
slot is not reusable until closure is confirmed. If every resident provider is
active, starting, stopping, or non-evictable, admission returns bounded
`provider_capacity_exhausted` before process creation or request transmission.
The error is safe to retry. Startup failures release reservations, shutdown has
a bounded deadline and failure state, and metrics expose live, reserved,
evictable, and non-evictable counts without configuration or credential data.

Provider concurrency is admitted through explicit, cancellation-aware bounds,
not an unbounded goroutine wait on a mutex. V1-alpha permits at most 128 active
provider calls and 512 queued calls globally. Each provider permits at most 64
queued calls; its active-call limit comes from the tested adapter concurrency
policy, defaults to one, and may be configured no higher than 32. Queue entries
contain only already bounded invocation data. Changing these maxima requires an
architecture and benchmark update.

Admission occurs before provider startup or acquisition. A full global or
provider queue returns `provider_overloaded` without enqueueing or calling the
provider; the response is bounded, classified as safe to retry, and includes a
bounded `retry_after_ms` only when the scheduler can estimate one. Queue waiting
is FIFO within a provider, counts against the invocation deadline, and is
removed immediately on cancellation. Available global active slots are assigned
round-robin across nonempty provider queues, so one saturated provider cannot
consume every slot. Active and queued counts are exposed as bounded metrics
without argument payloads.

#### 6.6.2 Ambiguous delivery and retry safety

V1 never automatically retries a downstream `tools/call` after any request byte
has been handed to the transport. A timeout, cancellation, connection loss, or
frame failure after that point can mean the provider executed the operation but
its result was lost. Tappet closes or repairs the connection for future work
and returns a typed `provider_outcome_unknown` error containing bounded provider
and invocation diagnostics; it does not replay the call. A call may be retried
automatically only when failure is proven to have occurred before transmission.
Any future post-transmission retry requires an explicit provider contract for
idempotency and a replay token; capability packages do not imply either.

#### 6.6.3 Downstream transport frame limits

Every message received from a downstream provider has a finite pre-decode
limit, regardless of method or direction of the JSON-RPC exchange. V1 permits
at most 16 MiB of encoded data, 128 levels of JSON object/array nesting, and
1,048,576 JSON syntax nodes per downstream protocol message. Each object member
name, scalar value, object, and array counts as a node. A token scanner enforces
all three limits before the SDK constructs the general object graph. During the
same scan, every object keeps a bounded set of decoded member names and rejects
duplicates, including escape-equivalent spellings, with
`provider_message_duplicate_key`. This applies to result content, structured
content, metadata, schemas, JSON-RPC error data, and every other nested object;
the SDK must never receive a message whose duplicate members it could collapse
into a map. The limits apply to
initialize and discovery responses, invocation results, errors, notifications,
ping responses, progress events, and unsolicited requests or callbacks. The
stricter metadata and result-ingestion quotas still apply after this transport
boundary.

Stdio adapters enforce the ceiling while reading a frame and never accumulate
an oversized line first. HTTP adapters use bounded body readers even without a
valid `Content-Length`; streaming transports enforce the limit independently
for each event. The bound must sit below the SDK decoder. An SDK or transport
that cannot expose a pre-decode boundary is unsupported for downstream V1 use.

An exceeded byte limit returns `provider_frame_limit_exceeded`; exceeded depth
returns `provider_message_depth_exceeded`; exceeded node count returns
`provider_message_node_limit_exceeded`; and a duplicate member returns
`provider_message_duplicate_key`. Any failure
fails every affected in-flight request and closes the protocol connection.
Tappet terminates a stdio child whose stream can no longer be trusted and
closes an HTTP or event stream session; it does not skip bytes or attempt
in-place resynchronization. The prior atomic metadata-cache snapshot remains
intact, but no further request is accepted until the provider manager
establishes and initializes a fresh connection. Repeated violations feed normal
failure backoff and bounded telemetry.

Per-message bounds also do not permit an unbounded stream of individually valid
provider-originated events. Each provider connection dispatches notifications
and callback requests through a cancellation-aware queue capped at 256 messages
and 16 MiB of encoded data, with at most 16 handlers active at once. The adapter
reserves both count and byte capacity after the pre-decode scan and before it
constructs a method-specific object or starts a handler goroutine. Terminal
responses route directly to the already bounded in-flight request set and do
not create an independent unbounded queue.

`notifications/tools/list_changed` has a synchronous invalidation step before
ordinary event enqueueing. In provider-wire receive order, immediately after
bounded pre-decode validation identifies that method, the reader acquires the
metadata-selection fence and marks the active provider generation dirty. Only
then may it reserve event-queue capacity and enqueue optional refresh or
observer work. Invocation dispatch acquires the same fence, so it cannot select
a supposedly clean schema while any earlier list-change frame is waiting for an
asynchronous handler. If the notification cannot be queued afterward, normal
event-overflow handling closes the connection; reconnect still refreshes
metadata before invocation.

The reader may exert transport backpressure only while reserved capacity exists;
it never waits indefinitely for an event consumer while continuing to retain
new frames. Failure to reserve event capacity closes the provider connection
and reports `provider_event_overflow`. Tappet terminates the stdio child or
closes the HTTP or event stream, cancels queued event work, and classifies every
affected transmitted invocation through the ambiguous-delivery rules above.
Unsupported callbacks are rejected within the same finite handler budget. Event
payloads, goroutines, and callback correlation state therefore remain bounded
even when a provider floods progress, list-change, or unsolicited messages.

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
long-lived connection, Tappet may reuse that invocation contract only when
the provider advertised reliable tool-list change notifications on the active
connection. The provider manager serializes notification handling with schema
selection: the wire reader marks metadata dirty synchronously before enqueueing
any asynchronous list-change work, and a dirty generation before dispatch
forces a bounded refresh and revalidation. Without that negotiated invalidation
contract, Tappet performs
a bounded metadata refresh immediately before every invocation, then validates
arguments against that result. A provider that advertises notifications but
changes schemas without sending them violates its negotiated contract; the
reported freshness state records which mode was used. If a digest changed,
Tappet atomically replaces the snapshot and invalidates old schema references.
If refresh fails, invocation fails without calling the provider; known-stale
metadata is never used as an invocation contract.

Queue admission leases the resolved package generation, operation, provider
binding, and provider-configuration fingerprint, but not a metadata generation
or schema digest. Under the same serialization used for refresh and dirty
notifications, dispatch atomically selects the current mapped tool and schema,
validates arguments, and leases that selected metadata generation through the
downstream call and terminal publication. A missing or incompatible current
mapping fails with a typed provider-metadata result before transmission. A
queued call therefore cannot use a pre-queue schema merely because its package
and binding generation remain leased.

V1 accepts only self-contained provider schemas. A `$ref` value may be `#` or a
same-document JSON Pointer beginning with `#/`; ingestion resolves and validates
that target exclusively within the received schema. V1 rejects `$id`,
`$anchor`, `$dynamicAnchor`, `$dynamicRef`, non-fragment references, unresolved
pointers, and reference cycles with `provider_schema_unsupported`. The schema
compiler has no network, filesystem, or general URI loader, so schema refresh or
argument validation can never fetch provider-controlled resources. A later
cache-only schema-resource registry requires a separate design and is not an
implicit fallback.

Schema safety has structural and work budgets in addition to byte quotas. Before
compilation or cache publication, each input or output schema is limited to 1
MiB encoded, depth 64, 16,384 syntax nodes, 1,024 local references, 64 reference
hops, 64 alternatives in any one applicator, and 1,024 alternatives across
`allOf`, `anyOf`, `oneOf`, `not`, `if`/`then`/`else`, and conditional array or
object applicators. Regular expressions are limited to 256 per schema and 4,096
bytes each and must use a linear-time engine. Exceeding a structural budget
returns `provider_schema_limit_exceeded` and leaves the previous atomic cache
snapshot unchanged.

Compilation and per-invocation validation each receive a deadline,
cancellation signal, memory ceiling, and deterministic work counter; validation
permits at most 1,000,000 keyword or instance-evaluation steps. The validator
checks cancellation and decrements the counter before descending into a schema
node or applicator branch. A library that cannot provide those hooks must run in
a killable resource-bounded worker, not unchecked in the broker process.
Exhaustion returns `provider_schema_validation_limit_exceeded`, does not call the
provider, and is distinct from ordinary invalid arguments.

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

A refresh also maintains one exact-name set across every `tools/list` page.
Seeing the same provider tool name twice rejects the entire refresh with
`provider_metadata_duplicate_tool`, even when both entries are byte-identical.
Duplicate detection occurs before insertion into a name-keyed map or schema
compilation, so page order cannot choose an invocation contract. The previous
atomic cache snapshot remains active, and first invocation fails rather than
calling an ambiguously described tool when no prior valid snapshot exists.

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
tappet.search
tappet.describe
tappet.read
tappet.invoke
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
- provider configuration; new fields use references or runtime injection, while
  inherited literal credential-bearing fields remain under the temporary
  compatibility exception in section 8.1
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
- explicit continuation handles for multi-call artifact and spill reads

### 7.4 Explicit application handles

MCP 2026-07-28 removes protocol-level sessions. Any cross-call scope, lease,
task, or activation must use an explicit application handle passed by the
caller. Spill references, resource handles, and artifact or spill continuation
handles follow this rule.

Do not map an implicit client connection to hidden capability state.

The first broker-mode vertical slice should remain stateless across Tappet
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

Tappet may support an optional external filter hook that removes unavailable capabilities from a projection, but the hook is an integration point, not a policy engine.

Every invocation still reaches the provider's normal authorization boundary. Tappet must propagate permission failures accurately.

### 8.1 Broker transport access

The inherited HTTP entrypoint optionally wraps the entire MCP endpoint with
`internal/server.newAuthMiddleware`, using static bearer tokens from
`mcpProxy.options.authTokens`. This is a transport access gate retained for
baseline compatibility. It does not identify a user, grant access to a
capability or operation, authorize a downstream action, or replace provider
authentication.

The catalog, resolver, materializer, and provider interfaces must remain
independent of this middleware. Provider configuration may contain credential
references or runtime injection settings, but new fields must not contain secret
values. Baseline compatibility nevertheless retains the inherited literal
inputs already accepted by current deployments:

- `mcpProxy.options.authTokens`
- `mcpServers.<provider>.env`
- `mcpServers.<provider>.headers`
- provider command arguments or URLs that contain inline credentials
- configuration-fetch headers supplied through the existing CLI

These are grandfathered compatibility inputs, not a Tappet credential store.
Milestone 0 continues to load and pass them through unchanged. Configuration
files and process arguments containing them must be treated as secrets; values
are redacted from logs, diagnostics, traces, metrics, panic reports, and audit
payloads, and are never copied into capability packages, metadata caches, or
generated hierarchy files. New provider fields and capability-package fields
must use references or runtime injection rather than adding another literal
secret location.

Removing or migrating any grandfathered field requires a separate compatibility
decision, tests for existing stdio, SSE, Streamable HTTP, outward HTTP, and
remote-configuration deployments, plus migration guidance toward environment or
external secret injection. Tappet does not grow this compatibility path into
credential lifecycle management, IAM, or policy.

## 9. Latest MCP strategy

The outward Tappet server should target the latest stable MCP revision and retain reasonable compatibility with legacy clients.

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

Current code uses `mcp-go v0.43.2`. `mark3labs/mcp-go` merged 2026-07-28 support after that version. Upgrade and conformance work must be isolated from the capability model so SDK migration does not drive architecture.

Do not switch Go SDKs without a focused comparison. First test the current SDK line at a revision containing modern support.

### 9.1 Multi-round-trip requests

Tappet must advertise downstream client capabilities only when its provider
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

### `tappet.search`

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

### `tappet.describe`

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
page, `describe` atomically materializes one bounded immutable projection
containing the capability version and digest plus only the sections requested
by `include`. When operations are included, the projection retains the exact
metadata-cache snapshots needed by those operation cards; a skills-only or
references-only projection retains no provider generation. The opaque cursor
binds that projection, the requested `include` set, and a page offset. Later
pages read the same retained projection, so package reinstall, provider refresh,
or empty-cache transition cannot mix generations or force a pagination restart.
New initial requests observe the new state. Projection bytes, live cursor count,
a five-minute idle lifetime, and a one-hour absolute lifetime are quota-bound;
an expired or unavailable projection returns an explicit stale-cursor error.

`operation_schemas` is an independent exact selector and may first appear on
any page. Each selector is resolved against the current provider metadata at
that request and reports its own generation, digest, observation time, and
freshness state; it neither mutates nor broadens the retained structure
projection. Selected schema content counts toward the response's size bound
without changing the structure cursor. Individual metadata entries are subject
to package validation limits.

Each operation card with cached metadata includes its schema reference, digest,
observation time, and freshness state. With an empty cache it instead reports
`metadata_state: "unavailable"`; an exact schema selector then returns the typed
cache-miss result defined in section 6.7. Full skill bodies remain deferred to
`read`. Schemas for unselected operations are never returned. Unknown operation
IDs fail explicitly, and the schema selector count and total response size are
bounded independently of structure pagination.

### `tappet.read`

Input:

```json
{
  "capability_id": "software.github.ci-debugging",
  "ref": "schema:v1:provider-fingerprint:schema-digest:inspect-failed-checks",
  "attempt_id": "read:client-generated-128-bit-random-value",
  "offset": 0,
  "max_bytes": 65536
}
```

The `ref` above is illustrative but not mutable: callers copy the exact opaque
schema reference returned by `describe`. Its encoded identity binds the
operation ID, provider configuration fingerprint, and schema digest. `read`
must return a typed stale-reference error if any binding no longer matches; it
must never resolve the token to whichever schema is current at read time.
`attempt_id` is required when `ref` is a stable artifact or spill reference;
the caller reuses it only to retry that same initial request. Continuation
requests use the returned continuation reference and do not reuse the attempt
ID.

Output: one operation schema, skill body, permitted reference resource, or
bounded chunk of one of those artifacts. `max_bytes` always caps decoded
artifact bytes returned by the call. A complete artifact is returned inline
only when the remaining bytes from `offset` fit both `max_bytes` and the encoded
response limit. Otherwise the response uses the same `offset`, `max_bytes`,
base64 chunk, byte-count, and digest fields shown below, even when the complete
artifact would fit the default inline-materialization limit. If the first chunk
is incomplete, its response also returns `continuation_ref` and `expires_at`;
every later chunk uses that continuation reference as `ref`, preserving the
same immutable snapshot across reinstall, uninstall, or cache refresh.

For an oversized invocation result or downstream JSON-RPC error, the same tool
accepts the corresponding spill reference:

```json
{
  "ref": "result:opaque-handle",
  "attempt_id": "read:client-generated-128-bit-random-value",
  "offset": 0,
  "max_bytes": 65536
}
```

The published `tappet.read` input schema is a union of two request shapes. An
initial request with a stable artifact, result-spill, or error-spill `ref`
requires `attempt_id`; an initial spill read also requires `offset: 0`. A
continuation request requires the returned continuation `ref` and omits
`attempt_id`. Schema validation rejects an initial request without its attempt
ID and rejects a continuation request that supplies one.

Output:

```json
{
  "ref": "result:opaque-handle",
  "continuation_ref": "read:opaque-handle",
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
digest verifies the reassembled value. An incomplete first spill response
returns `continuation_ref`; subsequent requests use it as `ref` with the exact
`next_offset`, as required by the bounded retrieval lease in section 6.5.1.
`offset`, `next_offset`, `max_bytes`, and `stored_bytes` count decoded bytes in
the stored UTF-8 JSON representation, not base64 characters.
For artifacts and spills, preparing any response creates or replaces the
bounded single-slot replay record defined in section 6.5.1. Retrying the exact
input reference, offset, and `max_bytes` before its effective expiry returns the
same response even if the first transport delivery was lost.

### `tappet.invoke`

Input:

```json
{
  "capability_id": "software.github.ci-debugging",
  "operation_id": "inspect-failed-checks",
  "arguments": {
    "repository": "gearboxlogic/tappet"
  }
}
```

Output: the provider's native MCP content blocks, structured content, and
`isError` at the outer `CallToolResult` level when inline, with Tappet metadata
under `_meta["io.tappet.proxy"].invocation` and all noncolliding provider
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
- every prepared read chunk is exactly retryable until receipt is proven or its
  bounded replay deadline passes
- request and provider-message nesting and syntax-node limits fire before
  general object construction
- broker requests and provider-originated events cannot create unbounded queues
  or handler concurrency
- provider schemas cannot trigger external I/O or unbounded validation work
- post-call preservation failures retain completed-call and original `isError` state
- rewritten resource links remain readable for their promised post-result lifetime
- plaintext provider resource URIs do not persist after materialization
- every published cross-call handle resolves across replicas, or startup rejects
  the unsupported replicated configuration
- every continuation or retained-data handle is unguessable and excluded from
  raw telemetry
- queued and active invocations cannot mix package or provider-binding generations
- live provider instances and stdio children remain within global budgets
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
