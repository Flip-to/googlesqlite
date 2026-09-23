# GoogleSQL compliance suite run against googlesqlite

This report records one offline run of Google's GoogleSQL compliance
suite (`google/googlesql`, `googlesql/compliance/testdata/*.test`, 285
files) through the `googlesqlite` driver. No BigQuery queries were
issued. Nothing in this report has been fixed; it is a divergence list.

- Driver revision: `flipto/flipto/main` at `274b9b1`.
- Suite revision: shallow clone of `google/googlesql` default branch,
  taken 2026-09-23.
- Host: Windows 11, local zone `E. South America Standard Time`
  (UTC-3), `TZDIR` pointed at Go's `zoneinfo.zip` contents.
- Runner: `go run ./cmd/specctl run-compliance` (see "Reproducing").
- Machine-readable output: `docs/compliance_run_results.json` holds
  every failing and erroring case (UTC run) with its SQL, expected
  block, driver result and failure reason, ranked as described below.

## Headline numbers

Two full runs were made. They differ only in the process local time
zone, because the first finding below depends on it.

| | UTC run (`-local-utc`) | host zone run (UTC-3) |
|---|---:|---:|
| query cases in the suite | 12062 | 12062 |
| run | 4797 | 4797 |
| passed | 2872 | 2757 |
| failed (wrong rows, no error) | 653 | 768 |
| errored (driver error where rows were expected) | 1272 | 1272 |
| skipped (reasons below) | 7265 | 7265 |
| pass rate of run cases | 59.9% | 57.5% |

Setup statements (`[prepare_database]`, counted separately from query
cases): 268 succeeded, 7 failed, 218 skipped.

115 cases pass in the UTC run and fail in the host zone run. No case
passes on the host zone and fails under UTC.

Of the 653 UTC failures, every one is silent (the driver returned rows
without error): 479 wrong values in the right shape, 145 cases where
the suite expects an error and the driver returned rows, 23 wrong row
counts, 6 wrong column shapes. Of the 1272 errors, 31 are Go panics
recovered by the driver and 1241 are ordinary driver errors.

## How cases were filtered

Skipping is explicit. Every skipped case carries a reason in the
all-cases JSON, and the runner never drops a case without one.

1. **Required features.** `cmd/specctl/compliancetest/filter.go`
   lists the `LanguageFeature` names treated as BigQuery features
   (`BigQueryFeatures`) and the ones treated as non-BigQuery, each with
   a reason (`NonBigQueryFeatures`). A case runs only if every required
   feature is on the BigQuery list. A feature on neither list is
   reported as "unclassified feature" instead of being skipped quietly;
   this run had none.
2. **Forbidden features.** A case with `[forbidden_features=X]`, where
   X is a BigQuery feature, is skipped because the suite expects X to be
   disabled.
3. **Non-BigQuery types.** Cases whose SQL or result type uses
   `INT32`, `UINT32`, `UINT64`, `FLOAT` (32-bit), `PROTO`, `ENUM`,
   `UUID`, `MAP`, graph types or the `googlesql_test.*` proto schema are
   skipped. BigQuery runs in the external product mode and has none of
   these types.
4. **Dependencies.** A case that references a setup table, function or
   TVF that was skipped or failed is skipped with the name of that
   object.
5. **Runner limits.** DML cases whose expected block is the
   `STRUCT<num_rows_modified, all_rows>` shape (109), script-mode cases,
   and 22 expected blocks the value parser cannot read are skipped and
   labelled `runner: ...`.

| skip reason | cases |
|---|---:|
| feature not in BigQuery: language feature not in BigQuery | 3116 |
| depends on a setup object that was skipped or failed | 1159 |
| non-BigQuery type in result | 981 |
| feature not in BigQuery: SQL graph (GQL) | 462 |
| feature not in BigQuery: anonymization / differential privacy | 280 |
| non-BigQuery type in SQL | 263 |
| feature not in BigQuery: type not available in BigQuery | 212 |
| feature not in BigQuery: proto / enum types | 146 |
| runner: DML result shape not compared | 109 |
| feature not in BigQuery: sampling output is nondeterministic (TABLESAMPLE) | 29 |
| runner: cannot parse expected value | 22 |

The per-reason table in "Full tables" below also lists skipped setup
statements.

## Comparison rules

- Rows are compared ignoring order unless the expected block says
  `known order:`; the same rule applies to nested arrays.
- DOUBLE values match within 4 ULP; NaN matches NaN; +/-inf must
  match exactly.
- NUMERIC and BIGNUMERIC are compared as exact rationals.
- TIMESTAMP values are compared as instants. DATETIME and TIME are
  compared with trailing fractional zeros removed.
- JSON is compared structurally, with numbers compared numerically.
- BYTES coming back from the driver as base64 text are decoded before
  comparison.
- For an expected `ERROR:` block, any driver error counts as a pass.
  81 of those passes fail at a different phase from the suite (for
  example, the suite expects a runtime `out_of_range` and the driver
  fails in analysis). These are counted, not failed.
- `[parameters=...]` values are inlined as `(expr)` in place of each
  `@name`. This differs from true query parameters when a function
  requires a literal argument.

## Ranking

Failures are ordered as follows:

1. Cases that use one of the dbt constructs listed below come first.
2. Within that, cases are ordered by how likely the failure is a silent
   wrong answer:
   1. wrong values with the right shape
   2. expected an error, got rows
   3. wrong row count
   4. wrong shape
   5. panic
   6. driver error
   7. timeout

The dbt construct list is the fallback list from the task:

- SAFE_DIVIDE, LEFT JOIN UNNEST, QUALIFY, FORMAT %T, HLL_COUNT.*
- ARRAY_AGG with ORDER BY or LIMIT, GENERATE_DATE_ARRAY, IS DISTINCT FROM
- window frames, EXCEPT DISTINCT, TO_JSON_STRING, FARM_FINGERPRINT
- APPROX_QUANTILES, NUMERIC arithmetic, DATE_TRUNC, TIMESTAMP_SUB
- COALESCE, CASE, LIKE, SPLIT, REGEXP_*

The list from `datamodeling/prod1-web/dbt/analyses/emulator_differential_results.md`
on `claude/emulator-differential-probes` did not exist when this ran.
Matching is regex-based on the case SQL (`compliancetest/constructs.go`).

| dbt construct | run | pass | fail | error |
|---|---:|---:|---:|---:|
| window frames | 436 | 272 | 80 | 84 |
| NUMERIC arithmetic | 268 | 149 | 55 | 64 |
| EXCEPT DISTINCT | 103 | 21 | 0 | 82 |
| LIKE | 99 | 33 | 14 | 52 |
| HLL_COUNT.* | 38 | 14 | 4 | 20 |
| ARRAY_AGG ORDER BY / LIMIT | 37 | 22 | 9 | 6 |
| REGEXP_* | 20 | 7 | 10 | 3 |
| CASE | 46 | 37 | 2 | 7 |
| QUALIFY | 27 | 18 | 3 | 6 |
| SPLIT | 19 | 13 | 4 | 2 |
| APPROX_QUANTILES | 26 | 23 | 2 | 1 |
| IS DISTINCT FROM | 19 | 16 | 2 | 1 |
| TO_JSON_STRING | 3 | 0 | 3 | 0 |
| FORMAT %T | 2 | 0 | 2 | 0 |
| GENERATE_DATE_ARRAY | 4 | 2 | 2 | 0 |
| SAFE_DIVIDE | 12 | 10 | 1 | 1 |
| DATE_TRUNC | 1 | 0 | 0 | 1 |
| COALESCE | 23 | 23 | 0 | 0 |
| LEFT JOIN UNNEST | 6 | 6 | 0 | 0 |

(The table has no FARM_FINGERPRINT or TIMESTAMP_SUB row: no case that
uses them ran.) All 82 EXCEPT DISTINCT errors are `CORRESPONDING`
set operations. They fail loudly with
`failed to analyze: CORRESPONDING for set operations is not supported`
(or the `CORRESPONDING BY` variant); plain `EXCEPT DISTINCT` did not
fail.

## Top 20 divergences

Every entry below is silent: the driver returns rows and no error.
Case names are `file` / `[name=...]`. Expected values are quoted from
the suite, and driver values are exact output from the run.

1. **DATE shifts by one day when the host is not on UTC.**
   (host zone run only; 115 cases, mostly RANGE, ARRAY and window
   cases involving DATE)
   `date.test` / `unix_date_1`:
   `select unix_date(date '2014-01-01'), ...`
   expected `{16071, 0, -365}`, driver on UTC-3 `{16070, -1, -366}`.
   A bare `SELECT DATE '2020-01-02'` returns `"2020-01-01"` on the same
   host. Upstream `main` (`4dd0bf5`) behaves the same. Setting
   `time.Local = time.UTC` in the process removes the difference. The
   known baseline failure `TestBindDateFromUnixDate` is probably this
   bug.
2. **DATE_FROM_UNIX_DATE wraps outside 1677..2262.**
   `date.test` / `date_from_unix_date_1`:
   `select date_from_unix_date(-719162), ..., date_from_unix_date(2932896)`
   expected `{0001-01-01, 1969-01-01, 1970-01-01, 2014-01-01, 9999-12-31}`,
   driver `{"1754-08-30", "1969-01-01", "1970-01-01", "2014-01-01", "1816-03-29"}`.
   The dates wrap at the range of int64 nanoseconds.
3. **CAST(BYTES AS STRING) returns base64 text.** (FORMAT %T)
   `strings.test` / `format_utf8`: `select cast(description_bytes as string), format('%T', cast(description_bytes as string)) ...`
   expected the decoded UTF-8 text
   `"累計100万DLを突破した..."`, driver `"57Sv6KiIMTAw5LiH..."`
   (the base64 encoding of the bytes).
4. **STRING_AGG over BYTES in a window concatenates base64 encodings.**
   (window frames)
   `analytic_string_aggregation.test` / `string_agg_BYTES_analytic_from_preceding_row`:
   `STRING_AGG(elem, b";") OVER (ORDER BY elem ROWS BETWEEN 2 PRECEDING AND UNBOUNDED FOLLOWING)`
   expected `b";a\xcf\x83;bc\xcf\x80;d\xe2\xa8\x9d"`, driver bytes
   decode to `";Ow==Yc+D..."`: the base64 of each element joined with
   the base64 of the separator.
5. **AVG(NUMERIC) over a RANGE frame returns 0.** (window frames,
   NUMERIC)
   `analytic_avg.test` / `analytic_moving_avg_numeric_no_overflow_1`:
   `AVG(val) OVER (ORDER BY row_id RANGE BETWEEN 1 PRECEDING AND 1 FOLLOWING)`
   over four rows of `99999999999999999999999999999.999999999`
   expected that same value for every row, driver `"0"` for every row.
   The `_2` and `_3` variants (negative and mixed values) also return
   `"0"`.
6. **QUALIFY with COUNT(*) OVER () after GROUP BY CUBE counts filtered rows.**
   (QUALIFY)
   `grouping_sets_queries.test` / `cube_with_qualify_clauses`:
   `SELECT a, b, COUNT(*) OVER() FROM simple_table GROUP BY CUBE(a, b) QUALIFY GROUPING(a) = 0 ORDER BY 1, 2, 3`
   expected the window count 12 on every row, driver 16.
   `cube_with_post_aggregate_clauses` behaves the same way (expected
   30, driver 34).
7. **SAFE_DIVIDE overflow returns +Inf instead of NULL.** (SAFE_DIVIDE)
   `arithmetic_functions.test` / `arithmetic_functions_14_safe_divide`:
   `SELECT safe_divide(1e300, 1e-300)` expected `NULL`, driver `+Inf`.
8. **APPROX_QUANTILES returns one element too many.**
   (APPROX_QUANTILES)
   `approx_aggregation.test` / `approx_quantiles_fixed_count_1000`:
   `SELECT ARRAY_LENGTH(APPROX_QUANTILES(x, (1000))) FROM UNNEST([1,2,3,3,2,1]) x`
   expected `1001`, driver `1002`.
9. **REGEXP_INSTR returns wrong positions.** (REGEXP_*)
   `strings.test` / `strings_function_regexp_instr`:
   `SELECT REGEXP_INSTR("abcabc", "a(b)c", 2, 1, 1), REGEXP_INSTR("щцфщфф", "щ(.).", 1, 2), REGEXP_INSTR("-2020-jack-class1", "", 2), REGEXP_INSTR("abcdef", "ac.*e.")`
   expected `{6, 5, 0, 0}`, driver `{7, 7, 2, 0}`.
10. **SAFE.REGEXP_INSTR ignores the one-capture-group rule.** (REGEXP_*)
    `safe_function.test` / `safe_regexp_instr`:
    `select safe.regexp_instr("abc", "(a)(b)")` expected `NULL`
    (two capture groups is an error, so SAFE yields NULL), driver `1`.
11. **SPLIT of an empty string returns an empty array.** (SPLIT)
    `strings.test` / `split_empty_delimiter`: `select split("", ""), split("abcd", "")`
    expected `{[""], ["a", "b", "c", "d"]}`, driver `{[], ["a", "b", "c", "d"]}`.
12. **GENERATE_DATE_ARRAY with a zero step returns NULL instead of
    failing.** (GENERATE_DATE_ARRAY)
    `array_functions.test` / `generate_date_array_literal_zero_step`:
    `SELECT GENERATE_DATE_ARRAY('2016-01-01', '2017-01-01', INTERVAL 0 DAY)`
    expected `ERROR: generic::out_of_range: Sequence step cannot be 0.`,
    driver `[{NULL}]`. The parameterized variant behaves the same.
13. **HLL_COUNT.INIT over only NULLs returns a sketch.** (HLL_COUNT.*)
    `hll_count.test` / `hll_count_init_only_null`:
    `SELECT HLL_COUNT.INIT(x) FROM (SELECT NULL AS x UNION ALL SELECT NULL AS x)`
    expected `NULL`, driver the bytes of base64 `"Ee9/"`.
14. **HLL_COUNT.MERGE accepts sketches of different input types.**
    (HLL_COUNT.*)
    `hll_count.test` / `hll_count_merge_incompatible_types`: merging
    `HLL_COUNT.INIT` of INT64 values with `HLL_COUNT.INIT` of STRING
    values expected
    `ERROR: generic::out_of_range: Invalid or incompatible sketch in HLL_COUNT.MERGE`,
    driver `5`. `MERGE_PARTIAL` also returns a sketch.
15. **NaN IS DISTINCT FROM NaN is true.** (IS DISTINCT FROM)
    `comparison_functions.test` / `is_distinct_nan`:
    `nan IS DISTINCT FROM nan` (with `nan = IEEE_DIVIDE(0, 0)`)
    expected `false`, driver `true`. `nan IS NOT DISTINCT FROM nan`
    expected `true`, driver `false`. The STRUCT variant
    (`is_distinct_nan_struct`) fails the same way.
16. **ARRAY_AGG(DISTINCT range ORDER BY range LIMIT n) orders unbounded
    ranges wrongly.** (ARRAY_AGG ORDER BY / LIMIT)
    `aggregation_distinct_queries.test` / `aggregation_distinct_array_agg_range_date_timestamp_order_by_limit`:
    `SELECT ARRAY_AGG(DISTINCT date_range_val ORDER BY date_range_val LIMIT 2), ... FROM TableWithRange`
    expected `[NULL, [UNBOUNDED, 2020-01-02)]`, driver
    `[NULL, "[2020-01-01, 2020-01-02)"]`. An UNBOUNDED start should sort
    first. Three cases in `aggregation_order_by_queries.test`
    (`aggregation_order_by_range_*`) fail the same way.
17. **SUM(DOUBLE) over a ROWS frame loses +inf.** (window frames)
    `analytic_sum.test` / `analytic_sum_double_inf_1`:
    `SUM(val) OVER (ORDER BY row_id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW)`
    over three `+inf` rows expected `inf` on every row, driver `NULL`
    on row 3. `_2` (-inf) and `_3` (mixed) fail the same way.
18. **VAR_POP / VAR_SAMP / STDDEV over a moving window return NULL
    where NaN is expected.** (window frames)
    `analytic_stat_aggregation.test` / `analytic_stats_single_column_moving_window`:
    `VAR_POP(x) OVER w ... WINDOW w AS (ORDER BY row_id ROWS BETWEEN 1 PRECEDING AND 1 FOLLOWING)`
    expected row 8 `{8, NaN, NaN, NaN, NaN}`, driver `NULL` in those
    columns. Other `analytic_stats_*` cases with NaN inputs fail the
    same way.
19. **TO_JSON_STRING escapes control characters with `\x` escapes,
    which are not valid JSON.** (TO_JSON_STRING)
    `strings.test` / `to_json_string_with_escaped_field_names`:
    `SELECT TO_JSON_STRING(STRUCT<... `abca\x00\x01\x1A\x1F` STRING>(1, 'foo'))`
    expected field name `"abca\u0000\u0001\u001a\u001f"`, driver
    `"abca\x00\x01\x1a\x1f"`.
20. **STRING(DATE) returns a timestamp string.**
    `date.test` / `date_constructor`:
    `select ..., string(date '1234-01-02')` expected `"1234-01-02"`,
    driver `"1234-01-02 00:00:00+00"`.

Other silent groups worth noting:

- `FORMAT` with too many arguments returns a value instead of an error.
  In `strings.test` / `format_invalid_true2`,
  `select format(x, y, z) from (select '%d' x, 17 y, 'abc' z)` returned
  `"17"`.
- LIKE on BYTES compares against base64 text. In `like_all.test` /
  `like_all_bytes_constant_patterns`, `Value LIKE ALL (b'Valu%1', b'V%lue1')`
  was expected to be `true` and returned `false`.
- The files with the most silent failures are `cast_function.test`
  (52), `stat_aggregation.test` (50), `analytic_sum.test` (36) and
  `collation.test` (31).

## Loud failures (errors), largest groups

These fail visibly, so they are lower risk than the silent ones.
Error text is quoted from the driver, with digits replaced by `N`.

| cases | driver error (prefix) |
|---:|---|
| 309 | `failed to analyze: CORRESPONDING BY for set operations is not supported` |
| 202 | `failed to analyze: CORRESPONDING for set operations is not supported` |
| about 100 | MATCH_RECOGNIZE not parsed (`Syntax error: Expected ";" or end of input but got "("` and `Expected keyword JOIN but got keyword MATCH_RECOGNIZE`) |
| 52 | `failed to analyze: The LIKE ANY\|SOME\|ALL operator does not support an ...` |
| 29 | `ARRAY_AGG: input value must be not null` |
| 28 | `panic: runtime error: invalid memory address or nil pointer dereference` |
| 25 + 18 | `frame starting offset must be a non-negative number` / `frame ending offset must be a non-negative number` |
| 25 + 17 + 6 | `unsupported gt operator for range value` (and `lt`, `gte`) |
| 17 | `zero divided error ( N.N / N )` |
| 12 | `unsupported add operator for interval value` |
| 8 | `failed to analyze: BY NAME for set operations is not supported` |
| 8 | `ROUND: invalid number of arguments: got N, want N or N` |

## Caveats

- The GoogleSQL reference allows NULL elements in arrays, but BigQuery
  rejects them at output (`Array cannot have a null element`). Some
  ARRAY_AGG failures, where the suite expects `[NULL, ...]` and the
  driver drops the NULL, are therefore not BigQuery divergences. The
  `ARRAY_AGG: input value must be not null` errors are closer to
  BigQuery behaviour than the suite's expectation.
- `REGEXP_MATCH` and `SPLIT_SUBSTR` appear in the suite but not in
  BigQuery. Their failures are listed for completeness only.
- Feature classification is one reviewer's reading of the BigQuery
  documentation. Moving a feature between the two lists in
  `filter.go` changes the counts.
- Neither full run recorded a timeout. Each file runs in its own
  child process with a watchdog (statement timeout plus 30 s). This was
  added after an early in-process attempt stalled: a query abandoned on
  timeout kept a driver-wide lock and blocked every later statement in
  that process.

## Reproducing

```bash
git clone --depth 1 --filter=blob:none --sparse https://github.com/google/googlesql.git /c/dev/wt/googlesql-src
git -C /c/dev/wt/googlesql-src sparse-checkout set googlesql/compliance/testdata
export MSYS2_ENV_CONV_EXCL=TZDIR TZDIR=/tmp/zoneinfo
go run ./cmd/specctl run-compliance -local-utc -j 6 -timeout 20s \
  -dir /c/dev/wt/googlesql-src/googlesql/compliance/testdata \
  -json compliance_results.json -all-json compliance_all.json -md compliance_summary.md
```

Drop `-local-utc` to reproduce the host zone run. `-files <regexp>`
limits the run to some files, and `-v` logs each case as it starts.

## Full tables (UTC run)

### Failure kinds

| kind | cases | silent |
|---|---:|---|
| wrong_values | 479 | yes |
| expected_error_got_rows | 145 | yes |
| row_count | 23 | yes |
| shape | 6 | yes |
| panic | 31 | no |
| driver_error | 1241 | no |

### Skipped, by reason

| reason | cases |
|---|---:|
| feature not in BigQuery: language feature not in BigQuery | 3269 |
| depends on a setup object that was skipped or failed | 1172 |
| non-BigQuery type in result | 1040 |
| feature not in BigQuery: SQL graph (GQL) is not part of the BigQuery dialect under test | 462 |
| non-BigQuery type in SQL | 345 |
| feature not in BigQuery: anonymization / differential privacy output is noise-dependent and uses non-BigQuery syntax | 280 |
| runner: DML result shape not compared | 242 |
| feature not in BigQuery: type not available in BigQuery | 221 |
| feature not in BigQuery: proto / enum types are not BigQuery types | 150 |
| (setup) non-BigQuery type in result | 105 |
| (setup) feature not in BigQuery: SQL graph (GQL) is not part of the BigQuery dialect under test | 61 |
| (setup) feature not in BigQuery: language feature not in BigQuery | 32 |
| feature not in BigQuery: storage-engine primary-key semantics do not apply to BigQuery | 32 |
| feature not in BigQuery: sampling output is nondeterministic | 29 |
| runner: cannot parse expected value | 22 |
| (setup) non-BigQuery type in SQL | 9 |
| (setup) feature not in BigQuery: type not available in BigQuery | 8 |
| (setup) non-UTC default_time_zone | 2 |
| (setup) runner: cannot parse expected value | 1 |
| runner: cannot parse file: compliancetest: no cases parsed; file may be malformed | 1 |

### dbt construct areas

| construct | run | pass | fail | error |
|---|---:|---:|---:|---:|
| window frames | 436 | 272 | 80 | 84 |
| NUMERIC arithmetic | 280 | 154 | 61 | 65 |
| EXCEPT DISTINCT | 103 | 21 | 0 | 82 |
| LIKE | 99 | 33 | 14 | 52 |
| HLL_COUNT.* | 38 | 14 | 4 | 20 |
| ARRAY_AGG ORDER BY / LIMIT | 37 | 22 | 9 | 6 |
| REGEXP_* | 20 | 7 | 10 | 3 |
| CASE | 46 | 37 | 2 | 7 |
| QUALIFY | 27 | 18 | 3 | 6 |
| SPLIT | 19 | 13 | 4 | 2 |
| APPROX_QUANTILES | 26 | 23 | 2 | 1 |
| IS DISTINCT FROM | 19 | 16 | 2 | 1 |
| TO_JSON_STRING | 3 | 0 | 3 | 0 |
| FORMAT %T | 2 | 0 | 2 | 0 |
| GENERATE_DATE_ARRAY | 4 | 2 | 2 | 0 |
| SAFE_DIVIDE | 12 | 10 | 1 | 1 |
| DATE_TRUNC | 1 | 0 | 0 | 1 |
| COALESCE | 23 | 23 | 0 | 0 |
| LEFT JOIN UNNEST | 6 | 6 | 0 | 0 |

### Per feature area (required_features)

| feature | run | pass | fail | error | skip |
|---|---:|---:|---:|---:|---:|
| CORRESPONDING_FULL | 519 | 0 | 0 | 519 | 93 |
| (no required feature) | 1851 | 1462 | 200 | 189 | 1286 |
| ANALYTIC_FUNCTIONS | 709 | 443 | 142 | 124 | 655 |
| RANGE_TYPE | 411 | 277 | 61 | 73 | 12 |
| MATCH_RECOGNIZE | 103 | 4 | 0 | 99 | 11 |
| CIVIL_TIME | 242 | 144 | 52 | 46 | 77 |
| NUMERIC_TYPE | 214 | 133 | 42 | 39 | 91 |
| PIPES | 121 | 49 | 1 | 71 | 162 |
| BIGNUMERIC_TYPE | 173 | 105 | 37 | 31 | 69 |
| ANNOTATION_FRAMEWORK | 114 | 48 | 36 | 30 | 299 |
| COLLATION_SUPPORT | 114 | 48 | 36 | 30 | 299 |
| LIKE_ANY_SOME_ALL | 86 | 22 | 12 | 52 | 63 |
| FORMAT_IN_CAST | 73 | 15 | 52 | 6 | 9 |
| INTERVAL_TYPE | 67 | 14 | 17 | 36 | 201 |
| LIKE_ANY_SOME_ALL_ARRAY | 56 | 4 | 0 | 52 | 0 |
| JSON_TYPE | 134 | 90 | 25 | 19 | 838 |
| GROUPING_SETS | 106 | 72 | 20 | 14 | 24 |
| HAVING_IN_AGGREGATE | 32 | 6 | 17 | 9 | 193 |
| NAMED_ARGUMENTS | 51 | 26 | 15 | 10 | 355 |
| SAFE_FUNCTION_CALL | 76 | 51 | 12 | 13 | 57 |
| ENCRYPTION | 60 | 38 | 8 | 14 | 0 |
| ORDER_BY_IN_AGGREGATE | 49 | 28 | 9 | 12 | 69 |
| NULL_HANDLING_MODIFIER_IN_AGGREGATE | 39 | 20 | 0 | 19 | 31 |
| ORDER_BY_COLLATE | 26 | 8 | 14 | 4 | 8 |
| WITH_RECURSIVE | 41 | 24 | 3 | 14 | 17 |
| ENFORCE_CONDITIONAL_EVALUATION | 16 | 0 | 0 | 16 | 4 |
| CREATE_TABLE_FUNCTION | 15 | 0 | 0 | 15 | 19 |
| GROUPING_BUILTIN | 28 | 13 | 10 | 5 | 11 |
| TABLE_VALUED_FUNCTIONS | 15 | 0 | 0 | 15 | 98 |
| TEMPLATE_FUNCTIONS | 15 | 0 | 0 | 15 | 19 |
| AGGREGATION_THRESHOLD | 12 | 0 | 0 | 12 | 0 |
| BY_NAME | 12 | 0 | 0 | 12 | 6 |
| PARAMETERIZED_TYPES | 16 | 5 | 8 | 3 | 33 |
| PIVOT | 32 | 21 | 3 | 8 | 0 |
| ROUND_WITH_ROUNDING_MODE | 12 | 1 | 1 | 10 | 0 |
| GROUP_BY_ROLLUP | 34 | 24 | 8 | 2 | 18 |
| LIMIT_IN_AGGREGATE | 30 | 20 | 8 | 2 | 22 |
| JSON_MUTATOR_FUNCTIONS | 26 | 17 | 9 | 0 | 75 |
| JSON_VALUE_EXTRACTION_FUNCTIONS | 42 | 33 | 2 | 7 | 82 |
| QUALIFY | 27 | 18 | 3 | 6 | 2 |
| EXTENDED_DATE_TIME_SIGNATURES | 8 | 0 | 3 | 5 | 9 |
| NULLS_FIRST_LAST_IN_ORDER_BY | 14 | 6 | 0 | 8 | 25 |
| NULL_HANDLING_MODIFIER_IN_ANALYTIC | 8 | 0 | 4 | 4 | 10 |
| JSON_CONSTRUCTOR_FUNCTIONS | 13 | 6 | 6 | 1 | 4 |
| WITH_ON_SUBQUERY | 19 | 12 | 1 | 6 | 80 |
| COLLATION_IN_EXPLICIT_CAST | 9 | 3 | 0 | 6 | 0 |
| JSON_ARRAY_FUNCTIONS | 15 | 9 | 3 | 3 | 8 |
| ADDITIONAL_STRING_FUNCTIONS | 15 | 10 | 3 | 2 | 0 |
| ALIASES_FOR_STRING_AND_DATE_FUNCTIONS | 4 | 0 | 0 | 4 | 0 |
| CORRESPONDING | 4 | 0 | 0 | 4 | 365 |
| GROUP_BY_STRUCT | 20 | 16 | 3 | 1 | 40 |
| PIPE_CALL_INPUT_TABLE | 4 | 0 | 0 | 4 | 0 |
| PIPE_WITH | 4 | 0 | 0 | 4 | 0 |
| ANALYSIS_CONSTANT_INTERVAL_CONSTRUCTOR | 3 | 0 | 0 | 3 | 0 |
| ANALYSIS_CONSTANT_PIVOT_COLUMN | 3 | 0 | 0 | 3 | 0 |
| DATE_TIME_CONSTRUCTORS | 4 | 1 | 1 | 2 | 1 |
| IS_DISTINCT | 19 | 16 | 2 | 1 | 25 |
| JSON_KEYS_FUNCTION | 9 | 6 | 3 | 0 | 0 |
| GROUP_BY_ALL | 25 | 23 | 0 | 2 | 7 |
| WEEK_WITH_WEEKDAY | 2 | 0 | 0 | 2 | 0 |
| ENFORCE_MICROS_MODE_IN_INTERVAL_TYPE | 1 | 0 | 1 | 0 | 0 |
| GEOGRAPHY | 3 | 2 | 0 | 1 | 1 |
| LITERAL_CONCATENATION | 1 | 0 | 0 | 1 | 0 |
| UNPIVOT | 19 | 18 | 0 | 1 | 0 |
| ADDITIONAL_DATE_TIME_FUNCTIONS | 0 | 0 | 0 | 0 | 18 |
| AGGREGATE_FILTERING | 0 | 0 | 0 | 0 | 45 |
| ALIGN_OPERATOR | 0 | 0 | 0 | 0 | 53 |
| ALLOW_CONSECUTIVE_ON | 0 | 0 | 0 | 0 | 1 |
| ANALYSIS_CONSTANT_STRUCT_POSITIONAL_ACCESSOR | 0 | 0 | 0 | 0 | 2 |
| ANONYMIZATION | 0 | 0 | 0 | 0 | 157 |
| ANONYMIZATION_THRESHOLDING | 0 | 0 | 0 | 0 | 2 |
| ARRAY_AGGREGATION_FUNCTIONS | 0 | 0 | 0 | 0 | 14 |
| ARRAY_DISTINCT | 0 | 0 | 0 | 0 | 14 |
| ARRAY_ELEMENTS_WITH_SET | 0 | 0 | 0 | 0 | 153 |
| ARRAY_EQUALITY | 0 | 0 | 0 | 0 | 31 |
| ARRAY_FIND_FUNCTIONS | 0 | 0 | 0 | 0 | 530 |
| ARRAY_OF_ARRAY | 0 | 0 | 0 | 0 | 29 |
| ARRAY_ORDERING | 0 | 0 | 0 | 0 | 67 |
| ARRAY_ZIP | 0 | 0 | 0 | 0 | 161 |
| BARE_ARRAY_ACCESS | 0 | 0 | 0 | 0 | 3 |
| BETWEEN_UINT64_INT64 | 0 | 0 | 0 | 0 | 5 |
| BITWISE_AGGREGATE_BYTES_SIGNATURES | 0 | 0 | 0 | 0 | 27 |
| BIT_CAST_BYTES_FUNCTIONS | 0 | 0 | 0 | 0 | 8 |
| BRACED_PROTO_CONSTRUCTORS | 0 | 0 | 0 | 0 | 4 |
| CAST_DIFFERENT_ARRAY_TYPES | 0 | 0 | 0 | 0 | 7 |
| CAST_OPERATORS | 0 | 0 | 0 | 0 | 3 |
| CAST_TO_JSON_TYPE | 0 | 0 | 0 | 0 | 52 |
| CHAINED_FUNCTION_CALLS | 0 | 0 | 0 | 0 | 17 |
| CONCAT_MIXED_TYPES | 0 | 0 | 0 | 0 | 18 |
| CORRELATED_REFS_IN_NESTED_DML | 0 | 0 | 0 | 0 | 8 |
| DECLARATIVE_TYPE_FRAMEWORK | 0 | 0 | 0 | 0 | 38 |
| DIFFERENTIAL_PRIVACY | 0 | 0 | 0 | 0 | 123 |
| DIFFERENTIAL_PRIVACY_MAX_ROWS_CONTRIBUTED | 0 | 0 | 0 | 0 | 4 |
| DIFFERENTIAL_PRIVACY_MIN_PRIVACY_UNITS_PER_GROUP | 0 | 0 | 0 | 0 | 17 |
| DIFFERENTIAL_PRIVACY_NESTED | 0 | 0 | 0 | 0 | 1 |
| DIFFERENTIAL_PRIVACY_PER_AGGREGATION_BUDGET | 0 | 0 | 0 | 0 | 5 |
| DIFFERENTIAL_PRIVACY_PUBLIC_GROUPS | 0 | 0 | 0 | 0 | 13 |
| DIFFERENTIAL_PRIVACY_REPORT_FUNCTIONS | 0 | 0 | 0 | 0 | 37 |
| DISALLOW_LEGACY_UNICODE_COLLATION | 0 | 0 | 0 | 0 | 1 |
| DISALLOW_NULL_PRIMARY_KEYS | 0 | 0 | 0 | 0 | 8 |
| DISALLOW_PRIMARY_KEY_UPDATES | 0 | 0 | 0 | 0 | 23 |
| DML_RETURNING | 0 | 0 | 0 | 0 | 37 |
| DML_UPDATE_WITH_JOIN | 4 | 4 | 0 | 0 | 11 |
| DOT_PRODUCT | 0 | 0 | 0 | 0 | 2 |
| ENABLE_CONSTANT_EXPRESSION_IN_JSON_PATH | 0 | 0 | 0 | 0 | 3 |
| ENABLE_MEASURES | 0 | 0 | 0 | 0 | 64 |
| ENUM_VALUE_DESCRIPTOR_PROTO | 0 | 0 | 0 | 0 | 6 |
| EXTRACT_FROM_PROTO | 0 | 0 | 0 | 0 | 13 |
| FILTER_FIELDS | 0 | 0 | 0 | 0 | 29 |
| FIRST_AND_LAST_N | 0 | 0 | 0 | 0 | 8 |
| GENERAL_QUANTIFIED_COMPARISONS | 0 | 0 | 0 | 0 | 439 |
| GROUP_BY_ARRAY | 0 | 0 | 0 | 0 | 166 |
| GROUP_BY_GRAPH_PATH | 0 | 0 | 0 | 0 | 6 |
| IMPLICIT_COERCION_STRING_LITERAL_TO_BYTES | 0 | 0 | 0 | 0 | 2 |
| INLINE_LAMBDA_ARGUMENT | 0 | 0 | 0 | 0 | 696 |
| JSON_ARRAY_VALUE_EXTRACTION_FUNCTIONS | 0 | 0 | 0 | 0 | 366 |
| JSON_CONTAINS_FUNCTION | 0 | 0 | 0 | 0 | 5 |
| JSON_FLATTEN_FUNCTION | 0 | 0 | 0 | 0 | 7 |
| JSON_LAX_VALUE_EXTRACTION_FUNCTIONS | 0 | 0 | 0 | 0 | 284 |
| JSON_MORE_VALUE_EXTRACTION_FUNCTIONS | 0 | 0 | 0 | 0 | 393 |
| JSON_QUERY_LAX | 0 | 0 | 0 | 0 | 7 |
| JSON_STRICT_NUMBER_PARSING | 0 | 0 | 0 | 0 | 2 |
| JSON_SUBFIELDS_WITH_SET | 0 | 0 | 0 | 0 | 69 |
| JSON_TYPE_COMPARISON | 0 | 0 | 0 | 0 | 27 |
| JSON_TYPE_COMPARISON_COERCION | 0 | 0 | 0 | 0 | 10 |
| KLL_QUANTILES_EXTRACT_RELATIVE_RANK | 0 | 0 | 0 | 0 | 33 |
| KLL_WEIGHTS | 0 | 0 | 0 | 0 | 42 |
| L1_NORM | 0 | 0 | 0 | 0 | 4 |
| L2_NORM | 0 | 0 | 0 | 0 | 4 |
| LATERAL_COLUMN_REFERENCES | 0 | 0 | 0 | 0 | 1 |
| LATERAL_JOIN | 0 | 0 | 0 | 0 | 7 |
| LIKE_ANY_SOME_ALL_SUBQUERY | 0 | 0 | 0 | 0 | 45 |
| LIMIT_ALL | 0 | 0 | 0 | 0 | 28 |
| LIMIT_OFFSET_EXPRESSIONS | 0 | 0 | 0 | 0 | 28 |
| MANHATTAN_DISTANCE | 0 | 0 | 0 | 0 | 2 |
| MAP_TYPE | 0 | 0 | 0 | 0 | 142 |
| MATCH_MAKE_STRUCT_IN_GROUP_BY | 0 | 0 | 0 | 0 | 2 |
| MULTILEVEL_AGGREGATION | 0 | 0 | 0 | 0 | 94 |
| MULTILEVEL_AGGREGATION_ON_UDAS | 0 | 0 | 0 | 0 | 20 |
| MULTIWAY_UNNEST | 0 | 0 | 0 | 0 | 51 |
| MULTI_GROUPING_SETS | 0 | 0 | 0 | 0 | 14 |
| NESTED_UPDATE_DELETE_WITH_OFFSET | 0 | 0 | 0 | 0 | 19 |
| PARSE_TIMESTAMP_WITH_PRECISION_AND_TIMEZONE | 0 | 0 | 0 | 0 | 11 |
| PIPE_AGGREGATE_WITH_DIFFERENTIAL_PRIVACY | 0 | 0 | 0 | 0 | 13 |
| PIPE_ASSERT | 0 | 0 | 0 | 0 | 25 |
| PIPE_CREATE_TABLE | 0 | 0 | 0 | 0 | 8 |
| PIPE_DESCRIBE | 0 | 0 | 0 | 0 | 8 |
| PIPE_FORK | 0 | 0 | 0 | 0 | 11 |
| PIPE_IF | 0 | 0 | 0 | 0 | 12 |
| PIPE_INSERT | 0 | 0 | 0 | 0 | 3 |
| PIPE_NAMED_WINDOWS | 0 | 0 | 0 | 0 | 2 |
| PIPE_RECURSIVE_UNION | 0 | 0 | 0 | 0 | 60 |
| PIPE_STATIC_DESCRIBE | 0 | 0 | 0 | 0 | 3 |
| PIPE_TEE | 0 | 0 | 0 | 0 | 13 |
| PROTO_DEFAULT_IF_NULL | 0 | 0 | 0 | 0 | 6 |
| PROTO_EXTENSIONS_WITH_NEW | 0 | 0 | 0 | 0 | 31 |
| PROTO_EXTENSIONS_WITH_SET | 0 | 0 | 0 | 0 | 11 |
| PROTO_MAPS | 0 | 0 | 0 | 0 | 35 |
| RADIANS_DEGREES_FUNCTIONS | 0 | 0 | 0 | 0 | 28 |
| RELAXED_WITH_RECURSIVE | 0 | 0 | 0 | 0 | 8 |
| REPLACE_FIELDS | 0 | 0 | 0 | 0 | 42 |
| REPLACE_FIELDS_ALLOW_MULTI_ONEOF | 0 | 0 | 0 | 0 | 8 |
| SAFE_FUNCTION_CALL_WITH_LAMBDA_ARGS | 0 | 0 | 0 | 0 | 34 |
| SELECT_STAR_EXCEPT_REPLACE | 2 | 2 | 0 | 0 | 0 |
| SINGLE_TABLE_NAME_ARRAY_PATH | 0 | 0 | 0 | 0 | 7 |
| SQL_GRAPH | 0 | 0 | 0 | 0 | 481 |
| SQL_GRAPH_ADVANCED_QUERY | 0 | 0 | 0 | 0 | 401 |
| SQL_GRAPH_BOUNDED_PATH_QUANTIFICATION | 0 | 0 | 0 | 0 | 156 |
| SQL_GRAPH_CALL | 0 | 0 | 0 | 0 | 6 |
| SQL_GRAPH_CHEAPEST_PATH | 0 | 0 | 0 | 0 | 52 |
| SQL_GRAPH_DYNAMIC_ELEMENT_TYPE | 0 | 0 | 0 | 0 | 47 |
| SQL_GRAPH_DYNAMIC_LABEL_PROPERTIES_IN_DDL | 0 | 0 | 0 | 0 | 47 |
| SQL_GRAPH_DYNAMIC_MULTI_LABEL_NODES | 0 | 0 | 0 | 0 | 17 |
| SQL_GRAPH_EXPOSE_GRAPH_ELEMENT | 0 | 0 | 0 | 0 | 49 |
| SQL_GRAPH_PATH_MODE | 0 | 0 | 0 | 0 | 63 |
| SQL_GRAPH_PATH_SEARCH_PREFIX_PATH_COUNT | 0 | 0 | 0 | 0 | 34 |
| SQL_GRAPH_PATH_TYPE | 0 | 0 | 0 | 0 | 90 |
| SQL_GRAPH_RETURN_EXTENSIONS | 0 | 0 | 0 | 0 | 10 |
| SQL_GRAPH_SAFE_SAME_ALL_DIFFERENT | 0 | 0 | 0 | 0 | 4 |
| SQL_GRAPH_SET_OPERATION_PROPAGATION_MODE | 0 | 0 | 0 | 0 | 14 |
| SQL_GRAPH_UNBOUNDED_PATH_QUANTIFICATION | 0 | 0 | 0 | 0 | 10 |
| STRATIFIED_RESERVOIR_TABLESAMPLE | 0 | 0 | 0 | 0 | 9 |
| STRUCT_POSITIONAL_ACCESSOR | 0 | 0 | 0 | 0 | 14 |
| TABLESAMPLE | 0 | 0 | 0 | 0 | 41 |
| TIMESTAMP_NANOS | 0 | 0 | 0 | 0 | 37 |
| TIMESTAMP_PICOS | 0 | 0 | 0 | 0 | 43 |
| TIMESTAMP_PRECISION | 0 | 0 | 0 | 0 | 4 |
| TIME_BUCKET_FUNCTIONS | 0 | 0 | 0 | 0 | 24 |
| TOP_LEVEL_TABLE_STATEMENTS | 0 | 0 | 0 | 0 | 9 |
| TUMBLE_HOP_TVFS | 0 | 0 | 0 | 0 | 62 |
| TYPEOF_FUNCTION | 0 | 0 | 0 | 0 | 30 |
| TYPE_ANNOTATIONS_ON_SQL_FUNCTION_ARGUMENTS | 0 | 0 | 0 | 0 | 17 |
| TYPE_MODIFIERS_IN_EXPLICIT_CONSTRUCTORS_AND_UDF | 0 | 0 | 0 | 0 | 18 |
| UNNEST_AND_FLATTEN_ARRAYS | 0 | 0 | 0 | 0 | 73 |
| UUID_TYPE | 0 | 0 | 0 | 0 | 53 |
| VECTOR_TYPE | 0 | 0 | 0 | 0 | 38 |
| WITH_EXPRESSION | 0 | 0 | 0 | 0 | 21 |
| WITH_GROUP_ROWS | 0 | 0 | 0 | 0 | 65 |
| WITH_RECURSIVE_DEPTH_MODIFIER | 0 | 0 | 0 | 0 | 28 |

### Per file

| file | run | pass | fail | error | skip |
|---|---:|---:|---:|---:|---:|
| additional_date_time_functions.test | 2 | 0 | 0 | 2 | 18 |
| aead.test | 21 | 13 | 0 | 8 | 0 |
| aggregate_analytic_filter.test | 0 | 0 | 0 | 0 | 1 |
| aggregate_filter.test | 0 | 0 | 0 | 0 | 35 |
| aggregate_percentile_cont.test | 14 | 0 | 0 | 14 | 0 |
| aggregation_distinct_queries.test | 80 | 47 | 7 | 26 | 78 |
| aggregation_having_modifier_queries.test | 11 | 0 | 11 | 0 | 32 |
| aggregation_order_by_queries.test | 17 | 14 | 3 | 0 | 19 |
| aggregation_queries.test | 116 | 97 | 4 | 15 | 145 |
| aggregation_threshold.test | 12 | 0 | 0 | 12 | 0 |
| ai_functions.test | 6 | 0 | 0 | 6 | 0 |
| align_operator.test | 0 | 0 | 0 | 0 | 48 |
| analytic_any_value.test | 11 | 11 | 0 | 0 | 11 |
| analytic_approx_count_distinct.test | 1 | 0 | 0 | 1 | 0 |
| analytic_approx_quantiles.test | 1 | 0 | 0 | 1 | 0 |
| analytic_approx_top_count.test | 1 | 0 | 0 | 1 | 0 |
| analytic_approx_top_sum.test | 1 | 0 | 0 | 1 | 0 |
| analytic_array_aggregation.test | 26 | 19 | 7 | 0 | 12 |
| analytic_array_concat_agg.test | 1 | 1 | 0 | 0 | 0 |
| analytic_avg.test | 37 | 23 | 14 | 0 | 23 |
| analytic_bignumeric_range_window_frames.test | 27 | 6 | 0 | 21 | 0 |
| analytic_bit_and.test | 0 | 0 | 0 | 0 | 4 |
| analytic_bit_or.test | 0 | 0 | 0 | 0 | 4 |
| analytic_bit_xor.test | 0 | 0 | 0 | 0 | 4 |
| analytic_count.test | 43 | 25 | 9 | 9 | 62 |
| analytic_count_if.test | 0 | 0 | 0 | 0 | 18 |
| analytic_cume_dist.test | 4 | 3 | 1 | 0 | 8 |
| analytic_dense_rank.test | 4 | 3 | 1 | 0 | 8 |
| analytic_first_value.test | 5 | 5 | 0 | 0 | 14 |
| analytic_hll_count.test | 22 | 2 | 0 | 20 | 0 |
| analytic_is_first_is_last.test | 7 | 7 | 0 | 0 | 0 |
| analytic_kll_quantiles.test | 8 | 0 | 0 | 8 | 2 |
| analytic_lag.test | 21 | 20 | 1 | 0 | 25 |
| analytic_last_value.test | 5 | 5 | 0 | 0 | 15 |
| analytic_lead.test | 21 | 20 | 1 | 0 | 25 |
| analytic_logical_and.test | 4 | 4 | 0 | 0 | 0 |
| analytic_logical_or.test | 4 | 4 | 0 | 0 | 0 |
| analytic_min_max.test | 37 | 22 | 15 | 0 | 17 |
| analytic_nth_value.test | 5 | 5 | 0 | 0 | 15 |
| analytic_ntile.test | 4 | 3 | 1 | 0 | 18 |
| analytic_null_handling_modifier_queries.test | 0 | 0 | 0 | 0 | 6 |
| analytic_numeric_range_window_frames.test | 27 | 6 | 0 | 21 | 0 |
| analytic_partitionby_orderby.test | 6 | 2 | 4 | 0 | 30 |
| analytic_percent_rank.test | 5 | 4 | 1 | 0 | 9 |
| analytic_percentile_cont.test | 16 | 3 | 9 | 4 | 0 |
| analytic_percentile_disc.test | 4 | 3 | 1 | 0 | 6 |
| analytic_rank.test | 3 | 2 | 1 | 0 | 8 |
| analytic_row_number.test | 1 | 1 | 0 | 0 | 5 |
| analytic_stat_aggregation.test | 108 | 89 | 19 | 0 | 10 |
| analytic_string_aggregation.test | 18 | 7 | 11 | 0 | 0 |
| analytic_sum.test | 87 | 34 | 36 | 17 | 37 |
| analytic_sum_2.test | 13 | 11 | 0 | 2 | 4 |
| analytic_window_frames.test | 49 | 49 | 0 | 0 | 100 |
| anonymization.test | 0 | 0 | 0 | 0 | 147 |
| anonymization_errors.test | 0 | 0 | 0 | 0 | 10 |
| any_value_aggregation.test | 6 | 2 | 0 | 4 | 9 |
| apply_lambda.test | 0 | 0 | 0 | 0 | 12 |
| approx_aggregation.test | 68 | 54 | 7 | 7 | 12 |
| arithmetic_functions.test | 23 | 21 | 2 | 0 | 2 |
| array_aggregation.test | 125 | 100 | 3 | 22 | 29 |
| array_constructors.test | 84 | 82 | 1 | 1 | 48 |
| array_find.test | 0 | 0 | 0 | 0 | 508 |
| array_functions.test | 122 | 107 | 6 | 9 | 57 |
| array_functions_with_lambda.test | 0 | 0 | 0 | 0 | 35 |
| array_includes.test | 9 | 9 | 0 | 0 | 35 |
| array_joins.test | 27 | 27 | 0 | 0 | 0 |
| array_of_arrays.test | 0 | 0 | 0 | 0 | 15 |
| array_parameters.test | 5 | 5 | 0 | 0 | 9 |
| array_path.test | 0 | 0 | 0 | 0 | 56 |
| array_queries.test | 0 | 0 | 0 | 0 | 59 |
| array_zip.test | 0 | 0 | 0 | 0 | 158 |
| authorization_functions.test | 2 | 2 | 0 | 0 | 0 |
| bytes.test | 61 | 46 | 13 | 2 | 1 |
| call_sql_objects.test | 5 | 0 | 0 | 5 | 0 |
| call_sql_tvf.test | 9 | 0 | 0 | 9 | 0 |
| call_sql_tvfs_with_multi_level_agg.test | 3 | 0 | 0 | 3 | 4 |
| call_sql_uda.test | 30 | 0 | 0 | 30 | 0 |
| call_sql_udas_with_multi_level_agg.test | 13 | 0 | 0 | 13 | 24 |
| call_sql_udf.test | 30 | 4 | 0 | 26 | 11 |
| case_statement_queries.test | 53 | 50 | 0 | 3 | 12 |
| cast_format_validation.test | 22 | 12 | 10 | 0 | 2 |
| cast_function.test | 122 | 60 | 52 | 10 | 65 |
| cast_function_to_json.test | 0 | 0 | 0 | 0 | 42 |
| cast_timestamp_with_timezone.test | 4 | 0 | 4 | 0 | 0 |
| chained_function_call.test | 0 | 0 | 0 | 0 | 5 |
| civil_time.test | 51 | 24 | 15 | 12 | 21 |
| collation.test | 71 | 35 | 31 | 5 | 49 |
| collation_in_sql_functions.test | 0 | 0 | 0 | 0 | 17 |
| comparison_functions.test | 92 | 77 | 11 | 4 | 24 |
| compression.test | 9 | 5 | 4 | 0 | 0 |
| concat_function.test | 0 | 0 | 0 | 0 | 16 |
| conditional_evaluation.test | 14 | 8 | 0 | 6 | 3 |
| constant_queries.test | 12 | 12 | 0 | 0 | 0 |
| corresponding_edge_cases.test | 0 | 0 | 0 | 0 | 24 |
| corresponding_subqueries.test | 0 | 0 | 0 | 0 | 288 |
| date.test | 24 | 16 | 5 | 3 | 5 |
| default_timezone_ist.test | 1 | 0 | 1 | 0 | 0 |
| default_timezone_pst.test | 1 | 0 | 1 | 0 | 0 |
| default_timezone_utc.test | 1 | 1 | 0 | 0 | 0 |
| differential_privacy.test | 0 | 0 | 0 | 0 | 97 |
| differential_privacy_errors.test | 0 | 0 | 0 | 0 | 13 |
| dml_delete.test | 6 | 2 | 4 | 0 | 7 |
| dml_insert.test | 23 | 8 | 15 | 0 | 58 |
| dml_nested.test | 16 | 16 | 0 | 0 | 93 |
| dml_returning.test | 0 | 0 | 0 | 0 | 35 |
| dml_update.test | 10 | 6 | 4 | 0 | 79 |
| dml_update_json.test | 0 | 0 | 0 | 0 | 69 |
| dml_update_proto.test | 0 | 0 | 0 | 0 | 93 |
| dml_update_struct.test | 2 | 1 | 1 | 0 | 53 |
| dml_value_table.test | 17 | 7 | 10 | 0 | 53 |
| elementwise_aggregation.test | 7 | 0 | 0 | 7 | 342 |
| enum_queries.test | 0 | 0 | 0 | 0 | 27 |
| equal_all.test | 0 | 0 | 0 | 0 | 37 |
| equal_any.test | 0 | 0 | 0 | 0 | 46 |
| except_intersect_queries.test | 76 | 38 | 0 | 38 | 68 |
| exists_functions.test | 33 | 33 | 0 | 0 | 13 |
| filter_fields.test | 0 | 0 | 0 | 0 | 29 |
| generalized_statement.test | 0 | 0 | 0 | 0 | 24 |
| generate_uuid.test | 4 | 4 | 0 | 0 | 1 |
| graph_cheapest.test | 0 | 0 | 0 | 0 | 47 |
| graph_cyclic.test | 0 | 0 | 0 | 0 | 28 |
| graph_functions.test | 0 | 0 | 0 | 0 | 2 |
| graph_gql_dynamic_graph_element.test | 0 | 0 | 0 | 0 | 30 |
| graph_gql_dynamic_graph_element_multi_labels_node.test | 0 | 0 | 0 | 0 | 17 |
| graph_query_statement.test | 0 | 0 | 0 | 0 | 11 |
| graph_table.test | 0 | 0 | 0 | 0 | 85 |
| graph_table_gql_extended1.test | 0 | 0 | 0 | 0 | 78 |
| graph_table_gql_extended2.test | 0 | 0 | 0 | 0 | 95 |
| graph_table_gql_extended3.test | 0 | 0 | 0 | 0 | 30 |
| graph_table_gql_extended4.test | 0 | 0 | 0 | 0 | 29 |
| graph_table_gql_extended5.test | 0 | 0 | 0 | 0 | 29 |
| greater_than_all.test | 0 | 0 | 0 | 0 | 33 |
| greater_than_any.test | 0 | 0 | 0 | 0 | 33 |
| greater_than_or_equal_all.test | 0 | 0 | 0 | 0 | 33 |
| greater_than_or_equal_any.test | 0 | 0 | 0 | 0 | 33 |
| group_by_all.test | 25 | 23 | 0 | 2 | 4 |
| group_rows.test | 0 | 0 | 0 | 0 | 65 |
| groupby_queries.test | 28 | 22 | 0 | 6 | 57 |
| groupby_queries_2.test | 7 | 5 | 0 | 2 | 60 |
| grouping_sets_queries.test | 103 | 71 | 20 | 12 | 4 |
| hash.test | 3 | 3 | 0 | 0 | 0 |
| having_queries.test | 47 | 45 | 0 | 2 | 0 |
| hints.test | 13 | 4 | 9 | 0 | 0 |
| hll_count.test | 16 | 12 | 4 | 0 | 0 |
| hop_tvf.test | 0 | 0 | 0 | 0 | 33 |
| iferror.test | 36 | 29 | 0 | 7 | 2 |
| in_queries.test | 63 | 49 | 7 | 7 | 54 |
| interval.test | 64 | 12 | 17 | 35 | 8 |
| invoke_view.test | 2 | 0 | 0 | 2 | 0 |
| is_unknown.test | 10 | 10 | 0 | 0 | 0 |
| iserror.test | 21 | 15 | 1 | 5 | 2 |
| join_queries.test | 50 | 47 | 2 | 1 | 11 |
| json_bool_array.test | 0 | 0 | 0 | 0 | 22 |
| json_comparison.test | 0 | 0 | 0 | 0 | 27 |
| json_contains.test | 0 | 0 | 0 | 0 | 5 |
| json_double_array.test | 0 | 0 | 0 | 0 | 24 |
| json_float.test | 0 | 0 | 0 | 0 | 33 |
| json_float_array.test | 0 | 0 | 0 | 0 | 24 |
| json_int32.test | 0 | 0 | 0 | 0 | 24 |
| json_int32_array.test | 0 | 0 | 0 | 0 | 22 |
| json_int64_array.test | 0 | 0 | 0 | 0 | 22 |
| json_lax_bool_array.test | 0 | 0 | 0 | 0 | 22 |
| json_lax_double_array.test | 0 | 0 | 0 | 0 | 23 |
| json_lax_float.test | 0 | 0 | 0 | 0 | 29 |
| json_lax_float_array.test | 0 | 0 | 0 | 0 | 23 |
| json_lax_int32.test | 0 | 0 | 0 | 0 | 24 |
| json_lax_int32_array.test | 0 | 0 | 0 | 0 | 22 |
| json_lax_int64_array.test | 0 | 0 | 0 | 0 | 22 |
| json_lax_string_array.test | 0 | 0 | 0 | 0 | 22 |
| json_lax_uint32.test | 0 | 0 | 0 | 0 | 24 |
| json_lax_uint32_array.test | 0 | 0 | 0 | 0 | 24 |
| json_lax_uint64.test | 0 | 0 | 0 | 0 | 24 |
| json_lax_uint64_array.test | 0 | 0 | 0 | 0 | 24 |
| json_queries.test | 118 | 79 | 24 | 15 | 24 |
| json_string_array.test | 0 | 0 | 0 | 0 | 22 |
| json_uint32.test | 0 | 0 | 0 | 0 | 24 |
| json_uint32_array.test | 0 | 0 | 0 | 0 | 24 |
| json_uint64.test | 0 | 0 | 0 | 0 | 24 |
| json_uint64_array.test | 0 | 0 | 0 | 0 | 24 |
| keys.test | 39 | 25 | 8 | 6 | 0 |
| kll_quantiles_extract_relative_rank.test | 0 | 0 | 0 | 0 | 33 |
| kll_quantiles_init.test | 20 | 8 | 10 | 2 | 52 |
| kll_quantiles_merge_partial.test | 4 | 4 | 0 | 0 | 3 |
| lateral_join.test | 0 | 0 | 0 | 0 | 5 |
| lateral_join_on_tvf.test | 0 | 0 | 0 | 0 | 2 |
| less_than_all.test | 0 | 0 | 0 | 0 | 33 |
| less_than_any.test | 0 | 0 | 0 | 0 | 33 |
| less_than_or_equal_all.test | 0 | 0 | 0 | 0 | 33 |
| less_than_or_equal_any.test | 0 | 0 | 0 | 0 | 33 |
| like_all.test | 40 | 9 | 5 | 26 | 27 |
| like_any.test | 46 | 13 | 7 | 26 | 26 |
| limit_all.test | 0 | 0 | 0 | 0 | 26 |
| limit_queries.test | 58 | 58 | 0 | 0 | 26 |
| logical_functions.test | 59 | 59 | 0 | 0 | 0 |
| map_functions.test | 0 | 0 | 0 | 0 | 138 |
| match_recognize.test | 47 | 0 | 0 | 47 | 5 |
| match_recognize_in_sql_functions.test | 1 | 0 | 0 | 1 | 0 |
| match_recognize_navigation_functions_in_define.test | 7 | 4 | 0 | 3 | 0 |
| math_functions.test | 1 | 0 | 1 | 0 | 28 |
| measure_annotations.test | 0 | 0 | 0 | 0 | 5 |
| measures.test | 0 | 0 | 0 | 0 | 55 |
| measures_with_udas.test | 0 | 0 | 0 | 0 | 4 |
| misc.test | 13 | 13 | 0 | 0 | 8 |
| multi_grouping_sets.test | 0 | 0 | 0 | 0 | 14 |
| multi_level_aggregation_basic.test | 0 | 0 | 0 | 0 | 6 |
| multi_level_aggregation_complex.test | 0 | 0 | 0 | 0 | 33 |
| nano_timestamp.test | 0 | 0 | 0 | 0 | 6 |
| new_uuid.test | 0 | 0 | 0 | 0 | 5 |
| no_tests.test | 0 | 0 | 0 | 0 | 1 |
| not_equal_all.test | 0 | 0 | 0 | 0 | 37 |
| not_equal_any.test | 0 | 0 | 0 | 0 | 37 |
| nulliferror.test | 21 | 17 | 0 | 4 | 3 |
| numeric.test | 9 | 2 | 4 | 3 | 0 |
| numeric_aggregation_queries.test | 68 | 59 | 9 | 0 | 0 |
| orderby_collate_queries.test | 21 | 7 | 12 | 2 | 1 |
| orderby_numeric_queries.test | 16 | 16 | 0 | 0 | 0 |
| orderby_queries.test | 10 | 9 | 0 | 1 | 129 |
| orderby_range_queries.test | 24 | 0 | 24 | 0 | 0 |
| pico_timestamp.test | 0 | 0 | 0 | 0 | 36 |
| pipe_aggregate_with_dp_errors.test | 0 | 0 | 0 | 0 | 13 |
| pipe_align_operator.test | 0 | 0 | 0 | 0 | 5 |
| pipe_assert.test | 0 | 0 | 0 | 0 | 25 |
| pipe_call.test | 6 | 0 | 0 | 6 | 0 |
| pipe_describe.test | 0 | 0 | 0 | 0 | 8 |
| pipe_if.test | 0 | 0 | 0 | 0 | 12 |
| pipe_match_recognize.test | 46 | 0 | 0 | 46 | 5 |
| pipe_operators.test | 53 | 37 | 0 | 16 | 5 |
| pipe_recursive_union.test | 0 | 0 | 0 | 0 | 60 |
| pipe_static_describe.test | 0 | 0 | 0 | 0 | 3 |
| pipe_syntax.test | 14 | 12 | 1 | 1 | 0 |
| pivot.test | 39 | 28 | 3 | 8 | 0 |
| proto2_unknown_enums.test | 0 | 0 | 0 | 0 | 16 |
| proto3_fields.test | 0 | 0 | 0 | 0 | 62 |
| proto_constructor.test | 0 | 0 | 0 | 0 | 49 |
| proto_extensions.test | 0 | 0 | 0 | 0 | 4 |
| proto_field_formats.test | 0 | 0 | 0 | 0 | 11 |
| proto_maps.test | 0 | 0 | 0 | 0 | 30 |
| proto_queries.test | 0 | 0 | 0 | 0 | 17 |
| proto_wkt.test | 0 | 0 | 0 | 0 | 2 |
| qualify.test | 12 | 12 | 0 | 0 | 0 |
| rand.test | 9 | 7 | 2 | 0 | 0 |
| range.test | 8 | 8 | 0 | 0 | 1 |
| range_constructors.test | 41 | 30 | 8 | 3 | 1 |
| range_functions.test | 43 | 37 | 3 | 3 | 4 |
| regexp_functions.test | 4 | 4 | 0 | 0 | 0 |
| regression2.test | 0 | 0 | 0 | 0 | 4 |
| replace_fields.test | 0 | 0 | 0 | 0 | 41 |
| round.test | 13 | 2 | 1 | 10 | 0 |
| safe_function.test | 40 | 25 | 9 | 6 | 18 |
| select_distinct.test | 53 | 48 | 1 | 4 | 15 |
| set_operation_by_name.test | 8 | 0 | 0 | 8 | 0 |
| set_operation_full_corresponding_by.test | 126 | 0 | 0 | 126 | 12 |
| set_operation_full_mode.test | 83 | 0 | 0 | 83 | 15 |
| set_operation_inner_corresponding_by.test | 46 | 0 | 0 | 46 | 9 |
| set_operation_left_corresponding.test | 49 | 0 | 0 | 49 | 24 |
| set_operation_left_corresponding_by.test | 66 | 0 | 0 | 66 | 12 |
| set_operation_nested.test | 72 | 0 | 0 | 72 | 0 |
| set_operation_strict_corresponding.test | 31 | 0 | 0 | 31 | 6 |
| set_operation_strict_corresponding_by.test | 31 | 0 | 0 | 31 | 6 |
| stat_aggregation.test | 179 | 129 | 50 | 0 | 0 |
| strings.test | 164 | 137 | 21 | 6 | 1 |
| struct_positional_accessor.test | 0 | 0 | 0 | 0 | 14 |
| struct_queries.test | 26 | 25 | 1 | 0 | 8 |
| subquery_expression_clause_correlation_matrix_queries.test | 4 | 4 | 0 | 0 | 46 |
| subquery_expression_queries.test | 21 | 17 | 4 | 0 | 10 |
| tablesample.test | 0 | 0 | 0 | 0 | 36 |
| timestamp_with_default_time_zone.test | 12 | 5 | 7 | 0 | 6 |
| timestamp_with_default_time_zone_2.test | 12 | 4 | 8 | 0 | 7 |
| timezones.test | 8 | 0 | 5 | 3 | 0 |
| top_level_table_statement.test | 0 | 0 | 0 | 0 | 9 |
| tumble_tvf.test | 0 | 0 | 0 | 0 | 29 |
| type_queries.test | 29 | 29 | 0 | 0 | 13 |
| typeof.test | 0 | 0 | 0 | 0 | 19 |
| union_distinct_queries.test | 6 | 5 | 1 | 0 | 16 |
| unionall_queries.test | 18 | 15 | 0 | 3 | 16 |
| unnest_multiway.test | 0 | 0 | 0 | 0 | 51 |
| unnest_queries.test | 20 | 19 | 1 | 0 | 1 |
| unpivot.test | 14 | 13 | 0 | 1 | 0 |
| uuid.test | 0 | 0 | 0 | 0 | 22 |
| value_table_queries.test | 9 | 4 | 5 | 0 | 17 |
| vector.test | 4 | 0 | 0 | 4 | 78 |
| with_expressions.test | 0 | 0 | 0 | 0 | 7 |
| with_queries.test | 21 | 20 | 0 | 1 | 0 |
| with_recursive.test | 38 | 23 | 1 | 14 | 5 |
| with_recursive_relaxed.test | 0 | 0 | 0 | 0 | 8 |
