# Tappet Agent Guide

This file contains durable instructions for agents and contributors working in this repository. Task-specific prompts, temporary branch scope, and one-off research directions do not belong here.

## Mission

Tappet reduces agent context pollution by progressively discovering and materializing coherent capabilities instead of exposing every installed tool schema, skill, and reference to every model call.

A capability is a dynamically discoverable package containing some combination of:

- Agent Skills-compatible procedural knowledge
- executable operations backed by providers
- selectively readable context or reference material
- provider bindings

Tappet is not a general agent framework.

## Current implementation status

The repository began from the January 2026 `voicetreelab/lazy-mcp` implementation and retains its two-tool broker behavior.

Verified current behavior:

- the public MCP surface consists of `get_tools_in_category` and `execute_tool`
- tool hierarchy data is stored as generated JSON files
- downstream MCP servers start lazily on first invocation
- downstream clients remain open until Tappet exits
- the unused provider-level `activate_<provider>` lazy-loading path has been retired; the shipped recursive hierarchy broker is the only lazy invocation path
- the module path is `github.com/gearboxlogic/tappet` and shipped binaries use Tappet names
- the MCP dependency is `github.com/mark3labs/mcp-go v0.43.2`
- downstream initialization uses the legacy MCP initialize handshake
- stdio and HTTP transports share one outward server constructor
- characterization and generator tests are active; obsolete `.skip` suites were retired with recorded dispositions
- skills, capability manifests, catalog search, idle provider shutdown, and harness adapters do not yet exist

Do not describe planned behavior as implemented behavior.

## Read before changing architecture

1. `docs/ARCHITECTURE.md`
2. `docs/CAPABILITY_PACKAGE.md`
3. `docs/PRIOR_ART.md`
4. `docs/ROADMAP.md`

When a proposed change conflicts with an accepted boundary in those documents, update the architecture decision explicitly rather than silently changing the design.

## Accepted initial boundaries

Tappet owns:

- capability registration and validation
- compact capability discovery
- hierarchy and search
- progressive materialization of skills, operation descriptions, schemas, and references
- downstream provider adaptation and process/connection lifecycle
- schema and metadata caching
- portable broker-mode MCP exposure
- observability of its own routing and provider actions

Tappet does not own:

- provider authentication or identity issuance
- authorization, RBAC, ABAC, or production policy
- credential lifecycle or secret storage
- human approval systems
- network policy
- model routing
- planning or general agent loops
- conversation memory
- subagent orchestration
- workflow durability
- arbitrary script execution

External providers remain responsible for enforcing whether an operation is authorized.

The inherited HTTP server has a narrow whole-endpoint access gate:
`internal/server.newAuthMiddleware` checks static bearer tokens from
`mcpProxy.options.authTokens`. Retain that behavior while establishing the
baseline, but do not treat it as capability authorization, provider
authentication, credential management, or an identity system. The capability
core must not depend on it. Any later removal or replacement requires an
explicit compatibility decision and migration guidance for HTTP deployments.

## Core invariants

- Installed does not mean loaded.
- Discovered does not mean materialized.
- Materialized does not mean authorized.
- Visible does not mean executable.
- Provider process lifetime is not MCP protocol session state.
- Hiding a tool or skill is never an authorization boundary.
- A generic MCP broker can prevent irrelevant content from entering context, but it cannot erase content already transmitted to a model.
- True context withdrawal requires a harness adapter, context reconstruction, compaction, or a fresh/subagent context.
- Capability identity is independent of MCP server identity.
- Cached metadata is derived data, not the source of truth.
- Exact capability or operation identifiers must not be lost behind ranked search results.

## Initial delivery mode

The portable baseline is a stable broker surface with a fixed, small set of MCP tools. Do not make dynamic `tools/list` mutation a V1 dependency. Client support for live tool-list refresh is inconsistent.

Native dynamic exposure may be added later through harness-specific adapters. The core registry, resolver, materializer, and provider interfaces must not depend on it.

## Capability model

Use these terms consistently:

- **Capability:** a coherent class of work exposed to discovery.
- **Capability card:** compact discovery metadata; never a full schema or skill body.
- **Skill:** procedural knowledge in the open Agent Skills format.
- **Operation:** a logical action offered by a capability.
- **Tool:** a provider-specific executable implementation of an operation.
- **Context reference:** selectively readable supporting information; not conversation memory.
- **Provider:** the mechanism that implements operations. MCP is the only required V1 provider.
- **Projection:** the materialized subset of capability information prepared for a particular consumer.
- **Harness adapter:** optional integration that injects or withdraws projections using native harness APIs.

## Engineering rules

- Preserve the existing implementation until a thin replacement path is tested.
- Prefer adapters and small interfaces over a greenfield rewrite.
- Keep the core deterministic. An LLM must not be required for routing, validation, lifecycle, or execution.
- Start capability matching with exact matching plus deterministic lexical ranking. Treat embeddings as an optional later index.
- Use Agent Skills directly rather than inventing a new skill format.
- Keep provider configuration separate from capability packages so packages do not contain credentials.
- Keep provider authorization errors intact and observable.
- Preserve structured MCP content and schemas rather than flattening everything into text.
- Avoid hidden global state. If cross-call application state is introduced, use an explicit handle and document its lifetime.
- Distinguish verified source behavior, accepted decisions, proposals, and unresolved questions.
- Pin external behavior claims to a source, version, or commit in `docs/PRIOR_ART.md`.

## Scope discipline

A change belongs in the initial Tappet core only when it is necessary to:

1. discover a capability,
2. materialize only the needed skills, references, and operation metadata,
3. invoke an operation through a provider,
4. manage the provider lifecycle, or
5. observe and test those actions.

Everything else requires a separate architecture decision and should normally be deferred.

## Testing expectations

At minimum, run:

```bash
go test ./...
go test -race ./...
go vet ./...
```

Before claiming current MCP compliance, run the official MCP conformance suite for every supported protocol revision and transport.

New work should add tests for:

- compact initial tool surface
- exact-match discovery
- ranked natural-language discovery
- hierarchy filtering
- skill metadata versus body loading
- provider lazy start and idle shutdown
- metadata cache invalidation
- downstream error preservation
- concurrent calls to the same and different providers
- legacy and modern MCP negotiation
- clients that do not refresh dynamic tool lists
- bounded and observable output

Do not enable skipped legacy tests blindly. First determine whether they still describe desired behavior.

## Durable versus task-specific information

Keep in the repository:

- architecture boundaries and invariants
- terminology
- capability package format
- accepted decisions
- source-backed prior-art findings
- roadmap milestones and exit criteria
- testing and benchmark methodology

Keep in the initiating prompt or issue:

- the current branch and immediate deliverable
- the selected implementation phase
- temporary time boxes
- current model or harness
- one-off experiments
- temporary assumptions
- requested reporting format
- work that should stop at analysis rather than implementation

## Definition of a successful V1

An unmodified MCP-capable harness can connect to Tappet and initially receive a small fixed broker surface regardless of the number of installed capabilities. It can find a relevant capability, load only the needed skill or reference material, inspect only the relevant operation schema, invoke the operation through a lazily managed MCP provider, and receive a faithful typed result.

V1 does not require native dynamic tool binding, literal deletion of prior model context, an IAM system, or support for providers other than MCP.
