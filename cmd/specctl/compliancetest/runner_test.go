package compliancetest

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustResult(t *testing.T, s string) Result {
	t.Helper()
	r, err := ParseResult(s)
	if err != nil {
		t.Fatalf("ParseResult(%q): %v", s, err)
	}
	return r
}

func TestParseTypeNestedStruct(t *testing.T) {
	ty, err := ParseType("ARRAY<STRUCT<a INT64, STRING, t STRUCT<x ARRAY<DOUBLE>, y RANGE<DATE>>>>")
	if err != nil {
		t.Fatal(err)
	}
	row := ty.Elem
	if row.Kind != "STRUCT" || len(row.Fields) != 3 {
		t.Fatalf("row type = %+v", row)
	}
	if row.Fields[0].Name != "a" || row.Fields[1].Name != "" || row.Fields[1].Type.Kind != "STRING" {
		t.Fatalf("fields = %+v", row.Fields)
	}
	inner := row.Fields[2].Type
	if inner.Fields[0].Type.Kind != "ARRAY" || inner.Fields[0].Type.Elem.Kind != "DOUBLE" || inner.Fields[1].Type.Elem.Kind != "DATE" {
		t.Fatalf("inner = %+v", inner)
	}
}

func TestParseResultOrderedRows(t *testing.T) {
	r := mustResult(t, `ARRAY<STRUCT<o INT64, s STRING>>[known order:{2, "a\"b"}, {3, NULL}]`)
	if !r.Rows.Ordered || len(r.Rows.Elems) != 2 {
		t.Fatalf("rows = %+v", r.Rows)
	}
	if got := r.Rows.Elems[0].Elems[1].S; got != `a"b` {
		t.Fatalf("string cell = %q", got)
	}
	if !r.Rows.Elems[1].Elems[1].Null {
		t.Fatal("NULL cell not parsed")
	}
}

func TestParseResultIgnoresLabelSections(t *testing.T) {
	r := mustResult(t, "ARRAY<STRUCT<INT64>>[{1}]\n--\nFunctionName::COLLATE\nTypeKind:TYPE_INT64")
	if len(r.Rows.Elems) != 1 {
		t.Fatalf("rows = %+v", r.Rows)
	}
}

func TestParseResultError(t *testing.T) {
	r := mustResult(t, "ERROR: generic::out_of_range: division by zero: 1 / 0")
	if r.Err == nil || r.Err.Code != "generic::out_of_range" || r.Err.Message != "division by zero: 1 / 0" {
		t.Fatalf("err = %+v", r.Err)
	}
}

func TestParseResultEmbeddedArrayTypeAndTypedNull(t *testing.T) {
	r := mustResult(t, `ARRAY<STRUCT<ARRAY<>, ARRAY<>>>[{ARRAY<INT64>[known order:1, 2], ARRAY<STRING>(NULL)}]`)
	cells := r.Rows.Elems[0].Elems
	if cells[0].Type.Elem.Kind != "INT64" || !cells[0].Ordered || len(cells[0].Elems) != 2 {
		t.Fatalf("array cell = %+v", cells[0])
	}
	if !cells[1].Null {
		t.Fatalf("typed NULL array not parsed: %+v", cells[1])
	}
}

func TestParseResultScalarsAndNote(t *testing.T) {
	r := mustResult(t, "ARRAY<STRUCT<DATE, TIMESTAMP, BYTES, JSON, RANGE<DATE>, INTERVAL>>[\n"+
		`  {2014-01-01, 2010-10-19 22:00:00+00, b"\xc3\x28", {"b": 1, "a": [1.0]}, [2020-01-01, NULL), 0-0 0 -4:5:6.780}`+"\n]\nNOTE: reference reports this differently")
	c := r.Rows.Elems[0].Elems
	if c[0].S != "2014-01-01" || c[1].S != "2010-10-19 22:00:00" || c[2].S != "\xc3\x28" {
		t.Fatalf("cells = %+v", c[:3])
	}
	if c[3].S != `{"a":[1],"b":1}` {
		t.Fatalf("json = %q", c[3].S)
	}
	if c[4].S != "[2020-01-01, UNBOUNDED)" || c[5].S != "0-0 0 -4:5:6.78" {
		t.Fatalf("range/interval = %q %q", c[4].S, c[5].S)
	}
}

func TestUnquoteEscapes(t *testing.T) {
	got, err := unquoteString(`\347\264\257-\x41-é-\U0001F600-\n`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\xe7\xb4\xaf-A-é-\U0001F600-\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestCompareRowsUnorderedAndOrdered(t *testing.T) {
	r := mustResult(t, `ARRAY<STRUCT<INT64, STRING>>[unknown order:{1, "a"}, {2, "b"}]`)
	if why := CompareRows(r.Rows, [][]any{{int64(2), "b"}, {int64(1), "a"}}); why != "" {
		t.Fatalf("unordered match failed: %s", why)
	}
	o := mustResult(t, `ARRAY<STRUCT<INT64, STRING>>[known order:{1, "a"}, {2, "b"}]`)
	if why := CompareRows(o.Rows, [][]any{{int64(2), "b"}, {int64(1), "a"}}); why == "" {
		t.Fatal("ordered comparison accepted swapped rows")
	}
	if why := CompareRows(r.Rows, [][]any{{int64(1), "a"}}); !strings.HasPrefix(why, "row count") {
		t.Fatalf("row count diff = %q", why)
	}
}

func TestCompareNestedArrayOrder(t *testing.T) {
	r := mustResult(t, `ARRAY<STRUCT<ARRAY<INT64>>>[{ARRAY<INT64>[unknown order:1, 2, 3]}]`)
	if why := CompareRows(r.Rows, [][]any{{[]any{int64(3), int64(1), int64(2)}}}); why != "" {
		t.Fatalf("unordered array: %s", why)
	}
	k := mustResult(t, `ARRAY<STRUCT<ARRAY<INT64>>>[{ARRAY<INT64>[known order:1, 2, 3]}]`)
	if why := CompareRows(k.Rows, [][]any{{[]any{int64(3), int64(1), int64(2)}}}); why == "" {
		t.Fatal("ordered array accepted permutation")
	}
}

func TestCompareFloatULP(t *testing.T) {
	r := mustResult(t, `ARRAY<STRUCT<DOUBLE, DOUBLE, DOUBLE>>[{0.1, nan, -inf}]`)
	near := math.Nextafter(math.Nextafter(0.1, 1), 1) // 2 ULP away
	if why := CompareRows(r.Rows, [][]any{{near, math.NaN(), math.Inf(-1)}}); why != "" {
		t.Fatalf("within tolerance rejected: %s", why)
	}
	if why := CompareRows(r.Rows, [][]any{{0.1000001, math.NaN(), math.Inf(-1)}}); why == "" {
		t.Fatal("value outside tolerance accepted")
	}
}

func TestCompareDriverRepresentations(t *testing.T) {
	r := mustResult(t, `ARRAY<STRUCT<BYTES, TIMESTAMP, DATETIME, NUMERIC, BOOL, STRUCT<a INT64, b STRING>, RANGE<DATE>>>[`+
		`{b"ab", 2020-01-02 03:04:05.123456+00, 2020-01-02 03:04:05, 1.20, true, {1, "x"}, [2020-01-01, NULL)}]`)
	row := []any{"YWI=", "2020-01-02 03:04:05.123456+00", "2020-01-02T03:04:05", "1.2", true, []any{int64(1), "x"}, "[2020-01-01, UNBOUNDED)"}
	if why := CompareRows(r.Rows, [][]any{row}); why != "" {
		t.Fatalf("driver representation mismatch: %s", why)
	}
	row[3] = "1.3"
	if why := CompareRows(r.Rows, [][]any{row}); !strings.Contains(why, "column 4") {
		t.Fatalf("numeric diff = %q", why)
	}
}

func TestCompareTimestampOffsets(t *testing.T) {
	a, _ := parseTimestamp("2020-01-02 03:04:05+05:30")
	b, _ := parseTimestamp("2020-01-01T21:34:05Z")
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
}

func TestSkipReason(t *testing.T) {
	cases := []struct {
		feats, forbidden []string
		sql, header      string
		wantPrefix       string
	}{
		{[]string{"ANALYTIC_FUNCTIONS"}, nil, "SELECT 1", "ARRAY<STRUCT<INT64>>", ""},
		{[]string{"SQL_GRAPH"}, nil, "", "", "feature not in BigQuery"},
		{[]string{"SOME_NEW_FEATURE"}, nil, "", "", "unclassified feature: SOME_NEW_FEATURE"},
		{nil, []string{"QUALIFY"}, "", "", "case expects feature QUALIFY disabled"},
		{nil, nil, "SELECT CAST(1 AS INT32)", "", "non-BigQuery type in SQL"},
		{nil, nil, "SELECT 'INT32 inside a string'", "ARRAY<STRUCT<STRING>>", ""},
		{nil, nil, "SELECT 1", "ARRAY<STRUCT<UINT64>>", "non-BigQuery type in result"},
		{nil, nil, "SELECT CAST(1 AS FLOAT64)", "ARRAY<STRUCT<DOUBLE>>", ""},
	}
	for _, c := range cases {
		got := SkipReason(c.feats, c.forbidden, c.sql, c.header)
		if c.wantPrefix == "" && got != "" || !strings.HasPrefix(got, c.wantPrefix) {
			t.Errorf("SkipReason(%v,%v,%q,%q) = %q, want prefix %q", c.feats, c.forbidden, c.sql, c.header, got, c.wantPrefix)
		}
	}
}

func TestFeatureListsDisjoint(t *testing.T) {
	for f := range BigQueryFeatures {
		if _, ok := NonBigQueryFeatures[f]; ok {
			t.Errorf("%s is in both feature lists", f)
		}
	}
}

func TestParamsAndSubstitution(t *testing.T) {
	ps, err := ParseParams(`cast(NULL as string) as separator, 2 as lmt`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Name != "separator" || ps[0].Expr != "cast(NULL as string)" || ps[1].Name != "lmt" {
		t.Fatalf("params = %+v", ps)
	}
	got := SubstituteParams(`SELECT @lmt, '@lmt', @@sys, @LMT`, ps)
	if want := `SELECT (2), '@lmt', @@sys, (2)`; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestLoadSuiteFileDefaultsAndPrepare(t *testing.T) {
	body := "[default required_features=ANALYTIC_FUNCTIONS]\n[prepare_database]\nCREATE TABLE T AS SELECT 1 AS x\n--\nARRAY<STRUCT<x INT64>>[{1}]\n==\n" +
		"[required_features=QUALIFY]\n[name=q]\n[parameters=1 as p]\nSELECT @p\n--\nARRAY<STRUCT<INT64>>[{1}]\n"
	path := filepath.Join(t.TempDir(), "x.test")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cs, err := LoadSuiteFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || !cs[0].Prepare || cs[1].Prepare {
		t.Fatalf("cases = %+v", cs)
	}
	if CreatedObject(cs[0].SQL) != "T" {
		t.Fatalf("created = %q", CreatedObject(cs[0].SQL))
	}
	if strings.Join(cs[1].AllFeatures, ",") != "ANALYTIC_FUNCTIONS,QUALIFY" || len(cs[1].Params) != 1 {
		t.Fatalf("case 2 = %+v", cs[1])
	}
}

func TestCreatedObjectVariants(t *testing.T) {
	for sql, want := range map[string]string{
		"CREATE TEMP AGGREGATE FUNCTION f(x INT64) AS (SUM(x))": "f",
		"CREATE TABLE FUNCTION tvf(x INT64) AS SELECT x":        "tvf",
		"CREATE TEMP FUNCTION g() AS (1)":                       "g",
		"SELECT 1":                                              "",
	} {
		if got := CreatedObject(sql); got != want {
			t.Errorf("CreatedObject(%q) = %q, want %q", sql, got, want)
		}
	}
}

func TestMatchConstructs(t *testing.T) {
	got := MatchConstructs(`SELECT SAFE_DIVIDE(a, b), ARRAY_AGG(x ORDER BY y LIMIT 1) FROM t LEFT JOIN UNNEST(arr) QUALIFY ROW_NUMBER() OVER (ORDER BY a ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) = 1`, "")
	want := []string{"SAFE_DIVIDE", "LEFT JOIN UNNEST", "QUALIFY", "ARRAY_AGG ORDER BY / LIMIT", "window frames"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", got, want)
	}
	if len(MatchConstructs("SELECT ARRAY_AGG(x) FROM t", "")) != 0 {
		t.Fatal("plain ARRAY_AGG matched")
	}
}

func TestParseMultiLineHeaderAndEscapedComment(t *testing.T) {
	cs, err := Parse(strings.NewReader("[name=m]\n[parameters=1 as a,\n            2 as b]\nSELECT @a + @b\n\\-- trailing comment\n--\nARRAY<STRUCT<INT64>>[{3}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].Attrs["parameters"] != "1 as a, 2 as b" {
		t.Fatalf("cases = %+v", cs)
	}
	if cs[0].SQL != "SELECT @a + @b\n-- trailing comment" {
		t.Fatalf("sql = %q", cs[0].SQL)
	}
}

func TestParseParamsWithoutAS(t *testing.T) {
	ps, err := ParseParams(`cast(1 as int64) lmt, "de" collation_name, NULL param`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 || ps[0].Name != "lmt" || ps[0].Expr != "cast(1 as int64)" || ps[1].Name != "collation_name" || ps[2].Expr != "NULL" {
		t.Fatalf("params = %+v", ps)
	}
}
