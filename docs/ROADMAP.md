# Tappet Roadmap

Status: **proposed milestone plan**

The roadmap is ordered to prove the narrow capability problem before expanding the platform.

## Milestone 0: Establish an honest baseline

Outcome: the repository can be changed safely without confusing inherited Lazy MCP behavior with planned Tappet behavior.

Work:

- add architecture and agent guidance
- inventory current behavior with characterization tests
- restore or retire `.skip` tests deliberately
- rename module, imports, binaries, and docs from Lazy MCP to Tappet
- establish upstream attribution and license notices
- add CI for tests, race detection, vet, formatting, and static analysis
- record current tool-surface and server-construction benchmarks
  ([Milestone 0 baseline](BASELINE_BENCHMARKS.md))
- retain inherited outward `authTokens` and provider `env`, `headers`, command
  arguments, URLs, and configuration-fetch headers as explicitly grandfathered,
  redacted compatibility inputs

Exit criteria:

- all inherited behavior is covered or intentionally removed
- repository naming is internally consistent
- CI is green
- no planned feature is represented as implemented
- existing stdio, SSE, Streamable HTTP, outward HTTP, and remote-configuration
  credential inputs remain compatible and absent from logs and generated data

## Milestone 1: Modern MCP foundation

Outcome: Tappet can serve a modern fixed broker surface and communicate with modern and legacy downstream providers.

Work:

- upgrade `mark3labs/mcp-go` to a tagged revision containing 2026-07-28 support
- verify outward `server/discover`
- verify modern stateless HTTP behavior
- verify modern stdio requests
- retain legacy negotiation where practical
- preserve structured content, output schemas, result types, and errors
- advertise no unsupported provider-to-client callback capabilities
- reject unsolicited multi-round-trip callbacks promptly and observably
- enforce downstream byte, nesting, and syntax-node budgets before SDK decoding
- bound provider-originated notification and callback queues, bytes, and handler
  concurrency
- run official MCP conformance suites in CI
- add dual-era downstream fixtures

Decision gate:

Switch to the official Go SDK only when a measured migration comparison shows a clear correctness or maintainability benefit.

Exit criteria:

- documented conformance results by protocol version and transport
- no hidden capability state tied to protocol sessions
- provider connections or invocations requiring unsupported callbacks fail within their applicable deadline
- provider event floods close the affected connection without unbounded message
  buffering or handler creation
- modern and legacy provider fixtures pass

## Milestone 2: Capability package and registry

Outcome: Tappet loads validated capability packages instead of treating hierarchy JSON as the domain model.

Work:

- implement V1-alpha manifest parser
- implement stable capability, operation, provider, skill, and context types
- integrate Agent Skills validation
- normalize packages into immutable records
- add exact-ID lookup and hierarchy browsing
- migrate existing hierarchy data through a candidate generator
- keep generated output reviewable

Exit criteria:

- at least three hand-reviewed packages load
- duplicate and invalid references fail deterministically
- unknown manifest fields at every mapping level reject the complete staged
  package
- FIFOs, devices, sockets, directories, and other non-regular package artifacts
  fail without blocking or reading from them
- no provider credentials appear in packages
- validation and normalized records use private staged bytes even when source
  files mutate during installation
- queued and active invocations retain one immutable package and provider-binding
  generation through terminal publication
- current tool mappings can be represented without loss

## Milestone 3: Catalog search and progressive reads

Outcome: an agent can find a capability without loading its schemas or skill body.

Work:

- compact capability cards
- exact match pinning
- deterministic lexical ranking
- stable capability-ID tie-breaking before top-K truncation
- optional path-constrained search
- explainable match reasons
- deterministic cursor pagination for `describe` materialization
- Agent Skill body reads
- individual reference reads
- bounded chunk retrieval for skills and references

Initial benchmark corpus:

- exact capability IDs
- exact operation names
- natural-language task descriptions
- ambiguous cross-domain requests
- negative/no-match requests

Exit criteria:

- 100% exact-ID recall
- agreed top-5 natural-language recall target on a versioned corpus
- no unrelated full skill bodies or operation schemas in search output
- `describe` returns bounded package structure without requiring provider
  metadata or starting a provider
- every accepted skill or reference artifact remains completely retrievable
  within response bounds
- a bounded continuation handle keeps an accepted multi-chunk artifact read
  available across a concurrent reinstall or uninstall
- every continuation handle is unguessable and excluded from raw telemetry,
  including after reinstall or uninstall
- every prepared artifact chunk, including the first, remains exactly retryable
  until receipt is proven or its bounded lease deadline passes
- independent initial readers of the same artifact receive separate leases,
  while exact retries with one attempt ID recover the same first response
- concurrent requests cannot advance one continuation lease along competing
  chunk boundaries
- large capability structures remain completely retrievable across stable pages
- routine provider refreshes and package changes do not invalidate an active
  immutable `describe` projection
- deterministic results across runs

## Milestone 4: Provider manager and metadata cache

Outcome: discovery works while providers are stopped, and providers consume resources only when used.

Work:

- one provider manager replacing overlapping lazy-client paths
- disk metadata cache with version and schema digests
- cache priming during capability installation and explicit operator refresh
- exact operation-schema selectors in `describe`
- bounded chunk retrieval for cached schemas
- atomic paginated metadata ingestion with exact duplicate-name rejection
- lazy start
- idle shutdown
- reconnect and failure backoff
- request cancellation and timeout
- same-provider concurrency policy
- different-provider parallelism
- bounded global and per-provider active/queued invocation admission
- bounded global live-provider and stdio-child admission, including explicit
  capacity reservations for non-evictable providers
- explicit refresh
- conservative cache invalidation
- migrate inherited literal provider credential inputs only through a tested
  compatibility decision and operator guidance
- metadata and schema refresh before every invocation unless the active
  connection negotiated reliable tool-list change notifications
- self-contained schema-reference policy with bounded, cancellation-aware
  compilation and argument validation
- bounded inline output plus quota-limited lossless spill and leased chunked retrieval
- lifecycle status and telemetry

Exit criteria:

- search and describe never start providers implicitly
- an empty cache yields typed metadata-unavailable cards until install-time refresh, explicit refresh, or first invoke populates it
- `describe` returns schema content or an exact digest-bound schema reference
  only for explicitly selected operation IDs with available cached metadata
- first invoke starts only the selected provider
- idle provider stops and reconnects on next invoke
- reconnect refreshes schemas before invoke and stale arguments never reach the provider
- an ambiguously delivered provider call is never replayed automatically
- saturated provider queues reject new work before enqueueing, while queued
  cancellation is prompt and observable
- sequential use of many providers cannot exceed live-provider or child-process
  budgets, even when idle shutdown is disabled
- reinstall and uninstall cannot retarget or tear down a queued or transmitted
  invocation leased to an older registry generation
- a queued invocation selects and validates the current provider schema
  atomically at dispatch rather than pinning pre-queue metadata
- post-call spill failures report the completed provider outcome and remain
  explicitly unsafe to retry
- large output cannot silently consume unbounded model context or lose structured content
- spill limits fail explicitly without exceeding per-result or aggregate quotas
- an accepted spill remains completely retrievable through a bounded active
  continuation lease even when its original start-by deadline passes
- every prepared spill chunk remains exactly replayable until receipt is proven
  or its bounded replay deadline passes
- JSON number lexemes survive invocation validation, provider forwarding, and
  structured result or error preservation without `float64` rounding
- duplicate JSON object members are rejected before argument validation or
  provider transmission
- duplicate JSON object members in downstream responses and errors are rejected
  before the SDK can collapse them into maps
- terminal provider-error spill capacity is reserved before transmission, so
  quota exhaustion cannot replace an authorization or other provider error
- provider metadata refresh fails atomically at finite response, page, item, schema-byte, or aggregate-byte limits
- duplicate provider tool names across one metadata refresh reject the snapshot
  before map insertion or schema selection
- external schema references and over-budget schema compilation or validation
  fail without network access or provider invocation

## Milestone 5: Portable broker vertical slice

Outcome: one unmodified MCP harness completes real tasks through a fixed small Tappet surface.

Candidate broker API:

```text
tappet.search
tappet.describe
tappet.read
tappet.invoke
```

The fixed tool surface is accompanied by the scoped MCP `resources/read` proxy
defined in `ARCHITECTURE.md`; it does not enumerate a provider-wide resource
catalog or add dynamic tools.

Milestone exit also requires deterministic encoded response ceilings, a bounded
HTTP terminal-response write deadline, attempt IDs on every initial artifact or
spill read, and tests proving that provider resource-read errors cannot retain
or republish a requested plaintext provider URI. Embedded-resource blocks must
also rewrite every provider URI to a readable broker snapshot before inline or
spill publication, and each complete snapshot read must fit the outward
response ceiling.

Test environment:

- two real downstream MCP servers
- three or more capability packages
- one Agent Skill
- one context reference
- one provider with many tools where a capability selects only a subset
- one provider tool that returns a provider-scoped resource link

Measure:

- initial tool-schema bytes/tokens
- total task tokens
- search top-K result
- schemas and skill bodies materialized
- provider startups
- end-to-end latency
- accepted task result
- error behavior

Exit criteria:

- fixed initial surface independent of catalog size
- global and per-method broker admission bounds reject or backpressure overload
  before retaining unbounded request state
- pre-decode header, body, and frame deadlines release ingress reservations held
  by stalled clients
- listener-level HTTP connection admission bounds sockets and goroutines before
  header parsing
- broker and downstream nesting and syntax-node limits are enforced during
  tokenization before general JSON object construction
- irrelevant capabilities do not enter the transcript
- provider authorization still applies
- structured result survives proxying
- every returned resource link resolves through the scoped broker resource
  route without exposing unrelated provider resources
- plaintext downstream resource URIs are discarded after materialization and
  never persist in snapshots or telemetry
- standard URI fields nested in snapshotted resource contents are rewritten to
  broker-owned opaque URIs before storage
- failed result publication releases staged resource capacity and preserves any
  structured downstream resource-read error
- resource-read error spills commit independently after staged resource handles
  are rolled back
- a preservation failure reports that the provider call completed, retains the
  original `isError` classification, and is explicitly unsafe to retry
- resources referenced by a spilled result remain live through result retrieval
  plus the bounded link-use grace period
- every enabled cross-call handle resolves through shared storage or explicit
  handle-routed replica affinity; unsupported replicated configurations fail at
  startup
- replicated garbage collection treats unexpired shared continuation leases as
  durable roots and cannot reclaim another replica's leased snapshot
- results beat eager exposure on context size without unacceptable task-success loss

## Milestone 6: Scale and retrieval evaluation

Outcome: prove behavior at realistic catalog sizes before adding semantic retrieval.

Catalog sizes:

```text
10
50
100
250
500+
```

Compare:

- eager exposure
- current hierarchy-only flow
- exact plus lexical search
- hierarchy-constrained lexical search
- optional embeddings experiment

Metrics:

- index size and build time
- search latency
- top-1/top-5 recall
- exact-match recall
- wrong-capability rate
- tool-schema tokens
- total task tokens
- model round trips
- task completion
- provider startup count

Decision gate:

Add embeddings only when they improve a versioned retrieval corpus enough to justify model/runtime/dependency cost.

## Milestone 7: Harness adapters

Outcome: selected harnesses can materialize capabilities using native tool and skill mechanisms.

Start with one harness whose extension API is stable and testable.

Adapter responsibilities may include:

- native direct-tool registration
- active projection management
- Agent Skill installation or injection
- fresh scoped contexts
- client-specific refresh behavior

Exit criteria for each adapter:

- precise supported client versions
- tested activation and deactivation semantics
- documented context-withdrawal limitations
- fallback to broker mode
- no core dependency on the adapter

Do not attempt all clients in parallel.

## Deferred until after the core is proven

- subagent orchestration
- planning
- conversation memory
- durable workflow state
- non-MCP providers
- package inheritance
- automatic dependency installation
- server registry provisioning
- agent-authored package mutation
- policy engines
- identity and credential lifecycle
- approval systems
- distributed activation/session state
- context RAG or knowledge reconciliation

## First implementation assignment

The first code PR after this architecture PR should be narrow:

1. characterize current outward tools and lazy startup,
2. rebrand module/imports without behavior change,
3. centralize the duplicate outward server setup,
4. add CI and active tests,
5. document the chosen `mcp-go` upgrade target,
6. stop before capability-package implementation.

This sequence lowers risk before changing the domain model.
