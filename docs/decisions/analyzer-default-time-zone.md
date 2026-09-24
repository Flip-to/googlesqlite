# Analyzer default time zone

## Status

Accepted. Driver-side re-evaluation of folded literals in UTC
(option 3 below).

## Problem

BigQuery's default time zone is UTC: a TIMESTAMP string without a zone
is read as UTC, and TIMESTAMP to DATE / DATETIME / TIME / STRING
conversions use UTC unless a zone is given.

The GoogleSQL analyzer constant-folds literal casts and literal
coercions while it resolves a statement, and it does so in its own
default time zone. Upstream hard-codes that default to
`America/Los_Angeles` in the `AnalyzerOptions` constructor. With
folding on, for example:

| Expression | Folded by the analyzer | BigQuery |
|---|---|---|
| `CAST('2024-01-01 10:00:00' AS TIMESTAMP)` | `2024-01-01 18:00:00+00` | `2024-01-01 10:00:00+00` |
| `CAST(TIMESTAMP '2024-01-01 03:00:00+00' AS DATE)` | `2023-12-31` | `2024-01-01` |
| `CAST(TIMESTAMP '2024-01-01 03:00:00+00' AS STRING)` | `2023-12-31 19:00:00-08` | `2024-01-01 03:00:00+00` |
| `ts_col > '2024-01-01 10:00:00'` (string coerced to TIMESTAMP) | `2024-01-01 18:00:00+00` | `2024-01-01 10:00:00+00` |

The earlier workaround turned literal-cast folding off
(`SetFoldLiteralCast(false)`) for every statement matching
`\bTIMESTAMP\b`, with a retry that turned it back on when the analyzer
then failed with "Invalid cast from" (an unfolded bare NULL is typed
INT64 and cannot be coerced to GEOGRAPHY). That changed other folded
behavior in those statements (a float literal cast to NUMERIC went
through DOUBLE and lost digits; `CastNode.FormatSQL` reads the literal
text back to fix that), and it did not cover string literals coerced
to TIMESTAMP in statements without the keyword.

## Why go-googlesql v0.4.0 cannot set UTC

`absl::TimeZone` is an opaque external type in the generated bindings.
The only exported ways to obtain one are
`AnalyzerOptions.DefaultTimeZone()` (a borrowed pointer to the options'
own zone, which is Los Angeles) and `SetDefaultTimeZone(*TimeZone)`,
which needs a zone to pass. `absl::UTCTimeZone`, `FixedTimeZone`,
`LoadTimeZone`, `googlesql::functions::MakeTimeZone` and
`AnalyzerOptions::Deserialize` are not bound, and the proto setter for
`default_timezone` was generated with a broken signature. The binding
code is generated from the wasm build, so adding a binding needs a new
wasm build (see
[analyzer-wasm-rebuild-plan.md](analyzer-wasm-rebuild-plan.md)).

## Options considered

1. **Rebuild the analyzer.** Fork `goccy/googlesql-wasm`,
   `goccy/googlesqlwasm2go` and `goccy/go-googlesql`, bump googlesql
   from 2026.01.1 to 2026.9.2, export a TimeZone-returning function, and
   rebuild the wasm on GitHub Actions. This is the clean fix and would
   also bring upstream fixes for pipe AGGREGATE and MATCH_RECOGNIZE.
   Cost: about two cold CI builds of about 3 hours each, a third fork to
   maintain, an 8-month upstream jump with expected churn in the
   generated API and in analyzer behavior, and `replace` directives that
   downstream consumers do not inherit. Full plan in
   [analyzer-wasm-rebuild-plan.md](analyzer-wasm-rebuild-plan.md).
2. **Wasm-memory stopgap.** In a go-googlesql fork, write a null
   `cctz::time_zone` implementation pointer (which means UTC) at the
   address returned by `DefaultTimeZone()`. No C++ work, but it depends
   on the absl/cctz memory layout and on handles being raw linear-memory
   addresses, and it still needs a fork.
3. **Driver-side re-evaluation (chosen).** Keep folding on and correct
   the folded values in the formatter.

## Decision

Literal-cast folding is on for every statement. The driver makes each
value that the analyzer folded in America/Los_Angeles come out as
BigQuery computes it in UTC:

- `TIMESTAMP '...'` and `RANGE<TIMESTAMP> '...'` literals without a zone
  are rewritten to carry `+00:00` before analysis
  (`applyNaiveTimestampUTC` in `internal/analyzer.go`).
- In the formatter (`utcLiteralSQL` in
  `internal/formatter_utc_literal.go`), a `ResolvedLiteral` whose value
  may depend on the zone has its source text recovered through its parse
  location, the same mechanism the NUMERIC fix uses
  (`withSourceQuery`). The text is evaluated with the runtime CAST,
  which works in UTC, and the result is emitted instead of the folded
  value. The shapes, found by comparing resolved ASTs with folding on
  and off, are:
  - `CAST` / `SAFE_CAST` of a literal expression to TIMESTAMP (from
    STRING, DATE, DATETIME, TIMESTAMP), including nested casts, which
    fold into one literal spanning the outermost cast;
  - `CAST` / `SAFE_CAST` from a TIMESTAMP to DATE, DATETIME, TIME or
    STRING (only checked when the statement contains the TIMESTAMP
    keyword);
  - a STRING literal coerced to TIMESTAMP (comparisons, TIMESTAMP
    function arguments, INSERT values).
- Function calls are never folded: `DATE(ts)`, `STRING(ts)`,
  `EXTRACT`, `TIMESTAMP_ADD` / `SUB` / `DIFF` / `TRUNC`,
  `FORMAT_TIMESTAMP` and so on reach the runtime and already use UTC.
  `CAST ... FORMAT` is not folded either.
- ARRAY and STRUCT literals with TIMESTAMP-derived members that come
  from a cast or a coerced string (for example
  `ARRAY<TIMESTAMP>['2024-01-01 10:00:00']`,
  `CAST(('2024-01-01 10:00:00', 1) AS STRUCT<TIMESTAMP, INT64>)`), and
  any scalar shape the evaluator does not understand, fall back to the
  old approach for that statement only: it is analyzed a second time
  with folding off, so the casts run at runtime. The fallback is used
  only for queries and DML (their statement actions have no side
  effects); if the unfolded analysis fails, the folded form is kept.

Statements that contain no zone-dependent folded literal are analyzed
once, as before; the check per literal is a type lookup, so the bench
suite is unchanged within noise.

## Limits

- The fallback path (composite literals, unknown shapes) still analyzes
  those statements with folding off, with the side effects described
  above. The NUMERIC / BIGNUMERIC literal-cast case is still handled by
  `CastNode.FormatSQL` there.
- For DDL and other statement kinds, a folded composite literal of an
  unsupported shape keeps the analyzer's Los Angeles value.
- The GoogleSQL compliance suite assumes an America/Los_Angeles default
  zone; its TIMESTAMP-to-string and naive-TIMESTAMP cases disagree with
  BigQuery and are expected to fail here.

## When to revisit

- A `goccy/googlesql-wasm` / `goccy/go-googlesql` release newer than
  v0.3.4 / v0.4.0 that exposes a way to build a UTC TimeZone (or to set
  the default zone by name). Then call `SetDefaultTimeZone` with UTC and
  remove `utcLiteralSQL`, the fallback, and the naive-literal rewrite.
- The upstream fixes behind `pipe_aggregate_mixed_expression`,
  `pipe_aggregate_correlated_subquery`, nested MATCH_RECOGNIZE, or the
  collation binding bugs start to matter. Those need the rebuild in
  option 1 anyway, and the TimeZone export should ride along.
