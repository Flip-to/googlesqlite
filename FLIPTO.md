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
| Numeric aggregates and windows follow GoogleSQL rules. SUM/AVG are exact and report `int64` / `numeric` / `double` overflow; NaN and infinities propagate; window SUM/AVG/MIN/MAX over DOUBLE, NUMERIC and BIGNUMERIC no longer use SQLite built-ins, which sum NaN as 0, slide inf-inf to NULL and order NUMERIC as text; SUM(DISTINCT) keeps DOUBLE. Statistical aggregates (VAR/STDDEV/COVAR/CORR) return NULL for too few rows, use exact NUMERIC arithmetic and do not overflow on extreme DOUBLEs. HAVING MAX/MIN works on every aggregate. QUALIFY filters after window functions (it filtered before them). INTERVAL: comparison, + and -, DISTINCT and GROUP BY by value, negative parts keep their sign, fractional seconds parsed exactly. RANGE ordering. The bench harness reads CRLF corpus files. | this PR | this PR | none | 2944 to 3176 |
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

Tracked in `docs/compliance_run_results.md`. Not yet fixed:

- The GoogleSQL analyzer folds constant expressions in
  America/Los_Angeles. On every host,
  `CAST(TIMESTAMP '1970-01-01 00:00:01+00' AS DATE)` returns
  `1969-12-31`. go-googlesql v0.4.0 has no public way to build the
  `TimeZone` that `AnalyzerOptions.SetDefaultTimeZone` takes, so this
  needs a go-googlesql change.
