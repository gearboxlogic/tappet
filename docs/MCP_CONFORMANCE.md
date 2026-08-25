# MCP conformance

Status: **Milestone 1 recorded matrix**

Recorded: 2026-08-24

Last verified: 2026-08-25

Tappet pins `@modelcontextprotocol/conformance@0.2.0-alpha.11` and runs the
frozen `2025-11-25` and `2026-07-28` requirements in both server and client
roles. Run the same gate locally with:

```bash
./script/test-mcp-conformance.sh
```

The 2026-08-25 verification starts the outward server from
`testdata/capabilities`; the official matrix therefore exercises the
package-backed runtime rather than the compatibility hierarchy.

The CI gate uses exact check-level expected-failure files under
`conformance/`. These are scope declarations, not claims that raw suite output
has no failures. The runner fails when an unexpected check fails or when an
expected failure starts passing.

## Recorded official-suite results

| Role | Revision | Raw result | Applicable protocol behavior | Baseline result |
| --- | --- | ---: | --- | --- |
| outward server | 2026-07-28 | 97 passed, 71 failed | discovery, stateless HTTP metadata, headers, fixed tools list, and capability discipline pass | passed |
| outward server | 2025-11-25 | 36 passed, 25 failed | initialize, ping, logging, and fixed tools list pass | passed |
| downstream client | 2026-07-28 | 53 passed, 63 failed | tool calls, request metadata and version retry, standard and custom headers, invalid-header tool rejection, and schema-reference preservation pass | passed |
| downstream client | 2025-11-25 | 4 passed, 55 failed | initialize and tool calls pass | passed |

Raw totals include scenarios that are not part of Tappet's broker surface,
provider-authentication responsibilities, and post-release or extension checks
that the runner executes without scoring for the selected revision.

Expected outward-server failures require diagnostic tools or optional prompt,
resource, completion, callback, or content fixtures that the fixed two-tool
broker does not expose. Expected downstream-client failures cover provider
OAuth, callback-driven multi-round-trip behavior that Tappet does not
advertise, and optional legacy standalone-GET stream resumption. The modern
client still passes MRTR isolation and default-result checks while rejecting
the callback-dependent rounds.

The 2026-07-28 outward-server result records subscription checks as skipped.
Tappet advertises no subscription-delivered capability, so those checks are not
applicable; direct manual requests to the unadvertised subscription method
return method-not-found promptly.

## Transport matrix

| Direction | Transport | 2026-07-28 | Legacy through 2025-11-25 |
| --- | --- | --- | --- |
| outward | Streamable HTTP | official server baseline passes; no protocol sessions | official server baseline passes |
| outward | stdio | modern stateless requests pass integration fixtures | initialize fallback passes integration fixtures |
| downstream | Streamable HTTP | official client metadata, headers, discovery, list, and call checks pass | initialize and call checks pass; optional GET resumption is baselined |
| downstream | stdio | discovery, calls, typed results, event bounds, and callback rejection pass integration fixtures | initialize fallback and calls pass integration fixtures |
| downstream | legacy HTTP+SSE | not a modern transport | configuration and regression tests pass |

The official server runner accepts an HTTP URL, so stdio remains covered by
repository integration tests. Provider authentication is deliberately absent:
Tappet passes configured credentials through but does not issue identities,
run OAuth flows, or become a credential store.

## Adapter boundaries proven by repository tests

- Every downstream frame or SSE event is limited to 16 MiB before SDK decode.
- JSON nesting is limited to 128 levels and syntax nodes to 1,048,576.
- Duplicate JSON object members fail before lossy map decoding.
- Provider event admission is limited to 256 queued messages, 16 MiB, and 16
  active handlers; overflow closes the affected connection.
- Unsupported provider callbacks are rejected promptly without advertising
  sampling, elicitation, or roots.
- Structured content, output schemas, `resultType`, `isError`, and JSON-RPC
  error code, message, and raw data remain typed and observable.
