package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// matchRecognizeT1 is the [prepare_database] table of the GoogleSQL
// compliance files match_recognize.test / pipe_match_recognize.test.
const matchRecognizeT1 = `CREATE TABLE t1 AS
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
    ])`

const mrStartEnd = `MEASURES min(rowid) AS start, max(rowid) AS ` + "`end`" + `, count(*) AS cnt`

func mrRepeat(row string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = row
	}
	return out
}

// TestMatchRecognize runs MATCH_RECOGNIZE cases taken from the GoogleSQL
// compliance fixtures (case names cited) and the pipe-syntax.md
// MATCH_RECOGNIZE Example. Rows are rendered as comma-joined values.
func TestMatchRecognize(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=matchrecognize")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, matchRecognizeT1); err != nil {
		t.Fatalf("setup: %v", err)
	}

	for _, tc := range []struct {
		name    string
		sql     string
		args    []any
		ordered bool
		want    []string
	}{
		{
			name: "pattern_always_false_on_nonempty_input",
			sql:  `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid + rowid MEASURES max(z+z) AS m PATTERN ( a ) DEFINE a AS false)`,
			want: nil,
		},
		{
			name: "empty_pattern_on_nonempty_input",
			sql:  `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid + rowid MEASURES max(z+z) AS m PATTERN ( a| ) DEFINE a AS false)`,
			want: mrRepeat("NULL", 16),
		},
		{
			name: "basic_with_no_quantifiers_nor_subqueries",
			sql:  `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid + rowid MEASURES max(z+z) AS m PATTERN ( a b ) DEFINE a AS y = 100, b AS y = 101)`,
			want: []string{"2002", "2008"},
		},
		{
			name:    "trivial_measures_clause_has_no_aggregations",
			sql:     `SELECT * FROM t1 MATCH_RECOGNIZE(PARTITION BY x ORDER BY rowid + rowid MEASURES 1 AS one PATTERN ( a b ) DEFINE a AS y = 100, b AS y = 101) ORDER BY 1`,
			ordered: true,
			want:    []string{"1,1", "2,1"},
		},
		{
			name: "correlated_columns_are_grouping_constants",
			sql: `WITH outer_tbl AS (SELECT 10000 AS outer_col UNION ALL SELECT 20000 AS outer_col)
SELECT (SELECT m FROM (SELECT * FROM t1 MATCH_RECOGNIZE(PARTITION BY x ORDER BY rowid + rowid
  MEASURES max(z+z) + x + outer_col AS m PATTERN ( a b ) DEFINE a AS y = 100, b AS y = 101) WHERE x = 1) AS tmp) AS c
FROM outer_tbl ORDER BY outer_col`,
			ordered: true,
			want:    []string{"12009", "22009"},
		},
		{
			name:    "non_overlapping_reluctant_plus",
			sql:     `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid ` + mrStartEnd + ` AFTER MATCH SKIP PAST LAST ROW PATTERN ( a+? b+? ) DEFINE a AS x = 1, b AS x = 2) ORDER BY start, ` + "`end`" + `, cnt`,
			ordered: true,
			want:    []string{"1,10,10"},
		},
		{
			name:    "overlapping_reluctant_star",
			sql:     `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid ` + mrStartEnd + ` AFTER MATCH SKIP TO NEXT ROW PATTERN ( a*? b*? ) DEFINE a AS x = 1, b AS x = 2) ORDER BY start, ` + "`end`" + `, cnt`,
			ordered: true,
			want:    mrRepeat("NULL,NULL,0", 16),
		},
		{
			name:    "longest_match_overlapping",
			sql:     `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid ` + mrStartEnd + ` AFTER MATCH SKIP TO NEXT ROW PATTERN ( x_eq_1 + | y_eq_100+ y_not_null+) DEFINE x_eq_1 AS x = 1, y_eq_100 AS y = 100, y_not_null AS y IS NOT NULL OPTIONS (use_longest_match = true)) ORDER BY start, ` + "`end`" + `, cnt`,
			ordered: true,
			want:    []string{"1,14,14", "2,14,13", "3,14,12", "4,14,11", "5,14,10", "6,14,9", "7,9,3", "8,9,2", "9,9,1", "10,14,5"},
		},
		{
			name:    "normal_match_non_overlapping",
			sql:     `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid ` + mrStartEnd + ` PATTERN ( x_eq_1 + | y_eq_100+ y_not_null+) DEFINE x_eq_1 AS x = 1, y_eq_100 AS y = 100, y_not_null AS y IS NOT NULL OPTIONS (use_longest_match = false)) ORDER BY start, ` + "`end`" + `, cnt`,
			ordered: true,
			want:    []string{"1,9,9", "10,14,5"},
		},
		{
			name:    "longest_match_option_as_a_param_false",
			sql:     `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid ` + mrStartEnd + ` PATTERN ( x_eq_1 +? | y_eq_100+? y_not_null+?) DEFINE x_eq_1 AS x = 1, y_eq_100 AS y = 100, y_not_null AS y IS NOT NULL OPTIONS (use_longest_match = @p_bool)) ORDER BY start, ` + "`end`" + `, cnt`,
			args:    []any{sql.Named("p_bool", false)},
			ordered: true,
			want:    []string{"1,1,1", "2,2,1", "3,3,1", "4,4,1", "5,5,1", "6,6,1", "7,7,1", "8,8,1", "9,9,1", "10,11,2"},
		},
		{
			name:    "longest_match_option_as_a_param_true",
			sql:     `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid ` + mrStartEnd + ` PATTERN ( x_eq_1 +? | y_eq_100+? y_not_null+?) DEFINE x_eq_1 AS x = 1, y_eq_100 AS y = 100, y_not_null AS y IS NOT NULL OPTIONS (use_longest_match = @p_bool)) ORDER BY start, ` + "`end`" + `, cnt`,
			args:    []any{sql.Named("p_bool", true)},
			ordered: true,
			want:    []string{"1,14,14"},
		},
		{
			name:    "params_in_quantifiers",
			sql:     `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid ` + mrStartEnd + ` PATTERN ( a {@p1, @p2} ) DEFINE a AS x = 1) ORDER BY start, ` + "`end`" + `, cnt`,
			args:    []any{sql.Named("p1", int64(1)), sql.Named("p2", int64(2))},
			ordered: true,
			want:    []string{"1,2,2", "3,4,2", "5,6,2", "7,8,2", "9,9,1"},
		},
		{
			name:    "start_and_end_anchors",
			sql:     `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid ` + mrStartEnd + ` PATTERN ( ^b*(^)+a+|b+$ ) DEFINE a AS x = 1, b AS x = 2) ORDER BY start, ` + "`end`" + `, cnt`,
			ordered: true,
			want:    []string{"1,9,9", "10,16,7"},
		},
		{
			name: "start_anchors_empty_match",
			sql:  `SELECT * FROM t1 MATCH_RECOGNIZE(PARTITION BY x ORDER BY rowid ` + mrStartEnd + ` PATTERN ( ^^a*(^^){100}a*^^(^)? ) DEFINE a AS false)`,
			want: []string{"1,NULL,NULL,0", "2,NULL,NULL,0"},
		},
		{
			name: "end_anchors_no_empty_match",
			sql:  `SELECT * FROM t1 MATCH_RECOGNIZE(PARTITION BY x ORDER BY rowid ` + mrStartEnd + ` PATTERN ( a? $$$$ ) DEFINE a AS false)`,
			want: nil,
		},
		{
			name: "null_define_exprs",
			sql:  `SELECT * FROM t1 MATCH_RECOGNIZE(PARTITION BY x ORDER BY rowid ` + mrStartEnd + ` PATTERN (a) DEFINE a AS CAST(NULL AS BOOL))`,
			want: nil,
		},
		{
			name: "pattern_variables_in_measures",
			sql: `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid + rowid
  MEASURES min(a.z) AS min_az, max(a.z) AS max_az, min(b.z) AS min_bz, max(b.z) AS max_bz, max(a.z) - max(b.z) AS m
  PATTERN ( a* b* ) DEFINE a AS x = 1, b AS x = 2)`,
			want: []string{"1000,1004,1000,1002,2"},
		},
		{
			name: "count_star_with_qualified_symbol_in_the_same_measure",
			sql: `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid
  MEASURES COUNT(*) + MAX(a.x) AS m1, MAX(a.x) + COUNT(*) AS m2, COUNT(0) + MAX(a.x) AS m3, MAX(a.x) + COUNT(0) AS m4,
    MAX(a.x) AS max_a_x, COUNT(0) AS cnt0, COUNT(*) AS cnt_star
  PATTERN ( a* b* ) DEFINE a AS x = 1, b AS x = 2)`,
			want: []string{"17,17,17,17,1,16,16"},
		},
		{
			name: "match_recognize_special_functions_first_and_last",
			sql: `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid
  MEASURES FIRST(z) AS first_z, LAST(z) AS last_z, FIRST(a.z) AS first_az, LAST(a.z) AS last_az,
    FIRST(no_match.z) AS first_no_match_z, LAST(no_match.z) AS last_no_match_z
  PATTERN ( a* no_match* b* ) DEFINE a AS x = 1, no_match AS false, b AS x = 2)`,
			want: []string{"1000,1002,1000,1002,NULL,NULL"},
		},
		{
			name: "match_recognize_special_functions_match_result",
			sql: `SELECT m_no, TO_JSON_STRING(r), s, TO_JSON_STRING(bz) FROM t1 MATCH_RECOGNIZE(ORDER BY rowid
  MEASURES match_number() AS m_no, ARRAY_AGG(match_row_number()) AS r, STRING_AGG(CLASSIFIER()) AS s, ARRAY_AGG(b.z) AS bz
  PATTERN ( a* no_match* b* ) DEFINE a AS x = 1, no_match AS false, b AS x = 2)`,
			want: []string{`1,[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16],a,a,a,a,a,a,a,a,a,b,b,b,b,b,b,b,[1000,1001,1002,null,null,1002,1002]`},
		},
		{
			name: "match_recognize_special_functions_composing",
			sql: `SELECT * FROM t1 MATCH_RECOGNIZE(ORDER BY rowid
  MEASURES FIRST(1000 * a.z + 100 * match_number() + match_row_number()) AS m1,
    LAST(1000 * b.z + 100 * match_number() + match_row_number()) AS m2
  PATTERN ( a* no_match* b* ) DEFINE a AS x = 1, no_match AS false, b AS x = 2)`,
			want: []string{"1000101,1002116"},
		},
		{
			// pipe_match_recognize.test: overlapping_greedy.
			name:    "pipe_overlapping_greedy",
			sql:     `FROM t1 |> MATCH_RECOGNIZE(ORDER BY rowid ` + mrStartEnd + ` AFTER MATCH SKIP TO NEXT ROW PATTERN ( a* b* ) DEFINE a AS x = 1, b AS x = 2) |> ORDER BY start, ` + "`end`" + `, cnt`,
			ordered: true,
			want: []string{"1,16,16", "2,16,15", "3,16,14", "4,16,13", "5,16,12", "6,16,11", "7,16,10", "8,16,9",
				"9,16,8", "10,16,7", "11,16,6", "12,16,5", "13,16,4", "14,16,3", "15,16,2", "16,16,1"},
		},
		{
			// docs/third_party/googlesql-docs/pipe-syntax.md, MATCH_RECOGNIZE
			// pipe operator Example (arrays rendered via TO_JSON_STRING).
			name: "docs_pipe_example",
			sql: `(SELECT 1 as x UNION ALL SELECT 2 UNION ALL SELECT 3)
|> MATCH_RECOGNIZE(ORDER BY x MEASURES ARRAY_AGG(high.x) AS high_agg, ARRAY_AGG(low.x) AS low_agg
   AFTER MATCH SKIP TO NEXT ROW PATTERN (low | high) DEFINE low AS x <= 2, high AS x >= 2)
|> SELECT TO_JSON_STRING(high_agg), TO_JSON_STRING(low_agg)`,
			want: []string{"null,[1]", "null,[2]", "[3],null"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := conn.QueryContext(ctx, tc.sql, tc.args...)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			defer rows.Close()
			cols, _ := rows.Columns()
			var got []string
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					t.Fatal(err)
				}
				parts := make([]string, len(vals))
				for i, v := range vals {
					if v == nil {
						parts[i] = "NULL"
					} else {
						parts[i] = fmt.Sprint(v)
					}
				}
				got = append(got, strings.Join(parts, ","))
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			want := append([]string(nil), tc.want...)
			if !tc.ordered {
				sort.Strings(got)
				sort.Strings(want)
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// TestMatchRecognizePrevNext covers PREV / NEXT inside DEFINE (lowered to
// LAG / LEAD analytic columns). Expected rows verified against BigQuery.
func TestMatchRecognizePrevNext(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=matchrecognizeprev")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT * FROM (SELECT * FROM UNNEST([STRUCT(1 AS id, 10 AS v), (2, 12), (3, 11), (4, 13), (5, 14), (6, 9)]))
MATCH_RECOGNIZE(ORDER BY id MEASURES MIN(id) AS s, MAX(id) AS e, COUNT(*) AS c
  PATTERN (up+) DEFINE up AS v > PREV(v) OR NEXT(v) IS NULL) ORDER BY s`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s, e, c int64
		if err := rows.Scan(&s, &e, &c); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%d,%d,%d", s, e, c))
	}
	if want := "2,2,1;4,6,3"; strings.Join(got, ";") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}
