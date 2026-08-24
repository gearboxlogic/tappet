# Capability Package Format

Status: **V1-alpha design proposal**

This document defines the durable package model Tappet should implement before introducing broader runtime features.

## 1. Package goals

A package must:

- give Tappet compact discovery metadata
- reference Agent Skills without copying their format
- select a bounded set of executable operations
- bind operations to providers without making provider identity the capability identity
- expose supporting context selectively
- remain version-controlled and reviewable
- contain no credentials
- be valid without an LLM

A package is not a plugin runtime, workflow, policy bundle, or agent definition.

## 2. Directory layout

Proposed layout:

```text
capabilities/
└── software.github.ci-debugging/
    ├── tappet.yaml
    ├── skills/
    │   └── github-actions-debugging/
    │       ├── SKILL.md
    │       └── references/
    │           └── common-failures.md
    └── context/
        └── repository-conventions.md
```

Skills must follow the open Agent Skills specification. Tappet-specific metadata belongs in `tappet.yaml`, not in proprietary additions to `SKILL.md`.

## 3. Minimal manifest

```yaml
apiVersion: tappet.gearboxlogic.dev/v1alpha1
kind: Capability

metadata:
  id: software.github.ci-debugging
  name: GitHub CI debugging
  version: 0.1.0
  description: >
    Inspect and diagnose GitHub Actions checks and workflow failures.
  tags:
    - github
    - ci
    - actions

spec:
  parent: software.github

  skills:
    - path: skills/github-actions-debugging
      resources:
        - id: common-failures
          kind: reference
          path: references/common-failures.md

  operations:
    - id: inspect-failed-checks
      description: Inspect failed checks for a pull request.
      provider: github
      target: get_check_runs

    - id: read-job-log
      description: Read the log for a failed workflow job.
      provider: github
      target: get_job_log

  context:
    - id: repository-conventions
      path: context/repository-conventions.md

  providers:
    - id: github
      type: mcp
      serverRef: github
```

The exact serialization and field names remain alpha. The conceptual separation is accepted.

## 4. Metadata

### `metadata.id`

A globally unique, stable, lowercase dotted identifier.

Recommended pattern:

```text
domain.product.task
```

Examples:

```text
software.github.ci-debugging
embedded.yocto.recipe-debugging
data.postgres.query-analysis
```

Renaming display text must not change the ID.

### `metadata.name`

Human-readable display name.

### `metadata.version`

Package version. Use semantic versioning initially.

A runtime projection should include the package version so traces and caches identify exactly which capability definition was used.

### `metadata.description`

Compact text used for discovery. It must state both what the capability does and when it is relevant.

### `metadata.tags`

Small deterministic search hints. Tags do not grant authority or activate behavior.

### V1-alpha discovery metadata limits

The package loader measures normalized UTF-8 bytes and enforces these hard
limits before registering a capability:

| Field | Limit |
| --- | ---: |
| capability ID | 128 bytes |
| name | 128 bytes |
| version | 64 bytes |
| description | 1,024 bytes |
| hierarchy path | 256 bytes |
| tags | 16 entries |
| one tag | 64 bytes |
| all tags | 512 bytes |
| complete normalized base card | 4,096 encoded JSON bytes |

Invalid UTF-8 or any exceeded field, count, or aggregate limit rejects the
package. Tappet does not truncate discovery metadata. These V1-alpha limits
may change only through an explicit format revision and benchmark update.

## 5. Hierarchy

`spec.parent` defines an optional parent capability path for browsing.

When present, the parent must be a proper dot-delimited prefix of
`metadata.id`: it cannot equal the capability ID or point sideways or downward.
For example, `software.github` is a valid parent of
`software.github.ci-debugging`; `software.github.ci-debugging` and
`software.gitlab` are not. An omitted parent attaches the capability directly
to the hierarchy root. This local rule makes self-parenting and multi-package
parent cycles impossible without requiring traversal of a partially installed
graph.

Hierarchy is organizational and should not imply inheritance of skills, tools, context, or permissions in V1.

A future package composition feature requires a separate decision. Do not silently merge parent content into children.

## 6. Skills

```yaml
skills:
  - path: skills/github-actions-debugging
    resources:
      - id: common-failures
        kind: reference
        path: references/common-failures.md
```

Each path points to an Agent Skills directory containing `SKILL.md`.

Supporting files are never discovered by recursively walking that directory.
Each V1-readable file must appear in the skill entry's bounded `resources`
collection with a skill-local stable `id`, `kind: reference`, and a normalized
path relative to the skill directory. The path must remain inside that skill
directory and pass the package containment rules. Unlisted files, including a
file merely linked from `SKILL.md`, are not indexed, snapshotted, counted, or
exposed by Tappet.

Installation snapshots every listed resource and assigns an opaque artifact
reference binding the package version and manifest digest, skill digest,
resource ID, and content digest. `describe` returns only bounded resource
metadata: ID, media type, byte count, digest, and the exact artifact reference.
`read` accepts that reference and returns the immutable content in bounded
chunks. A changed package or skill produces a new reference; an old binding
returns a typed stale-reference result instead of resolving by current path.

Tappet should index:

- `name`
- `description`
- package-relative path
- digest
- optional compatibility metadata

Tappet should defer:

- full `SKILL.md` body
- explicitly listed supporting references

until explicitly read.

Scripts and assets are not implicitly enumerable or readable in V1. Adding
explicit resource kinds for them requires a format revision with media, size,
and execution-boundary rules; listing a script would never make it executable.

### Skill boundaries

- `SKILL.md` is procedural knowledge.
- A script bundled with a skill is an artifact, not automatically a Tappet operation.
- `allowed-tools` is an experimental Agent Skills hint, not authorization.
- Provider bindings live in the capability manifest.
- Tappet must validate skills with the official reference validator where practical.

### Bounded `SKILL.md` validation

The loader reads each `SKILL.md` through a 1 MiB bounded reader and extracts at
most 64 KiB of YAML frontmatter before parsing metadata. The frontmatter scanner
applies the same preconstruction safety rules as the capability manifest: a
maximum nesting depth of 64, at most 16,384 syntax nodes, no anchors, aliases,
or merge keys, and no duplicate mapping keys. It enforces those budgets while
scanning syntax events, before alias resolution or semantic allocation.

Only the resulting bounded metadata record is passed to the Agent Skills
reference validation path. If the official validator API reparses raw
frontmatter without equivalent reader, depth, node, and alias-expansion limits,
Tappet must place a bounded adapter in front of it or reject the skill; it must
not hand untrusted package bytes to an unbounded parser. The complete bounded
`SKILL.md` is copied into private immutable staging before parsing, and both
metadata and body are published from those exact staged bytes only after
reference validation succeeds.

## 7. Operations

```yaml
operations:
  - id: inspect-failed-checks
    description: Inspect failed checks for a pull request.
    provider: github
    target: get_check_runs
```

### `id`

Stable within the capability. The fully qualified identifier is:

```text
software.github.ci-debugging/inspect-failed-checks
```

### `description`

Compact search and selection text.

### `provider`

References a provider entry in the same manifest.

### `target`

Provider-specific executable name. For MCP V1, this is the downstream tool name.

### Schema ownership

The downstream provider owns the executable input and output schemas.

Tappet caches those schemas and records a digest. The manifest may later permit a stricter compatibility schema or fixture, but V1 should not duplicate every provider schema in YAML.

Provider ownership does not authorize schema-driven I/O or unbounded
validation. V1 accepts only bounded, self-contained schemas with same-document
JSON Pointer references; it rejects external or dynamic references and enforces
the compilation and evaluation budgets in `docs/ARCHITECTURE.md`.

An operation should fail visibly when the provider tool no longer exists or its schema becomes incompatible.

## 8. Context references

```yaml
context:
  - id: repository-conventions
    path: context/repository-conventions.md
```

Context references are selectively readable supporting data.

V1 rules:

- local package-relative files only by default
- no `..` traversal
- no symlinks in manifest, skill, context, reference, script, or asset paths
- no implicit recursive directory loading
- bounded file size
- text-first formats
- stable digest in projections and traces

### Package path containment

Tappet resolves the configured package root to a canonical absolute path and
opens a directory handle for that root. Every package-relative component is
then opened relative to the preceding directory handle with no-follow and
beneath-root semantics. On Linux this means `openat2` with
`RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS` where available, or a
component-by-component `openat` walk using directory descriptors and
`O_NOFOLLOW`; other platforms must provide equivalent descriptor-relative,
reparse-point-safe operations. The loader rejects `..`, absolute paths, symlink
or reparse-point components, and non-directory intermediate components.

Every final manifest, `SKILL.md`, context entry, and listed resource is opened
descriptor-relative with no-follow and nonblocking semantics before any read.
On Unix this includes `O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC`. Tappet immediately
uses `fstat` on that descriptor and accepts only a regular file; directories,
FIFOs, sockets, block or character devices, and every other special type are
rejected. `O_NONBLOCK` prevents a FIFO open from waiting for a writer, and no
bytes are read from a device or other rejected descriptor. Platforms that
cannot provide an equivalent nonblocking type check must reject local package
ingestion rather than attempt a possibly blocking open.

Tappet never validates bytes from the mutable source descriptor. Immediately
after the regular-file check, it uses the descriptor metadata to reject a
declared size above the applicable per-artifact or remaining 64 MiB package
budget. Before copying the first byte, it atomically reserves that declared
length from both the per-install staging quota and the prospective immutable
snapshot quota. If a reliable length is unavailable, it reserves the full
per-artifact maximum. It then copies through a bounded reader with one sentinel
byte into a private Tappet-owned staging object while computing the exact length
and digest. Growth beyond the reservation or per-artifact maximum aborts the
install; a shorter complete copy releases unused capacity. Tappet then closes
the source, durably finishes the staging write, prevents further writes to that
object, and performs parsing, reference checks, Agent Skills validation, and
normalization only through a read-only descriptor for the staged bytes. A
concurrent source rewrite may change what the bounded copy observes, but it
cannot make the published bytes differ from the bytes that were validated.

The staged manifest is parsed first to identify its bounded artifact set; every
referenced candidate is then staged before cross-file validation. If any copy,
digest, or validation fails, Tappet deletes all private staging objects and
does not change the registry. Only after the complete staged set validates does
it atomically commit those same objects into the immutable content-addressed
snapshot and publish the normalized record with their recorded lengths and
digests. It never validates a pathname and later reopens that pathname for use.
Subsequent `read` calls use that installed snapshot, not the mutable source
descriptor, and verify the stored length and digest before returning bytes.
Changing a source package requires an explicit reinstall that creates a new
snapshot and manifest digest; in-place source writes cannot change an already
published artifact reference.

Install, reinstall, and uninstall update the durable installed-package registry
as one atomic generation. A successful reinstall first commits and validates
the new snapshot, then swaps the registry entry; the previous generation becomes
unreachable to new initial `read` calls and its old artifact references return
the typed stale-reference result. An explicit continuation handle issued before
the swap remains bound to the old immutable snapshot until that handle is
released or expires. Uninstall removes the registry reference in the same way.
A failed install never changes the active generation.

Invocation resolution atomically leases one immutable registry generation and
its exact operation record, provider binding, provider-configuration
fingerprint before the invocation enters any queue. It does not pin a provider
metadata generation or schema digest at queue admission. At dispatch, after any
refresh required by `ARCHITECTURE.md` section 6.7, Tappet atomically selects
the current mapped tool and schema for the leased binding, validates the
arguments against that schema, and leases that metadata generation and digest
through transmission and terminal publication. If the current provider
metadata cannot be reconciled with the leased operation and binding, the call
fails with a typed provider-metadata result before transmitting bytes. The
registry-generation lease remains live through provider acquisition and call,
resource materialization, and terminal result or preservation-error
publication. Reinstall or uninstall prevents new resolutions from using the old
generation but cannot retarget a queued invocation, combine old arguments with
a new binding, or close the leased provider generation during a transmitted
call. Old provider state remains subject to the global residency budget.

If resolution cannot acquire a generation lease because retirement has already
begun, Tappet may re-resolve entirely against the new generation before
transmitting bytes. It must revalidate the provider binding as one unit and then
perform the same dispatch-time current-schema selection and argument validation,
or return typed `operation_generation_stale`; it never mixes package or binding
generations. Once any downstream request byte has been transmitted, the package,
binding, and selected metadata generations cannot change and ambiguous-delivery
rules apply. Uninstall and reinstall are not authorization revocation mechanisms;
providers still enforce authorization for already admitted calls.

Snapshot objects are reference-counted by active registry generations and by
explicit, bounded continuation-handle leases. Resolving an artifact reference,
acquiring its snapshot lease, and issuing a continuation handle for an
incomplete first response are atomic. Every later chunk uses that opaque handle
rather than resolving the mutable registry again, so reinstall or uninstall
cannot interrupt an accepted multi-call read. An unfinished handle lease is
released on a five-minute idle timeout or a one-hour absolute lifetime. Each
successful non-final chunk renews the idle deadline without extending the
absolute deadline. Preparing the final chunk retains the immutable object and
an exact, idempotent response for a fixed five-minute replay grace. Every prepared
non-final chunk also has one exact replay record until the next request proves
receipt and atomically replaces it, or the idle or absolute lease deadline
passes. Every initial stable-reference request carries a caller-generated
`attempt_id` with at least 128 bits of randomness. The authorized requester,
stable reference, attempt ID, offset, and `max_bytes` map to the same lease and
response on an exact retry, while independent readers use distinct attempt IDs
and receive distinct leases. Loss of the response containing `continuation_ref`
is therefore recoverable without making separate readers share advancement
state. Replays never extend a deadline. Single-chunk reads receive the same
protection if reinstall races delivery. Each response reports the effective
expiry. Counts of live handles, replay records, and retained bytes have finite
operator-configurable quotas, and admission failure returns a typed error before
promising retrieval.
Continuation advancement is serialized per lease and uses a monotonic
generation compare-and-swap. At one expected offset, the first exact request
tuple commits; an identical competitor receives its replay and a different
`max_bytes` receives bounded `continuation_conflict` without changing lease
state. Only the committed response's `next_offset` can advance the generation.
Garbage collection may delete a superseded or uninstalled snapshot only after
both its registry count and continuation-lease count reach zero. Identical
content-addressed objects shared by active snapshots remain live until the
final reference is released. Continuation handles are application handles, not
implicit connection or MCP session state.

Reinstall and uninstall schedule collection, and quota admission runs collection
before rejecting a new snapshot. Startup removes abandoned temporary writes and
performs a mark-and-sweep from the durable installed registry. A replicated
shared store also persists active continuation-lease and replay records and
includes every unexpired record as a sweep root. Collection is transactionally
or epoch-fenced so one replica cannot reclaim a snapshot leased by another. An
owner-routed store sweeps only its durable owner namespace. Registry-only startup
sweeping is permitted solely for a non-replicated process-local store, where no
handle remains routable after process exit. If live registry or lease-rooted data
still consumes the aggregate quota, installation fails explicitly with
`snapshot_quota_exceeded`; garbage collection never deletes a live generation
merely to make room.

The snapshot store has finite per-artifact and aggregate quotas and is not
writable through package paths. A platform or filesystem adapter that cannot
provide the descriptor-relative race protection needed during ingestion is
unsupported for local package reads and must reject installation explicitly.
Reopening and revalidating a canonical source path is not an accepted fallback
because another writer can swap a component between those operations.

MCP resources may be added as a context source after the local-file vertical slice.

Context is not memory. Tappet does not decide what should be remembered across conversations.

## 9. Providers

```yaml
providers:
  - id: github
    type: mcp
    serverRef: github
```

### `id`

Package-local provider alias.

### `type`

Only `mcp` is required in V1.

### `serverRef`

References an external provider configuration by stable ID.

Provider configuration is separate from packages:

```yaml
mcpServers:
  github:
    transport: stdio
    command: github-mcp-server
    args: ["stdio"]
```

Credentials come from the environment or an external secret mechanism. They must not be copied into capability packages or caches.

### Lifecycle

Provider lifecycle defaults belong in provider configuration, not skill files:

```yaml
lifecycle: lazy
idleTimeout: 10m
requestTimeout: 30s
```

A capability may eventually request compatibility requirements, but should not silently override operator lifecycle policy.

### V1-alpha structure metadata limits

The loader enforces input limits during bounded ingestion and normalized limits
before registering a package. It reads at most 1 MiB plus one sentinel byte
before invoking the YAML parser, rejecting overflow without buffering the full
input. The parser builds a bounded syntax tree with a maximum depth of 64 and
16,384 nodes, enforcing both budgets as syntax events are scanned and aborting
before constructing a node beyond either budget. V1 rejects anchors, aliases,
and merge keys before alias resolution, and rejects duplicate mapping keys
rather than ambiguously resolving them. Semantic decoding starts only after
those syntax budgets pass.

The V1-alpha manifest schema is closed. Semantic decoding rejects an
unrecognized field at every mapping node, including the root, skills,
operations, context references, provider bindings, and nested structures; it
uses a known-fields decoder or equivalent generated validation rather than a
permissive YAML-to-struct decode. Versioned additions require an explicit
schema change. A misspelled field or attempted inline credential such as
provider `token` rejects the complete staged package, and the rejected manifest
bytes are deleted with the other private staging objects rather than committed
to an immutable snapshot.

Field byte limits are normalized UTF-8 bytes; item and aggregate limits use the
canonical encoded JSON representation. These limits cover material returned by
`describe`, independently of the smaller discovery-card limits above.

| Structure | Limit |
| --- | ---: |
| encoded `tappet.yaml` input | 1 MiB |
| YAML nesting depth | 64 |
| YAML syntax nodes | 16,384 |
| YAML aliases, anchors, or merge keys | 0 |
| encoded `SKILL.md` input | 1 MiB |
| one listed skill-resource body | 4 MiB |
| one context-reference body | 4 MiB |
| all immutable artifact bodies per package | 64 MiB |
| `SKILL.md` YAML frontmatter | 64 KiB |
| frontmatter nesting depth | 64 |
| frontmatter syntax nodes | 16,384 |
| frontmatter aliases, anchors, or merge keys | 0 |
| skills | 32 entries |
| skill resources | 128 entries per package, 32 per skill |
| operations | 128 entries |
| context references | 128 entries |
| providers | 32 entries |
| skill path | 512 bytes |
| indexed skill name | 128 bytes |
| indexed skill description | 1,024 bytes |
| skill resource ID | 128 bytes |
| skill resource kind | 32 bytes |
| skill resource path | 512 bytes |
| operation ID | 128 bytes |
| operation description | 1,024 bytes |
| operation provider alias | 128 bytes |
| operation target | 256 bytes |
| context reference ID | 128 bytes |
| context path | 512 bytes |
| provider ID | 128 bytes |
| provider type | 32 bytes |
| provider `serverRef` | 256 bytes |
| one normalized structure item | 4,096 encoded JSON bytes |
| all normalized structure metadata | 512 KiB encoded JSON |

Invalid UTF-8 or any exceeded field, item, count, manifest, or aggregate limit
rejects the package; Tappet never truncates a structure entry. Referenced
artifact bodies use the numeric per-file and per-package limits above;
provider-owned schemas use the independent limits in `ARCHITECTURE.md` because
they are not package artifacts. Changing a V1-alpha artifact maximum requires
an architecture and benchmark update.

## 10. Derived capability card

A normalized card may look like:

```json
{
  "id": "software.github.ci-debugging",
  "name": "GitHub CI debugging",
  "version": "0.1.0",
  "description": "Inspect and diagnose GitHub Actions checks and workflow failures.",
  "path": "software.github",
  "tags": ["github", "ci", "actions"],
  "counts": {
    "skills": 1,
    "operations": 2,
    "references": 2
  }
}
```

The card excludes skill and operation lists, full schemas, full instructions,
provider commands, environment values, and context bodies. Search may identify
the one operation, skill, tag, or field that caused a match, but it must not
enumerate the capability structure. `describe` owns that materialization step.

## 11. Validation

Reject packages with:

- duplicate capability IDs
- invalid version or identifier syntax
- a parent that is not a proper dot-delimited prefix of the capability ID
- missing or escaping paths
- symlinked package content or paths that fail canonical containment
- invalid Agent Skills metadata
- duplicate normalized skill paths
- duplicate skill resource IDs or normalized resource paths within a skill
- unlisted or unsupported skill resources requested for publication
- duplicate operation IDs
- duplicate context reference IDs or normalized context paths
- unknown provider references
- duplicate provider aliases
- provider targets absent from a refreshed metadata snapshot when strict validation is requested
- unbounded or unsupported referenced files
- discovery or structure metadata that exceeds a per-field, item, count,
  manifest, or aggregate limit

Every manifest collection addressed by `id` must be unique within its artifact
kind. V1 therefore validates operation IDs, context reference IDs, provider
aliases, and each skill's resource IDs before constructing artifact references.
The explicit reference-kind prefix keeps those namespaces separate.
Collections addressed by path, including skills and their resources, reject
duplicate normalized paths within their documented scope.

Warnings may be appropriate when a provider is offline and strict target validation cannot run.

## 12. Provenance and digests

A normalized package record should include:

```text
manifest digest
skill metadata digest
skill body digest
reference digests
provider configuration fingerprint
provider schema digest
```

These enable deterministic cache invalidation and explain which material an agent used.

Do not record secrets in fingerprints or traces.

## 13. Importing the existing hierarchy

The current JSON hierarchy can be migrated incrementally:

1. Treat each existing hierarchy leaf as an operation group.
2. Convert `server` and `maps_to` into provider bindings.
3. Generate a candidate `tappet.yaml`.
4. Preserve manually written descriptions.
5. Require review before accepting generated capability boundaries.
6. Add skills and context manually where they add procedural value.

The generator should produce candidates, not infer authoritative semantic groupings without review.

## 14. Deferred package features

Do not add these to V1:

- embedded credentials
- RBAC or permission rules
- human approval definitions
- subagent definitions
- model selection
- workflow graphs
- conversation memory
- arbitrary remote downloads
- automatic script execution
- package inheritance
- dependency installation
- provider types beyond MCP
- agent plugin packaging

These may be integrated later without changing the core capability/skill/operation/context/provider model.
