# Milestone 0 completion audit

Status: **complete as of 2026-08-24**

Verified: **2026-08-25**

This audit maps every Milestone 0 roadmap commitment to durable evidence. The
merged implementation is [PR #2](https://github.com/gearboxlogic/tappet/pull/2),
whose `verify` job passed before merge.

## Work items

| Roadmap commitment | Evidence |
| --- | --- |
| Add architecture and agent guidance | `AGENTS.md`, [Architecture](ARCHITECTURE.md), [Capability package format](CAPABILITY_PACKAGE.md), [Prior-art assessment](PRIOR_ART.md), and [Roadmap](ROADMAP.md) define the accepted boundaries, current implementation, terms, and staged plan. |
| Inventory current behavior with characterization tests | `TestNewTappetServerOutwardContract` pins the two-tool broker. `TestHierarchyTraversalAndResolution`, `TestProviderLifecycleIsLazyAndReusable`, the provider concurrency tests, and `TestDownstreamErrorsAndStructuredResultsArePreserved` cover hierarchy resolution, lazy lifecycle, concurrency, and result fidelity. Generator tests cover the checked-in hierarchy format and safe replacement. |
| Restore or retire `.skip` tests deliberately | [Baseline test notes](TESTING.md) records the disposition and replacement for every former suite. No `.skip` file remains, and CI rejects any tracked `.skip` file. |
| Rename the repository to Tappet | `go.mod` declares `github.com/gearboxlogic/tappet`; commands, release archives, container configuration, examples, imports, and current user documentation use Tappet names. Remaining Lazy MCP references identify the inherited source or describe the historical baseline. |
| Preserve upstream attribution and license notices | [README](../README.md) identifies `voicetreelab/lazy-mcp`; [LICENSE](../LICENSE) retains the original MIT notice; [Prior-art assessment](PRIOR_ART.md) records source commit `44abd4d468a4e7ae99380c4ecb43ff4e64f2d0d2` and its matching tree. |
| Add CI for the baseline checks | `.github/workflows/ci.yml` runs formatting, disabled-suite rejection, tests, race detection, vet, a GoReleaser snapshot, and a live container smoke test. PR #2's hosted `verify` check passed. |
| Record tool and construction benchmarks | [Milestone 0 benchmark baseline](BASELINE_BENCHMARKS.md) records the host, Go version, command, five-run medians, allocations, encoded size, and fixed two-tool count. `BenchmarkNewTappetServer` and `BenchmarkOutwardToolSurfaceEncoding` remain executable. |
| Retain grandfathered credentials without publishing them | `TestProviderCredentialsAreExcludedFromLogsAndGeneratedHierarchy` proves argument, environment, URL, and header values reach provider setup but never enter generator logs or hierarchy files. `TestOutwardAuthCredentialIsExcludedFromRequestLogs` covers the outward bearer token. The transport-specific tests listed in [Baseline test notes](TESTING.md) cover stdio, SSE, Streamable HTTP, and remote-configuration headers. |

## Exit criteria

| Exit criterion | Result |
| --- | --- |
| All inherited behavior is covered or intentionally removed | The active characterization suites cover the shipped broker, hierarchy, generator, provider lifecycle, transport adapters, concurrency rules, and error preservation. [Baseline test notes](TESTING.md) records the abandoned provider activation path and every retired test suite. |
| Repository naming is internally consistent | The module and shipped artifacts use Tappet. Searches for old project names return only explicit source attribution, prior-art discussion, or the roadmap's historical wording. |
| CI is green | PR #2 merged after its `verify` job passed. The required local test, race, vet, build, formatting, and container checks are recorded in the pull request. |
| Planned work is not described as implemented | `AGENTS.md` lists the verified implementation and names unimplemented skills, capability manifests, search, idle shutdown, and adapters. Architecture proposals carry proposal or accepted-direction status rather than implementation claims. |
| Credential-bearing compatibility inputs remain usable and excluded from logs and generated data | The regression tests cover outward bearer tokens, stdio environment and arguments, SSE and Streamable HTTP headers, provider URLs, and remote-configuration headers. `TestLoadSendsConfiguredHTTPHeadersWithoutLoggingCredentials` covers remote-fetch logging directly. The generated hierarchy model contains server and tool metadata only. |

The current repository gate is:

```bash
go test ./...
go test -race ./...
go vet ./...
```
