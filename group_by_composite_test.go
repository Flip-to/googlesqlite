package googlesqlite_test

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/goccy/googlesqlite"
)

// TestGroupByCompositeKey covers STRUCT and ARRAY grouping keys. Per the
// data types reference only PROTO, GEOGRAPHY and JSON are not groupable,
// so an ARRAY, and a STRUCT containing one, is a valid key whether it is
// written explicitly or inferred by GROUP BY ALL. BigQuery returns two
// rows for the GROUP BY ALL case.
func TestGroupByCompositeKey(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=group_by_composite")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		sql  string
		want []string // TO_JSON_STRING(s) and n, largest group first
	}{
		{
			"group by all, struct with array",
			"SELECT STRUCT(x AS id, [x] AS arr) AS s, COUNT(*) AS n FROM UNNEST([1, 1, 2]) AS x GROUP BY ALL",
			[]string{`{"id":1,"arr":[1]} 2`, `{"id":2,"arr":[2]} 1`},
		},
		{
			"explicit struct with array",
			"SELECT STRUCT(x AS id, [x] AS arr) AS s, COUNT(*) AS n FROM UNNEST([1, 1, 2]) AS x GROUP BY s",
			[]string{`{"id":1,"arr":[1]} 2`, `{"id":2,"arr":[2]} 1`},
		},
		{
			"plain struct",
			"SELECT STRUCT(x AS id) AS s, COUNT(*) AS n FROM UNNEST([1, 1, 2]) AS x GROUP BY s",
			[]string{`{"id":1} 2`, `{"id":2} 1`},
		},
		{
			"array",
			"SELECT [x] AS s, COUNT(*) AS n FROM UNNEST([1, 1, 2]) AS x GROUP BY s",
			[]string{`[1] 2`, `[2] 1`},
		},
	} {
		rows, err := db.QueryContext(ctx, "SELECT FORMAT('%s %d', TO_JSON_STRING(s), n) FROM ("+tc.sql+") ORDER BY n DESC")
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		var got []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			got = append(got, s)
		}
		if err := rows.Err(); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		rows.Close()
		if len(got) != len(tc.want) || (len(got) == 2 && (got[0] != tc.want[0] || got[1] != tc.want[1])) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
