# CapScope Roadmap

Status: **proposed milestone plan**

The roadmap is ordered to prove the narrow capability problem before expanding the platform.

## Milestone 0: Establish an honest baseline

Outcome: the repository can be changed safely without confusing inherited Lazy MCP behavior with planned CapScope behavior.

Work:

- add architecture and agent guidance
- inventory current behavior with characterization tests
- restore or retire `.skip` tests deliberately
- rename module, imports, binaries, and docs from Lazy MCP to CapScope
- establish upstream attribution and license notices
- add CI for tests, race detection, vet, formatting, and static analysis
- record current tool-surface and server-construction benchmarks ([Milestone 0 baseline](BASELINE_BENCHMARKS.md))

Exit criteria:

- all inherited behavior is covered or intentionally removed
- repository naming is internally consistent
- CI is green
- no planned feature is represented as implemented

## Milestone 1: Modern MCP foundation

Outcome: CapScope can serve a modern fixed broker surface and communicate with modern and legacy downstream providers.

Work:

- upgrade `mark3labs/mcp-go` to a tagged revision containing 2026-07-28 support
- verify outward `server/discover`
- verify modern stateless HTTP behavior
- verify modern stdio requests
- retain legacy negotiation where practical
- preserve structured content, output schemas, result types, and errors
- run official MCP conformance suites in CI
- add dual-era downstream fixtures

Decision gate:

Switch to the official Go SDK only when a measured migration comparison shows a clear correctness or maintainability benefit.

Exit criteria:

- documented conformance results by protocol version and transport
- no hidden capability state tied to protocol sessions
- modern and legacy provider fixtures pass

## Milestone 2: Capability package and registry

Outcome: CapScope loads validated capability packages instead of treating hierarchy JSON as the domain model.

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
- no provider credentials appear in packages
- current tool mappings can be represented without loss

## Milestone 3: Catalog search and progressive reads

Outcome: an agent can find a capability without loading its schemas or skill body.

Work:

- compact capability cards
- exact match pinning
- deterministic lexical ranking
- optional path-constrained search
- explainable match reasons
- `describe` materialization
- exact operation-schema selectors in `describe`
- Agent Skill body reads
- individual reference reads
- output bounds

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
- `describe` returns full schemas only for explicitly selected operation IDs
- deterministic results across runs

## Milestone 4: Provider manager and metadata cache

Outcome: discovery works while providers are stopped, and providers consume resources only when used.

Work:

- one provider manager replacing overlapping lazy-client paths
- disk metadata cache with version and schema digests
- lazy start
- idle shutdown
- reconnect and failure backoff
- request cancellation and timeout
- same-provider concurrency policy
- different-provider parallelism
- explicit refresh
- conservative cache invalidation
- bounded inline output plus lossless spill and chunked retrieval
- lifecycle status and telemetry

Exit criteria:

- search and describe work from cache without starting providers
- first invoke starts only the selected provider
- idle provider stops and reconnects on next invoke
- stale schemas are detected
- large output cannot silently consume unbounded model context or lose structured content

## Milestone 5: Portable broker vertical slice

Outcome: one unmodified MCP harness completes real tasks through a fixed small CapScope surface.

Candidate broker API:

```text
capscope.search
capscope.describe
capscope.read
capscope.invoke
```

Test environment:

- two real downstream MCP servers
- three or more capability packages
- one Agent Skill
- one context reference
- one provider with many tools where a capability selects only a subset

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
- irrelevant capabilities do not enter the transcript
- provider authorization still applies
- structured result survives proxying
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
