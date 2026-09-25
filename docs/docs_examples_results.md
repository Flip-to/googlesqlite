# Reference documentation examples: divergence run

Every example in the upstream GoogleSQL reference pages (a SQL block
followed by its documented result table) was extracted, replayed on this
driver, and compared cell by cell with typed values. Every example that
does not pass as documented is triaged in
`testdata/docs_examples/classification.yaml`. Where the docs and
BigQuery disagree, BigQuery was queried with the `bq` CLI and the case
follows BigQuery's answer.

- Source: `google/googlesql` `docs/*.md` at commit `d82db99`
  (2026-09-15), 70 pages. The vendored snapshot under
  `docs/third_party/googlesql-docs/` is older (`36dd14a`); pass `--docs`
  to choose.
- Driver: `flipto/main` at `4dd0bf5` plus the fixes in this change.
- Tie-breaker for BigQuery applicability: the function list on the
  BigQuery "all functions" reference page (413 names), and, for syntax,
  the error BigQuery returns (for example "TABLE statements are not
  supported", "Pipe TEE not supported", "CAST operators are not
  supported").

## Totals

| measure | previous run (`274b9b1`) | this run |
|---|---:|---:|
| examples extracted | 3237 | 3227 |
| duplicates (`functions-and-operators.md` repeats the per-function pages) | 1258 | 1253 |
| already covered by an existing `testdata/specs` case (same normalised SQL) | 241 | 265 |
| new (not covered, not a duplicate) | 1847 | 1830 |
| pass, new | 1034 | 1213 |
| fail, new (silent) | 193 | 77 |
| error, new (loud) | 174 | 94 |
| skipped at extraction, new (non-deterministic, undefined fixtures, untypable cells) | 446 | 446 |

The extractor now keeps a same-line comment after `;` ("SELECT ...; --
Throws an error") with the statement it ends; before, it was read as the
next statement's comment, which paired "Throws an error" with `SAFE.`
calls that return NULL. That merges or drops ten mis-built examples,
hence 3227 instead of 3237.

### Where the 171 new non-passing examples went

| class | disposition | examples |
|---|---|---:|
| `not_in_bigquery` | skipped in `testdata/specs` | 116 |
| `docs_artifact` | skipped in `testdata/specs` | 14 |
| `la_timezone` | skipped in `testdata/specs` | 7 |
| `nondeterministic` | skipped in `testdata/specs` | 5 |
| `bigquery` | runs in `testdata/specs` with BigQuery's answer | 15 |
| `driver_bug` | pending in `testdata/specs_pending` | 10 |
| `analyzer` | pending in `testdata/specs_pending` | 4 |

Five more examples pass the typed comparison but are pinned to
BigQuery's exact output (`bigquery`, 20 in total): the numeric
`FORMAT` tables trim the fixed-width output that BigQuery returns.

### Regression tests emitted

- `testdata/specs/googlesql/docs_examples/*.yaml`: 1341 cases in 47
  files (was 1013). 1199 run under `TestSpec`: 1179 pass exactly as
  documented and 20 follow BigQuery, each with a comment citing the doc
  and the BigQuery answer. The other 142 carry a `skip:` reason and keep
  the documented expectation. The spec is
  `docs/specs/googlesql/syntax/docs_examples.md`.
- `testdata/specs_pending/googlesql/docs_examples/*.yaml`: 14 cases in
  4 files (was 367 in 33), each with a `pending:` reason. `TestSpec`
  walks only `testdata/specs`, so these files don't run by default.
- Not emitted although they pass the typed comparison: 7 whose
  documented floats are rounded for display (the spec runner compares to
  1e-9), and 22 the spec runner's string comparison cannot express (JSON
  objects whose keys the docs print sorted, `Infinity` printed for
  `+Inf`, and similar).
- `docs/docs_examples_failures.json` holds every fail and error: page,
  SQL, setup, documented result, driver result or exact error text,
  silent or loud, class and reason, BigQuery applicability, and dbt
  constructs.

## Driver fixes in this change

Each fix is covered by the docs example it was found with (now in
`testdata/specs/googlesql/docs_examples/`); where the docs could be
wrong, the behaviour was checked on BigQuery first.

| area | fix | examples |
|---|---|---|
| analyzer options | Enable bare `array[i]`, positional struct access (`s[0]`, `s[OFFSET(0)]`), the `WITH(...)` expression, and named `WINDOW` clauses in pipe `SELECT` / `EXTEND`; BigQuery accepts all four and the resolved trees need no formatter change | arrays#2, operators#2, operators#11, operators#72, operators#74, operators#76, pipe-syntax#7 |
| JSON conversions | `INT64`, `STRING` and `FLOAT64` of JSON `null` raise, as `BOOL` already did (BigQuery: "The provided JSON input is not an integer") | json_functions `INT64` / `STRING` sections |
| `LAX_*` | `LAX_INT64` rounds half away from zero (`3.5` is 4, `"+1.5"` is 2) and returns NULL outside the INT64 range (`1e100`); `LAX_FLOAT64` of a JSON boolean is NULL; `LAX_BOOL` matches "true"/"false" case-insensitively and nothing else (`"1"` is NULL) | json_functions `LAX_*` sections |
| JSON functions | `JSON_ARRAY_APPEND` appends to a JSON `null` member as to `[]`; `JSON_FLATTEN` flattens nested arrays recursively; `JSON_REMOVE(..., '$')` raises; `JSON_EXTRACT_ARRAY` re-serialises elements without whitespace; `PARSE_JSON` honours `wide_number_mode` (`exact` rejects a number FLOAT64 cannot round-trip, `round` stores the nearest FLOAT64); `TO_JSON` quotes wide INT64 values only with `stringify_wide_numbers=>TRUE` | json_functions#75, #137, #139, #190, #111, #420, #421, #438 (previous numbering) |
| `CAST(... AS INTERVAL)` | Accept the partial forms `Y-M`, `H:M:S[.F]`, `Y-M D`, `D H:M:S` and ISO 8601 durations (`P1Y2M3D`, `PT10H20M30,456S`) | conversion_functions#9 |
| `CAST(numeric AS STRING FORMAT ...)` | Implement the numeric format model: `0`, `9`, `.`/`D`, `,`/`G`, `S`, `MI`, `PR`, `$`/`L`/`C`, `FM`, `B`, with fixed width, sign slot, `#` overflow and half-away-from-zero rounding, all checked on BigQuery (`EEEE`, `V`, `X` and `RN` raise instead of being ignored) | format-elements#33 to #38 |
| `FORMAT_TIMESTAMP` | `%c` pads the day with a space (`Sun Nov  3 ...`); `%Z` prints the UTC offset (`UTC`, `UTC-7`, `UTC+0530`), not the zone abbreviation | data-types#14, data-types#15 |
| geography | Empty geographies print as `GEOMETRYCOLLECTION EMPTY` (an empty collection printed `GEOMETRYCOLLECTION ()`, which could not be read back); a collection of points reads back as `MULTIPOINT`; `ST_GEOGFROM` accepts hex WKB; `ST_AZIMUTH` of antipodal points is NULL; `ST_NUMGEOMETRIES` counts collection members | data-types#8, data-types#9, geography_functions#4, #21, #27, #34, #40 |

Harness fixes: a setup statement that is itself a query (a neighbouring
example in the same block) is no longer executed; a page fixture that
fails to build (a recursive CTE body turned into `CREATE TABLE`) is
dropped instead of failing the example; a header-only result box for a
query whose columns are all anonymous is read as the single row
(lexical.md, "Tokens in literals").

## Pending

| example | class | reason |
|---|---|---|
| aggregate-function-calls#7, aggregate_functions#29, #30, #31 | analyzer | BigQuery supports the aggregate `WHERE` modifier (`COUNT(DISTINCT x WHERE x > 0)` is 3 there), but go-googlesql v0.4.0 exposes no `where_expr` accessor on `ResolvedAggregateFunctionCall`; enabling `FEATURE_AGGREGATE_FILTERING` makes the analyzer accept the modifier while the filter is silently dropped, so it stays off. |
| geography_functions#10 | driver_bug | `ST_CONVEXHULL` is planar; the docs' S2 hull keeps geodesic vertices. |
| geography_functions#11 | driver_bug | `ST_COVERS` does not count a polygon vertex as covered. |
| geography_functions#18, #19 | driver_bug | Polygon and ring vertex order is not normalised the way BigQuery does (`POLYGON((0 0, 0 2, 2 2, 2 0, 0 0))` prints as `POLYGON((2 0, 2 2, 0 2, 0 0, 2 0))` on BigQuery). |
| geography_functions#23 | driver_bug | `ST_CONTAINS` of a point inside a polygon is FALSE; `oriented => TRUE` does not invert a clockwise ring. |
| geography_functions#24 | driver_bug | Self-intersecting polygons are accepted; `make_valid => TRUE` does not repair them. |
| geography_functions#39 | driver_bug | `ST_MAKELINE` of equal points or overlapping segments raises. |
| geography_functions#43 | driver_bug | `ST_SIMPLIFY` does not collapse degenerate results. |
| geography_functions#45 | driver_bug | `ST_UNION` of touching linestrings is not merged into one `LINESTRING`. |
| json_functions#469 | driver_bug | `JSON_VALUE` on a malformed STRING returns NULL; BigQuery stops at the path and returns `'world'` for `'{"hello": "world"'`. |

## Follows BigQuery, not the docs

Each answer below was observed on BigQuery with the `bq` CLI on
2026-09-25. The spec case carries the same note.

| example | docs | BigQuery |
|---|---|---|
| collation-concepts#5 | `orange1`, `orange2`, `orange3` | the inputs unchanged, with the invisible U+2060 |
| conversion_functions#13, #14, #16, #17 | bounds as typed literals (`[DATE '2020-01-01', ...)`) | `[2020-01-01, 2020-01-02)`; DATETIME bounds as `CAST(... AS STRING)` prints them |
| conversion_functions#26 | `PARSE_BIGNUMERIC("123.456E37")` returns a value | error: "Invalid input to PARSE_BIGNUMERIC" (1.23456e39 exceeds BIGNUMERIC) |
| data-types#15 | `Sun Nov 3 08:30:00 2024 UTC` | `Sun Nov  3 08:30:00 2024 UTC` |
| format-elements#30 | `'01:05:07.16'` with `FF1` parses | error: "Illegal non-space trailing data '6'" |
| format-elements#32 | `HH` without `AM`/`PM` parses | error: "Format element in category MERIDIAN_INDICATOR is required" |
| format-elements#33 to #35, #37, #38 | trimmed output (`12.50`) | fixed width with the sign slot (` 12.50`) |
| net_functions#7 | `NULL` | the STRING `'NULL'` that `FORMAT("%T", NULL)` returns |
| query-syntax#70 | `t-shirt` before `polo` under `ORDER BY product_name` | `polo` before `t-shirt` |
| string_functions#7, #11 | `CHR(0)` omitted | the NUL character |
| string_functions#109 | `SPLIT('', ' ')` is `[]` | `[""]` |
| string_functions#145 | `TRIM('Ū̊aba', 'Y̊')` drops the ring | input unchanged |

## Per page

Status of every extracted example (duplicates and covered examples
included), after this change.

| page | extracted | covered | new | pass | fail | error | skipped |
|---|---:|---:|---:|---:|---:|---:|---:|
| aggregate-dp-functions | 38 | 0 | 38 | 0 | 0 | 0 | 38 |
| aggregate-function-calls | 7 | 0 | 7 | 4 | 0 | 1 | 2 |
| aggregate_functions | 53 | 2 | 51 | 30 | 2 | 3 | 18 |
| approximate_aggregate_functions | 12 | 0 | 12 | 12 | 0 | 0 | 0 |
| array_functions | 76 | 9 | 67 | 76 | 0 | 0 | 0 |
| arrays | 46 | 0 | 45 | 31 | 0 | 2 | 13 |
| bit_functions | 5 | 0 | 5 | 3 | 2 | 0 | 0 |
| collation-concepts | 5 | 0 | 5 | 4 | 1 | 0 | 0 |
| conditional_expressions | 12 | 0 | 12 | 12 | 0 | 0 | 0 |
| conversion_functions | 39 | 0 | 35 | 20 | 6 | 5 | 8 |
| data-definition-language | 2 | 0 | 2 | 0 | 0 | 1 | 1 |
| data-manipulation-language | 4 | 0 | 4 | 0 | 0 | 0 | 4 |
| data-model | 3 | 0 | 3 | 2 | 0 | 0 | 1 |
| data-types | 16 | 7 | 9 | 8 | 2 | 1 | 5 |
| date_functions | 41 | 1 | 40 | 27 | 0 | 7 | 7 |
| datetime_functions | 38 | 0 | 38 | 26 | 0 | 7 | 5 |
| debugging_functions | 23 | 19 | 4 | 23 | 0 | 0 | 0 |
| differential-privacy | 1 | 0 | 1 | 0 | 0 | 0 | 1 |
| format-elements | 38 | 0 | 34 | 30 | 1 | 2 | 5 |
| functions-and-operators | 1347 | 125 | 124 | 4 | 1 | 21 | 1321 |
| functions-reference | 2 | 0 | 2 | 2 | 0 | 0 | 0 |
| geography_functions | 47 | 21 | 26 | 33 | 9 | 1 | 4 |
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
| json_functions | 472 | 9 | 463 | 430 | 31 | 2 | 9 |
| lexical | 12 | 0 | 12 | 12 | 0 | 0 | 0 |
| mathematical_functions | 23 | 0 | 23 | 23 | 0 | 0 | 0 |
| navigation_functions | 13 | 0 | 13 | 2 | 0 | 0 | 11 |
| net_functions | 7 | 0 | 7 | 6 | 1 | 0 | 0 |
| numbering_functions | 13 | 0 | 13 | 5 | 4 | 0 | 4 |
| operators | 76 | 0 | 64 | 42 | 1 | 8 | 25 |
| pipe-syntax | 66 | 1 | 65 | 51 | 1 | 7 | 7 |
| procedural-language | 1 | 0 | 1 | 0 | 0 | 0 | 1 |
| protocol-buffers | 5 | 0 | 4 | 0 | 0 | 0 | 5 |
| protocol_buffer_functions | 24 | 7 | 17 | 4 | 1 | 3 | 16 |
| query-syntax | 145 | 6 | 138 | 105 | 2 | 7 | 31 |
| range-functions | 27 | 27 | 0 | 17 | 2 | 0 | 8 |
| recursive-ctes | 5 | 0 | 5 | 3 | 0 | 1 | 1 |
| security_functions | 1 | 0 | 1 | 0 | 0 | 0 | 1 |
| statistical_aggregate_functions | 36 | 0 | 36 | 35 | 0 | 0 | 1 |
| string_functions | 148 | 26 | 122 | 140 | 7 | 0 | 1 |
| subqueries | 11 | 0 | 10 | 0 | 0 | 1 | 10 |
| time-series-functions | 6 | 0 | 6 | 4 | 0 | 0 | 2 |
| time_functions | 13 | 0 | 11 | 10 | 0 | 0 | 3 |
| timestamp_functions | 42 | 2 | 39 | 31 | 6 | 0 | 5 |
| user-defined-aggregates | 1 | 0 | 1 | 1 | 0 | 0 | 0 |
| user-defined-functions | 20 | 0 | 20 | 9 | 1 | 9 | 1 |
| window-function-calls | 15 | 0 | 15 | 13 | 0 | 0 | 2 |

## Skip reasons at extraction

| skip reason | count |
|---|---:|
| duplicate of an example on another page | 1253 |
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
3. Every example that does not pass is looked up in
   `testdata/docs_examples/classification.yaml` and written to
   `yaml/specs` (passes, `skip:` classes, and `bigquery` cases that
   match BigQuery's answer) or `yaml/pending` (`analyzer`,
   `driver_bug`, unclassified) under the output directory.
4. `tools/docs_examples_report.py` builds the tables here and the
   tagged failure JSON.

To reproduce (Windows needs the TZDIR export):

```bash
export MSYS2_ENV_CONV_EXCL=TZDIR TZDIR=/tmp/zoneinfo
go run ./cmd/specctl extract-docs-examples --docs ../googlesql/docs --out /tmp/de/extracted.json
GOOGLESQLITE_DOCS_EXAMPLES_SOURCE="google/googlesql@d82db99 docs" \
GOOGLESQLITE_DOCS_EXAMPLES=/tmp/de/extracted.json go test -p 1 -run TestDocsExamples -count=1 -timeout 60m .
cp /tmp/de/yaml/specs/*.yaml testdata/specs/googlesql/docs_examples/
cp /tmp/de/yaml/pending/*.yaml testdata/specs_pending/googlesql/docs_examples/
python3 tools/docs_examples_report.py /tmp/de/results.json bq_functions.txt /tmp/de/failures_tagged.json
```

## Limitations

- Pages that reference tables, property graphs, or proto types defined
  nowhere in the docs are skipped at extraction (177 examples, 124 of
  them graph queries).
- Example IDs (`page#ordinal`) depend on the extractor and the docs
  commit. `TestDocsExamples` prints a warning when a classified example
  starts to pass, so stale entries in the classification file surface.
- JSON values compare semantically in the typed comparison, so key order
  is ignored there. BigQuery prints JSON objects with sorted keys; the
  driver keeps insertion order, which is why 22 typed passes stay out of
  `testdata/specs`.
