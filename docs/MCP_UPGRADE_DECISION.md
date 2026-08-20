# MCP SDK upgrade target

Status: selected compatibility-test target for Milestone 1; adoption is gated on the minimum Go toolchain decision

Decision date: 2026-08-19

## Decision

Keep `github.com/mark3labs/mcp-go v0.43.2` in Milestone 0. Target `github.com/mark3labs/mcp-go v1.0.0-beta.1` in a separate Milestone 1 pull request.

`v1.0.0-beta.1` is the first and, as of the decision date, latest tag containing commit [`42009d4`](https://github.com/mark3labs/mcp-go/commit/42009d4ed77d6b209cf895871576153129f9dff5). The tag points to `56af04b2a9aedefa7756d9eef77065e4275a7ae6`; `v0.58.0` points to the modern-support commit's parent. The [release notes](https://github.com/mark3labs/mcp-go/releases/tag/v1.0.0-beta.1) identify MCP 2026-07-28 support as the release change. Although GitHub does not mark the release as a prerelease, the SemVer tag is a beta and should be treated that way.

The upgrade is not needed for characterization, rebranding, common server construction, or CI. Including it here would mix protocol behavior changes into a compatibility baseline.

## Minimum Go toolchain gate

The selected tag's [`go.mod`](https://github.com/mark3labs/mcp-go/blob/v1.0.0-beta.1/go.mod) declares Go `1.25.5`. CapScope currently declares Go `1.24.0` as its minimum language version, while CI, release artifacts, and the Docker builder use Go `1.24.13`. Adopting this SDK tag therefore also requires an explicit minimum-toolchain change. It is not only an SDK or protocol compatibility update.

Before accepting the beta dependency for production, Milestone 1 must decide whether to:

- raise CapScope's minimum Go version to at least `1.25.5` and update `go.mod`, CI, the Docker builder, contributor documentation, and supported local environments together; or
- evaluate another SDK target if retaining the Go 1.24 baseline is a requirement.

The `mcp-go` beta remains the first experiment because it is the first tag containing the required modern-protocol commit. It should be adopted only if the coordinated toolchain change is acceptable, the beta passes the modern and legacy conformance matrix below, typed results and errors remain intact, and provider lifecycle behavior remains controllable. A newer stable `mcp-go` tag or the official Go SDK should be preferred if it meets those criteria before Milestone 1 begins.

## Current behavior

CapScope calls `Initialize` explicitly in `internal/hierarchy.newProviderClient`, `internal/client.Client.AddToMCPServer`, and `structure_generator/cmd.fetchToolsFromServer`. With `mcp-go v0.43.2`, those calls use the legacy initialize handshake and protocol-session behavior. The outward server uses the same dependency for stdio, SSE, and Streamable HTTP.

## Negotiation in the target

The [`42009d4` implementation](https://github.com/mark3labs/mcp-go/commit/42009d4ed77d6b209cf895871576153129f9dff5) keeps the public `Initialize` call but changes its wire behavior:

- A modern client probes `server/discover`. On success it sends 2026-07-28 client identity, capabilities, and protocol metadata with each request. It does not send the legacy initialize handshake or use `Mcp-Session-Id`.
- If discovery is unsupported, the client falls back to the legacy initialize handshake and caps legacy negotiation at `2025-11-25`.
- A modern server classifies each request by protocol metadata while retaining the legacy session path for older clients.
- Streamable HTTP modern requests are stateless. Legacy GET streams, session IDs, and resumability remain on the legacy path.

The [MCP 2026-07-28 announcement](https://blog.modelcontextprotocol.io/posts/2026-07-28/) and [specification repository](https://github.com/modelcontextprotocol/modelcontextprotocol) are the protocol authority.

## Expected CapScope impact

Existing `Initialize` call sites should remain source-compatible, but their tests must stop assuming that the method always sends an initialize request. The upgrade review must cover:

- outward stdio and Streamable HTTP negotiation with modern and legacy clients;
- downstream modern discovery and legacy fallback;
- absence of modern HTTP session IDs, GET streams, and DELETE shutdown calls;
- request metadata and standard MCP headers;
- structured content, output schemas, `isError`, and JSON-RPC error propagation;
- same-provider and cross-provider concurrency;
- timeout, cancellation, and provider shutdown;
- deterministic list ordering and cache metadata;
- multi round-trip requests, or an explicit unsupported result where CapScope has no handler.

## Conformance plan and caveats

Use the [official MCP conformance repository](https://github.com/modelcontextprotocol/conformance) and pin its action or package version. Run client and server roles separately.

1. Run the frozen `2026-07-28` requirements against CapScope's outward Streamable HTTP server.
2. Run applicable legacy requirements, including `2025-11-25`, because success on one wire version says nothing about the other.
3. Run the CapScope downstream client against modern and legacy conformance fixtures.
4. Keep stdio negotiation in CapScope integration tests because the server conformance runner takes an HTTP URL.
5. Record expected failures per check, not per scenario, and fail CI when a baseline becomes stale.

The suite changes over time. `--requirements <revision>` is the frozen release contract; `--suite` and `--spec-version` describe the current suite. Some post-release, extension, or pending checks run without affecting a revision score. `wire-schema-harness-error` identifies invalid traffic from the harness rather than the implementation, and scenarios with no instrumented traffic emit no wire-schema check.

## SDK choice

Remain on `mcp-go` for the first experiment because CapScope already uses its client, server, and transport APIs throughout the codebase, and the target commit includes a modern/legacy compatibility matrix. This makes the protocol upgrade smaller than an SDK migration.

Evaluate the official Go SDK if the beta target fails required conformance checks, cannot preserve typed results and errors, or makes provider lifecycle control difficult. The official SDK's [`v1.7.0` release](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0) implements 2026-07-28 and integrates official client and server conformance tests, but migration would touch nearly every MCP call site.

Milestone 1 should upgrade `mcp-go` in isolation, add dual-era fixtures, run conformance by protocol revision and transport, and decide whether the beta is acceptable before capability-package work begins.
