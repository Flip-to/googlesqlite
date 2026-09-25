#!/usr/bin/env python3
"""Summarise a TestDocsExamples results.json into markdown tables.

Usage:
    python3 tools/docs_examples_report.py RESULTS_JSON BQ_FUNCTIONS_TXT > summary.md

BQ_FUNCTIONS_TXT lists the function names on the BigQuery "all
functions" reference page, one per line. A failure is tagged
GoogleSQL-only when it calls a function missing from that list or
returns a type BigQuery does not have.
"""
import collections
import json
import re
import sys

# Constructs a dbt project leans on, most important first. Each entry
# is (label, regex over the example SQL).
DBT_CONSTRUCTS = [
    ("SAFE_DIVIDE", r"\bSAFE_DIVIDE\s*\("),
    ("UNNEST join", r"\b(JOIN|,)\s*UNNEST\s*\(|CROSS\s+JOIN\s+UNNEST"),
    ("QUALIFY", r"\bQUALIFY\b"),
    ("FORMAT", r"\bFORMAT\s*\("),
    ("HLL_COUNT", r"\bHLL_COUNT\."),
    ("ARRAY_AGG", r"\bARRAY_AGG\s*\("),
    ("GENERATE_DATE_ARRAY", r"\bGENERATE_DATE_ARRAY\s*\("),
    ("IS DISTINCT FROM", r"\bIS\s+(NOT\s+)?DISTINCT\s+FROM\b"),
    ("window function", r"\bOVER\s*\(|\bOVER\s+\w+\b"),
    # UNION ALL alone is how the docs build inline fixtures; only count
    # the less common set operations.
    ("set operation", r"\b(INTERSECT|EXCEPT)\s+(ALL|DISTINCT)\b|\bUNION\s+DISTINCT\b|\b(BY\s+NAME|CORRESPONDING)\b"),
    ("TO_JSON_STRING", r"\bTO_JSON_STRING\s*\("),
    ("FARM_FINGERPRINT", r"\bFARM_FINGERPRINT\s*\("),
    ("APPROX_QUANTILES", r"\bAPPROX_QUANTILES\s*\("),
    ("NUMERIC", r"\b(BIG)?NUMERIC\b"),
    ("DATE/TIMESTAMP function", r"\b(DATE|DATETIME|TIMESTAMP|TIME)(_\w+)?\s*\(|\bEXTRACT\s*\(|\bPARSE_(DATE|DATETIME|TIMESTAMP|TIME)\b|\bFORMAT_(DATE|DATETIME|TIMESTAMP|TIME)\b|\bCAST\s*\(.*\bAS\s+(TIMESTAMP|DATE|DATETIME)\b"),
    ("COALESCE", r"\bCOALESCE\s*\("),
    ("LIKE", r"\bLIKE\b"),
    ("SPLIT", r"\bSPLIT\s*\("),
    ("REGEXP_*", r"\bREGEXP_\w+\s*\("),
    ("CAST ... FORMAT", r"\bAS\s+\w+(<[^>]*>)?\s+FORMAT\b"),
    ("JSON function", r"\bJSON_\w+\s*\(|\bLAX_\w+\s*\("),
]

SYNTAX_WORDS = {
    "CAST", "SAFE_CAST", "IF", "IFNULL", "NULLIF", "STRUCT", "ARRAY", "UNNEST",
    "EXISTS", "IN", "AS", "OVER", "SELECT", "FROM", "WHERE", "VALUES", "JSON",
    "DATE", "DATETIME", "TIME", "TIMESTAMP", "INTERVAL", "NUMERIC", "BIGNUMERIC",
    "RANGE", "ANY", "SOME", "ALL", "AND", "OR", "NOT", "CASE", "WHEN", "THEN",
    "ELSE", "END", "USING", "ON", "PIVOT", "UNPIVOT", "EXTRACT", "COLLATE",
    "OFFSET", "ORDINAL", "SAFE_OFFSET", "SAFE_ORDINAL", "WITH", "JOIN", "LIKE",
    "TABLESAMPLE", "GROUPING", "ROLLUP", "CUBE", "SETS", "QUALIFY", "WINDOW",
    "PARTITION", "BY", "ORDER", "ROWS", "BETWEEN", "INTERSECT", "EXCEPT", "UNION",
    "DISTINCT", "LIMIT", "HAVING", "GROUP", "FORMAT", "NEW", "REPLACE", "BYTES",
    "STRING", "INT64", "FLOAT64", "BOOL", "GEOGRAPHY", "TABLE", "MATCH", "GRAPH",
    "LAMBDA", "KEY", "ERROR", "COALESCE", "SAFE", "HLL_COUNT", "KLL_QUANTILES",
    "NET", "AEAD", "KEYS", "DETERMINISTIC_ENCRYPT", "DETERMINISTIC_DECRYPT_BYTES",
}
GOOGLESQL_ONLY_TYPES = ("INT32", "UINT32", "UINT64", "FLOAT", "ENUM", "PROTO", "MAP")


def load_bq_functions(path):
    with open(path, encoding="utf-8") as f:
        return {l.strip().upper() for l in f if l.strip()}


def strip_strings(sql):
    return re.sub(r"'''.*?'''|\"\"\".*?\"\"\"|'(?:\\.|[^'\\])*'|\"(?:\\.|[^\"\\])*\"", "''", sql, flags=re.S)


def googlesql_only(r, bq):
    code = strip_strings(" ".join((r.get("setup") or []) + [r["sql"]]))
    code = re.sub(r"--[^\n]*", "", code)
    missing = []
    for m in re.finditer(r"\b([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\s*\(", code):
        name = m.group(1).upper()
        base = name.split(".")[-1]
        if name.startswith("SAFE."):
            name = base
        if name in SYNTAX_WORDS or base in SYNTAX_WORDS:
            continue
        if not re.fullmatch(r"[A-Z0-9_.]+", m.group(1)):
            continue  # lower/mixed case: a UDF or table function defined in the example
        if name not in bq and base not in bq:
            missing.append(name)
    for t in r.get("emulator_types") or []:
        head = re.split(r"[<(]", t)[0].upper()
        if head in GOOGLESQL_ONLY_TYPES or any(x in t.upper() for x in ("<INT32", "<UINT", "<FLOAT>", "PROTO<")):
            missing.append("type " + t)
    return sorted(set(missing))


def constructs(r):
    code = strip_strings(" ".join((r.get("setup") or []) + [r["sql"]]))
    return [label for label, rx in DBT_CONSTRUCTS if re.search(rx, code, re.I | re.S)]


def priority(r):
    labels = constructs(r)
    order = [l for l, _ in DBT_CONSTRUCTS]
    best = min((order.index(l) for l in labels), default=len(order))
    silent = {"silent": 0, "loud": 1}.get(r.get("kind"), 2)
    return (1 if r.get("googlesql_only") else 0, best, silent, r["id"])


def main():
    results = json.load(open(sys.argv[1], encoding="utf-8"))
    bq = load_bq_functions(sys.argv[2])
    for r in results:
        if r["status"] in ("fail", "error"):
            r["googlesql_only"] = googlesql_only(r, bq)
            r["dbt_constructs"] = constructs(r)
    json.dump([r for r in results if r["status"] in ("fail", "error")],
              open(sys.argv[3], "w", encoding="utf-8"), indent=2, ensure_ascii=False)

    total = collections.Counter(r["status"] for r in results)
    new = [r for r in results if r["new"]]
    out = []
    out.append(f"examples: {len(results)}")
    out.append(f"already covered by testdata/specs: {sum(1 for r in results if r.get('covered_by'))}")
    out.append(f"duplicates across pages: {sum(1 for r in results if r.get('reason','').startswith('duplicate of '))}")
    out.append(f"new: {len(new)}")
    out.append("status (all): " + ", ".join(f"{k}={total[k]}" for k in ("pass", "fail", "error", "skipped")))
    nc = collections.Counter(r["status"] for r in new)
    out.append("status (new): " + ", ".join(f"{k}={nc[k]}" for k in ("pass", "fail", "error", "skipped")))
    fails = [r for r in results if r["status"] in ("fail", "error")]
    bqf = [r for r in fails if not r["googlesql_only"]]
    out.append(f"fail+error: {len(fails)}; BigQuery-applicable: {len(bqf)} "
               f"(silent {sum(1 for r in bqf if r.get('kind') == 'silent')}, loud {sum(1 for r in bqf if r.get('kind') == 'loud')}, order-only {sum(1 for r in bqf if r.get('kind') == 'order')}); "
               f"GoogleSQL-only: {len(fails) - len(bqf)}")
    out.append(f"passes not emitted (rounded float display): {sum(1 for r in results if r.get('rounded_float_match') and r['new'])}")
    out.append(f"passes not emitted (spec runner string comparison differs): {sum(1 for r in results if r.get('spec_runner_mismatch'))}")
    out.append("")

    out.append("| page | extracted | covered | new | pass | fail | error | skipped |")
    out.append("|---|---:|---:|---:|---:|---:|---:|---:|")
    by = collections.defaultdict(list)
    for r in results:
        by[r["page"]].append(r)
    for page in sorted(by):
        rs = by[page]
        c = collections.Counter(r["status"] for r in rs)
        out.append(f"| {page} | {len(rs)} | {sum(1 for r in rs if r.get('covered_by'))} | "
                   f"{sum(1 for r in rs if r['new'])} | {c['pass']} | {c['fail']} | {c['error']} | {c['skipped']} |")
    out.append("")

    out.append("| skip reason | count |")
    out.append("|---|---:|")
    sk = collections.Counter()
    for r in results:
        if r["status"] != "skipped":
            continue
        reason = r.get("reason", "")
        reason = re.sub(r"duplicate of .*", "duplicate of an example on another page", reason)
        reason = re.sub(r'cannot type documented cell ".*" as (\S+):.*', r"cannot type documented cell as \1", reason)
        reason = re.sub(r"references table \S+ that", "references a table that", reason)
        reason = re.sub(r"result row has \d+ cells, header has \d+", "result row and header cell counts differ", reason)
        sk[reason] += 1
    for k, v in sk.most_common():
        out.append(f"| {k} | {v} |")
    out.append("")

    out.append("| construct | BigQuery-applicable fail+error |")
    out.append("|---|---:|")
    cc = collections.Counter(l for r in bqf for l in r["dbt_constructs"])
    for label, _ in DBT_CONSTRUCTS:
        out.append(f"| {label} | {cc[label]} |")
    out.append("")

    out.append("Ranked BigQuery-applicable divergences:")
    out.append("")
    for r in sorted(bqf, key=priority):
        got = r.get("emulator_error") or json.dumps(r.get("emulator_rows"), ensure_ascii=False)
        doc = ("error: " + (r.get("documented_error") or "(docs say it errors)")) if r.get("documented_expects_error") \
            else json.dumps(r.get("documented_rows"), ensure_ascii=False)
        out.append(f"- {r['id']} [{r.get('kind')}] [{', '.join(r['dbt_constructs']) or '-'}] {r.get('reason','')}")
        out.append(f"  sql: {' '.join(r['sql'].split())[:300]}")
        out.append(f"  docs: {doc[:300]}")
        out.append(f"  driver: {' '.join(got.split())[:300]}")
    print("\n".join(out))


if __name__ == "__main__":
    main()
