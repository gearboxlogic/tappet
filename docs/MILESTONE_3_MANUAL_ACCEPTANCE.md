# Milestone 3 manual acceptance

Status: **passed on 2026-08-25**

This checklist validates the internal catalog, projection, and package-artifact
read services delivered in Milestone 3. The public MCP surface is intentionally
still `get_tools_in_category` plus `execute_tool`; registering `tappet.search`,
`tappet.describe`, and `tappet.read` belongs to Milestone 5.

Run focused checks uncached with Go 1.25.5. Use the race detector for concurrent
lease and registry transitions. Since the Phase 3 services are not public MCP
tools yet, direct verification uses their core acceptance harnesses rather than
claiming an end-to-end broker flow that does not exist.

## Acceptance checklist

| ID | Check | Pass condition |
| --- | --- | --- |
| M3-MAN-01 | Frozen search corpus | Corpus integrity and generated exact coverage pass in normal, reversed, and seeded-random install orders. |
| M3-MAN-02 | Retrieval quality | All 14 unambiguous acceptance queries rank the accepted capability first, MRR is 1.0, and all 3 ambiguous judgments pass in every install order. |
| M3-MAN-03 | Compact discovery | Search excludes skill bodies, references, provider fields, paths, digests, and other non-allowlisted data; each result remains a bounded card plus match evidence. |
| M3-MAN-04 | Catalog child pagination | A 205-child hierarchy reconstructs in stable pages, zero-child requests do not emit a non-advancing cursor, and add/reinstall/uninstall stale prior cursors. |
| M3-MAN-05 | Selected immutable describe | Only requested package structure is copied; no provider dependency or artifact-body read exists; repeated and concurrent cursor calls return the same page. |
| M3-MAN-06 | Large describe reconstruction | Maximum accepted item counts and a Loader-backed projection above 512 KiB reconstruct completely while every compact page stays at or below 1 MiB. |
| M3-MAN-07 | Describe lifetime and admission | Idle and absolute expiry, count and byte quotas, random failure/collision, bounded input validation, and shutdown all fail explicitly and release state. |
| M3-MAN-08 | Complete artifact retrieval | Skill documents, listed skill references, context references, and one exact 4 MiB accepted reference reconstruct from chunks no larger than 64 KiB. |
| M3-MAN-09 | Exact replay and isolation | Initial, intermediate, final, and one-chunk responses replay exactly; independent attempts and broker-instance scopes do not share leases. |
| M3-MAN-10 | Registry mutation survival | Artifact-only leases continue across sequential and concurrent reinstall/uninstall; new reads through stale stable refs fail explicitly. |
| M3-MAN-11 | Concurrent advancement and cleanup | One competing boundary wins, identical initial attempts share one response, expiry releases pinned/replay reservations, and capacity is reusable. |
| M3-MAN-12 | Scope and repository boundary | Handle/attempt values do not enter errors or logs, the complete suite passes under race detection and vet, and outward tool listing remains exactly the inherited two tools. |

## Execution record

Acceptance candidate: commit `ef8e28725dec`

Host:

- Linux 7.1.4-204.fc44.x86_64, x86_64
- Go 1.25.5

| ID | Result | Recorded evidence |
| --- | --- | --- |
| M3-MAN-01 | Pass | Uncached `TestSearchCorpusV1Integrity` and `TestSearchLexicalV1GeneratedExactCoverage` passed across normal, reversed, and seeded-random catalogs. |
| M3-MAN-02 | Pass | Uncached acceptance output reported 14/14 Success@1, 14/14 Success@5, MRR 1.0, no wrong rank-one results, and 3/3 ambiguous passes in all three install orders. |
| M3-MAN-03 | Pass | `TestSearchDoesNotIndexExcludedRecordFields` passed all 18 excluded-field canaries, and `TestSearchReturnsOnlyBoundedCardAndMatchMetadata` passed. |
| M3-MAN-04 | Pass | All six uncached `TestCatalog*` checks passed, including 205-child reconstruction and cursor staleness after every successful mutation. |
| M3-MAN-05 | Pass | `TestBuildProjectionIncludesOnlySelectedPackageStructure`, stable uninstall paging, concurrent cursor retry, and one-page no-retention checks passed. |
| M3-MAN-06 | Pass | Maximum counts reconstructed 32 skills, 128 operations, 128 skill references, and 128 context references. The Loader-backed 32-skill fixture exceeded 512 KiB, required multiple pages at `limit: 100`, and kept every compact page within 1 MiB. |
| M3-MAN-07 | Pass | Describe expiry, quotas, cryptographic key failure, bounded collision retries, oversized/invalid UTF-8 rejection, and Close linearization checks passed under the race detector. |
| M3-MAN-08 | Pass | All three readable package artifact kinds reconstructed at 17-byte chunks. A separate exact 4 MiB context reference reconstructed through 64 KiB chunks. |
| M3-MAN-09 | Pass | Exact first/intermediate/final replay, one-chunk replay after uninstall, distinct attempts, distinct scopes, and 32 identical concurrent initial calls passed. |
| M3-MAN-10 | Pass | Sequential and simultaneous continuation advances across reinstall/uninstall passed. The artifact-only retention check left exactly one object and the artifact's byte count after uninstall. |
| M3-MAN-11 | Pass | Competing `max_bytes` advances produced one winner and one typed conflict. Fake-clock idle, absolute, and final expiry released accounting and allowed new admission. |
| M3-MAN-12 | Pass | Secrecy checks found no raw handle or attempt value in errors/logs. Uncached `go test -race ./...` and `go vet ./...` passed. `TestNewTappetServerOutwardContract` listed exactly `execute_tool` and `get_tools_in_category`. |

## Acceptance conclusion

All 12 checks passed against `ef8e28725dec`. Every Milestone 3 behavior that can
be exercised before the Milestone 5 MCP adapter is directly covered. The
current public surface was also rechecked so internal service completion is not
misreported as public broker availability.
