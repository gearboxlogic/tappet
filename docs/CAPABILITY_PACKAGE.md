# Capability Package Format

Status: **V1-alpha design proposal**

This document defines the durable package model CapScope should implement before introducing broader runtime features.

## 1. Package goals

A package must:

- give CapScope compact discovery metadata
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
    ├── capscope.yaml
    ├── skills/
    │   └── github-actions-debugging/
    │       ├── SKILL.md
    │       └── references/
    │           └── common-failures.md
    └── context/
        └── repository-conventions.md
```

Skills must follow the open Agent Skills specification. CapScope-specific metadata belongs in `capscope.yaml`, not in proprietary additions to `SKILL.md`.

## 3. Minimal manifest

```yaml
apiVersion: capscope.gearboxlogic.dev/v1alpha1
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
package. CapScope does not truncate discovery metadata. These V1-alpha limits
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
```

Each path points to an Agent Skills directory containing `SKILL.md`.

CapScope should index:

- `name`
- `description`
- package-relative path
- digest
- optional compatibility metadata

CapScope should defer:

- full `SKILL.md` body
- references
- scripts
- assets

until explicitly read.

### Skill boundaries

- `SKILL.md` is procedural knowledge.
- A script bundled with a skill is an artifact, not automatically a CapScope operation.
- `allowed-tools` is an experimental Agent Skills hint, not authorization.
- Provider bindings live in the capability manifest.
- CapScope must validate skills with the official reference validator where practical.

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

CapScope caches those schemas and records a digest. The manifest may later permit a stricter compatibility schema or fixture, but V1 should not duplicate every provider schema in YAML.

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

CapScope resolves the configured package root to a canonical absolute path and
opens a directory handle for that root. Every package-relative component is
then opened relative to the preceding directory handle with no-follow and
beneath-root semantics. On Linux this means `openat2` with
`RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS` where available, or a
component-by-component `openat` walk using directory descriptors and
`O_NOFOLLOW`; other
platforms must provide equivalent descriptor-relative, reparse-point-safe
operations. The loader rejects `..`, absolute paths, symlink or reparse-point
components, and non-directory intermediate components.

Validation and snapshot copying use the same opened file descriptor; CapScope
never validates a pathname and later reopens that pathname for use. During
installation it copies every accepted manifest and artifact through bounded
readers into a CapScope-owned, immutable content-addressed snapshot while
computing the digest. It publishes artifact references only after the complete
snapshot has been atomically committed with the recorded length and digest.
Subsequent `read` calls use that installed snapshot, not the mutable source
descriptor, and verify the stored length and digest before returning bytes.
Changing a source package requires an explicit reinstall that creates a new
snapshot and manifest digest; in-place source writes cannot change an already
published artifact reference.

The snapshot store has finite per-artifact and aggregate quotas and is not
writable through package paths. A platform or filesystem adapter that cannot
provide the descriptor-relative race protection needed during ingestion is
unsupported for local package reads and must reject installation explicitly.
Reopening and revalidating a canonical source path is not an accepted fallback
because another writer can swap a component between those operations.

MCP resources may be added as a context source after the local-file vertical slice.

Context is not memory. CapScope does not decide what should be remembered across conversations.

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

Field byte limits are normalized UTF-8 bytes; item and aggregate limits use the
canonical encoded JSON representation. These limits cover material returned by
`describe`, independently of the smaller discovery-card limits above.

| Structure | Limit |
| --- | ---: |
| encoded `capscope.yaml` input | 1 MiB |
| YAML nesting depth | 64 |
| YAML syntax nodes | 16,384 |
| YAML aliases, anchors, or merge keys | 0 |
| skills | 32 entries |
| operations | 128 entries |
| context references | 128 entries |
| providers | 32 entries |
| skill path | 512 bytes |
| indexed skill name | 128 bytes |
| indexed skill description | 1,024 bytes |
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
rejects the package; CapScope never truncates a structure entry. Referenced
artifact bodies and provider-owned schemas have their own bounded-ingestion
limits because they are not structure metadata.

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
- duplicate operation IDs
- duplicate context reference IDs or normalized context paths
- unknown provider references
- duplicate provider aliases
- provider targets absent from a refreshed metadata snapshot when strict validation is requested
- unbounded or unsupported referenced files
- discovery or structure metadata that exceeds a per-field, item, count,
  manifest, or aggregate limit

Every manifest collection addressed by `id` must be unique within its artifact
kind. V1 therefore validates operation IDs, context reference IDs, and provider
aliases before constructing artifact references. The explicit reference-kind
prefix keeps those three namespaces separate. Collections addressed by path,
including skills, reject duplicate normalized paths.

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
3. Generate a candidate `capscope.yaml`.
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
