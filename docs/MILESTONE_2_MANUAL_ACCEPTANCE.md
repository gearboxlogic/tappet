# Milestone 2 manual acceptance

Status: **passed on 2026-08-25**

This checklist validates the package and registry milestone from the command
line and supplements direct runtime checks with focused adversarial tests for
boundaries that cannot be observed through the current two-tool public surface.
It does not claim Milestone 3 search or artifact-read behavior.

Run from the repository root on Linux with Go 1.25.5, Node.js, `curl`, `jq`, and
a working container engine. Record tool versions and the exact candidate commit
before starting.

## Acceptance checklist

| ID | Check | Pass condition |
| --- | --- | --- |
| M2-MAN-01 | Build and identify Tappet | Both shipped commands build from the pinned module graph and Tappet reports the expected candidate identity. |
| M2-MAN-02 | Candidate migration | Converting the checked hierarchy twice produces byte-identical review-marked candidates for all ten mappings and does not copy a provider credential or schema. |
| M2-MAN-03 | Reviewed package set | Startup atomically loads ten packages, including the three hand-reviewed packages and the add package's validated skill, listed reference, and context artifact. |
| M2-MAN-04 | Package hierarchy without provider startup | Root and `everything` browsing expose package-backed `capability_id` and `operation_id` metadata, and no provider process starts before invocation. |
| M2-MAN-05 | Real lazy invocation and reuse | Calling `everything.add` starts exactly one pinned Everything provider, returns 42, and a second call succeeds through the same provider process. |
| M2-MAN-06 | Structured result preservation | Calling the reviewed structured-content capability preserves both text and typed structured content. |
| M2-MAN-07 | Closed schema and credential exclusion | Focused tests reject unknown fields at every manifest mapping, abort the whole package-set load, reject credential-bearing provider fields without echoing their value, and keep generator output credential-free. |
| M2-MAN-08 | Special-file safety | Focused tests reject FIFO, directory, symlink, socket, device, and symlinked-intermediate fixtures without blocking or retaining bytes. |
| M2-MAN-09 | Staging and quota boundaries | Focused tests prove private-byte validation under mutation, exact per-artifact limits for skills/resources/context, pre-copy aggregate reservations, and cleanup after failure. |
| M2-MAN-10 | Immutable records and generations | Focused tests prove defensive record getters, exact lookup, deterministic hierarchy order, atomic reinstall/uninstall, old-generation retention, terminal release, and no mixed binding under concurrency or race detection. |
| M2-MAN-11 | Compatibility backend selection | An explicit `--hierarchy` flag selects the inherited hierarchy backend even when the loaded config names a package path; `--capabilities` selects package mode. |
| M2-MAN-12 | Modern and legacy outward runtime | The package-backed HTTP and stdio processes retain the fixed two-tool surface and both supported protocol eras. |
| M2-MAN-13 | Official protocol matrix | The package-backed server passes pinned check-level conformance baselines for server and client roles under 2025-11-25 and 2026-07-28. |
| M2-MAN-14 | Container runtime | A fresh image starts with a read-only package mount, lists the two broker tools, and browses package-backed capability metadata. |
| M2-MAN-15 | Repository gate | Formatting, disabled-suite rejection, tests, race detection, vet, GoReleaser snapshot, and container smoke all pass. |

## Evidence rules

Retain complete stdio exchanges and server logs for direct broker checks. Record
the provider-start log position before and after browsing and invocation. Run
focused boundary tests with `-count=1 -v`; a cached package result is not manual
acceptance evidence. Generate candidates outside the repository and compare the
two output trees byte-for-byte. Record official conformance output directories
and the container image tag.

If host policy prevents a socket, device-node, provider-network, or container
check, record it as blocked. Do not replace a blocked direct check with source
inspection. Platform rejection outside Linux and macOS is verified through
cross-compilation because safe local ingestion is intentionally unavailable
there.

## Execution record

Acceptance candidate: commit `d9f7f8a`

Host:

- Linux 7.1.4-204.fc44.x86_64, x86_64
- Go 1.25.5
- Node.js 25.9.0 and npm 11.12.1
- curl 8.18.0 and jq 1.8.1
- Podman 5.8.4 with crun

| ID | Result | Recorded evidence |
| --- | --- | --- |
| M2-MAN-01 | Pass | Both commands built from the pinned module graph. The acceptance binary reported `manual-m2-d9f7f8a`. |
| M2-MAN-02 | Pass | Two independent candidate generations each produced ten manifests and `diff -qr` reported no difference. All ten retained the review marker. Direct inspection found no schema or credential field; the planted-credential generator regression passed uncached. |
| M2-MAN-03 | Pass | `TestReviewedCapabilityPackagesLoadAndPreserveCurrentMappings` loaded all ten packages. The three reviewed manifests were `everything.add`, `everything.echo`, and `everything.structured-content`; add loaded one skill, one listed reference, and one context artifact. |
| M2-MAN-04 | Pass | Direct stdio calls browsed root and `everything`, returned all ten capabilities with operation metadata, and produced no provider-start log entry. |
| M2-MAN-05 | Pass | The first add call started one Everything client and returned `The sum of 19 and 23 is 42.` The second returned `The sum of 1 and 2 is 3.` The complete process log contained exactly one initialized-client entry. |
| M2-MAN-06 | Pass | A direct structured-content call for Denver returned text JSON plus typed `temperature`, `conditions`, and `humidity` structured content. An earlier call with the obsolete `city` field preserved the provider's typed validation error; rerunning with the fixture's required `location` field passed. |
| M2-MAN-07 | Pass | Uncached focused tests rejected unknown fields at all eight mapping levels, released an earlier valid package after a later package failed, rejected all six credential-bearing provider fields without echoing the planted value, and excluded planted hierarchy credentials from candidates. |
| M2-MAN-08 | Pass | FIFO, directory, symlink, socket, and symlinked-intermediate cases passed under the nonblocking assertion. This host cannot create a device node, so that synthetic subtest skipped; `TestUnixPackageDirectoryRejectsExistingCharacterDeviceWithoutReading` directly rejected `/dev/null` without returning a readable file and passed. |
| M2-MAN-09 | Pass | Uncached focused tests passed source mutation, exact skill/resource/context maxima, over-limit rejection, aggregate pre-copy reservation, failure cleanup, and snapshot release. |
| M2-MAN-10 | Pass | Defensive-copy, exact lookup, deterministic hierarchy, reinstall, uninstall, terminal reclaim, and 64-way concurrent generation tests passed. The complete suite also passed under the race detector. |
| M2-MAN-11 | Pass | A direct run using `config.json --hierarchy testdata/mcp_hierarchy` logged `Loading compatibility hierarchy`, returned hierarchy-only output, and emitted no `capability_id`. Package-mode runs logged the configured capability root. |
| M2-MAN-12 | Pass | Direct package-backed HTTP discovery returned 2026-07-28 plus all supported revisions and no session ID; modern tool listing returned the fixed two tools. The same process accepted legacy 2025-11-25 initialize. Modern and legacy stdio characterization tests also passed. |
| M2-MAN-13 | Pass | `script/test-mcp-conformance.sh` ran the package-backed server and exited zero for both roles under 2025-11-25 and 2026-07-28. Every observed failure remained in the pinned expected-failure baseline. |
| M2-MAN-14 | Pass | A fresh `tappet:smoke` image mounted `/config/capabilities` read-only, initialized, listed exactly the two broker tools, and structurally verified `everything.add` package metadata. The corrected test passed five consecutive repetitions and a fresh rebuild. |
| M2-MAN-15 | Pass | Formatting, disabled-suite rejection, `go test ./...`, `go test -race ./...`, `go vet ./...`, the pinned GoReleaser 2.17.1 snapshot, and final container smoke completed successfully. Windows and macOS package tests also cross-compiled. |

### Defects found and repaired

The acceptance review found that an explicit `--hierarchy` flag could not
select compatibility mode when the loaded configuration also contained
`capabilityPath`. Override handling now selects one backend explicitly;
`--capabilities` wins only when both mode flags are supplied.

The first expanded container check exposed two test-client defects. It omitted
the legacy initialized notification and matched nested JSON with raw grep.
The smoke client now completes the handshake, decodes the nested response with
`jq`, checks exact tool names and capability identity structurally, and prints
server logs plus response bodies on failure.

The host disallowed creating a synthetic device node. A separate regression now
opens the existing `/dev` directory descriptor and proves `/dev/null` is
rejected as non-regular without returning a file for reading, so the device exit
criterion does not depend on privileged `mknod` support.

### Acceptance conclusion

All 15 checks passed. Direct observation covers migration, package-backed
browsing, provider-free discovery, lazy invocation and reuse, typed result
fidelity, backend compatibility, both protocol eras, official conformance,
release archives, and the container. Exact parser, filesystem, quota, snapshot,
and generation boundaries remain backed by uncached focused tests because the
current public two-tool surface does not expose package installation or
artifact reads.
