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

## Credential compatibility and exclusion

The Milestone 0 compatibility boundary accepts existing literal credentials
without copying them into generated hierarchy data or request logs. These tests
pin both halves of that contract:

- `TestProviderCredentialsAreExcludedFromLogsAndGeneratedHierarchy` passes
  credentials through provider arguments, environment variables, URLs, and
  headers. It then scans captured generator logs and every generated hierarchy
  file for each exact value.
- `TestOutwardAuthCredentialIsExcludedFromRequestLogs` sends a valid outward
  bearer token through the access middleware while request logging is enabled
  and proves the token is absent from the log.
- `TestFetchFromConfigPassesStdioEnvironment`,
  `TestFetchFromConfigSupportsStreamableHTTPHeaders`, and
  `TestSSEProviderReceivesConfiguredHeadersAndOutlivesTriggeringRequest` prove
  that exclusion does not break provider credential propagation.
- `TestLoadSendsConfiguredHTTPHeadersWithoutLoggingCredentials` proves
  remote-configuration request headers remain compatible and absent from logs.

The values remain compatibility inputs only. Tappet does not persist them in
generated metadata or manage their lifecycle.

## Same-provider serialization

Tappet retains the inherited per-provider mutex for this baseline. This is a transport compatibility rule, not a provider lifecycle design decision.

The inherited broker serializes calls to one provider while allowing different
providers to run concurrently. This remains an explicit provider policy after
the `mcp-go v1.0.0-beta.1` transport added its own write synchronization.
Commit `d9288f566e64e841c641b0f0a106a3f7284dcfa0` introduced the original guard
after concurrent stdio calls timed out. Active tests pin the resulting policy:

- one provider receives one call at a time;
- different providers can run concurrently;
- a queued call stops waiting when its context is canceled or reaches its deadline, without invoking the provider, and the returned error preserves the context classification.

Milestone 1 rechecked the rule against the selected SDK and retained
context-aware admission as an explicit provider policy.

## Failed provider initialization

Ordinary SDK close can block while waiting for a failed stdio child. Tappet's
failure-specific close path kills and reaps that child under a finite cleanup
context before returning the original start or negotiation error. Cleanup
occurs outside the registry lock:
`TestFailedProviderCleanupDoesNotBlockOtherProviderLoads` holds one provider's
context-aware cleanup open while another provider loads,
`TestFailedProviderCleanupHonorsDeadline` covers the cleanup deadline, and
`TestFailedStdioCleanupKillsAndReapsProvider` exercises the production stdio
process path.

Provider startup is coordinated per provider rather than under the registry-wide lock. `TestSlowProviderLoadDoesNotBlockOtherProviders` proves that one cold provider does not block another, `TestConcurrentCallsShareOneProviderLoad` proves concurrent requests do not duplicate startup, `TestLiveWaiterRetriesLoadCanceledByInitiatingCaller` gives each live waiter its own acquisition budget, and `TestProviderFinishingAfterRegistryCloseIsClosed` covers the shutdown race.
