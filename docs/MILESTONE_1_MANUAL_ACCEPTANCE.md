# Milestone 1 manual acceptance

Status: **passed on 2026-08-25**

This checklist verifies the Milestone 1 runtime behavior from outside the Go
packages. It complements unit tests and the official MCP conformance suite. It
does not replace exact automated checks for byte limits, JSON structure limits,
race detection, or queue capacity.

Run the checklist from the repository root on Linux with Go 1.25.5, Node.js,
`curl`, `jq`, and a working container engine. Record the tool versions and the
commit under test before starting.

## Acceptance checklist

| ID | Check | Pass condition |
| --- | --- | --- |
| M1-MAN-01 | Build and identify Tappet | The binary builds from the pinned module graph and reports the expected build identity. |
| M1-MAN-02 | Modern HTTP discovery | `server/discover` returns 2026-07-28 in `supportedVersions`, identifies Tappet, advertises tools, and advertises no sampling, elicitation, roots, prompt, resource, or subscription-delivered capability. |
| M1-MAN-03 | Modern HTTP stateless behavior | Discovery and `tools/list` return no `Mcp-Session-Id`; `tools/list` returns only `get_tools_in_category` and `execute_tool`. |
| M1-MAN-04 | Modern HTTP validation and routing | Missing client capabilities returns HTTP 400 and JSON-RPC `-32602`; a protocol-header mismatch returns HTTP 400 and `-32020`; removed, unknown, and unadvertised subscription methods return HTTP 404 and `-32601` with the original request ID. |
| M1-MAN-05 | Legacy HTTP on the same process | A 2025-11-25 initialize request succeeds, and the resulting client can list the same two broker tools. |
| M1-MAN-06 | Modern stdio | A line-delimited modern discovery and tool-list exchange succeeds without an initialize handshake. |
| M1-MAN-07 | Legacy stdio | A 2025-11-25 initialize, initialized notification, and tool-list exchange succeeds. |
| M1-MAN-08 | Real downstream stdio provider | Browsing the checked-in `everything` hierarchy does not start the provider. The first `execute_tool` call starts `@modelcontextprotocol/server-everything`, calls `add`, and returns `42`. A second call in the same Tappet process succeeds. |
| M1-MAN-09 | Structured downstream result | Calling the real provider's `structuredContent` tool preserves text and structured content in Tappet's outward response. |
| M1-MAN-10 | Dual-era downstream Streamable HTTP | The focused process-backed test negotiates 2026-07-28 discovery with a modern provider and 2025-11-25 initialize fallback with a legacy-only provider. |
| M1-MAN-11 | Unsupported callbacks | The stdio callback fixture receives method-not-found and completes its tool call. The HTTP multi-round-trip fixture fails within one second because Tappet has no callback handler. |
| M1-MAN-12 | Pre-decode limits | Focused tests reject oversized stdio and HTTP messages, duplicate members, excessive depth, and excessive syntax nodes before SDK decoding. |
| M1-MAN-13 | Provider event admission | Focused tests close the affected connection after count or aggregate-byte overflow and cap active handlers at 16. |
| M1-MAN-14 | Provider concurrency and cancellation | Focused tests serialize calls to one provider, allow calls to different providers concurrently, cancel queued calls, and avoid duplicate startup. |
| M1-MAN-15 | Official protocol matrix | The pinned conformance package passes its check-level baselines for server and client roles under 2025-11-25 and 2026-07-28. The subscription checks must be recorded as skipped because Tappet advertises no applicable capability. |
| M1-MAN-16 | Container runtime | A freshly built container answers legacy initialize and tool-list requests. It also answers modern discovery and tool-list requests without a session ID. |
| M1-MAN-17 | Repository gate | Formatting, disabled-suite rejection, `go test ./...`, `go test -race ./...`, `go vet ./...`, release snapshot build, and container smoke test pass. |

## HTTP request notes

Modern Streamable HTTP requests use all of these headers:

```text
Content-Type: application/json
Accept: application/json, text/event-stream
Mcp-Protocol-Version: 2026-07-28
Mcp-Method: <JSON-RPC method>
```

Each modern request also carries these `_meta` members:

```json
{
  "io.modelcontextprotocol/protocolVersion": "2026-07-28",
  "io.modelcontextprotocol/clientInfo": {
    "name": "tappet-manual-acceptance",
    "version": "1.0.0"
  },
  "io.modelcontextprotocol/clientCapabilities": {}
}
```

When a request has a named target, use the required `Mcp-Name` header too.
Do not reuse a modern request ID for a second request.

## Evidence rules

For HTTP checks, record the status, response headers, and decoded JSON body.
For stdio checks, retain the complete line-delimited exchange and process exit
status. For focused boundary tests, run the named test with `-count=1 -v` and
record the package result. For conformance, retain the generated `checks.json`
files and confirm the baseline command exits zero.

Do not mark a check passed from source inspection alone. If local host policy
prevents a container or network-backed provider check, record it as blocked
rather than substituting an automated unit test.

## Execution record

Acceptance candidate: commit `d6ddda3`

Host:

- Linux 7.1.4-204.fc44.x86_64, x86_64
- Go 1.25.5
- Node.js 25.9.0 and npm 11.12.1
- curl 8.18.0 and jq 1.8.1
- Podman 5.8.4 with crun

| ID | Result | Recorded evidence |
| --- | --- | --- |
| M1-MAN-01 | Pass | A clean module build completed and the binary reported `manual-m1-d6ddda3`. |
| M1-MAN-02 | Pass | Direct HTTP discovery returned 200, Tappet 0.1.0, 2026-07-28 support, logging and tools capabilities, and no unsupported callback or subscription capability. |
| M1-MAN-03 | Pass | Direct discovery and tool-list requests returned no `Mcp-Session-Id`. The list contained exactly `get_tools_in_category` and `execute_tool`. |
| M1-MAN-04 | Pass | Direct requests produced the required 400/`-32602`, 400/`-32020`, and 404/`-32601` responses. Request IDs were preserved for removed, unknown, and subscription methods. |
| M1-MAN-05 | Pass | The same HTTP process accepted a 2025-11-25 initialize request, an initialized notification, and a tool-list request containing the same two tools. |
| M1-MAN-06 | Pass | A raw line-delimited stdio exchange completed modern discovery and tool listing, then exited zero on EOF. |
| M1-MAN-07 | Pass | A raw line-delimited stdio exchange completed legacy initialize, initialized notification, and tool listing, then exited zero on EOF. |
| M1-MAN-08 | Pass after repair | Hierarchy browsing produced no provider startup. The first real provider call started one client and returned `The sum of 19 and 23 is 42.` A second call returned `The sum of 1 and 2 is 3.` without another startup. |
| M1-MAN-09 | Pass | The real provider returned both text content and typed structured content for Denver, including temperature, conditions, and humidity fields. |
| M1-MAN-10 | Pass | `TestProviderClientNegotiatesModernAndLegacyStreamableHTTP` passed its modern and legacy subtests with `-count=1 -v`. |
| M1-MAN-11 | Pass | `TestStdioProviderRejectsUnsolicitedCallbackPromptly` and `TestProviderClientRejectsUnsupportedMultiRoundTripInput` passed with `-count=1 -v`. |
| M1-MAN-12 | Pass | The focused stdio, HTTP, duplicate-member, depth, and syntax-node limit tests passed with `-count=1 -v` and emitted the expected rejection diagnostics. |
| M1-MAN-13 | Pass | The provider event count-overflow and aggregate-byte-overflow tests passed with `-count=1 -v`; the count test also covers the 16-handler cap. |
| M1-MAN-14 | Pass | The shared startup, same-provider serialization, different-provider concurrency, and queued-call cancellation tests passed with `-count=1 -v`. |
| M1-MAN-15 | Pass | The full pinned conformance script exited zero for both roles and both revisions. Generated modern server results recorded subscription checks as skipped because no subscription capability is advertised. |
| M1-MAN-16 | Pass | A fresh `tappet:manual-m1` image passed the repository legacy smoke script. Direct requests against a separate container passed modern discovery and tool listing with no session ID. |
| M1-MAN-17 | Pass | Formatting, disabled-suite rejection, `go test ./...`, `go test -race ./...`, `go vet ./...`, the container smoke test, and the pinned GoReleaser 2.17.1 snapshot build all completed successfully. |

### Defect found and repaired

The real-provider check initially failed with JSON-RPC `-32602` because the
unversioned `@modelcontextprotocol/server-everything` command resolved to
2026.8.18, whose 13 kebab-case tools no longer matched the checked-in hierarchy
of 10 tools. The fixture is now pinned to 2025.9.25 in every shipped and test
configuration, the hierarchy was regenerated from that version, and the
configuration guide requires the package pin and hierarchy to move together.
The real-provider and structured-result checks passed after the repair.

### Acceptance conclusion

All 17 manual acceptance checks passed. Direct observation covers both outward
transports and protocol eras, real lazy provider startup and reuse, typed result
fidelity, failure routing, conformance, and packaged execution. Exact numeric
limits remain backed by deterministic focused tests because manual timing or
payload inspection cannot prove those boundaries more reliably.
