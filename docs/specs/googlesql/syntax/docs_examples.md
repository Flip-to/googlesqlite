---
name: Reference documentation examples
dialect: googlesql
category: syntax
status: tested
source_url: docs/third_party/googlesql-docs/functions-and-operators.md
upstream_url: https://github.com/google/googlesql/tree/master/docs
last_synced: 2026-09-25
testdata: testdata/specs/googlesql/docs_examples/string_functions.yaml
---

# Reference documentation examples

## Summary

Regression corpus built from every example in the upstream GoogleSQL
reference pages (`google/googlesql` `docs/*.md`): each SQL block paired
with its documented result table. Every file under
`testdata/specs/googlesql/docs_examples/` points at this spec; the
`testdata:` field above names one representative file.

## Signatures

Not applicable; the corpus spans every function, operator, and query
construct the reference pages exemplify.

## Behavior

`specctl extract-docs-examples` parses the pages. `TestDocsExamples`
(env-gated, see `docs/docs_examples_results.md`) replays each example,
types the documented cells with the column types the analyzer reports,
and compares. Examples that match are emitted here. Every example that
does not match is triaged in `testdata/docs_examples/classification.yaml`:

- examples that assume the America/Los_Angeles default time zone, use a
  feature BigQuery does not have, are non-deterministic, or whose table
  cannot be compared as printed are emitted here with a `skip:` reason
  and the documented expectation;
- examples where the docs contradict real BigQuery are emitted here with
  BigQuery's answer and a comment citing both;
- examples blocked by the analyzer or by a driver bug go to
  `testdata/specs_pending/googlesql/docs_examples/` with a `pending:`
  reason; the default suite does not run that directory.

## Examples

See the testdata files. Expected values are the documented result
tables or, for cases that say so, the answer real BigQuery returned;
never observed driver output.

## Edge cases

- Non-deterministic examples (RAND, CURRENT_*, ARRAY_AGG without ORDER
  BY, differential privacy, and similar) are not emitted.
- Examples whose documented floats are rounded for display are not
  emitted, because the spec runner compares floats to 1e-9.
- Rows are compared as a multiset unless the outermost query has an
  ORDER BY.

## Reference (upstream)

The upstream pages under `docs/third_party/googlesql-docs/` and
https://github.com/google/googlesql/tree/master/docs.
