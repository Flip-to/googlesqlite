# dbt golden probes

`probes.jsonl` holds the differential probes of the Flip-to/flipto-dbt
emulator harness (`datamodeling/prod1-web/dbt/scripts/emulator_diff`)
together with the answer real BigQuery gave for each one. One JSON
object per line:

```json
{"id": "abs-3820.0.direct",
 "sql": "SELECT ABS(0) AS c0",
 "bigquery": {"schema": [{"name": "c0", "type": "INTEGER"}], "rows": [[0]]}}
```

- `bigquery.schema` is the BigQuery result schema (`mode` appears only
  for `REPEATED`; `fields` only for `RECORD`).
- `bigquery.rows` uses the harness's normalized value form
  (`compare.norm_value`): integers, strings and booleans as JSON, and
  `{"float": x}` (`"nan"`, `"inf"`, `"-inf"`), `{"numeric": "..."}`,
  `{"date": ...}`, `{"datetime": ...}`, `{"timestamp": ...}` (UTC,
  naive ISO), `{"bytes": base64}` and `{"struct": [[name, value], ...]}`.
- `bigquery.error` replaces schema and rows when BigQuery rejected the
  query; only the message is kept (no request URL, project or job id).

Every probe is a synthetic literal query (no table, dataset or project
references). The emulator's answers, the harness verdicts and the
construct-usage inventory are not copied.

`TestDBTGolden` (`dbt_golden_test.go`) runs every probe through the
driver and judges it the way `compare.py` does. Probes that do not
match yet are listed in `known_divergences.txt` with a reason; the test
fails when an unlisted probe diverges and when a listed probe starts to
match, so the list only shrinks.

## Regenerating

After a new harness run (`uv run python -m emulator_diff.run bigquery`
and `... report` in flipto-dbt), convert its output directory:

```sh
go run ./cmd/specctl import-dbt-golden \
  --src <flipto-dbt>/datamodeling/prod1-web/dbt/target/emudiff
```

The command reads `verdicts.json`, drops every probe whose SQL or
answer mentions a project, dataset or table name (it prints them), and
rewrites `probes.jsonl`. Then run `TestDBTGolden` and update
`known_divergences.txt`.
