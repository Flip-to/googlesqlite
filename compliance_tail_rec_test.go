package googlesqlite_test

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// TestComplianceTailRecursive covers recursive CTE shapes SQLite does
// not accept natively (a nested WITH inside the recursive UNION, a
// recursive reference inside a derived table, a UNION inside the
// recursive term), SAFE calls of SQL UDFs, TVF table arguments that
// must be evaluated once, INPUT TABLE in pipe CALL, TEMP views, nested
// joins and CASE over STRUCT values. Expected values come from the
// GoogleSQL compliance fixtures under compliance/testdata; the file and
// case name are cited per case. Rows are compared as an unordered
// multiset unless ordered is set; NULL renders as "NULL".
func TestComplianceTailRecursive(t *testing.T) {
	db, err := sql.Open("googlesqlite", ":memory:?_test=compliancetailrec")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for _, stmt := range []string{
		// join_queries.test setup (the TIMESTAMP column of
		// DateTimestampTable is left out: its expected values assume the
		// America/Los_Angeles default time zone).
		`CREATE TABLE R AS SELECT cast(1 as int64) as primary_key, cast(1 as int64) as id, cast("a1" as string) as a UNION ALL SELECT 2, 2, "a2"`,
		`CREATE TABLE S AS SELECT cast(1 as int64) as primary_key, cast(2 as int64) as id, cast("b21" as string) as b UNION ALL SELECT 2, 2, "b22" UNION ALL SELECT 3, 3, "b3"`,
		`CREATE TABLE DateTable AS SELECT 1 AS row_id, DATE '2018-02-03' AS date_val UNION ALL SELECT 2, DATE '2018-02-03' UNION ALL SELECT 3, DATE '2018-02-04' UNION ALL SELECT 4, NULL`,
		// call_sql_tvf.test setup (t1).
		`CREATE TABLE tvf_t1 AS
  SELECT * FROM UNNEST([
    STRUCT(1 AS rowid, 1 AS x, 100 AS y, 1000 AS z),
    STRUCT(2 AS rowid, 1 AS x, 100 AS y, 1000 AS z),
    STRUCT(3 AS rowid, 1 AS x, 100 AS y, 1001 AS z),
    STRUCT(4 AS rowid, 1 AS x, 100 AS y, 1002 AS z),
    STRUCT(5 AS rowid, 1 AS x, 100 AS y, 1003 AS z),
    STRUCT(6 AS rowid, 1 AS x, 100 AS y, 1004 AS z),
    STRUCT(7 AS rowid, 1 AS x, 101 AS y, 1000 AS z),
    STRUCT(8 AS rowid, 1 AS x, 101 AS y, 1001 AS z),
    STRUCT(9 AS rowid, 1 AS x, 101 AS y, 1002 AS z),
    STRUCT(10 AS rowid, 2 AS x, 100 AS y, 1000 AS z),
    STRUCT(11 AS rowid, 2 AS x, 101 AS y, 1001 AS z),
    STRUCT(12 AS rowid, 2 AS x, 102 AS y, 1002 AS z),
    STRUCT(13 AS rowid, 2 AS x, 102 AS y, NULL AS z),
    STRUCT(14 AS rowid, 2 AS x, 102 AS y, NULL AS z),
    STRUCT(15 AS rowid, 2 AS x, NULL AS y, 1002 AS z),
    STRUCT(16 AS rowid, 2 AS x, NULL AS y, 1002 AS z)
    ])`,
		// pipe_call.test setup (t1, t2).
		`CREATE TABLE pipe_t1 AS SELECT * FROM UNNEST([
			STRUCT(1 AS rowid, 1 AS x), STRUCT(2 AS rowid, 1 AS x), STRUCT(3 AS rowid, 2 AS x), STRUCT(4 AS rowid, 2 AS x)])`,
		`CREATE TABLE pipe_t2 AS SELECT * FROM UNNEST([
			STRUCT(5 AS rowid, 3 AS x), STRUCT(6 AS rowid, 3 AS x), STRUCT(7 AS rowid, 4 AS x), STRUCT(8 AS rowid, 5 AS x)])`,
		`CREATE TABLE FUNCTION tvf_nullary() AS (SELECT *, rand() AS rnd FROM tvf_t1)`,
		`CREATE TABLE FUNCTION tvf_references_arg_multiple_times(arg_t TABLE<rowid INT64, rnd FLOAT64>)
AS (
  SELECT r1 AS r1, r2 AS r2,
  FROM arg_t AS r1 INNER JOIN arg_t AS r2
    ON abs(r1.rnd - r2.rnd) < 0.0000000000001
)`,
		`CREATE TABLE FUNCTION tvf_references_arg_multiple_times_templated(arg_t ANY TABLE)
AS (
  SELECT r1 AS r1, r2 AS r2,
  FROM arg_t AS r1 INNER JOIN arg_t AS r2
    ON abs(r1.rnd - r2.rnd) < 0.0000000000001
)`,
		`CREATE TABLE FUNCTION tvf_add_column(t ANY TABLE) AS (FROM t |> EXTEND "added" AS added_column)`,
		`CREATE TABLE FUNCTION tvf_union2(arg1 ANY TABLE, arg2 ANY TABLE)
AS (
  SELECT "arg1" AS source_table, * FROM arg1
  UNION ALL
  SELECT "arg2", * FROM arg2
)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	// call_sql_udf.test and invoke_view.test declare TEMP functions and
	// views, which live for one script.
	const udfs = `CREATE TEMP FUNCTION OneOverArg(a FLOAT64) AS ( 1 / a );
CREATE TEMP FUNCTION Element(a ANY TYPE) AS ( (SELECT e FROM UNNEST(a) e) );
CREATE TEMP FUNCTION ErrorSubqueryFunction() AS (
  ARRAY(SELECT a FROM (SELECT 1 AS a) WHERE ERROR('oops'))
);
`
	const views = `CREATE TEMP FUNCTION One() AS ( 1 );
CREATE TEMP VIEW SelectOne SQL SECURITY INVOKER AS SELECT 1 AS a;
CREATE TEMP VIEW ViewWithUdf SQL SECURITY INVOKER AS SELECT One() AS a;
`
	rangeRows := func(vals ...string) [][]string {
		out := make([][]string, len(vals))
		for i, v := range vals {
			out[i] = []string{v}
		}
		return out
	}
	diag := make([][]string, 16)
	for i := range diag {
		v := strconv.Itoa(i + 1)
		diag[i] = []string{v, v}
	}

	for _, tc := range []struct {
		name    string
		sql     string
		rows    [][]string
		ordered bool
	}{
		// with_recursive.test
		{
			name: "recursive_query_nested_recursive_term",
			sql: `WITH RECURSIVE
  a AS (SELECT 1 AS n UNION ALL (
    WITH RECURSIVE
      b AS (SELECT 1 AS n UNION ALL (
        SELECT n + 1 FROM b WHERE n < 5
      ))
    SELECT n + 1 FROM a INNER JOIN b USING (n) WHERE n < 10
)) SELECT * FROM a`,
			rows: rangeRows("1", "2", "3", "4", "5", "6"),
		},
		{
			name: "recursive_query_nested_top_level",
			sql: `WITH RECURSIVE
  a AS (
    WITH RECURSIVE
      b AS (SELECT 0 AS n UNION ALL (
        SELECT n + 1 FROM b WHERE n < 5
      ))
      SELECT * FROM b
    UNION ALL SELECT 10 + n FROM a WHERE n < 30
) SELECT * FROM a ORDER BY n`,
			rows: rangeRows("0", "1", "2", "3", "4", "5", "10", "11", "12", "13", "14", "15",
				"20", "21", "22", "23", "24", "25", "30", "31", "32", "33", "34", "35"),
			ordered: true,
		},
		{
			name: "recursive_query_string_pattern_match",
			sql: `WITH RECURSIVE
  Inputs AS (
    SELECT '' AS s UNION ALL SELECT 'ab' AS s UNION ALL SELECT 'abb' AS s UNION ALL
    SELECT 'abab' AS s UNION ALL SELECT 'aa' AS s UNION ALL SELECT 'aab' AS s UNION ALL
    SELECT 'aabb' AS s UNION ALL SELECT 'aabbb' AS s UNION ALL SELECT 'aabbc' AS s UNION ALL
    SELECT 'caabb' AS s UNION ALL SELECT 'aacbb' AS s UNION ALL SELECT 'aaabb' AS s UNION ALL
    SELECT 'b' AS s UNION ALL SELECT 'ba' AS s UNION ALL SELECT 'bb' AS s UNION ALL
    SELECT 'aaaaabbbbb' AS s
  ),
 State AS (
   SELECT STRUCT(0 AS count, 'a' AS expected, FALSE AS error) AS state, 1 AS index, s AS input,
   FROM Inputs
   UNION ALL
   SELECT (
     CASE TRUE
       WHEN state.error THEN state
       WHEN state.count = 0 AND next_char = '' THEN STRUCT(0, '', FALSE)
       WHEN state.expected = 'a' AND next_char = 'a' THEN STRUCT(state.count + 1, 'a', FALSE)
       WHEN state.count > 0 AND next_char = 'b' THEN STRUCT(state.count - 1, 'b', FALSE)
       ELSE STRUCT(0, '', TRUE)
       END
       ) AS state,
       index + 1,
       input
   FROM (SELECT *, SUBSTR(input, index, 1) AS next_char FROM State)
   WHERE index <= LENGTH(input)
 )
SELECT
  State.input,
  State.state.count, State.state.expected, State.state.error,
  State.state.count = 0 AND NOT State.state.error AS match FROM State
WHERE State.index = LENGTH(State.input) + 1
ORDER BY match, input`,
			rows: [][]string{
				{"aa", "2", "a", "false", "false"},
				{"aaabb", "1", "b", "false", "false"},
				{"aab", "1", "b", "false", "false"},
				{"aabbb", "0", "", "true", "false"},
				{"aabbc", "0", "", "true", "false"},
				{"aacbb", "0", "", "true", "false"},
				{"abab", "0", "", "true", "false"},
				{"abb", "0", "", "true", "false"},
				{"b", "0", "", "true", "false"},
				{"ba", "0", "", "true", "false"},
				{"bb", "0", "", "true", "false"},
				{"caabb", "0", "", "true", "false"},
				{"", "0", "a", "false", "true"},
				{"aaaaabbbbb", "0", "b", "false", "true"},
				{"aabb", "0", "b", "false", "true"},
				{"ab", "0", "b", "false", "true"},
			},
			ordered: true,
		},
		{
			name: "with_recursive_union_distinct_multiple_rows_each_term",
			sql: `WITH RECURSIVE
  t AS (
    SELECT 0 AS a
    UNION DISTINCT (
      SELECT 5
      UNION ALL
      SELECT MOD(a + 1, 10) FROM t
    )
  )
SELECT * FROM t`,
			rows: rangeRows("0", "1", "5", "2", "6", "3", "7", "4", "8", "9"),
		},
		{
			name: "recursive_query_distinct_nested_in_non_recursive_term",
			sql: `WITH RECURSIVE t AS (
  WITH RECURSIVE t1 AS (
    SELECT 0 AS n
    UNION DISTINCT SELECT MOD(n + 1, 5) AS abcd FROM t1
  ) SELECT n * 2 AS n FROM t1 WHERE n >= 3
  UNION DISTINCT SELECT MOD(n + 1, 15) AS abcd FROM t
) SELECT * FROM t`,
			rows: rangeRows("6", "8", "9", "7", "10", "11", "12", "13", "14", "0", "1", "2", "3", "4", "5"),
		},
		{
			name: "recursive_query_distinct_nested_in_recursive_term",
			sql: `WITH RECURSIVE t AS (
  SELECT 0 AS n
  UNION DISTINCT (
    WITH RECURSIVE t1 AS (
        SELECT 0 AS n
        UNION DISTINCT SELECT MOD(n + 1, 3) AS abcd FROM t1
    )
    SELECT MOD(t.n + t1.n, 15) AS abcd FROM t CROSS JOIN t1
  )
) SELECT * FROM t`,
			rows: rangeRows("0", "2", "1", "4", "3", "6", "5", "8", "7", "10", "9", "12", "11", "14", "13"),
		},
		{
			name: "recursive_query_distinct_non_determinism",
			sql: `WITH RECURSIVE
  t AS (
    SELECT 0 AS a
    UNION DISTINCT
    SELECT CAST(RAND() * 100000 AS INT64) AS a FROM t
  )
SELECT COUNT(*) >= 3, COUNT(*) < 100000 FROM t`,
			rows: [][]string{{"true", "true"}},
		},
		{
			name: "recursive_query_non_recursive_term_empty_union_distinct",
			sql: `WITH RECURSIVE
  t0 AS (SELECT 0 AS n),
  t AS (
    SELECT n FROM t0 WHERE FALSE
    UNION DISTINCT SELECT * FROM (
      SELECT MOD(n + 1, 5) AS n FROM t UNION ALL (SELECT 1 AS n))
) SELECT * FROM t`,
			rows: rangeRows("1", "2", "3", "4", "0"),
		},
		// join_queries.test, join_8 (without the TIMESTAMP column).
		{
			name: "join_8",
			sql:  `SELECT * FROM R JOIN (S JOIN DateTable d ON S.id = d.row_id) ON R.id = S.id`,
			rows: [][]string{
				{"2", "2", "a2", "1", "2", "b21", "2", "2018-02-03"},
				{"2", "2", "a2", "2", "2", "b22", "2", "2018-02-03"},
			},
		},
		// struct_queries.test
		{
			name: "struct_equality_null_field",
			sql: `SELECT
  CASE s WHEN STRUCT(1, 2) THEN 1
         WHEN STRUCT(NULL, 2) THEN 2
         ELSE 3
  END AS value1,
  NULLIF(s, (NULL, 2)).x AS value2_x
FROM (SELECT STRUCT(1 AS x, 2 AS y) AS s
      UNION ALL SELECT STRUCT(NULL AS x, 2 AS y) AS s
      UNION ALL SELECT STRUCT(3 AS x, 2 AS y) AS s)`,
			rows: [][]string{{"3", "NULL"}, {"1", "1"}, {"3", "3"}},
		},
		// call_sql_tvf.test
		{
			name:    "tvf_references_arg_multiple_times",
			sql:     "SELECT r1.rowid, r2.rowid FROM tvf_references_arg_multiple_times((SELECT rowid, rand() AS rnd FROM tvf_t1)) ORDER BY r1.rowid, r2.rowid",
			rows:    diag,
			ordered: true,
		},
		{
			name:    "templated_tvf_references_arg_multiple_times_templated",
			sql:     "SELECT r1.rowid, r2.rowid FROM tvf_references_arg_multiple_times_templated((SELECT rowid, rand() AS rnd FROM tvf_t1)) ORDER BY r1.rowid, r2.rowid",
			rows:    diag,
			ordered: true,
		},
		{
			name:    "tvf_arg_is_itself_a_tvf_call",
			sql:     "SELECT r1.rowid, r2.rowid FROM tvf_references_arg_multiple_times(TABLE tvf_nullary()) ORDER BY r1.rowid, r2.rowid",
			rows:    diag,
			ordered: true,
		},
		// pipe_call.test
		{
			name: "tvf_call_with_input_table",
			sql:  "FROM pipe_t1\n|> CALL tvf_add_column(INPUT TABLE)",
			rows: [][]string{{"1", "1", "added"}, {"2", "1", "added"}, {"3", "2", "added"}, {"4", "2", "added"}},
		},
		{
			name: "tvf_call_two_tables_input_table_first",
			sql:  "FROM pipe_t1\n|> CALL tvf_union2( INPUT TABLE, TABLE pipe_t2 )\n|> ORDER BY 1,2",
			rows: unionRows, ordered: true,
		},
		{
			name: "tvf_call_two_tables_input_table_second",
			sql:  "FROM pipe_t2\n|> CALL tvf_union2( TABLE pipe_t1, INPUT TABLE )\n|> ORDER BY 1,2",
			rows: unionRows, ordered: true,
		},
		{
			name: "tvf_call_two_tables_input_table_named_args",
			sql:  "FROM pipe_t1\n|> CALL tvf_union2( arg2=>(SELECT * FROM pipe_t2), arg1=>INPUT TABLE )\n|> ORDER BY 1,2",
			rows: unionRows, ordered: true,
		},
		// call_sql_udf.test
		{name: "safe_call_sql_udf_division", sql: udfs + "SELECT SAFE.OneOverArg(0)", rows: [][]string{{"NULL"}}},
		{name: "safe_call_to_element", sql: udfs + "SELECT SAFE.Element(['a', 'b'])", rows: [][]string{{"NULL"}}},
		{name: "safe_error_subquery_function", sql: udfs + "SELECT SAFE.ErrorSubqueryFunction() IS NULL", rows: [][]string{{"true"}}},
		// invoke_view.test
		{name: "invoke_trival_view", sql: views + "SELECT * FROM SelectOne", rows: [][]string{{"1"}}},
		{name: "invoke_view_with_udf", sql: views + "SELECT * FROM ViewWithUdf", rows: [][]string{{"1"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Recursive queries can loop forever when mistranslated.
			qctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			_, got, err := queryStrings(qctx, db, tc.sql)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			want := tc.rows
			if !tc.ordered {
				sortRows(got)
				want = append([][]string(nil), want...)
				sortRows(want)
			}
			if len(got) == 0 && len(want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rows mismatch\n got: %v\nwant: %v", got, want)
			}
		})
	}
}

// unionRows is the expected result of the tvf_union2 cases in
// pipe_call.test.
var unionRows = [][]string{
	{"arg1", "1", "1"}, {"arg1", "2", "1"}, {"arg1", "3", "2"}, {"arg1", "4", "2"},
	{"arg2", "5", "3"}, {"arg2", "6", "3"}, {"arg2", "7", "4"}, {"arg2", "8", "5"},
}
