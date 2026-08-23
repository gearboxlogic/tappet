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

The active `internal/client/lazy_load_test.go` suite and its provider-level `activate_<provider>` implementation were also retired. That internal path was not used by either shipped command, overlapped with the recursive hierarchy broker, and depended on external Serena and Playwright processes. The current `internal/client` package now only adapts downstream transports for `ServerRegistry`; hierarchy lifecycle tests cover the shipped lazy-start path.

## Hermetic integration tests

Routine `go test ./...` runs do not download or execute third-party MCP packages. The structure-generator config test launches the repository's test binary as a local stdio MCP fixture. The downstream SSE lifecycle test uses an in-process MCP server. These tests cover provider environment propagation and request-independent provider lifetime without external installations.

## Same-provider serialization

Tappet retains the inherited per-provider mutex for this baseline. This is a transport compatibility rule, not a provider lifecycle design decision.

At `mcp-go v0.43.2`, `client/transport/stdio.go` protects the response map but calls `stdin.Write` without a write mutex. Concurrent large JSON-RPC writes to one stdio pipe can interleave. Commit `d9288f566e64e841c641b0f0a106a3f7284dcfa0` added Tappet's inherited mutex after such concurrent calls timed out. Active tests pin the resulting policy:

- one provider receives one call at a time;
- different providers can run concurrently;
- a queued call stops waiting when its context is canceled or reaches its deadline, without invoking the provider, and the returned error preserves the context classification.

Milestone 1 should recheck the serialization rule against the selected SDK version and transport implementations. Context-aware admission is retained while serialization remains necessary.

## Failed provider initialization

At `mcp-go v0.43.2`, closing a failed stdio client can block while waiting for its child process. Tappet returns the original start or initialize error and performs that cleanup asynchronously. `TestFailedProviderCleanupDoesNotBlockOtherProviderLoads` holds a failed client's `Close` call open and verifies that another provider can still load.

Provider startup is coordinated per provider rather than under the registry-wide lock. `TestSlowProviderLoadDoesNotBlockOtherProviders` proves that one cold provider does not block another, `TestConcurrentCallsShareOneProviderLoad` proves concurrent requests do not duplicate startup, `TestLiveWaiterRetriesLoadCanceledByInitiatingCaller` gives each live waiter its own acquisition budget, and `TestProviderFinishingAfterRegistryCloseIsClosed` covers the shutdown race.
