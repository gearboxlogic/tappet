# Milestone 3 search contract

Status: **accepted search and benchmark contract as of 2026-08-25; the
`lexical-v1` score contract remains provisional until calibration**

This document fixes the catalog-search and benchmark decisions that must remain
stable while Milestone 3 search is implemented. Search keeps irrelevant package
content out of broad discovery. It is not an authorization boundary.

## Identifiers and path filtering

The canonical fully qualified operation identifier is:

```text
<capability-id>/<operation-id>
```

An exact capability ID or fully qualified operation ID is a global exact match.
A local operation ID can match more than one capability and is therefore a
strong local-field match, not a global identifier. Provider targets never
participate in search.

An optional hierarchy path filters the candidate set before matching and
ranking. An omitted path, an empty path, and `/` select the complete catalog. A
specific path selects that dot-delimited node and its descendants. Exact IDs
outside the selected subtree remain excluded. An unknown path returns
`path_not_found`; path filtering never grants or denies execution authority.

## Searchable fields

Only these normalized fields participate in search:

| Field | Matching behavior |
| --- | --- |
| capability ID | global exact match |
| fully qualified operation ID | global exact match |
| capability name | exact and lexical match |
| capability description | lexical match |
| tag | exact normalized tag match |
| local operation ID | exact local-field match |
| operation description | lexical match |
| skill name | exact and lexical match |
| skill description | lexical match |

The index excludes provider IDs, types, targets, and server references; package
version and provenance; hierarchy paths as scoring text; skill paths, license,
`allowed-tools`, compatibility, and arbitrary metadata; resource and context
IDs and paths; artifact metadata; complete skill bodies; references; context
bodies; and operation schemas. The hierarchy path remains available only as a
filter and card field.

A bounded `matched_field` and match reason identify one primary matched
capability field, tag, local operation ID, or skill name. They never enumerate
a capability's structure or return a matched description body.

## Text normalization

Search rejects invalid UTF-8 and applies this versioned pipeline:

1. Unicode NFC normalization.
2. Leading and trailing Unicode whitespace removal.
3. Camel-case and acronym-boundary splitting for lexical matching.
4. Language-independent Unicode case folding.
5. Lexical splitting at whitespace, dots, slashes, underscores, hyphens,
   punctuation, and symbols.
6. Repeated-boundary collapse while preserving letters, numbers, and
   diacritics inside tokens.

Exact identifiers preserve their separators. Exact names and tags compare
normalized full text; punctuation splitting applies only to lexical matching.
The normalized query may not exceed 4,096 UTF-8 bytes and is never truncated.

`lexical-v1` does not use stemming, synonyms, fuzzy spelling, accent removal,
locale-specific rules, NFKC compatibility folding, or stop-word removal. The
implementation pins its Unicode behavior through the checked module graph.

## Ordering and relevance

Each candidate receives one best match tier:

1. exact capability or fully qualified operation ID
2. exact capability name
3. exact tag, local operation ID, or skill name
4. lexical capability name or description
5. lexical operation or skill metadata

The response uses this closed `match_kind` enum:

| `match_kind` | Tier | Permitted primary fields, in tie precedence order |
| --- | --- | --- |
| `exact_capability_id` | 1 | `capability_id` |
| `exact_operation_id` | 1 | `operation_id` |
| `exact_capability_name` | 2 | `capability_name` |
| `exact_tag` | 3 | `tag` |
| `exact_local_operation_id` | 3 | `local_operation_id` |
| `exact_skill_name` | 3 | `skill_name` |
| `lexical_capability` | 4 | `capability_name`, `capability_description` |
| `lexical_operation` | 5 | `operation_description` |
| `lexical_skill` | 5 | `skill_name`, `skill_description` |

When multiple match kinds qualify in the same tier, the table order wins. For
lexical fields within one match kind, the field with the largest individual
field score is primary; a tie uses the listed field order. The primary field
selection affects only the bounded explanation, not the candidate score.

The deterministic ordering key is match tier ascending, lexical score
descending within the tier, then capability ID ascending by UTF-8 bytes. The ID
tie-break runs before top-K truncation. A score is an integer from 0 through
1,000,000 and is comparable only within one match tier and ranking version.
Scores describe lexical evidence, not confidence, permission, or authority.

Exact matches receive score 1,000,000 and always qualify. Lexical matches must
meet the fixed `lexical-v1` minimum score. Search returns fewer than the
requested limit when fewer candidates qualify. If none qualify, it returns a
successful response with no capability cards. It never widens the selected
hierarchy path or fills unused result positions with below-threshold candidates.

The response identifies the ranking version and returns the match kind, score,
one primary matched field, one bounded reason, and the hierarchy path. Integer
field weights, the score formula, the minimum score, and rounding behavior are
part of the ranking version. Changing any of them requires a new version.

## Benchmark judgments and gates

The versioned corpus has separate calibration and acceptance partitions.
Unambiguous natural-language queries name exactly one required capability and
must reach 100% Success@5 on the acceptance partition. The evaluator also
reports Success@1, mean reciprocal rank, exact rank, wrong rank-one results,
and per-domain results without treating them as Milestone 3 gates.

Each ambiguous acceptance query exhaustively lists two through five valid
capabilities. Search must return all of them within the top five, return no
other capability in those positions, and rank one of them first. This requires
100% Recall@5 and 100% Precision@5 per query. A query whose valid set cannot be
reviewed exhaustively belongs in a diagnostic corpus instead.

Negative acceptance cases cover zero indexed-token overlap, matches found only
in excluded fields, and exact IDs outside a selected path. They must return no
cards. Semantic near misses that share searchable words remain diagnostics
until a reviewed ranking version defines their expected behavior.

## Corpus change control

The initial corpus is frozen before ranking implementation. A manifest records
hashes for every package, artifact, query, judgment, and normalization fixture.
CI validates those hashes and the judgment references.

A semantic corpus change includes searchable package metadata, query text,
accepted IDs, query class, or a normalization expectation. It cannot share a
commit with tokenizer, ranking, score, threshold, or evaluator-semantic changes.
Old corpus versions remain available; a semantic correction creates a new
version with a recorded rationale. Every new version receives an independent
adversarial review based on the package cards and task meaning rather than the
current ranking output.

The integer weights and minimum relevance score use only the calibration
partition. They are frozen as `lexical-v1` before the acceptance partition is
evaluated. A failed acceptance query changes the algorithm or ranking version,
not the frozen acceptance judgment.
