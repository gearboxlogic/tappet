# Milestone 1 completion audit

Status: **complete as of 2026-08-24**

This audit maps every Milestone 1 roadmap commitment to executable evidence.
Official-suite totals and expected-failure scope are recorded separately in
[MCP conformance](MCP_CONFORMANCE.md).
The [manual acceptance record](MILESTONE_1_MANUAL_ACCEPTANCE.md) passed on
2026-08-25 after detecting and repairing demo-provider version drift.

## Work items

| Roadmap commitment | Evidence |
| --- | --- |
| Tagged SDK with 2026-07-28 support | `go.mod` pins `github.com/mark3labs/mcp-go v1.0.0-beta.1`; provenance and tag comparison are recorded in [MCP SDK upgrade target](MCP_UPGRADE_DECISION.md). |
| Outward `server/discover` | `TestTappetStreamableHTTPNegotiatesModernStatelessProtocol` and the official 2026-07-28 server baseline. |
| Modern stateless HTTP | `TestTappetStreamableHTTPNegotiatesModernStatelessProtocol` verifies 2026-07-28 and absence of an HTTP session ID; the official modern server baseline verifies request metadata and headers. |
| Modern stdio requests | `TestTappetStdioNegotiatesModernAndLegacyProtocols/modern`. |
| Legacy negotiation where practical | `TestTappetStreamableHTTPRetainsLegacyNegotiation`, `TestTappetStdioNegotiatesModernAndLegacyProtocols/legacy`, and `TestProviderClientNegotiatesModernAndLegacyStreamableHTTP`. |
| Preserve structured content, output schemas, result types, and errors | `TestProviderClientPreservesTypedToolResult`, `TestDownstreamErrorsAndStructuredResultsArePreserved`, `TestFetchFromConfigPreservesCompletePagedToolMetadata`, and `TestResponseValidatingTransportPreservesToolRPCError`. |
| Do not advertise unsupported callbacks | `TestResponseValidatingTransportDoesNotAdvertiseUnsupportedCallbacks` and provider initialization assertions in `TestProviderClientNegotiatesModernAndLegacyStreamableHTTP`. |
| Reject unsolicited callbacks promptly and observably | `TestStdioProviderRejectsUnsolicitedCallbackPromptly` proves an actual stdio callback receives method-not-found and the invocation continues; `TestProviderClientRejectsUnsupportedMultiRoundTripInput` proves deadline-bounded HTTP behavior. |
| Pre-decode byte, nesting, and node budgets | `TestLimitedStdioTransportRejectsOversizeBeforeDecode`, `TestResponseLimitedRoundTripperRejectsUndeclaredOversizeBeforeRelease`, `TestRejectDuplicateJSONMembersEnforcesDepthLimit`, and `TestRejectJSONMembersEnforcesNodeLimit`. |
| Bound provider events | `TestProviderEventQueueOverflowIsBoundedAndClosesConnection` proves the 256-message and 16-handler limits; `TestProviderEventQueueByteOverflowClosesConnection` proves the 16 MiB aggregate limit. Unsupported callbacks are rejected by the transport and do not enter an application callback queue. |
| Official conformance in CI | `.github/workflows/ci.yml` runs `script/test-mcp-conformance.sh` for both roles and both revisions with check-level baselines. |
| Dual-era downstream fixtures | The HTTP and stdio negotiation tests above exercise both 2026-07-28 discovery and 2025-11-25 initialize fallback. |

## Decision gate

The measured comparison in [MCP SDK upgrade target](MCP_UPGRADE_DECISION.md)
uses official SDK `v1.7.0` and records the current migration surface: 9
production Go files and 133 SDK-qualified references. Both SDKs cover the
needed protocols, while Tappet's pre-decode, event, callback, and error-fidelity
adapters remain necessary. The comparison does not show a clear correctness or
maintenance benefit, so Milestone 1 remains on the selected `mcp-go` tag.

## Exit criteria

| Exit criterion | Result |
| --- | --- |
| Conformance documented by protocol and transport | The four official-suite runs and five transport paths are recorded in [MCP conformance](MCP_CONFORMANCE.md). |
| No hidden capability state tied to protocol sessions | The outward surface is the same fixed two-tool server for both revisions. Modern HTTP is stateless and has no session ID; no capability activation or projection state exists in Milestone 1. |
| Unsupported callback-dependent work fails within its deadline | The stdio callback fixture receives method-not-found immediately, and the HTTP fixture completes inside its one-second assertion. |
| Event floods close the affected connection without unbounded buffering or handlers | Count, aggregate-byte, and active-handler tests prove the fixed limits and single close signal. |
| Modern and legacy provider fixtures pass | HTTP and stdio dual-era fixtures run in `go test ./...`; both official client revisions run in the conformance gate. |

The required repository gate is `go test ./...`, `go test -race ./...`,
`go vet ./...`, and `./script/test-mcp-conformance.sh`.
