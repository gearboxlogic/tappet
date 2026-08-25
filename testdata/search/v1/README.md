# Search corpus v1

Status: frozen after independent adversarial review on 2026-08-25.

This corpus exercises deterministic Phase 3 catalog retrieval. Every package is
loaded through the production capability loader. The checked queries were
written from task intent and reviewed against package cards, not ranked output.

The `capabilities` catalog contains relevance fixtures. The
`contract-capabilities` catalog contains synthetic fixtures used only to test
field boundaries, tie ordering, and other mechanical behavior. Contract
fixtures never participate in relevance calibration or acceptance.

The evaluator generates one exact capability-ID query for every package and one
exact fully qualified operation-ID query for every operation. The JSONL files
contain calibration, acceptance, contract, and diagnostic queries that cannot
be derived mechanically.

Acceptance query kinds:

- `unambiguous` requires one capability within the first five results.
- `ambiguous` exhaustively lists two through five acceptable capabilities; the
  returned prefix of at most five results must contain all and only those
  capabilities.
- `negative` requires an empty result.
- `error` requires the named typed error.
- `tie` requires the exact ordered result IDs.

`calibration.jsonl` is the only partition used to select ranking weights and the
minimum relevance score. `acceptance.jsonl` remains unchanged while the target
`lexical-v1` ranker is implemented. `contract.jsonl` tests deterministic API and
field-boundary behavior without influencing relevance choices.
`diagnostic.jsonl` records non-gating semantic near misses.

`normalization.json` contains normalization examples. `invalid-utf8.hex` stores
an invalid byte sequence because JSON cannot represent invalid UTF-8. Skill
declaration paths, provider artifact contents, provider type, and operation
schemas must be covered by direct implementation tests rather than duplicated
as corpus fixtures.

`SHA256SUMS` covers every other file below this directory. The corpus becomes
frozen only after the verifier passes and an independent adversarial review is
recorded in `corpus.json`. A later semantic change creates a new corpus version
and receives a separate review.
