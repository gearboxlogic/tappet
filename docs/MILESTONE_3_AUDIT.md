# Milestone 3 completion audit

Status: **complete as of 2026-08-25**

This audit maps the Milestone 3 roadmap to implementation and acceptance
evidence. The separate
[`manual acceptance record`](MILESTONE_3_MANUAL_ACCEPTANCE.md) passed all 12
checks against commit `ef8e28725dec`. The implementation design and final code
received adversarial approval after three implementation review rounds.

## Work items

| Roadmap commitment | Evidence |
| --- | --- |
| Compact cards, exact pinning, and deterministic lexical ranking | `internal/capability/search.go` implements the accepted `lexical-v1` contract. The frozen corpus and evaluator cover exact capability/operation IDs, natural-language tasks, ambiguity, path constraints, and stable ordering. |
| Path-constrained search and hierarchy paging | `Catalog.Search` combines ranking, direct-child capture, and catalog revision under one registry read lock. HMAC-authenticated cursors bind normalized path, revision, and offset. |
| Deterministic describe pagination | `BuildProjection` is pure and copies only requested package-owned structure. `ProjectionStore` retains a byte-accounted immutable copy, pages by stable section and ID order, and chooses the largest prefix within both item and encoded-byte limits. |
| Agent Skill metadata versus body loading | Describe returns stable skill-path identity, Agent Skill name/description/license/compatibility, and an immutable artifact descriptor. The full validated `SKILL.md` remains absent until `ReadLeaseStore.Read`. |
| Individual reference reads | Only skill documents, explicitly listed skill references, and context references are readable. Manifests and unknown/future artifact kinds are rejected before lease admission. |
| Bounded chunk retrieval | Artifact-level snapshot leases return at most 64 KiB decoded bytes per call and verify stored length/digest at acquisition. Exact compact responses stay below the 128 KiB replay reservation and 1 MiB core-response ceiling. |
| Explicit cross-call state | Projection and read stores have finite count/byte quotas, five-minute idle deadlines, one-hour absolute deadlines, explicit `Close`, injected clocks/random sources for testing, and no global state. |

## Exit criteria

| Exit criterion | Result |
| --- | --- |
| Exact and natural-language retrieval gates | Generated exact coverage is 100%. Acceptance is 14/14 at rank 1 and rank 5 with MRR 1.0; all 3 ambiguous exhaustive judgments pass in normal, reversed, and seeded-random install orders. |
| Search output excludes unrelated bodies and schemas | The card type contains only allowlisted metadata and counts. Eighteen excluded-field canaries, including skill/reference/provider data, produce no match. There is no provider metadata dependency. |
| Describe is bounded and provider-free | Pure projection construction has no provider interface. Every page is compact-encoded and checked at or below 1 MiB. Operation cards report constant `metadata_state: "unavailable"`; Phase 4 schema selectors are absent. |
| Every accepted skill/reference is retrievable | Tests reconstruct all readable artifact kinds and an exact 4 MiB accepted reference from chunks at or below 64 KiB, checking stored length and SHA-256. |
| Continuation survives reinstall/uninstall | An artifact object is independently reference-counted before the package-generation lease is released. Sequential and concurrent mutation tests preserve the accepted continuation while new stale-reference reads fail. |
| Handles are unguessable and excluded from telemetry | Describe IDs and read handles use 128 random bits; catalog/describe cursors are HMAC-authenticated with 256-bit keys. Random failure and bounded collision paths are tested. Errors and configured logs exclude raw attempts and handles. |
| Every prepared chunk is retryable | One immutable replay slot is committed before publication. First, intermediate, final, and one-chunk responses replay byte-exactly until advancement proves receipt or expiry releases the lease. |
| Independent readers do not share advancement | The attempt key binds broker scope, capability ID, stable ref, attempt ID, offset, and max bytes. Independent attempts/scopes get distinct leases; 32 identical concurrent calls with one attempt recover one response and one reservation. |
| Competing advances expose one boundary | Read advancement is serialized. An identical tuple receives the committed replay; a different `max_bytes` at the same expected offset receives `continuation_conflict` without exposing another chunk. |
| Large structure paging is complete and stable | Tests reconstruct maximum item counts and a valid Loader-backed projection above 512 KiB. Cursor retries are idempotent, concurrent retries cannot skip, and byte-driven pages advance by actual item count. |
| Package/provider changes do not invalidate a projection | Describe releases the registry generation immediately after copying selected structure. Later pages survive uninstall and have no provider-generation dependency. |
| Results are deterministic | Ranking, cards, hierarchy children, section order, stable IDs, scores, and page content are deterministic. Initial opaque handles vary by design; exact retries preserve the original handle, timestamp, and response bytes. |

## Current boundary

Milestone 3 delivers internal core services only. Tappet still advertises the
two inherited MCP tools. Milestone 4 adds provider metadata and schema caching;
Milestone 5 registers the fixed `tappet.search`, `tappet.describe`,
`tappet.read`, and `tappet.invoke` broker surface and applies exact outward
JSON-RPC envelope accounting.
