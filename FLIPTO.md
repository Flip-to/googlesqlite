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

Every fix PR should re-run the suite, check that no previously passing
case now fails, and record the before and after pass counts in its
changelog entry below.

## Changelog

Newest first. Pass counts are for the compliance suite with the host
on UTC-3. "Upstream" names the matching PR on `goccy/googlesqlite`,
or says "none" if there isn't one.

### Fixes from compliance-suite divergences

| change | Flip-to PR | upstream | compliance passed |
|---|---|---|---|
| Build DATE values in UTC and decode TIMESTAMP values in UTC. Before this, DATE results shifted by one day on hosts west of UTC. Also avoid `time.Duration` overflow in `DATE_FROM_UNIX_DATE` outside 1677..2262. | #5 | none | 2757 to 2876 |
| Add the `specctl run-compliance` runner and the first compliance report. | #3 | none | baseline 2757 |

### Fixes carried before the compliance run

These were merged into the fork's integration branch before
`flipto/main` existed. Each one has a test in the repository.

| change | upstream |
|---|---|
| udf: format a SQL function body with column ids (UDF parameter used in a subquery) | goccy/googlesqlite#102 |
| tvf: format a templated TVF body with its own column state (aggregate in a subquery) | goccy/googlesqlite#101 |
| analyzer: allow ARRAY grouping keys and group composites by encoding | goccy/googlesqlite#100 |
| value: give `%T` its literal form for NUMERIC, BIGNUMERIC, JSON, INTERVAL | goccy/googlesqlite#99 |
| value: round NUMERIC and BIGNUMERIC products to the type's scale | goccy/googlesqlite#98 |
| approx_aggregate: sort APPROX_QUANTILES input and ignore NULLs by default | goccy/googlesqlite#97 |
| driver: clean up temp objects when QueryContext fails mid-script | goccy/googlesqlite#96 |
| value: round NUMERIC and BIGNUMERIC quotients to the type's scale | goccy/googlesqlite#95 |
| catalog: resolve a quoted dotted prefix in scalar UDF paths | goccy/googlesqlite#94 |
| json: encode DATE, DATETIME, TIME and TIMESTAMP as JSON strings in TO_JSON_STRING | goccy/googlesqlite#93 |
| catalog: skip cleanup of a temp table the script already dropped | goccy/googlesqlite#92 |
| script: evaluate ASSERT instead of treating it as a no-op | goccy/googlesqlite#91 |
| ddl: support DROP TABLE FUNCTION | goccy/googlesqlite#90 |
| catalog: resolve a quoted dotted prefix in table and TVF paths | goccy/googlesqlite#89 |
| tvf: support ANY TABLE and TABLE<...> parameters | goccy/googlesqlite#88 |
| catalog: keep TVF handles alive while the catalog references them | goccy/googlesqlite#87 |
| string: return a NULL array from SPLIT when an argument is NULL | goccy/googlesqlite#86 |
| formatter: keep the outer row in a correlated LEFT JOIN UNNEST | goccy/googlesqlite#85 |
| value: render FLOAT64 as text the way BigQuery does (CAST, CONCAT, FORMAT, TO_JSON_STRING) | goccy/googlesqlite#58 (third-party) |
| operator: IS [NOT] TRUE / FALSE never return NULL | goccy/googlesqlite#71 (third-party) |
| catalog: do not register builtins in sub-catalogs (memory growth on DROP TABLE); LIKE `_` and escapes | none found |
| array: return NULL from ARRAY_TO_STRING when an argument is NULL; script splitting respects comments | none found |
| FLOAT64 arithmetic on values nested in STRUCT and ARRAY | none found |
| GROUP BY ALL, including composite grouping keys | none found |
| math: return INT64 from MOD of two INT64 arguments | none found |
| deps: bump grpc to 1.83.2 and otel/sdk to 1.45.0 (security fixes) | none |

### Known open divergences

Tracked in `docs/compliance_run_results.md`. Not yet fixed:

- The GoogleSQL analyzer folds constant expressions in
  America/Los_Angeles. On every host,
  `CAST(TIMESTAMP '1970-01-01 00:00:01+00' AS DATE)` returns
  `1969-12-31`. go-googlesql v0.4.0 has no public way to build the
  `TimeZone` that `AnalyzerOptions.SetDefaultTimeZone` takes, so this
  needs a go-googlesql change.
