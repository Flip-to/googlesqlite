package googlesqlite_test

import (
	"database/sql"
	"reflect"
	"testing"
)

// TestSQLUserDefinedAggregates covers CREATE TEMP AGGREGATE FUNCTION,
// NOT AGGREGATE parameters and templated aggregates. Expected values
// come from the GoogleSQL compliance suite (file and case name cited
// per case) and docs/third_party/googlesql-docs/user-defined-aggregates.md.
func TestSQLUserDefinedAggregates(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  [][]any
	}{
		{
			// call_sql_uda.test: call_non_aggregating_udaf
			name: "body without aggregate call yields one row",
			query: `CREATE TEMP AGGREGATE FUNCTION NoAggregateAggregate() AS (
  (SELECT a + a FROM (SELECT 1 + 1 AS a))
);
SELECT NoAggregateAggregate() FROM UNNEST([1, 2, 3, 4, 5]) e`,
			want: [][]any{{int64(4)}},
		},
		{
			// call_sql_uda.test: call_non_aggregating_udaf_twice_group_all
			name: "body without aggregate call twice",
			query: `CREATE TEMP AGGREGATE FUNCTION NoAggregateAggregate() AS (
  (SELECT a + a FROM (SELECT 1 + 1 AS a))
);
SELECT COUNT(*), NoAggregateAggregate() + NoAggregateAggregate()
FROM UNNEST([1, 2, 3, 4, 5]) e`,
			want: [][]any{{int64(5), int64(8)}},
		},
		{
			// call_sql_uda.test: call_sum_squares_gbk
			name: "aggregate over groups",
			query: `CREATE TEMP AGGREGATE FUNCTION SumSqaures(a INT64) AS (SUM(a * a));
SELECT offset < 2, offset < 4, SumSqaures(a)
FROM UNNEST([1, 2, 3, 4, 5]) AS a WITH OFFSET
GROUP BY 1, 2 ORDER BY 1, 2`,
			want: [][]any{{false, false, int64(25)}, {false, true, int64(25)}, {true, true, int64(5)}},
		},
		{
			// call_sql_uda.test: call_count_plus_two_times_n_gba
			name: "NOT AGGREGATE parameter",
			query: `CREATE TEMP AGGREGATE FUNCTION CountTimesTwoN(n INT64 NOT AGGREGATE) AS (
  COUNT(*) * (n + n)
);
SELECT CountTimesTwoN(0), CountTimesTwoN(1), CountTimesTwoN(2), CountTimesTwoN(5)
FROM UNNEST([1, 2, 3, 4, 5])`,
			want: [][]any{{int64(0), int64(10), int64(20), int64(50)}},
		},
		{
			// call_sql_uda.test: call_bunch_of_aggs2_gba
			name: "NOT AGGREGATE parameter used in and out of aggregates",
			query: `CREATE TEMP AGGREGATE FUNCTION BunchOfAggs2(a INT64, b INT64 NOT AGGREGATE) AS (
  STRUCT(SUM(a) AS asum, SUM(b) AS bsum, b AS b, b + SUM(a) AS b_plus_asum)
);
SELECT s.asum, s.bsum, s.b, s.b_plus_asum
FROM (SELECT BunchOfAggs2(a, 12) AS s FROM UNNEST([1, 2, 3, 4, 5]) AS a)`,
			want: [][]any{{int64(15), int64(60), int64(12), int64(27)}},
		},
		{
			// call_sql_uda.test: call_template_int_gba
			name: "templated aggregate returning a struct",
			query: `CREATE TEMP AGGREGATE FUNCTION BunchOfArgsTemplate(a ANY TYPE) AS (
  STRUCT(ARRAY_AGG(a) AS aarr, COUNT(a) AS acount, MIN(a) AS amin, MAX(a) AS amax)
);
SELECT ARRAY_LENGTH(s.aarr), s.acount, s.amin, s.amax
FROM (SELECT BunchOfArgsTemplate(offset) AS s
      FROM UNNEST(['2020-01-01','2000-02-02','2022-03-03']) AS e WITH OFFSET)`,
			want: [][]any{{int64(3), int64(3), int64(0), int64(2)}},
		},
		{
			// user-defined-aggregates.md: ScaledSum example
			name: "documentation ScaledSum example",
			query: `CREATE TEMP AGGREGATE FUNCTION ScaledSum(
  dividend DOUBLE,
  divisor DOUBLE NOT AGGREGATE)
RETURNS DOUBLE
AS (
  SUM(dividend) / divisor
);
SELECT ScaledSum(col1, 2) AS scaled_sum
FROM (
  SELECT 1 AS col1 UNION ALL
  SELECT 3 AS col1 UNION ALL
  SELECT 5 AS col1
)`,
			want: [][]any{{4.5}},
		},
		{
			// call_sql_udf.test: sql_udf_arguments_as_if_once
			name: "volatile SQL UDF argument evaluated once",
			query: `CREATE TEMP FUNCTION FloatReflexiveEquals(a FLOAT64) AS ( a = a );
SELECT FloatReflexiveEquals(rand())`,
			want: [][]any{{true}},
		},
		{
			// with_recursive.test: by_name_union_distinct
			name: "recursive UNION DISTINCT BY NAME",
			query: `with recursive t as (
  select 1 as a, 2 as b
  union distinct by name
  select b, a from t
)
select * from t`,
			want: [][]any{{int64(1), int64(2)}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := sql.Open("googlesqlite", ":memory:?_test=sql_uda")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			rows, err := db.Query(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			var got [][]any
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					t.Fatal(err)
				}
				got = append(got, vals)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
