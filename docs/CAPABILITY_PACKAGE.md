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

CapScope resolves the configured package root to a canonical absolute path
once, then cleans every package-relative path and checks each component with
`lstat`. V1 rejects any symlink component or symlink target rather than
following it. The final absolute path must remain beneath the canonical package
root. Reads must use no-follow filesystem operations where available, or reopen
and revalidate canonical containment, so a path cannot be swapped to an
external target between validation and use.

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
- missing or escaping paths
- symlinked package content or paths that fail canonical containment
- invalid Agent Skills metadata
- duplicate operation IDs
- unknown provider references
- duplicate provider aliases
- provider targets absent from a refreshed metadata snapshot when strict validation is requested
- unbounded or unsupported referenced files
- discovery metadata that exceeds a per-field, count, or aggregate card limit

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
