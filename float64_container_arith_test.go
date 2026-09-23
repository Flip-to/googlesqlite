package googlesqlite_test

import (
	"context"
	"database/sql"
	"math"
	"testing"

	_ "github.com/goccy/googlesqlite"
)

// TestFloat64InContainerKeepsFloatArithmetic pins the arithmetic symptom
// of an integral FLOAT64 nested in a STRUCT or ARRAY coming back as INT64:
// dividing it then truncates. SAFE_DIVIDE(s.r, 3) over STRUCT(1.0 AS r)
// returned 0 instead of 1/3, silently, with no type error.
func TestFloat64InContainerKeepsFloatArithmetic(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=float64_container_arith")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	for _, tc := range []struct {
		query string
		want  float64
	}{
		{"SELECT SAFE_DIVIDE(s.r, 3) FROM (SELECT STRUCT(1.0 AS r) AS s)", 1.0 / 3},
		{"SELECT a[OFFSET(0)] / 3 FROM (SELECT [1.0, 2.0] AS a)", 1.0 / 3},
		{"SELECT SAFE_DIVIDE(e.r, 3) FROM UNNEST([STRUCT(1.0 AS r)]) e", 1.0 / 3},
		{"SELECT SUM(e.r) / 2 FROM (SELECT [STRUCT(1.0 AS r)] AS a), UNNEST(a) e", 0.5},
		{"SELECT SAFE_DIVIDE(e, 4) FROM UNNEST([3.0, 2.5]) e ORDER BY e DESC LIMIT 1", 0.75},
	} {
		var got float64
		if err := db.QueryRowContext(ctx, tc.query).Scan(&got); err != nil {
			t.Errorf("%s: %v", tc.query, err)
			continue
		}
		if math.Abs(got-tc.want) > 1e-15 {
			t.Errorf("%s = %v; want %v", tc.query, got, tc.want)
		}
	}
}
