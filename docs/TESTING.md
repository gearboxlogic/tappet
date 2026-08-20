# Baseline test notes

## Retired `.skip` files

Milestone 0 removed every `.skip` file after reviewing its assertions.

| Former file | Disposition | Reason and replacement |
| --- | --- | --- |
| `internal/hierarchy/hierarchy_test.go.skip` | Replace with a better characterization test | It imported an older `TBXark/mcp-proxy` package and expected Serena and Playwright fixtures that are no longer present. `hierarchy_characterization_test.go` now creates its own current hierarchy and covers traversal, missing categories, exact paths, and mapped names. |
| `internal/hierarchy/hierarchy_integration_test.go.skip` | Rewrite to current intended behavior | Its useful lazy-start and concurrency assertions depended on `SERENA_PATH` and obsolete hierarchy data. Active tests now use recording in-process MCP providers and require no external installation. |
| `internal/server/server_test.go.skip` | Replace with a better characterization test | It duplicated the production tool registration and asserted an abandoned `categories` response field. `server_characterization_test.go` tests the shared production constructor and the exact two-tool outward contract. |
| `structure_generator/generator_test.go.skip` | Rewrite to current intended behavior | It asserted a removed `ToolNode.Categories` field. The active generator tests cover the current directory-backed branch format, leaf mappings, manual overview preservation, moved tools, and invalid output paths. |

The active root-level `recursive_lazy_load_test.go` was also retired. It was not disabled, but it expected the same removed Serena and Playwright fixture while the repository ships the generated `everything` hierarchy. Its supported assertions now live in the package-level characterization tests.

## External integration tests

Routine `go test ./...` runs do not download or execute third-party MCP packages. `TestLazyLoadingPlaywright` runs only when `CAPSCOPE_PLAYWRIGHT_INTEGRATION=1` is set and invokes the exact package version `@playwright/mcp@0.0.79`. This test remains outside the hermetic CI baseline because it requires Node.js, npm registry access, and a browser-capable environment.

## Same-provider serialization

CapScope retains the inherited per-provider mutex for this baseline. This is a transport compatibility rule, not a provider lifecycle design decision.

At `mcp-go v0.43.2`, `client/transport/stdio.go` protects the response map but calls `stdin.Write` without a write mutex. Concurrent large JSON-RPC writes to one stdio pipe can interleave. Commit `d9288f566e64e841c641b0f0a106a3f7284dcfa0` added CapScope's inherited mutex after such concurrent calls timed out. Active tests pin the resulting policy:

- one provider receives one call at a time;
- different providers can run concurrently;
- a canceled waiter remains blocked until the plain mutex is released, then returns an error that preserves `context.DeadlineExceeded`.

Milestone 1 should recheck this rule against the selected SDK version and transport implementations. It should replace the plain mutex with context-aware admission only as a separately tested behavior change.

## Failed provider initialization

At `mcp-go v0.43.2`, closing a failed stdio client can block while waiting for its child process. CapScope returns the original start or initialize error and performs that cleanup asynchronously. `TestFailedProviderCleanupDoesNotBlockOtherProviderLoads` holds a failed client's `Close` call open and verifies that another provider can still load.
