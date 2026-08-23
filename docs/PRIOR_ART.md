# Prior-Art Assessment

Status: **research record, not an endorsement of every project design**

Research date: **2026-08-23**

This document records source-backed mechanisms that CapScope can reuse or learn from. Links point to authoritative project repositories, specifications, or tracked source behavior.

## 1. Current CapScope repository

Repository snapshot:

- base commit: `44abd4d468a4e7ae99380c4ecb43ff4e64f2d0d2`
- tree: `2a57084976e30c0ae30955104c38fe88915cafdd`
- upstream equivalent: `voicetreelab/lazy-mcp` at the same commit

Observed source behavior:

- `internal/server/server.go` exposes two meta-tools
- `internal/hierarchy/hierarchy.go` loads a filesystem-backed JSON hierarchy
- tool execution resolves `server` plus `maps_to`
- `ServerRegistry` lazily starts providers and holds them until shutdown
- same-provider calls are serialized with a mutex
- downstream clients use an initialize handshake
- `structure_generator` inventories MCP tools and creates hierarchy files
- `go.mod` still declares `github.com/voicetreelab/lazy-mcp`
- several test files remain disabled with `.skip`

Assessment:

The repository is a useful seed for broker-mode invocation and lazy process startup. It is not yet a capability runtime. Extend it incrementally, but first simplify overlapping legacy lazy-loading paths and add characterization tests.

## 2. Lazy MCP

Source:

- https://github.com/voicetreelab/lazy-mcp
- inspected commit: `44abd4d468a4e7ae99380c4ecb43ff4e64f2d0d2`
- license: MIT

Useful mechanisms:

- very small outward tool surface
- hierarchical browsing
- downstream tool-name mapping
- provider startup on first execution
- schema included in error diagnostics
- per-provider serialization
- practical recognition that a broker obscures native per-tool permission dialogs

Keep:

- stable broker compatibility
- compact hierarchy cards
- explicit mappings
- lazy provider start
- useful schema diagnostics

Change:

- hierarchy nodes must become capability packages rather than server-shaped categories
- add exact and lexical search
- add Agent Skills
- add metadata caching and idle lifecycle
- separate provider lifecycle from protocol state
- preserve structured results
- modernize MCP
- avoid turning permission hooks into a CapScope authorization system

## 3. Pi MCP Adapter

Source:

- https://github.com/nicobailon/pi-mcp-adapter
- inspected tree: `8eabb29c5d7a343e4a67bdec8dd624b804479a03`
- license: MIT

Useful mechanisms:

- one stable proxy tool for search, describe, and invocation
- disk-cached tool metadata so discovery works while providers are stopped
- lazy connection by default
- configurable idle disconnect
- `lazy`, `eager`, `keep-alive`, and `lazy-keep-alive` modes
- selected `directTools` promotion for native harness exposure
- include/exclude filters applied consistently
- output guard that spills oversized text rather than injecting it all
- metadata-only tracing
- clear separation between shared upstream process ownership and adapter-owned client sockets

Keep:

- metadata cache independent of connection state
- lifecycle modes
- idle timeout and reconnect
- bounded output
- optional direct-tool adapter path
- provider-side argument validation
- machine-readable runtime status

Do not copy as core:

- Pi-specific UI, commands, OAuth UX, or event bus
- one-session-per-process assumptions
- harness-specific direct tool registration

CapScope should implement the reusable lifecycle and cache contracts in Go and expose Pi-like behavior through adapters later.

## 4. PMCP

Source:

- https://github.com/Consiliency/pmcp
- inspected tree: `50a7f5ce7e8dfc3e7b62dc249e9a83741b8838cb`
- license: MIT

Useful mechanisms:

- compact capability cards followed by detailed description
- stable gateway API
- deterministic built-in capability matching
- manifest-backed server provisioning
- shared HTTP gateway mode
- lifecycle diagnostics
- bounded outputs and optional redaction
- rich typed result models
- explicit modern/legacy MCP compatibility work
- strong operational documentation

Keep:

- capability-card terminology
- search -> describe -> invoke progression
- typed gateway results
- explicit lifecycle status
- versioned compatibility matrix
- refresh safety while work is active

Do not copy into V1:

- 26-tool administration surface
- server registry provisioning
- installer and update management
- task gateway
- identity service features
- broad policy engine
- multi-tenant service scope

PMCP demonstrates that the gateway can become a platform quickly. CapScope should deliberately remain narrower.

## 5. `mcp-shark/lazy-tool`

Source:

- https://github.com/mcp-shark/lazy-tool
- inspected commit: `06cc200e85dd916dff7c0d789b7f0fca56192ab3`
- license: MIT

Useful mechanisms:

- local SQLite catalog
- lexical search with optional vector search
- separate search, inspect, invoke, prompt, and resource tools
- direct, search, and hybrid modes
- explicit benchmark methodology
- circuit breaking, session reuse, tracing, and response caching
- auto-import of MCP provider configuration

Keep:

- search/inspect separation
- explainable score output
- local deterministic search baseline
- benchmark corpus and mode comparison
- direct/search/hybrid as deployment choices
- bounded reliability behavior around providers

Defer:

- vector search until lexical retrieval is benchmarked
- response caching until operation idempotency semantics are clear
- auto-import until the package/provider model is stable

## 6. IONOS Cloud MCP

Source:

- https://github.com/ionos-cloud/ionoscloud-mcp
- official project documentation in its README

Useful mechanisms:

- explicit `eager`, `lazy`, and `dynamic` loading modes
- two sentinel tools plus `tools/list_changed` in lazy mode
- three fixed meta-tools in dynamic mode
- documented client incompatibility with live tool-list refresh
- recommendation to select exposure mode based on client capability
- server-side enforcement independent of tool visibility

The key lesson is architectural:

```text
dynamic native tool lists are an optimization
stable broker tools are the portability baseline
```

CapScope should not depend on live tool-list changes for V1.

## 7. CrowdStrike Falcon MCP and other dynamic-mode servers

Source:

- https://github.com/CrowdStrike/falcon-mcp/blob/c7e1db203f2e48099a7d7d9afa7b3af9b01998ea/falcon_mcp/server.py
- https://github.com/CrowdStrike/falcon-mcp/blob/c7e1db203f2e48099a7d7d9afa7b3af9b01998ea/README.md
- inspected revision `c7e1db203f2e48099a7d7d9afa7b3af9b01998ea`
  (`falcon_mcp/server.py` and `README.md`, 2026-08-22 release 0.17.0)

Useful mechanism:

- dynamic mode's fixed `falcon_list_enabled_tools`, `falcon_search_tools`, and
  `falcon_execute_tool` dispatcher for large catalogs

This independently validates the stable dispatcher pattern. It does not add a skill or capability package model.

## 8. DeerFlow

Source:

- https://github.com/bytedance/deer-flow
- inspected commit/file set around `b47c7838a57732c598ade701d14d175ee5adc518`

Useful mechanisms:

- standard Agent Skills packages
- enabled-skill projection into an isolated sandbox filesystem
- per-agent skill allowlists
- separation of source skill trees from materialized views
- manifest/digest-based projection rebuild
- context injection controlled by the harness
- subagents with scoped contexts

Keep:

- source versus projection separation
- immutable/copy-based materialized views
- skill digests and rebuild detection
- adapter-owned injection
- per-consumer projection concept

Do not copy:

- the full LangGraph harness
- subagents, memory, planning, dependency installation, or autonomous skill evolution
- the assumption that CapScope itself owns the system prompt

DeerFlow supports the conclusion that true skill withdrawal belongs in a harness adapter, not a generic MCP broker.

## 9. Agent Skills

Authoritative sources:

- https://agentskills.io
- https://github.com/agentskills/agentskills
- inspected specification blob `d9a2db099d905da8b879a5c6f996728073985279`

The format already defines the progressive pattern CapScope needs:

1. metadata loaded for discovery
2. complete `SKILL.md` loaded on activation
3. references, scripts, and assets loaded only as needed

Adopt the format rather than creating CapScope-specific skill Markdown.

Important constraints:

- required `name` and `description`
- package directory name matches skill name
- bounded metadata
- recommended `SKILL.md` size
- one-level references
- official validator
- experimental `allowed-tools` is not an authorization contract

## 10. MCP 2026-07-28

Authoritative sources:

- https://blog.modelcontextprotocol.io/posts/2026-07-28/
- https://github.com/modelcontextprotocol/modelcontextprotocol
- https://github.com/modelcontextprotocol/conformance
- https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0

Relevant changes:

- stateless protocol core
- per-request protocol/client metadata
- `server/discover`
- no modern initialize handshake or protocol session ID
- method/name HTTP headers
- Multi Round-Trip Requests
- deterministic cacheable list results
- modern subscription mechanism
- formal extensions
- JSON Schema 2020-12 expectations
- official versioned conformance suites

CapScope implications:

- fixed outward broker tools fit stateless request handling when responses remain inline; process-local spill handles require additional deployment constraints
- hidden client-to-capability session mapping should be avoided
- any later activation state needs an explicit application handle
- provider process reuse remains valid
- list and catalog ordering must be deterministic
- internal cache scope defaults should be conservative
- conformance belongs in CI

## 11. Go MCP SDK options

### Current dependency: `mark3labs/mcp-go`

Current CapScope version: `v0.43.2`.

The project merged 2026-07-28 support in commit:

- https://github.com/mark3labs/mcp-go/commit/42009d4ed77d6b209cf895871576153129f9dff5

That implementation includes modern/legacy negotiation, stateless HTTP behavior, result typing, cache hints, MRTR, headers, and compatibility tests.

Recommendation:

- first test an official tagged release containing that commit
- avoid a full SDK migration unless compatibility or maintenance evidence justifies it
- add conformance tests before claiming compliance

### Official Go SDK

The official SDK released 2026-07-28 support in `v1.7.0`.

It is a credible fallback and comparison target, but switching SDKs would touch most client/server code. Compare API fit, conformance, lifecycle control, and migration cost in a focused experiment.

## 12. Client behavior and dynamic tool lists

Evidence from client issue trackers and provider documentation shows that live
tool-list refresh is not consistently handled across clients, versions, modes,
and transports. These are compatibility reports, not claims about every build:

- OpenAI Codex issue
  [#33266](https://github.com/openai/codex/issues/33266) reports that Codex
  CLI/core `0.144.1` over stdio did not refetch `tools/list` after
  `notifications/tools/list_changed`. The issue was open when inspected on
  2026-08-23.
- OpenAI Codex issue
  [#35583](https://github.com/openai/codex/issues/35583) reports the same failure
  in Microsoft Store desktop build `26.721.4979.0`, with Codex CLI `0.145.0`,
  on Windows 11 over stdio. The issue was open when inspected on 2026-08-23.
- Claude Code issue
  [#62844](https://github.com/anthropics/claude-code/issues/62844) reports that
  Claude Code `2.1.152` did not refresh tools in a persistent headless
  `--print` stream-JSON process. The issue was closed by stale automation on
  2026-08-12 without a documented fix; comments also reported interactive and
  stdio failures, so closure is not evidence of compatibility.
- The IONOS Cloud MCP
  [tool-loading documentation](https://github.com/ionos-cloud/ionoscloud-mcp/blob/89b804b70eb19dc300a251f3d7679c2389916760/README.md#tool-loading-mode)
  names clients that ignore `notifications/tools/list_changed` and provides a
  fixed three-tool dynamic mode that does not require refresh support.

Therefore:

- do not use `tools/list_changed` or its modern equivalent as the only path to capability access
- keep the fixed broker surface fully functional
- test dynamic native exposure per client and version
- version any compatibility matrix by date and client build

## 13. Previously named but unverified project

Earlier discussion named `igrigorik/MCProxy`.

No authoritative repository matching that owner/project and the described MCP behavior was verified during this research. Do not use it as a design dependency or citation unless an exact repository URL is supplied and inspected.

IONOS dynamic mode, Pi MCP Adapter, PMCP, Lazy MCP, and `lazy-tool` provide verified examples for the relevant mechanisms.

## 14. Synthesis

| Concern | Primary inspiration | CapScope decision |
| --- | --- | --- |
| Tiny portable surface | Lazy MCP | fixed broker mode |
| Hierarchy | Lazy MCP | capability paths, no inheritance |
| Search | `lazy-tool`, PMCP | exact-first deterministic lexical baseline |
| Skill format | Agent Skills | adopt directly |
| Skill projection | DeerFlow | harness adapter concern |
| Metadata cache | Pi MCP Adapter | derived disk cache |
| Provider lifecycle | Pi MCP Adapter | lazy plus idle shutdown |
| Dynamic direct tools | Pi, IONOS | optional client adapter |
| Capability cards | PMCP | compact discovery result |
| Modern protocol | MCP spec, Go SDKs | latest outward server plus legacy fallback |
| Authority | provider systems | explicitly outside CapScope core |
| Context deletion | harness/fresh context | never claim in generic broker mode |
