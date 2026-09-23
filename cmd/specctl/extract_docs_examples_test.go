package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitStatementsRespectsStringsAndComments(t *testing.T) {
	got := splitStatements("CREATE TEMP FUNCTION f() AS (';'); -- a ; comment\nSELECT \"x;y\", f();\n")
	want := []string{"CREATE TEMP FUNCTION f() AS (';')", "-- a ; comment\nSELECT \"x;y\", f()"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	got = splitStatements("SELECT '''a;\nb''' AS s")
	if len(got) != 1 {
		t.Fatalf("triple-quoted string split: %q", got)
	}
}

func TestStripSQLComments(t *testing.T) {
	got := stripSQLComments("SELECT 'a--b' /* c */ -- d\n, \"#\" # e\n")
	if strings.Contains(got, " c ") || strings.Contains(got, " d") || strings.Contains(got, " e") {
		t.Fatalf("comment survived: %q", got)
	}
	if !strings.Contains(got, "'a--b'") || !strings.Contains(got, `"#"`) {
		t.Fatalf("string mangled: %q", got)
	}
}

func TestParseResultTable(t *testing.T) {
	tbl := `/*--------+----------------+
 | a      | b              |
 +--------+----------------+
 | 1      | [1, 2]         |
 | NULL   | "x | y"        |
 +--------+----------------*/`
	cols, rows, skip := parseResultTable(tbl)
	if skip != "" {
		t.Fatalf("skip: %s", skip)
	}
	if !reflect.DeepEqual(cols, []string{"a", "b"}) {
		t.Fatalf("cols %q", cols)
	}
	want := [][]string{{"1", "[1, 2]"}, {"NULL", `"x | y"`}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows %q", rows)
	}
	_, _, skip = parseResultTable("/*---+\n | a |\n +---+\n | ... |\n +---*/")
	if skip == "" {
		t.Fatalf("elided table not skipped")
	}
}

func TestHasTopLevelOrderBy(t *testing.T) {
	cases := map[string]bool{
		"SELECT x FROM t ORDER BY x":                       true,
		"SELECT SUM(x) OVER (ORDER BY y) FROM t":           false,
		"SELECT ARRAY_AGG(x ORDER BY x) FROM t":            false,
		"FROM t\n|> ORDER BY x":                            true,
		"SELECT 'ORDER BY' AS s":                           false,
		"WITH a AS (SELECT 1 ORDER BY 1) SELECT * FROM a":  false,
		"SELECT x FROM t -- ORDER BY x":                    false,
		"SELECT x FROM (SELECT 1 AS x) ORDER\n  BY x DESC": true,
	}
	for sql, want := range cases {
		if got := hasTopLevelOrderBy(sql); got != want {
			t.Errorf("%q: got %v want %v", sql, got, want)
		}
	}
}

const samplePage = "# Page\n\n" +
	"## Common tables\n\n" +
	"```googlesql\nWITH Produce AS\n (SELECT 'kale' as item, 23 as purchases\n  UNION ALL SELECT 'apple', 8)\nSELECT * FROM Produce\n\n" +
	"/*------------------+\n | item | purchases |\n +------------------+\n | kale | 23        |\n | apple| 8         |\n +------------------*/\n```\n\n" +
	"## SUM\n\n**Examples**\n\n" +
	"```googlesql\nSELECT SUM(purchases) AS s FROM Produce\n\n/*-----+\n | s   |\n +-----+\n | 31  |\n +-----*/\n```\n\n" +
	"Multi-statement with errors:\n\n" +
	"```googlesql\nSELECT SAFE_CAST('x' AS INT64) AS v;\n\n/*------+\n | v    |\n +------+\n | NULL |\n +------*/\n\n" +
	"SELECT CAST('x' AS INT64);\n-- Error: Bad INT64 value\n```\n\n" +
	"The following query produces an error:\n\n" +
	"```googlesql\nSELECT 1/0\n```\n\n" +
	"```googlesql\nSELECT RAND() AS r\n\n/*---+\n | r |\n +---+\n | 0.3 |\n +---*/\n```\n\n" +
	"```googlesql\nSELECT ARRAY_AGG(x) AS a FROM UNNEST([1,2]) AS x\n\n/*---+\n | a |\n +---+\n | [1, 2] |\n +---*/\n```\n\n" +
	"```googlesql\nSELECT x FROM UNNEST([2,1]) AS x ORDER BY x\n```\n\nThe results look like this:\n\n" +
	"```googlesql\n/*---+\n | x |\n +---+\n | 1 |\n | 2 |\n +---*/\n```\n"

func TestExtractPageExamples(t *testing.T) {
	exs := extractPageExamples("sample", samplePage)
	if len(exs) != 8 {
		for _, e := range exs {
			t.Logf("%+v", e)
		}
		t.Fatalf("got %d examples, want 8", len(exs))
	}
	fixture := exs[0]
	if len(fixture.Fixtures) != 0 {
		t.Errorf("fixture block attached itself: %v", fixture.Fixtures)
	}
	sum := exs[1]
	if sum.Section != "SUM" || !reflect.DeepEqual(sum.Fixtures, []string{"Produce"}) {
		t.Errorf("sum example: section %q fixtures %v", sum.Section, sum.Fixtures)
	}
	if len(sum.Setup) != 1 || !strings.HasPrefix(sum.Setup[0], "CREATE TABLE Produce AS SELECT 'kale'") {
		t.Errorf("sum setup: %q", sum.Setup)
	}
	if !reflect.DeepEqual(sum.Rows, [][]string{{"31"}}) || sum.Skip != "" {
		t.Errorf("sum rows %q skip %q", sum.Rows, sum.Skip)
	}
	if exs[2].SQL != "SELECT SAFE_CAST('x' AS INT64) AS v" || !reflect.DeepEqual(exs[2].Rows, [][]string{{"NULL"}}) {
		t.Errorf("multi-statement first: %+v", exs[2])
	}
	if !exs[3].ExpectError || exs[3].ErrorText != "Bad INT64 value" || exs[3].SQL != "SELECT CAST('x' AS INT64)" {
		t.Errorf("error comment example: %+v", exs[3])
	}
	if !exs[4].ExpectError || exs[4].SQL != "SELECT 1/0" {
		t.Errorf("prose error example: %+v", exs[4])
	}
	if exs[5].Skip != "RAND() is non-deterministic" {
		t.Errorf("rand skip: %q", exs[5].Skip)
	}
	if !strings.HasPrefix(exs[6].Skip, "ARRAY_AGG without ORDER BY") {
		t.Errorf("array_agg skip: %q", exs[6].Skip)
	}
}

func TestExtractPairsSeparateResultBlock(t *testing.T) {
	exs := extractPageExamples("sample", samplePage)
	_ = exs
	// The ORDER BY query's result lives in the next code block.
	all := extractPageExamples("sample", samplePage+"\n")
	var found *docExample
	for i := range all {
		if strings.HasSuffix(all[i].SQL, "ORDER BY x") {
			found = &all[i]
		}
	}
	if found == nil {
		t.Fatalf("paired example missing")
	}
	if !found.Ordered || !reflect.DeepEqual(found.Rows, [][]string{{"1"}, {"2"}}) {
		t.Errorf("paired example: %+v", *found)
	}
}

func TestProseErrorUsesStatementComments(t *testing.T) {
	page := "## PARSE_DATE\n\nThe following produces an error:\n\n" +
		"```googlesql\n-- This works because elements on both sides match.\nSELECT PARSE_DATE('%Y', '2008');\n\n" +
		"-- This produces an error because the year element is in different locations.\nSELECT PARSE_DATE('%Y %A', 'Thursday 2008');\n```\n"
	exs := extractPageExamples("p", page)
	if len(exs) != 1 || !exs[0].ExpectError || !strings.Contains(exs[0].SQL, "Thursday 2008") {
		t.Fatalf("got %+v", exs)
	}
}

func TestBareTableAndDistantProse(t *testing.T) {
	page := "## LIKE ANY\n\nIf not, an error is returned.\n\nIn the example, the operator checks a match:\n\n" +
		"```googlesql\nSELECT 'corba' LIKE ANY ('%orb%') as result;\n\n+--------+\n| result |\n+--------+\n| TRUE   |\n+--------+\n```\n"
	exs := extractPageExamples("p", page)
	if len(exs) != 1 || exs[0].ExpectError || !reflect.DeepEqual(exs[0].Rows, [][]string{{"TRUE"}}) {
		t.Fatalf("got %+v", exs)
	}
}

func TestCommentedParagraphsWithoutSemicolons(t *testing.T) {
	page := "## Interval\n\nFor example:\n\n" +
		"```googlesql\n-- -23 years\nSELECT INTERVAL '-23-2 10 -0:30' YEAR TO MINUTE\n\n" +
		"-- Produces an error because the sign is misplaced.\nSELECT INTERVAL '-23-2 10 0:-30' YEAR TO MINUTE\n```\n"
	exs := extractPageExamples("p", page)
	if len(exs) != 1 || !exs[0].ExpectError || len(exs[0].Setup) != 0 || !strings.Contains(exs[0].SQL, "0:-30") {
		t.Fatalf("got %+v", exs)
	}
}

func TestBareCTEFixtureAndAltSetups(t *testing.T) {
	page := "## LIKE\n\nConsider `Words`:\n\n" +
		"```googlesql\nWITH Words AS\n (SELECT 'Intend with clarity.' as value UNION ALL\n  SELECT 'Secure.')\n\n/*---+\n | value |\n +---+\n | Intend with clarity. |\n | Secure. |\n +---*/\n```\n\n" +
		"```googlesql\nSELECT * FROM Words WHERE value LIKE 'Intend%';\n\n/*---+\n | value |\n +---+\n | Intend with clarity. |\n +---*/\n```\n"
	exs, fx := extractPageExamplesFx("p", page)
	if len(exs) != 2 || exs[0].Skip != "fixture definition without a query" {
		t.Fatalf("got %+v", exs)
	}
	if len(exs[1].Setup) != 1 || !strings.HasPrefix(exs[1].Setup[0], "CREATE TABLE Words AS SELECT 'Intend") {
		t.Fatalf("setup %q", exs[1].Setup)
	}
	other := []fixture{{name: "Words", stmts: []string{"CREATE TABLE Words AS SELECT 'x' AS value"}, page: "q"}}
	for i := range fx {
		fx[i].page = "p"
	}
	addAltSetups(&exs[1], fx, append(fx, other...))
	if len(exs[1].AltSetups) != 1 || exs[1].AltSetups[0][0] != other[0].stmts[0] {
		t.Fatalf("alt %q", exs[1].AltSetups)
	}
}

func TestFunctionFixture(t *testing.T) {
	page := "## UDF\n\n```googlesql\nCREATE TEMP FUNCTION AddOne(x INT64) AS (x + 1);\n```\n\n" +
		"```googlesql\nSELECT AddOne(1) AS y;\n\n/*---+\n | y |\n +---+\n | 2 |\n +---*/\n```\n"
	exs := extractPageExamples("p", page)
	if len(exs) != 1 || len(exs[0].Setup) != 1 || !strings.HasPrefix(exs[0].Setup[0], "CREATE TEMP FUNCTION AddOne") {
		t.Fatalf("got %+v", exs)
	}
}

func TestNormalizeSQLDedup(t *testing.T) {
	a := normalizeSQL("SELECT  UPPER( 'x' )  AS u ;")
	b := normalizeSQL("select upper(\"x\") as u -- comment")
	if a != b {
		t.Fatalf("%q != %q", a, b)
	}
}

func TestSkipReasons(t *testing.T) {
	cases := map[string]string{
		"SELECT CURRENT_DATE() AS d":             "CURRENT_* depends on the clock",
		"SELECT STRING_AGG(x ORDER BY x) FROM t": "",
		"SELECT x FROM t LIMIT 2":                "LIMIT without ORDER BY picks arbitrary rows",
		"SELECT @p AS v":                         "uses query parameters",
		"SELECT APPROX_COUNT_DISTINCT(x) FROM UNNEST(GENERATE_ARRAY(1, 100000)) AS x": "APPROX_* over a large input is approximate",
		"SELECT APPROX_COUNT_DISTINCT(x) FROM UNNEST([1, 1, 2]) AS x":                 "",
	}
	for sql, want := range cases {
		ex := &docExample{SQL: sql, Columns: []string{"c"}}
		if got := skipReason(ex); got != want {
			t.Errorf("%q: got %q want %q", sql, got, want)
		}
	}
}
