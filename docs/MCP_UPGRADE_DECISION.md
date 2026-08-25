# MCP SDK upgrade target

Status: **adopted for Milestone 1**

Decision date: 2026-08-19

## Decision

Adopt `github.com/mark3labs/mcp-go v1.0.0-beta.1` with Go 1.25.5 for
Milestone 1. Keep narrow Tappet adapters around the SDK where protocol fidelity
or resource bounds require behavior the beta does not provide directly.

`v1.0.0-beta.1` is the first and, as of the decision date, latest tag containing commit [`42009d4`](https://github.com/mark3labs/mcp-go/commit/42009d4ed77d6b209cf895871576153129f9dff5). The tag points to `56af04b2a9aedefa7756d9eef77065e4275a7ae6`; `v0.58.0` points to the modern-support commit's parent. The [release notes](https://github.com/mark3labs/mcp-go/releases/tag/v1.0.0-beta.1) identify MCP 2026-07-28 support as the release change. Although GitHub does not mark the release as a prerelease, the SemVer tag is a beta and should be treated that way.

The dependency remains a beta. Its protocol behavior is therefore pinned by
Tappet integration tests and the official conformance package rather than by
an assumption of SDK stability.

## Minimum Go toolchain gate

The selected tag's [`go.mod`](https://github.com/mark3labs/mcp-go/blob/v1.0.0-beta.1/go.mod)
declares Go `1.25.5`. Tappet now declares and uses Go 1.25.5 in `go.mod`, CI,
release automation, the Docker builder, and contributor setup.

The coordinated toolchain change is accepted. Returning to Go 1.24 would
require selecting a different MCP SDK target and repeating the protocol matrix.

## Current behavior

Tappet retains the SDK's public `Initialize` entry point in provider and
inventory code, but the wire behavior is negotiated. Modern providers receive
`server/discover` and stateless requests; legacy providers receive the
initialize handshake. The outward server uses the same dependency for stdio,
SSE, and Streamable HTTP.

## Negotiation in the target

The [`42009d4` implementation](https://github.com/mark3labs/mcp-go/commit/42009d4ed77d6b209cf895871576153129f9dff5) keeps the public `Initialize` call but changes its wire behavior:

- A modern client probes `server/discover`. On success it sends 2026-07-28 client identity, capabilities, and protocol metadata with each request. It does not send the legacy initialize handshake or use `Mcp-Session-Id`.
- If discovery is unsupported, the client falls back to the legacy initialize handshake and caps legacy negotiation at `2025-11-25`.
- A modern server classifies each request by protocol metadata while retaining the legacy session path for older clients.
- Streamable HTTP modern requests are stateless. Legacy GET streams, session IDs, and resumability remain on the legacy path.

The [MCP 2026-07-28 announcement](https://blog.modelcontextprotocol.io/posts/2026-07-28/) and [specification repository](https://github.com/modelcontextprotocol/modelcontextprotocol) are the protocol authority.

## Expected Tappet impact

Existing `Initialize` call sites should remain source-compatible, but their tests must stop assuming that the method always sends an initialize request. The upgrade review must cover:

- outward stdio and Streamable HTTP negotiation with modern and legacy clients;
- downstream modern discovery and legacy fallback;
- absence of modern HTTP session IDs, GET streams, and DELETE shutdown calls;
- request metadata and standard MCP headers;
- structured content, output schemas, `isError`, and JSON-RPC error propagation;
- same-provider and cross-provider concurrency;
- timeout, cancellation, and provider shutdown;
- deterministic list ordering and cache metadata;
- multi round-trip requests, or an explicit unsupported result where Tappet has no handler.

## Conformance plan and caveats

Use the [official MCP conformance repository](https://github.com/modelcontextprotocol/conformance) and pin its action or package version. Run client and server roles separately.

1. Run the frozen `2026-07-28` requirements against Tappet's outward Streamable HTTP server.
2. Run applicable legacy requirements, including `2025-11-25`, because success on one wire version says nothing about the other.
3. Run the Tappet downstream client against modern and legacy conformance fixtures.
4. Keep stdio negotiation in Tappet integration tests because the server conformance runner takes an HTTP URL.
5. Record expected failures per check, not per scenario, and fail CI when a baseline becomes stale.

The suite changes over time. `--requirements <revision>` is the frozen release contract; `--suite` and `--spec-version` describe the current suite. Some post-release, extension, or pending checks run without affecting a revision score. `wire-schema-harness-error` identifies invalid traffic from the harness rather than the implementation, and scenarios with no instrumented traffic emit no wire-schema check.

The plan is implemented with
`@modelcontextprotocol/conformance@0.2.0-alpha.11` in
`script/test-mcp-conformance.sh` and CI. Check-level baselines cover only
surfaces outside the fixed broker or accepted boundaries, such as prompts,
resources, provider OAuth, callback-driven MRTR, and optional legacy GET-stream
resumption. A newly passing baseline entry fails the job as stale. See
[MCP conformance](MCP_CONFORMANCE.md) for the recorded matrix.

## SDK choice

Remain on `mcp-go` for Milestone 1. Tappet already uses its client, server, and
transport APIs throughout the codebase, and the beta passed the applicable
modern and legacy matrix once wrapped by narrow adapters.

The measured comparison used official Go SDK `v1.7.0`. It provides first-party
2026-07-28 support and conformance integration, but migration would replace
nearly every MCP client, server, transport, and typed-content call site. The
comparison was made against official SDK tag `v1.7.0`, module sum
`h1:yqjY2dsbKAC0LSuWZVBMrHgiG8ukXv6NRo0JiALay44=`. In the completed Milestone 1
tree, the current SDK appears in 9 production Go files and 133 SDK-qualified
references. These measurements are reproducible with:

```bash
rg -l 'github.com/mark3labs/mcp-go' --glob='*.go' --glob='!*_test.go'
rg -o 'mcp(client|server)?\.|transport\.' --glob='*.go' --glob='!*_test.go' | wc -l
```

The official SDK supplies modern and legacy stdio, HTTP, and Streamable HTTP
transports, so it clears the protocol-coverage gate. It does not remove
Tappet's required boundaries: pre-decode byte and JSON budgets, event
admission, callback policy, and lossless error capture remain broker-specific.
Replacing 9 production files and 133 call-site references while retaining
those adapters is not a clear correctness or maintenance benefit for this
milestone.

Revisit the official SDK when those adapters can be reduced materially, or
when a stable SDK release removes the beta risk without weakening the tested
boundaries.
