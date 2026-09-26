# Flip-to fork of googlesqlite

This repository is Flip-to's fork of
[`goccy/googlesqlite`](https://github.com/goccy/googlesqlite). We use
it as the local, zero-cost execution backend for BigQuery-dialect SQL
in dbt tests. The fork carries fixes that have not landed upstream yet,
so that downstream projects can depend on one revision that has all of
them.

The public surface is unchanged: driver name `googlesqlite`, the Go
module path `github.com/goccy/googlesqlite`, the DSN format, and the
`Driver` / `Conn` / `Tx` types. Code written against upstream works
against the fork.

## Branches and remotes

| remote | repository | purpose |
|---|---|---|
| `origin` | `goccy/googlesqlite` | upstream |
| `flipto` | `Flip-to/googlesqlite` | this fork |
| `fork` | a personal fork | head branches for upstream PRs |

- `flipto/main` on `Flip-to/googlesqlite` is the branch consumers
  should use. It is upstream `main` plus everything in the changelog
  below.
- Fixes are developed on `fix/<topic>` branches cut from `flipto/main`
  and merged back through a PR on `Flip-to/googlesqlite`.
- Fixes that are useful upstream are also proposed to
  `goccy/googlesqlite`. The changelog records the upstream PR where
  one exists.

## Using the fork

Keep the upstream import path and redirect it with a `replace`
directive:

```bash
go mod edit -replace github.com/goccy/googlesqlite=github.com/Flip-to/googlesqlite@<commit>
go mod tidy
```

Use a commit hash from `flipto/main`. `go mod tidy` turns it into a
pseudo-version and pins it in `go.mod`.

### Windows

Set these before running tests. Without them the analyzer fails with
`wasm trap: wasm: unreachable`.

```bash
export MSYS2_ENV_CONV_EXCL=TZDIR TZDIR=/tmp/zoneinfo
```

`/tmp/zoneinfo` must hold an unpacked zoneinfo tree. Go's
`$(go env GOROOT)/lib/time/zoneinfo.zip` works:

```bash
mkdir -p /tmp/zoneinfo
unzip -oq "$(go env GOROOT)/lib/time/zoneinfo.zip" -d /tmp/zoneinfo
```

## Compliance suite

`specctl run-compliance` runs Google's GoogleSQL compliance suite
(`google/googlesql`, `googlesql/compliance/testdata/*.test`) through
the driver, offline. It filters out cases that need features BigQuery
does not have, and it ranks failures by how likely they are to be
silent wrong answers. See `docs/compliance_run_results.md` for the
latest report and the exact command.

Every fix PR should re-run the suite, check that no previously passing
case now fails, and record the before and after pass counts in its
changelog entry below.

## Changelog

Newest first. "Landed in" links the commit on `flipto/main` that
brought the change in; for a PR it is the merge commit. Pass counts are
for the compliance suite with the host on UTC-3. "Upstream" names the
matching PR on `goccy/googlesqlite`, or says "none" if there isn't one.

A new PR adds its row with "this PR" in the "Landed in" column. The
next PR replaces that with the merge commit.

### Fixes from compliance-suite divergences

| change | landed in | Flip-to PR | upstream | compliance passed |
|---|---|---|---|---|
| Text rendering matches BigQuery (checked with `bq` on 2026-09-25; `text_rendering_bigquery_test.go`, 344 probes in literal and column form plus 47 docs examples): `TO_JSON_STRING` / `TO_JSON` of INTERVAL is ISO 8601 (`P1Y2M-3DT4H`, `PT-1.5S`, zero is `P0Y`); CAST / FORMAT of INTERVAL print the fraction in groups of three digits (`6.500`); RANGE is `{"start":...,"end":...}` with `null` for an unbounded side (keys sorted by `TO_JSON`) and `FORMAT('%T')` names the element type (`RANGE<DATE> "..."`); WKT has no space after the type name (`POINT(1 1)`), coordinates use 15 significant digits with an exponent below 1e-4 (`1e-07`), every empty geography is `GEOMETRYCOLLECTION EMPTY` and a one-member multi-geometry or collection prints as its member; `FORMAT('%T')` of a geography is `ST_GeogFromText("...")`; `ST_ASGEOJSON` uses BigQuery's spacing and supports collections; `JSON_EXTRACT` / `JSON_QUERY` of a STRING return STRING, so the JSON text is quoted. The docs-examples runner and `docs_examples` spec cases compare WKT text without the spacing relaxation. Open: `FORMAT('%t'/'%T')` of a top-level GEOGRAPHY or RANGE is rejected by the go-googlesql analyzer; BigQuery densifies GeoJSON linestrings. | this PR | this PR | none | 4988 to 4988 (no case changed) |
| Reference-docs example corpus (`specctl extract-docs-examples`, `TestDocsExamples`), refreshed on this main: 1198 of the upstream GoogleSQL docs examples run under `TestSpec` (1178 as documented, 20 following BigQuery where the docs contradict it, each checked with `bq`); 142 are kept with a `skip:` reason (America/Los_Angeles default zone, features BigQuery lacks, non-deterministic output, docs artifacts) and 15 stay in `testdata/specs_pending` (aggregate `WHERE` modifier blocked by go-googlesql v0.4.0, S2 geography gaps, lenient `JSON_VALUE`, nanosecond TIMESTAMP literals). 936 cases were verified on BigQuery with `bq` on 2026-09-25 (`testdata/docs_examples/bigquery_verified.yaml`). Triage lives in `testdata/docs_examples/classification.yaml`; spec cases gain `skip` and `pending` fields. Driver fixes found this way, all verified on BigQuery: bare `array[i]`, positional struct access, the `WITH(...)` expression and pipe `WINDOW` clauses are enabled; `INT64` / `STRING` / `FLOAT64` of JSON `null` raise; `LAX_INT64` rounds half away from zero and returns NULL out of range, `LAX_FLOAT64` of a boolean is NULL, `LAX_BOOL` string match is case-insensitive; `JSON_ARRAY_APPEND` on a `null` member, recursive `JSON_FLATTEN`, `JSON_REMOVE(..., '$')` raises, compact `JSON_EXTRACT_ARRAY` elements, `PARSE_JSON` `wide_number_mode`, `TO_JSON` quotes wide INT64 only with `stringify_wide_numbers`; `CAST(STRING AS INTERVAL)` accepts partial and ISO 8601 forms; the numeric `CAST ... FORMAT` model (`0 9 . , S MI PR $ C L FM B`); `%c` pads the day and `%Z` prints `UTC-7`; empty geographies print `GEOMETRYCOLLECTION EMPTY`, a point collection reads back as `MULTIPOINT`, hex WKB in `ST_GEOGFROM`, `ST_AZIMUTH` of antipodes is NULL, `ST_NUMGEOMETRIES` counts collection members. | [`ac67606`](https://github.com/Flip-to/googlesqlite/commit/ac67606) | [#4](https://github.com/Flip-to/googlesqlite/pull/4) | none | 4988 to 4988 (no case changed) |
| One current time per statement, and bounded wasm memory in long-lived processes (flipto-dbt differential harness on the emulator). CURRENT_TIMESTAMP / CURRENT_DATETIME / CURRENT_DATE / CURRENT_TIME read one instant per statement, frozen on the connection while the statement runs (including while its rows are read), as BigQuery does (checked on BigQuery 2026-09-25): each call site read the wall clock, so `CURRENT_TIMESTAMP() = CURRENT_TIMESTAMP()` was false on nanosecond clocks (Linux) and per-row calls drifted. The instant is supplied at run time, so views, SQL function bodies and column DEFAULTs still see the time of the statement that uses them; a time injected with `WithCurrentTime` is now passed before the zone argument (`CURRENT_DATE('<zone>')` failed with it). Memory: every statement was parsed with parser options carrying one long-lived arena (and `AnalyzerOptions.GetParserOptions` leaked its by-value copy per call), so each AST stayed in wasm memory for good, about 2.4 KiB per parse; the emulator grew by about 145 MiB per 1,000 queries. Statements are parsed with arena-less options (the ParserOutput owns its arena), and a GC is forced every 1,024 analyses so finalizer-freed wasm memory is reused before the linear memory grows. Tests: `TestCurrentTimeStablePerStatement`, `TestCurrentTimeInStoredBodiesIsNotFrozen`, `TestParseAndAnalyzeMemoryIsReleased` (60,000 statements: +609 MiB before, 0 after). | this PR | this PR | none | not run |
| Golden regression test over every flipto-dbt differential probe (`TestDBTGolden`, `testdata/dbt_golden/`): 6,136 synthetic literal probes with the answer real BigQuery recorded (schema + rows or error), judged like the harness's `compare.py` (types with legacy synonyms, multiset rows, FLOAT64 rel. tolerance 1e-12, NaN = NaN, NUMERIC rounded to 28 digits as Python's Decimal does, error vs error). `specctl import-dbt-golden` regenerates the data from the harness output. 8 probes are listed in `known_divergences.txt` (all BigQuery-only rejections); the test fails if an unlisted probe diverges or a listed one starts to match. Fixed: LPAD with a negative length panicked (now an error, as RPAD), LPAD / RPAD on BYTES with a pattern at least as long as the padding panicked, an empty pattern is an error; REGEXP_EXTRACT and REGEXP_EXTRACT_ALL with more than one capturing group are an error (the non-upstream spec case "capture-group extraction returns digits" now expects that error, per string_functions.md and a real BigQuery check); COUNT(DISTINCT) counts -0.0 and 0.0 once; CAST(STRING AS TIMESTAMP) of a time in a DST gap keeps the offset before the transition (`'2024-03-10 02:30:00 America/New_York'` is 07:30 UTC), also in folded TIMESTAMP literal casts. | [`e126da1`](https://github.com/Flip-to/googlesqlite/commit/e126da1) | [#23](https://github.com/Flip-to/googlesqlite/pull/23) | none | not run |
| Literal-cast folding is on again for every statement. Values the analyzer folds in its America/Los_Angeles default zone are re-evaluated in UTC from their source text: CAST / SAFE_CAST of literals to TIMESTAMP and from TIMESTAMP to DATE / DATETIME / TIME / STRING, and string literals coerced to TIMESTAMP (also in statements without the TIMESTAMP keyword, e.g. `ts_col > '2024-01-01 10:00:00'`). RANGE<TIMESTAMP> literal bounds without a zone are UTC, also inside ARRAY / STRUCT literals; bounds with an offset are kept. A bare date (`TIMESTAMP '2020-01-01'`, a RANGE bound) is UTC midnight; its trailing `-01` was taken for an offset. A DATETIME leap second drops its fraction (`12:59:60.123456` is `13:00:00`). Compliance: 5 cases fixed; 32 RANGE<TIMESTAMP> cases now fail only because their expected bounds assume America/Los_Angeles (they pass with the bounds pinned to that zone). ARRAY / STRUCT literals built from such values fall back to analyzing that statement without folding. See `docs/decisions/analyzer-default-time-zone.md` and `analyzer-wasm-rebuild-plan.md`. Bench unchanged within noise. | this PR | this PR | none | not re-run |
| Silent divergences left by the full flipto-dbt differential run of 2026-09-24 (`dbt_divergences_3_test.go`, want = recorded BigQuery answer, literal and column inputs). CAST(DATE AS DATETIME) no longer folds to 1970-01-01 (the folded literal is re-evaluated from its source text). SAFE_CAST: `' true'` to BOOL is NULL, any non-zero INT64 to BOOL is TRUE, `'0x10'` to FLOAT64 is 16 and to NUMERIC is NULL, a STRING with more than 6 fractional digits to TIMESTAMP is NULL. FORMAT: `%s` honours width, `-` and precision; `%+d` of 0 is `+0`; NaN / infinities print as `nan`, `inf`, `-inf`; `%f` of a value with more than 17 integer digits keeps 17 significant digits. -0.0 keeps its sign in FORMAT, TO_JSON_STRING and CAST AS STRING of a column (a literal CAST(-0.0 AS STRING) is "0", as in BigQuery), also inside folded ARRAY / STRUCT literals and through the SQLite layer. TO_JSON_STRING quotes a NUMERIC that is not an integer in +/-2^53 and re-renders JSON values canonically (`2.50` is `2.5`, `'` is `'`); JSON_VALUE_ARRAY keeps number text. MAX_BY / MIN_BY with a NaN key are NULL; APPROX_QUANTILES sorts NaN first; HLL_COUNT.INIT counts `''`; LN / LOG of X <= 0 is an error, so SAFE.LOG(-1) is NULL; ROUND of NUMERIC that overflows is an error. Not changed (BigQuery-only rejections of SQL the project does not use): REGEXP_EXTRACT with 2 groups, CONTAINS_SUBSTR with a NULL literal needle, COUNT(DISTINCT struct), ARRAY_AGG ORDER BY ... ASC NULLS LAST, FULL JOIN over UNNEST, a TIMESTAMP literal with 9 fractional digits. JSON numbers that are doubles keep a fraction or exponent in TO_JSON_STRING (`1e2` is `100.0`). | this PR | this PR | none | not run |
| Compliance batch 3 (tail). Recursive CTEs: WITH nested in a recursive body is hoisted, projection/filter chains over the self-reference are flattened, a UNION inside the recursive term becomes SQLite's multiple recursive SELECTs. TVF table arguments that call RAND() are evaluated once; named TVF arguments resolve; pipe CALL with INPUT TABLE; a join nested on the right of another join is parenthesized; CASE on STRUCT uses GoogleSQL equality; TEMP VIEW setup replayed by the runner. SAFE. on a SQL UDF is rejected as in BigQuery ("SAFE with function X is not supported"), not the upstream fixture's NULL. NaN ties in RANK/CUME_DIST/PERCENT_RANK/DENSE_RANK; DOUBLE RANGE frame bounds clamp at the largest finite value; exact PERCENTILE_DISC; UNION ALL with a duplicated first-branch column; `1e300/1e-300` overflows; `-0.0` keeps its sign; LAST_DAY QUARTER, WEEK(day), ISOWEEK, ISOYEAR; GROUP BY () / GROUP BY ALL without keys; APPROX_* with OVER; HLL sketches record their input type and reject mixed merges; COLLATE "binary:cs" errors; setting a field of a NULL struct errors. | [`686599e`](https://github.com/Flip-to/googlesqlite/commit/686599e) | [#20](https://github.com/Flip-to/googlesqlite/pull/20) | none | 4635 to 4681 |
| The analyzer starts on Windows without TZDIR: the driver embeds Go's zoneinfo.zip, extracts it once per user (content-hashed cache dir) and points TZDIR at it in the drive-relative form the wasm WASI layer resolves; a working TZDIR is respected, and a drive-letter TZDIR is rewritten. `GOOGLESQLITE_WASM_EMBEDDED_ZONEINFO=0` (EnvWasmEmbeddedZoneinfo) turns it off. Before, the analyzer failed with "wasm trap: wasm: unreachable". | [`5dce62e`](https://github.com/Flip-to/googlesqlite/commit/5dce62e) | [#19](https://github.com/Flip-to/googlesqlite/pull/19) | none | unchanged (4635) |
| Compliance batch 2. MATCH_RECOGNIZE in standard and pipe syntax (Go row-pattern engine with bounded state, PREV/NEXT, anchors, quantifiers, both AFTER MATCH SKIP modes). Pipe operators (\|> WHERE, bare FROM, \|> JOIN, \|> WITH), PIVOT fixes, RAND() evaluated once per row, SELECT AS STRUCT returns one column per field, unknown hints rejected, ASSERT_ROWS_MODIFIED, INSERT OR IGNORE/REPLACE/UPDATE require a primary key. KEYS.* and AEAD.* use real Tink keysets (JSON round-trip, AES-SIV deterministic encryption); KLL_QUANTILES.INIT_* NULL and precision rules; APPROX_TOP_COUNT/SUM and APPROX_QUANTILES fixes; windowed ARRAY_AGG keeps NULLs unless IGNORE NULLS; aggregation threshold; ZSTD argument checks; WITH bodies that call non-deterministic functions are materialized. Scalars: exact NUMERIC ROUND with rounding modes; errors inside IF/CASE/IFERROR/ISERROR/COALESCE branches that are not taken no longer raise (deferred aggregate errors); SAFE. on aggregates and windows; RANGE constructor and function rules; REGEXP_MATCH full match; STRPOS counts characters; FORMAT * precision; NUMERIC(P,S) casts; CAST FORMAT type checks; CHR(0) is NUL as in BigQuery. BOOL values keep their type inside ARRAY, STRUCT and JSON (they printed 1/0). LAG/LEAD reject a negative offset. LOG(X, Y) fixed. | [`86c0546`](https://github.com/Flip-to/googlesqlite/commit/86c0546) | [#17](https://github.com/Flip-to/googlesqlite/pull/17) | none | 4310 to 4635 |
| Compliance batch. Set operations: CORRESPONDING, CORRESPONDING BY, BY NAME, FULL/LEFT/STRICT modes, INTERSECT ALL / EXCEPT ALL. SQL user-defined aggregates (CREATE AGGREGATE FUNCTION, NOT AGGREGATE parameters); more WITH RECURSIVE shapes; the compliance runner now runs setup-only blocks and keeps temp functions for later cases. RANGE window frames over INT64, NUMERIC, BIGNUMERIC and DOUBLE keys (Go frame implementation, NaN and infinities handled); windowed HLL_COUNT, BigQuery-format HLL sketches in MERGE/EXTRACT; PERCENTILE_CONT/DISC exact; GROUPING SETS / ROLLUP / CUBE fixes; ARRAY_AGG keeps NULL elements (BigQuery rejects them only in the final result); INT64 window SUM no longer overflows on intermediate totals. INTERVAL: big-integer time part, range errors, `*` and `/` INT64, arithmetic with DATE/DATETIME/TIMESTAMP in either order, SUM/AVG, QUARTER/WEEK/MILLISECOND/MICROSECOND parts, JUSTIFY_* sign rules, value equality in IN/PARTITION BY/DISTINCT/APPROX_*. LIKE ANY/SOME/ALL with arrays and subqueries; `und:ci` collation in comparisons, grouping, ordering, joins, set operations, PIVOT/UNPIVOT and table columns; JSONPath with quoted names; JSON_OBJECT/JSON_SET/JSON_REMOVE/JSON_STRIP_NULLS/JSON_KEYS fixes. CAST to STRING(L)/BYTES(L) checks length; a scalar subquery returning more than one row is an error; CURRENT_TIME/DATETIME/TIMESTAMP have microsecond precision; DATETIME/TIME/TIMESTAMP text prints fractional seconds in groups of three digits; STRING(timestamp, zone) prints the zone offset. | [`aa4d0f4`](https://github.com/Flip-to/googlesqlite/commit/aa4d0f4) | [#16](https://github.com/Flip-to/googlesqlite/pull/16) | none | 3277 to 4310 |
| Memory no longer grows without bound on DDL-heavy sessions (flipto-dbt L12). Each DROP and temp-object cleanup rebuilds the analyzer catalog, and retired catalogs (about 1.5 MB of wasm memory each) were only freed by a finalizer after a GC that rarely ran; the heap reached 1.2 to 2.9 GB. A GC now runs every 8 rebuilds; the heap stays near 100 MB. `latency_growth_test.go` guards it. Bench unchanged within noise. | [`589e255`](https://github.com/Flip-to/googlesqlite/commit/589e255) | [#15](https://github.com/Flip-to/googlesqlite/pull/15) | none | unchanged (3277) |
| A float literal cast to NUMERIC or BIGNUMERIC keeps full precision in statements that mention TIMESTAMP (literal-cast folding is off there, so the value went through DOUBLE); the cast now reads the literal text from the query. | [`868e0dd`](https://github.com/Flip-to/googlesqlite/commit/868e0dd) | [#14](https://github.com/Flip-to/googlesqlite/pull/14) | none | unchanged (3277) |
| CAST(STRING AS DATE / DATETIME / TIME / TIMESTAMP FORMAT ...) parses with format elements, AT TIME ZONE, the format model rules and SAFE_CAST; where upstream doc examples disagree with real BigQuery, BigQuery wins (see `cast_format_parse_test.go`). STRUCT literals keep anonymous fields unnamed, so `TO_JSON_STRING(STRUCT(1, 2))` is `{"":1,"":2}` as in BigQuery (it printed `_field_0`, `_field_1`). | [`a4ce662`](https://github.com/Flip-to/googlesqlite/commit/a4ce662) | [#13](https://github.com/Flip-to/googlesqlite/pull/13) | none | 3265 to 3277 |
| The rest of the flipto-dbt PR 365 probes, all checked on real BigQuery (`dbt_probes_test.go` replays every one, with literal and column inputs). A zone-less time in a DST gap moves forward (S2). TO_JSON_STRING quotes INT64 outside +/-2^53 and pretty-prints with two spaces (S8, S9). JSON_VALUE keeps number text (`1.0`) and returns NULL for invalid JSON; JSON_QUERY / JSON_EXTRACT keep a matched null as JSON `null` for JSON input (S10, S11, L9). CAST ... FORMAT for DATE, DATETIME, TIME and TIMESTAMP to STRING, including AT TIME ZONE (S12). FORMAT: `%t` / `%T` print NULL, `-` left-justifies numbers, negative `%x` / `%o`, too many arguments is an error (S6, L7, L8). `>>` is a logical shift (S16). FLOOR / CEIL / MOD on NUMERIC are exact (S18). GREATEST / LEAST / MIN / MAX propagate NaN (S19). DATE(y, m, d) validates its parts (S21). Month intervals clamp to the month end (S22). DATETIME_DIFF counts unit boundaries (S24). UPPER uses full case mapping, INITCAP splits on all whitespace, REPLACE with an empty pattern is a no-op, REGEXP_EXTRACT returns NULL for an unmatched group, CONTAINS_SUBSTR uses NFKC and case folding (S27 to S31). HLL_COUNT.EXTRACT(NULL) is 0 (S34). ARRAY_CONCAT with a NULL array is NULL (L3). FIRST_VALUE / LAST_VALUE / NTH_VALUE support IGNORE NULLS. | [`a7d6791`](https://github.com/Flip-to/googlesqlite/commit/a7d6791) | [#12](https://github.com/Flip-to/googlesqlite/pull/12) | none | 3254 to 3265 |
| Divergences measured on real BigQuery by the flipto-dbt differential probes (flipto-dbt PR 365). S1: casts involving TIMESTAMP run in UTC (the analyzer folded them in America/Los_Angeles; literal-cast folding is now off for statements that mention TIMESTAMP). TIMESTAMP / DATETIME cast to STRING in BigQuery's text form. TIMESTAMP_DIFF DAY counts 24-hour units; EXTRACT(WEEK) is Sunday-based and WEEK(<weekday>) works. SUBSTR counts characters (S26). BETWEEN with a NULL bound (S5). NULLIF with a NULL argument no longer panics (L2). Unary minus on a column works (L1). String casts trim whitespace and follow BigQuery's literal rules; FLOAT64 to INT64 rounds half away from zero (S14, S15). ABS keeps INT64 (S17). A zero step in GENERATE_ARRAY / GENERATE_DATE_ARRAY / GENERATE_TIMESTAMP_ARRAY is an error, not an endless loop (L11) or NULL; generated arrays are capped at 10,000,000 elements. APPROX_TOP_SUM rejects negative weights. | [`a53ae2c`](https://github.com/Flip-to/googlesqlite/commit/a53ae2c) | [#10](https://github.com/Flip-to/googlesqlite/pull/10) | none | 3229 to 3254 |
| NULL and NaN comparison semantics. `x IN (..., NULL)`, `NOT IN` and `IN UNNEST` are three-valued (`3 NOT IN (1, 2, NULL)` was true), and `IN UNNEST` no longer panics on NULL elements. STRUCT `=` / `!=` return NULL when a field comparison is NULL. IS [NOT] DISTINCT FROM treats NaN as equal to NaN, also inside STRUCT and ARRAY. ORDER BY and window ORDER BY on DOUBLE put NaN right after NULL (it sorted last). Windowed VAR/STDDEV/COVAR/CORR share the plain aggregates' moments code (NULL for too few rows, NaN propagation). | [`78d7888`](https://github.com/Flip-to/googlesqlite/commit/78d7888) | [#9](https://github.com/Flip-to/googlesqlite/pull/9) | none | 3177 to 3229 |
| Numeric aggregates and windows follow GoogleSQL rules. SUM/AVG are exact and report `int64` / `numeric` / `double` overflow; NaN and infinities propagate; window SUM/AVG/MIN/MAX over DOUBLE, NUMERIC and BIGNUMERIC no longer use SQLite built-ins, which sum NaN as 0, slide inf-inf to NULL and order NUMERIC as text; SUM(DISTINCT) keeps DOUBLE. Statistical aggregates (VAR/STDDEV/COVAR/CORR) return NULL for too few rows, use exact NUMERIC arithmetic and do not overflow on extreme DOUBLEs. HAVING MAX/MIN works on every aggregate. QUALIFY filters after window functions (it filtered before them). INTERVAL: comparison, + and -, DISTINCT and GROUP BY by value, negative parts keep their sign, fractional seconds parsed exactly. RANGE ordering. The bench harness reads CRLF corpus files. | [`9b76c96`](https://github.com/Flip-to/googlesqlite/commit/9b76c96) | [#8](https://github.com/Flip-to/googlesqlite/pull/8) | none | 2944 to 3177 |
| BYTES functions use the bytes rather than their base64 storage text: CAST(BYTES AS STRING), ASCII, LIKE, TRIM/LTRIM/RTRIM, INSTR, SPLIT, REGEXP_* (matched byte by byte), STRING_AGG and ARRAY_TO_STRING over BYTES (which return BYTES). Also: CAST ... FORMAT for BYTES and STRING (HEX, BASE2/8/16/32/64, BASE64M, ASCII, UTF-8); an explicit empty STRING_AGG delimiter is kept; windowed STRING_AGG skips NULLs; INSTR returns 0 past the end and counts characters; REGEXP_INSTR reports the capture group and rejects more than one. | [`86e88fc`](https://github.com/Flip-to/googlesqlite/commit/86e88fc) | [#7](https://github.com/Flip-to/googlesqlite/pull/7) | none | 2876 to 2944 |
| Add FLIPTO.md with the fork changelog. | [`4c8c12f`](https://github.com/Flip-to/googlesqlite/commit/4c8c12f) | [#6](https://github.com/Flip-to/googlesqlite/pull/6) | none | unchanged |
| Add the `specctl run-compliance` runner and the first compliance report. | [`a761def`](https://github.com/Flip-to/googlesqlite/commit/a761def) | [#3](https://github.com/Flip-to/googlesqlite/pull/3) | none | baseline 2757 |
| Build DATE values in UTC and decode TIMESTAMP values in UTC. Before this, DATE results shifted by one day on hosts west of UTC. Also avoid `time.Duration` overflow in `DATE_FROM_UNIX_DATE` outside 1677..2262. | [`c8a1097`](https://github.com/Flip-to/googlesqlite/commit/c8a1097) | [#5](https://github.com/Flip-to/googlesqlite/pull/5) | none | 2757 to 2876 |

### Fixes carried before the compliance run

These were merged into the fork's integration branch before
`flipto/main` existed. Each one has a test in the repository.

| change | landed in | upstream |
|---|---|---|
| value: render FLOAT64 as text the way BigQuery does (CAST, CONCAT, FORMAT, TO_JSON_STRING) | [`274b9b1`](https://github.com/Flip-to/googlesqlite/commit/274b9b1) | [goccy/googlesqlite#58](https://github.com/goccy/googlesqlite/pull/58) (third-party) |
| operator: IS [NOT] TRUE / FALSE never return NULL | [`59ef8e7`](https://github.com/Flip-to/googlesqlite/commit/59ef8e7) | [goccy/googlesqlite#71](https://github.com/goccy/googlesqlite/pull/71) (third-party) |
| udf: format a SQL function body with column ids (UDF parameter used in a subquery) | [`2a652e6`](https://github.com/Flip-to/googlesqlite/commit/2a652e6) | [goccy/googlesqlite#102](https://github.com/goccy/googlesqlite/pull/102) |
| tvf: format a templated TVF body with its own column state (aggregate in a subquery) | [`25424da`](https://github.com/Flip-to/googlesqlite/commit/25424da) | [goccy/googlesqlite#101](https://github.com/goccy/googlesqlite/pull/101) |
| analyzer: allow ARRAY grouping keys and group composites by encoding | [`a549f13`](https://github.com/Flip-to/googlesqlite/commit/a549f13) | [goccy/googlesqlite#100](https://github.com/goccy/googlesqlite/pull/100) |
| value: give `%T` its literal form for NUMERIC, BIGNUMERIC, JSON, INTERVAL | [`4f69e91`](https://github.com/Flip-to/googlesqlite/commit/4f69e91) | [goccy/googlesqlite#99](https://github.com/goccy/googlesqlite/pull/99) |
| approx_aggregate: sort APPROX_QUANTILES input and ignore NULLs by default | [`e0f0399`](https://github.com/Flip-to/googlesqlite/commit/e0f0399) | [goccy/googlesqlite#97](https://github.com/goccy/googlesqlite/pull/97) |
| value: round NUMERIC and BIGNUMERIC products to the type's scale | [`3194b19`](https://github.com/Flip-to/googlesqlite/commit/3194b19) | [goccy/googlesqlite#98](https://github.com/goccy/googlesqlite/pull/98) |
| value: round NUMERIC and BIGNUMERIC quotients to the type's scale | [`0823ac1`](https://github.com/Flip-to/googlesqlite/commit/0823ac1) | [goccy/googlesqlite#95](https://github.com/goccy/googlesqlite/pull/95) |
| driver: clean up temp objects when QueryContext fails mid-script | [`bdcd061`](https://github.com/Flip-to/googlesqlite/commit/bdcd061) | [goccy/googlesqlite#96](https://github.com/goccy/googlesqlite/pull/96) |
| catalog: resolve a quoted dotted prefix in scalar UDF paths | [`feed715`](https://github.com/Flip-to/googlesqlite/commit/feed715) | [goccy/googlesqlite#94](https://github.com/goccy/googlesqlite/pull/94) |
| json: encode DATE, DATETIME, TIME and TIMESTAMP as JSON strings in TO_JSON_STRING | [`a613dfc`](https://github.com/Flip-to/googlesqlite/commit/a613dfc) | [goccy/googlesqlite#93](https://github.com/goccy/googlesqlite/pull/93) |
| float64: arithmetic on values nested in STRUCT and ARRAY | [`55eb981`](https://github.com/Flip-to/googlesqlite/commit/55eb981) | none found |
| script: evaluate ASSERT instead of treating it as a no-op | [`80e0cdf`](https://github.com/Flip-to/googlesqlite/commit/80e0cdf) | [goccy/googlesqlite#91](https://github.com/goccy/googlesqlite/pull/91) |
| catalog: skip cleanup of a temp table the script already dropped | [`80e0cdf`](https://github.com/Flip-to/googlesqlite/commit/80e0cdf) | [goccy/googlesqlite#92](https://github.com/goccy/googlesqlite/pull/92) |
| deps: bump grpc to 1.83.2 (xDS missing :authority DoS) | [`bb33cf3`](https://github.com/Flip-to/googlesqlite/commit/bb33cf3) | none |
| deps: bump grpc to 1.83.1, otel/sdk to 1.45.0, and tools deps with security fixes | [`da30ae6`](https://github.com/Flip-to/googlesqlite/commit/da30ae6) | none |
| catalog: do not register builtins in sub-catalogs (memory growth on DROP TABLE); LIKE `_` and escapes | [`2996694`](https://github.com/Flip-to/googlesqlite/commit/2996694) | none found |
| array: return NULL from ARRAY_TO_STRING when an argument is NULL; script splitting respects comments | [`8935cec`](https://github.com/Flip-to/googlesqlite/commit/8935cec) | none found |
| ddl: support DROP TABLE FUNCTION | [`2d1c5f4`](https://github.com/Flip-to/googlesqlite/commit/2d1c5f4) | [goccy/googlesqlite#90](https://github.com/goccy/googlesqlite/pull/90) |
| catalog: resolve a quoted dotted prefix in table and TVF paths | [`46dad9a`](https://github.com/Flip-to/googlesqlite/commit/46dad9a) | [goccy/googlesqlite#89](https://github.com/goccy/googlesqlite/pull/89) |
| tvf: support ANY TABLE and TABLE<...> parameters | [`87922d2`](https://github.com/Flip-to/googlesqlite/commit/87922d2) | [goccy/googlesqlite#88](https://github.com/goccy/googlesqlite/pull/88) |
| catalog: keep TVF handles alive while the catalog references them | [`bada781`](https://github.com/Flip-to/googlesqlite/commit/bada781) | [goccy/googlesqlite#87](https://github.com/goccy/googlesqlite/pull/87) |
| formatter: keep the outer row in a correlated LEFT JOIN UNNEST | [`f766d8c`](https://github.com/Flip-to/googlesqlite/commit/f766d8c) | [goccy/googlesqlite#85](https://github.com/goccy/googlesqlite/pull/85) |
| GROUP BY ALL, including composite grouping keys | [`e980e02`](https://github.com/Flip-to/googlesqlite/commit/e980e02) | none found |
| string: return a NULL array from SPLIT when an argument is NULL | [`078f675`](https://github.com/Flip-to/googlesqlite/commit/078f675) | [goccy/googlesqlite#86](https://github.com/goccy/googlesqlite/pull/86) |
| math: return INT64 from MOD of two INT64 arguments | [`af134ab`](https://github.com/Flip-to/googlesqlite/commit/af134ab) | none found |

### Known open divergences

Tracked in `docs/compliance_run_results.md` and in flipto-dbt
`analyses/emulator_differential_results.md`. Not yet fixed:

- ARRAY / STRUCT literals whose TIMESTAMP members come from a cast or a
  coerced string are analyzed with literal-cast folding off (go-googlesql
  v0.4.0 cannot set a UTC default time zone); for DDL statements such a
  literal keeps the analyzer's America/Los_Angeles value. Scalar folded
  literals are re-evaluated in UTC. See
  `docs/decisions/analyzer-default-time-zone.md`.
- The GoogleSQL compliance suite assumes an America/Los_Angeles default
  time zone; its TIMESTAMP-to-string and naive-TIMESTAMP cases disagree
  with BigQuery (UTC) and are expected to fail here.

- HLL_COUNT.MERGE of two sparse BigQuery sketches that hit the same
  bucket keeps both entries instead of the higher rank; the driver's own
  HLL_COUNT.INIT sketches do not match BigQuery's bytes or record the
  input type.
- A top-level ARRAY_AGG result with a NULL element is returned; BigQuery
  rejects it when writing the result ("Array cannot have a null
  element"). The emulator's output step is the place to enforce it.
- CREATE CONSTANT and some nested WITH RECURSIVE shapes (recursive
  references inside subqueries or nested unions) are not supported.
  Nested MATCH_RECOGNIZE and two pipe AGGREGATE shapes are rejected by
  the go-googlesql v0.4.0 analyzer.
- AEAD ciphertexts round-trip within the driver but are not
  interchangeable with real Tink / BigQuery ciphertexts.
- flipto-dbt S36 (anonymous STRUCT fields collapse): the driver returns
  both fields positionally; the collapse happens in the harness, which
  keys fields by name. BigQuery's API names them `_field_1`, `_field_2`.

