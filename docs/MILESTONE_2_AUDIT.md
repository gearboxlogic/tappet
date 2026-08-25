# Milestone 2 completion audit

Status: **complete as of 2026-08-25**

This audit maps every Milestone 2 roadmap commitment and exit criterion to
repository evidence. The separate
[manual acceptance checklist](MILESTONE_2_MANUAL_ACCEPTANCE.md) records direct
runtime, migration, compatibility, conformance, and packaging checks; all 15
checks passed on 2026-08-25.

## Work items

| Roadmap commitment | Evidence |
| --- | --- |
| V1-alpha manifest parser | `internal/capability/manifest.go` parses the closed schema under encoded-byte, depth, syntax-node, field, item, and aggregate structure limits. `TestParseManifestAcceptsV1AlphaPackage`, `TestParseManifestRejectsUnknownFieldsAtEveryMappingLevel`, `TestParseManifestRejectsAmbiguousYAML`, and `TestParseManifestEnforcesInputAndSyntaxLimits` cover the contract. |
| Stable package types | `internal/capability/types.go` defines capability metadata, skills, operations, context references, and MCP provider bindings independently of the compatibility hierarchy. |
| Agent Skills validation | `internal/capability/skill.go` mirrors the reference validator rules at Agent Skills commit `69ef37e9424c0a7ea9dd2293b559e43ec8176379`, adds finite byte and syntax limits, and validates only private staged bytes. The `TestParseSkill*` suite covers accepted and rejected metadata. |
| Immutable normalized records | `internal/capability/loader.go`, `record.go`, and `snapshot.go` create private content-addressed snapshots and expose defensive copies. `TestLoaderStagesValidPackageIntoImmutableRecord`, `TestLoaderValidatesOnlyPrivateStagedBytes`, and `TestRecordGettersDoNotExposeMutableState` cover immutability. |
| Exact lookup and hierarchy browsing | `internal/capability/registry.go` provides deterministic exact-ID lookup, operation resolution, and hierarchy views. `TestRegistryProvidesExactLookupAndDeterministicHierarchy` covers ordering and lookup. |
| Immutable generation lifecycle | Registry add, reinstall, uninstall, and invocation leases retain one package and provider-binding generation. `TestInvocationLeaseKeepsOneGenerationAcrossReinstall`, `TestInvocationLeaseSurvivesUninstallUntilTerminalRelease`, and `TestRegistryConcurrentResolutionNeverMixesGenerations` cover queued, active, retired, and concurrent access. |
| Existing hierarchy migration | `GenerateCapabilityCandidates` converts leaves into closed V1-alpha packages while preserving provider/target mappings and excluding provider configuration and schemas. `TestGenerateCapabilityCandidatesMigratesCurrentHierarchyWithoutMappingLoss` covers all current mappings. |
| Reviewable generated output | Candidates contain a review marker and source path, generation is deterministic, and regeneration refuses to overwrite reviewed or unrecognized packages. `TestGenerateCapabilityCandidatesIsDeterministicAndReviewable` and `TestGenerateCapabilityCandidatesWillNotOverwriteReviewedPackages` cover the workflow. |
| Package-backed broker runtime | `capabilityPath` selects the package loader and registry while `hierarchyPath` remains a compatibility fallback. `TestNewTappetServerBrowsesCapabilityPackagesWithoutStartingProvider`, `TestLoadServerPrefersCapabilityPackages`, direct acceptance calls, conformance, and container smoke cover the runtime path. |

## Exit criteria

| Exit criterion | Result |
| --- | --- |
| At least three hand-reviewed packages load | `everything.add`, `everything.echo`, and `everything.structured-content` are explicitly hand-reviewed; `TestReviewedCapabilityPackagesLoadAndPreserveCurrentMappings` loads them with the seven remaining migration candidates. The add package also contains one validated skill, one listed skill reference, and one context reference. |
| Duplicate and invalid references fail deterministically | `TestParseManifestRejectsDuplicateAndInvalidReferences` covers duplicate provider aliases, unknown operation providers, invalid parents, and escaping paths; registry duplicate installation has its own deterministic sentinel error. |
| Unknown fields reject the complete staged package | `TestParseManifestRejectsUnknownFieldsAtEveryMappingLevel` covers every mapping level. `TestLoadAllUnknownFieldRejectsCompletePackageSet` proves a bad package aborts the atomic package-set load, releases earlier snapshots, and excludes the planted value from its error. |
| Special artifacts fail without blocking or reading | `TestLoaderRejectsSpecialAndLinkedArtifactsWithoutBlocking` exercises FIFO, directory, symlink, Unix socket, and character-device fixtures under a one-second bound. `TestUnixPackageDirectoryRejectsExistingCharacterDeviceWithoutReading` proves the device case against `/dev/null` without privileged fixture creation. `TestLoaderRejectsSymlinkedIntermediateDirectory` covers path-component replacement. Local ingestion is explicitly unsupported outside Linux and macOS. |
| Packages contain no provider credentials | The provider schema admits only `id`, `type`, and `serverRef`. `TestParseManifestExcludesCredentialBearingProviderFields` rejects `token`, `env`, `headers`, `command`, `args`, and `url` without echoing the planted value. `TestGenerateCapabilityCandidatesIsDeterministicAndReviewable` proves provider and schema credentials are not copied into candidates. |
| Validation uses private staged bytes under source mutation | `TestLoaderValidatesOnlyPrivateStagedBytes` mutates the source after staging and proves the published artifact remains the validated version. `TestLoaderStagesValidPackageIntoImmutableRecord` proves later source changes and caller mutations cannot alter snapshot reads. |
| Numeric artifact and aggregate quotas are reserved before copying | `TestLoaderRejectsArtifactAtDeclaredHardLimitBeforeRead` covers `SKILL.md`, listed skill resources, and context references. `TestLoaderAcceptsArtifactsAtExactHardLimits`, `TestLoaderReservesAggregateCapacityBeforeArtifactCopies`, and `TestLoaderRejectsAggregatePackageBytesBeforeArtifactCopies` cover exact boundaries and pre-copy reservations. |
| Queued and active invocations retain one generation through terminal publication | `InvocationLease` owns the resolved operation, provider binding, and registry generation until explicit release. Reinstall, uninstall, and 64-way concurrent resolution tests prove old leases remain intact and are reclaimed only after terminal release. The package-backed server holds the lease across provider execution and result construction. |
| Current mappings are represented without loss | Both the candidate-generator test and checked-package integration test assert the exact ten inherited `everything` provider targets, including camelCase targets and compatibility tool paths. |

## Current boundary

Milestone 2 snapshots and the installed registry are process-local and rebuilt
from `capabilityPath` at startup. This is the in-memory V1 option accepted by
`ARCHITECTURE.md` section 6.2. Search and progressive artifact reads begin in
Milestone 3. Provider schema caching, strict target validation, idle shutdown,
and provider-generation management begin in Milestone 4.

The required completion gate is formatting, disabled-suite rejection,
`go test ./...`, `go test -race ./...`, `go vet ./...`, a GoReleaser snapshot,
the container smoke test, and the official legacy/modern MCP conformance matrix.
