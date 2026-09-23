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
| CAST(STRING AS DATE / DATETIME / TIME / TIMESTAMP FORMAT ...) parses with format elements, AT TIME ZONE, the format model rules and SAFE_CAST; where upstream doc examples disagree with real BigQuery, BigQuery wins (see `cast_format_parse_test.go`). STRUCT literals keep anonymous fields unnamed, so `TO_JSON_STRING(STRUCT(1, 2))` is `{"":1,"":2}` as in BigQuery (it printed `_field_0`, `_field_1`). | this PR | this PR | none | 3265 to 3277 |
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

- A statement that contains both TIMESTAMP and a NUMERIC literal cast
  analyzes with literal-cast folding off, so the NUMERIC literal can
  lose precision through DOUBLE. A full fix needs go-googlesql to
  expose a UTC TimeZone for AnalyzerOptions.SetDefaultTimeZone.
- The GoogleSQL compliance suite assumes an America/Los_Angeles default
  time zone; its TIMESTAMP-to-string and naive-TIMESTAMP cases disagree
  with BigQuery (UTC) and are expected to fail here.

- RANGE window frames ordered by a DOUBLE key that contains NaN or
  infinities: SQLite drops rows or builds wrong frames, and the
  driver's emulation fallback calls unregistered functions. Needs a Go
  frame implementation.
- flipto-dbt S36 (anonymous STRUCT fields collapse): the driver returns
  both fields positionally; the collapse happens in the harness, which
  keys fields by name. BigQuery's API names them `_field_1`, `_field_2`.

