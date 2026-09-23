# Reference documentation examples: divergence run

Every example in the upstream GoogleSQL reference pages (a SQL block
followed by its documented result table) was extracted, replayed on this
driver, and compared cell by cell with typed values. Offline; no
BigQuery queries were run.

- Source: `google/googlesql` `docs/*.md` at commit `d82db99`
  (2026-09-15), 70 pages. The vendored snapshot under
  `docs/third_party/googlesql-docs/` is older (`36dd14a`); pass `--docs`
  to choose.
- Driver: `flipto/main` at `274b9b1`.
- Tie-breaker for BigQuery applicability: the function list on the
  BigQuery "all functions" reference page (413 names). A failure is
  GoogleSQL-only when it calls a function missing from that list
  (`MAP_FROM_ARRAY`, `ADD_MONTHS`, `SPLIT_SUBSTR`,
  `REGEXP_EXTRACT_GROUPS`, `LAX_FLOAT`, ...) or returns a type BigQuery
  lacks (`INT32`, `UINT64`, `FLOAT`, `PROTO`, `ENUM`).

## Totals

| measure | count |
|---|---:|
| examples extracted | 3237 |
| duplicates (`functions-and-operators.md` repeats the per-function pages) | 1258 |
| already covered by an existing `testdata/specs` case (same normalised SQL) | 241 |
| new (not covered, not a duplicate) | 1847 |
| pass (all) | 1135 |
| fail, silent wrong answer (all) | 199 |
| error, loud (all) | 176 |
| skipped (all, including duplicates) | 1727 |
| pass (new) | 1034 |
| fail (new) | 193 |
| error (new) | 174 |
| skipped (new) | 446 |

Of the 375 fail + error results, 253 are BigQuery-applicable (131
silent, 116 loud, 6 order-only) and 122 are GoogleSQL-only.

"Order-only" means the rows match but their order differs under an ORDER
BY. In every such case seen, the sort key has ties, so the order is
unspecified; these are listed but not counted as silent divergences.

### Regression tests emitted

- `testdata/specs/googlesql/docs_examples/*.yaml`: 1013 cases in 43
  files. These are the new passing examples, each round-tripped through
  the spec runner's own comparison, and all pass under `TestSpec`. The
  spec is `docs/specs/googlesql/syntax/docs_examples.md`.
- Not emitted although they pass the typed comparison: 3 whose
  documented floats are rounded for display (the spec runner compares to
  1e-9), and 18 the spec runner's string comparison cannot express (for
  example JSON objects with a different key order).
- `testdata/specs_pending/googlesql/docs_examples/*.yaml`: 367 failing
  new cases in 33 files, same format. Each file's header lists the
  driver's result per case. `TestSpec` walks only `testdata/specs`, so
  these files don't run by default.
- `docs/docs_examples_failures.json` holds every fail and error: page,
  SQL, setup, documented result, driver result or exact error text,
  silent or loud, BigQuery applicability, and dbt constructs.

## Top 20 BigQuery-applicable divergences

Ranked by the dbt construct list (the differential-probe results file
was not yet published, so the fallback list was used), then silent
before loud. Each entry is one root cause; the count in parentheses is
the number of examples that hit it.

| # | example | minimal SQL | documented | driver | kind |
|---|---|---|---|---|---|
| 1 | array_functions#70 (4) | `SELECT GENERATE_TIMESTAMP_ARRAY('2016-10-05 00:00:00', '2016-10-07 00:00:00', INTERVAL 1 DAY)` | `[2016-10-05 00:00:00+00, ...]` | `[2016-10-05 07:00:00+00, ...]` | silent |
| 2 | format-elements#11 (30) | `SELECT CAST(DATE '2018-01-30' AS STRING FORMAT 'YYYY')` | `2018` | `2018-01-30` (FORMAT clause ignored) | silent |
| 3 | timestamp_functions#3 (8) | `SELECT EXTRACT(WEEK FROM TIMESTAMP("2005-01-03 12:34:56+00"))` | `1` | `53` | silent |
| 4 | string_functions#109 | `SELECT SPLIT('', ' ')` | `[]` | `[""]` | silent |
| 5 | data-types#3 | `SELECT CAST(NULL AS ARRAY<INT64>)` in a result | `[]` | `NULL` | silent |
| 6 | string_functions#68 | `SELECT REGEXP_EXTRACT('ab', '(z)?b')` | `NULL` | `''` | silent |
| 7 | statistical_aggregate_functions#24 (4) | `SELECT STDDEV_SAMP(x) FROM UNNEST([10, NULL]) AS x` | `NULL` | `NaN` | silent |
| 8 | aggregate-function-calls#2 (2) | `SELECT AVG(inches HAVING MAX year) FROM Precipitation` | `5` | `4.5` (HAVING MAX ignored) | silent |
| 9 | json_functions#334 (4) | `SELECT LAX_INT64(JSON '3.5')` | `4` | `3` | silent |
| 10 | json_functions#111 | `SELECT JSON_EXTRACT_ARRAY('{"fruit": [{"apples": 5, ...}]}', '$.fruit')` | `[{"apples":5,"oranges":10}, ...]` | `["{\"apples\": 5, \"oranges\": 10}", ...]` | silent |
| 11 | json_functions#208 (8) | `SELECT JSON_STRIP_NULLS(JSON '[1, null, 2, null]')` | `[1,2]` | `[1,null,2,null]` | silent |
| 12 | json_functions#430 (11) | `SELECT STRING(JSON 'null')` | error | `NULL` | silent |
| 13 | json_functions#437 (2) | `SELECT TO_JSON(9007199254740993, stringify_wide_numbers=>TRUE)` | `"9007199254740993"` | `9007199254740993` | silent |
| 14 | json_functions#443 | `SELECT TO_JSON_STRING(STRUCT(1 AS id, [10,20] AS coordinates), true)` | pretty-printed, 7 lines | `{"id":1,"coordinates":[10,20]}` | silent |
| 15 | json_functions#477 | `SELECT JSON_QUERY(JSON '{"name": null}', "$.name") IS NULL` | `false` (JSON `null`) | `true` (SQL NULL) | silent |
| 16 | mathematical_functions#9 | `SELECT SAFE.COT(0)` | `NULL` | `+Inf` | silent |
| 17 | data-types#15 (2) | `SELECT FORMAT_TIMESTAMP("%c %Z", "2024-11-03 01:30:00 America/Los_Angeles", "UTC")` | `Sun Nov 3 08:30:00 2024 UTC` | `Sun Nov 03 08:30:00 2024 UTC` | silent |
| 18 | query-syntax#80 (7) | `SELECT 1 AS one_digit, 10 AS two_digit UNION ALL BY NAME SELECT 20 AS two_digit, 2 AS one_digit` | 2 rows | `failed to analyze: BY NAME for set operations is not supported [at 2:11]` | loud |
| 19 | subqueries#5 (2) | `SELECT 'corba' LIKE ANY (SELECT chars FROM Words)` | `TRUE` | `failed to analyze: The LIKE ANY\|SOME\|ALL operator does not support subquery expression as patterns. Patterns must be string or bytes; did you mean LIKE ANY (pattern1, pattern2, ...)? [at 5:25]` | loud |
| 20 | pipe-syntax#38 | `SELECT * FROM UNNEST([1, 2, 3, 3, 4]) AS number \|> INTERSECT ALL (SELECT * FROM UNNEST([2, 3, 3, 5]) AS number)` | `2, 3, 3` | `sqlite3: SQL logic error: near "ALL": syntax error` | loud |

Notes on the list:

1. The zone-less string is read as America/Los_Angeles (the GoogleSQL
   reference default) instead of UTC. A direct probe shows the driver
   is inconsistent: `TIMESTAMP '2016-10-05 00:00:00'` gives
   `2016-10-05 00:00:00+00`, but `CAST('2016-10-05 00:00:00' AS
   TIMESTAMP)` gives `2016-10-05 07:00:00+00` and `CAST(TIMESTAMP
   '2016-10-05 00:00:00' AS STRING)` gives `2016-10-04 17:00:00-07`.
   BigQuery uses UTC for all three. This is the highest-risk item for
   dbt models that cast strings to TIMESTAMP.
2. Every `CAST(... AS STRING FORMAT ...)` example on format-elements
   and conversion_functions fails. Date, time and numeric format models
   are ignored silently, and several string-to-date/time forms error
   (for example `unexpected numeric literal: .1234`).
3. `EXTRACT(WEEK ...)` also disagrees in datetime_functions#16; in five
   timestamp_functions examples the docs table omits a column, so those
   fail on column count.

Other loud gaps that don't make the top 20 but are BigQuery-applicable:
`CORRESPONDING` for set operations, `WHERE` modifiers inside aggregate
calls (`COUNT(DISTINCT x WHERE x > 0)`), `LATERAL` joins, pipe `WITH` /
`TEE` / `DESCRIBE` and pipe `WINDOW` clauses, `array[position]` element
access, and a `reflect: call of reflect.Value.Type on zero Value` panic
in two JSON examples. `HLL_COUNT.INIT` sketches (hll_functions#2, #4)
differ byte-for-byte from the docs; cardinality estimates built from
them were not checked.

## dbt constructs among BigQuery-applicable fail + error

| construct | BigQuery-applicable fail+error |
|---|---:|
| SAFE_DIVIDE | 0 |
| UNNEST join | 1 |
| QUALIFY | 0 |
| FORMAT | 0 |
| HLL_COUNT | 2 |
| ARRAY_AGG | 0 |
| GENERATE_DATE_ARRAY | 1 |
| IS DISTINCT FROM | 0 |
| window function | 4 |
| set operation | 14 |
| TO_JSON_STRING | 1 |
| FARM_FINGERPRINT | 0 |
| APPROX_QUANTILES | 0 |
| NUMERIC | 1 |
| DATE/TIMESTAMP function | 14 |
| COALESCE | 0 |
| LIKE | 4 |
| SPLIT | 1 |
| REGEXP_* | 1 |
| CAST ... FORMAT | 30 |
| JSON function | 45 |

## Per page

| page | extracted | covered | new | pass | fail | error | skipped |
|---|---:|---:|---:|---:|---:|---:|---:|
| aggregate-dp-functions | 38 | 0 | 38 | 0 | 0 | 0 | 38 |
| aggregate-function-calls | 7 | 0 | 7 | 2 | 2 | 1 | 2 |
| aggregate_functions | 53 | 2 | 51 | 30 | 2 | 3 | 18 |
| approximate_aggregate_functions | 12 | 0 | 12 | 12 | 0 | 0 | 0 |
| array_functions | 76 | 9 | 67 | 71 | 5 | 0 | 0 |
| arrays | 46 | 0 | 45 | 30 | 0 | 3 | 13 |
| bit_functions | 5 | 0 | 5 | 3 | 2 | 0 | 0 |
| collation-concepts | 5 | 0 | 5 | 2 | 3 | 0 | 0 |
| conditional_expressions | 12 | 0 | 12 | 12 | 0 | 0 | 0 |
| conversion_functions | 39 | 0 | 35 | 10 | 8 | 13 | 8 |
| data-definition-language | 2 | 0 | 2 | 0 | 0 | 1 | 1 |
| data-manipulation-language | 4 | 0 | 4 | 0 | 0 | 0 | 4 |
| data-model | 3 | 0 | 3 | 2 | 0 | 0 | 1 |
| data-types | 16 | 7 | 9 | 5 | 5 | 1 | 5 |
| date_functions | 41 | 1 | 40 | 25 | 1 | 8 | 7 |
| datetime_functions | 38 | 0 | 38 | 24 | 1 | 8 | 5 |
| debugging_functions | 23 | 19 | 4 | 23 | 0 | 0 | 0 |
| differential-privacy | 1 | 0 | 1 | 0 | 0 | 0 | 1 |
| format-elements | 38 | 0 | 34 | 5 | 21 | 7 | 5 |
| functions-and-operators | 1352 | 113 | 124 | 4 | 1 | 21 | 1326 |
| functions-reference | 2 | 0 | 2 | 2 | 0 | 0 | 0 |
| geography_functions | 47 | 21 | 26 | 28 | 10 | 5 | 4 |
| graph-data-types | 1 | 1 | 0 | 0 | 0 | 0 | 1 |
| graph-gql-functions | 26 | 0 | 26 | 5 | 0 | 0 | 21 |
| graph-intro | 3 | 0 | 3 | 0 | 0 | 0 | 3 |
| graph-operators | 13 | 0 | 13 | 3 | 0 | 0 | 10 |
| graph-patterns | 55 | 0 | 55 | 9 | 0 | 1 | 45 |
| graph-query-statements | 64 | 0 | 57 | 6 | 0 | 0 | 58 |
| graph-schema-statements | 1 | 0 | 1 | 0 | 0 | 0 | 1 |
| graph-sql-queries | 5 | 0 | 5 | 2 | 0 | 3 | 0 |
| graph-subqueries | 6 | 0 | 6 | 0 | 0 | 0 | 6 |
| hash_functions | 5 | 0 | 5 | 3 | 1 | 1 | 0 |
| hll_functions | 4 | 2 | 2 | 2 | 2 | 0 | 0 |
| interval_functions | 7 | 0 | 7 | 7 | 0 | 0 | 0 |
| json_functions | 477 | 9 | 468 | 355 | 99 | 14 | 9 |
| lexical | 12 | 0 | 12 | 5 | 3 | 4 | 0 |
| mathematical_functions | 23 | 0 | 23 | 15 | 4 | 4 | 0 |
| navigation_functions | 13 | 0 | 13 | 2 | 0 | 0 | 11 |
| net_functions | 7 | 0 | 7 | 7 | 0 | 0 | 0 |
| numbering_functions | 13 | 0 | 13 | 5 | 4 | 0 | 4 |
| operators | 76 | 0 | 64 | 34 | 0 | 17 | 25 |
| pipe-syntax | 66 | 1 | 65 | 42 | 0 | 17 | 7 |
| procedural-language | 1 | 0 | 1 | 0 | 0 | 0 | 1 |
| protocol-buffers | 5 | 0 | 4 | 0 | 0 | 0 | 5 |
| protocol_buffer_functions | 24 | 7 | 17 | 4 | 1 | 3 | 16 |
| query-syntax | 145 | 6 | 138 | 84 | 2 | 28 | 31 |
| range-functions | 27 | 27 | 0 | 18 | 1 | 0 | 8 |
| recursive-ctes | 5 | 0 | 5 | 3 | 0 | 1 | 1 |
| security_functions | 1 | 0 | 1 | 0 | 0 | 0 | 1 |
| statistical_aggregate_functions | 36 | 0 | 36 | 31 | 4 | 0 | 1 |
| string_functions | 148 | 14 | 134 | 137 | 10 | 0 | 1 |
| subqueries | 11 | 0 | 10 | 0 | 0 | 1 | 10 |
| time-series-functions | 6 | 0 | 6 | 4 | 0 | 0 | 2 |
| time_functions | 13 | 0 | 11 | 10 | 0 | 0 | 3 |
| timestamp_functions | 42 | 2 | 39 | 30 | 6 | 1 | 5 |
| user-defined-aggregates | 1 | 0 | 1 | 0 | 0 | 1 | 0 |
| user-defined-functions | 20 | 0 | 20 | 9 | 1 | 9 | 1 |
| window-function-calls | 15 | 0 | 15 | 13 | 0 | 0 | 2 |

## Skip reasons

| skip reason | count |
|---|---:|
| duplicate of an example on another page | 1258 |
| references a property graph that the docs page never defines | 124 |
| differential privacy adds noise | 80 |
| result table has a line outside the pipe grid | 43 |
| references a proto or enum type that is not registered | 33 |
| ARRAY_AGG without ORDER BY has unspecified element order | 21 |
| fixture definition without a query | 19 |
| references a table that the docs page never defines | 19 |
| CURRENT_* depends on the clock | 18 |
| result table has no header | 16 |
| LIMIT without ORDER BY picks arbitrary rows | 12 |
| docs assume the America/Los_Angeles default time zone; the driver uses UTC, as BigQuery does | 11 |
| ANY_VALUE picks an arbitrary row | 10 |
| STRING_AGG without ORDER BY has unspecified element order | 10 |
| result row and header cell counts differ (multi-line or pipe-containing cell) | 8 |
| cannot type documented cell as TIMESTAMP | 6 |
| ARRAY_CONCAT_AGG without ORDER BY has unspecified element order | 5 |
| final statement is not a query | 5 |
| block is marked {.bad} but carries a result table | 4 |
| cannot type documented cell as ARRAY<RANGE<DATE>> | 4 |
| result table elides values with ... | 3 |
| RAND() is non-deterministic | 3 |
| cannot type documented cell as ARRAY<BOOL> | 2 |
| SESSION_USER depends on the caller | 2 |
| cannot type documented cell as INT64 | 2 |
| cannot type documented cell "[{point: [1,5]}," as ARRAY<STRUCT<point ARRAY<INT64>>>: not an ARRAY literal | 1 |
| UUID generation is non-deterministic | 1 |
| cannot type documented cell as ARRAY<INT64> | 1 |
| cannot type documented cell as ARRAY<DOUBLE> | 1 |
| cannot type documented cell "{ {blue color, round shape} info }" as STRUCT<color STRING, shape STRING>: struct has 1 fields, type has 2 | 1 |
| procedural script statement | 1 |
| cannot type documented cell "{" as STRUCT<last_name STRING, first_name STRING, age INT64>: not a STRUCT literal | 1 |
| cannot type documented cell "[{" as ARRAY<STRUCT<last_name STRING, first_name STRING, age INT64>>: not an ARRAY literal | 1 |
| cannot type documented cell as DOUBLE | 1 |


## Method

1. `go run ./cmd/specctl extract-docs-examples --docs <googlesql>/docs --out extracted.json`
   parses each page into (page, section, line, setup, SQL, columns,
   rows). It handles:
   - multi-statement blocks, including statements separated only by
     blank lines;
   - `-- Error:` comments, per-statement comments ("-- Produces an
     error ...", "-- This works ..."), and prose such as "produces an
     error" in the paragraph above a block;
   - result tables with and without the `/* */` wrapper, and tables in
     a separate block after the SQL;
   - page fixtures: `WITH T AS (...) SELECT * FROM T` blocks, bare
     `WITH T AS (...)` blocks, `CREATE TABLE` / `INSERT` statements,
     and `CREATE FUNCTION` statements referenced by later examples.
     Other definitions of the same name on the page or on other pages
     become alternative setups the runner tries in turn.
   Non-deterministic examples are marked skipped with the reason. Dedup
   against `testdata/specs` uses normalised SQL: comments dropped,
   whitespace collapsed, case folded, quotes unified.
2. `TestDocsExamples` (skipped unless `GOOGLESQLITE_DOCS_EXAMPLES` is
   set) runs each example on a fresh `:memory:` connection. It types
   every documented cell with the column type the analyzer reports
   (NULL, INT64, FLOAT64, NUMERIC, BOOL, STRING, BYTES, DATE, DATETIME,
   TIME, TIMESTAMP, JSON, GEOGRAPHY, INTERVAL, RANGE, ARRAY, STRUCT),
   then compares:
   - as an ordered list when the outermost query has an ORDER BY,
     otherwise as a multiset;
   - floats to 1e-9, or to the printed precision when the docs show
     2 or more significant digits after a decimal point.
   A cell that cannot be typed becomes a skip with the reason, never a
   pass. An example whose setup creates a TEMP object runs as one
   script, because TEMP functions and tables last for one
   multi-statement script, as in BigQuery outside a session.
3. `tools/docs_examples_report.py` builds the tables here and the
   tagged failure JSON.

To reproduce (Windows needs the TZDIR export):

```bash
export MSYS2_ENV_CONV_EXCL=TZDIR TZDIR=/tmp/zoneinfo
go run ./cmd/specctl extract-docs-examples --docs ../googlesql/docs --out /tmp/de/extracted.json
GOOGLESQLITE_DOCS_EXAMPLES=/tmp/de/extracted.json go test -run TestDocsExamples -count=1 -timeout 60m .
python3 tools/docs_examples_report.py /tmp/de/results.json bq_functions.txt /tmp/de/failures_tagged.json
```

## Limitations

- Pages that reference tables, property graphs, or proto types defined
  nowhere in the docs are skipped (177 examples, 124 of them graph
  queries).
- 11 examples print TIMESTAMP results under an America/Los_Angeles
  session zone where the driver, like BigQuery, uses UTC. These are
  skipped, not counted as divergences.
- A few pending cases may still be fixture mix-ups (for example
  `functions-and-operators#1185` reads an `items` table defined for a
  different section).
- JSON values compare semantically, so key order is ignored. The 18
  passes whose key order differs from the docs are kept out of
  `testdata/specs`.
