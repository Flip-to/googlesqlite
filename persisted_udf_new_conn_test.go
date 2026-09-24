package googlesqlite_test

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"testing"

	"github.com/goccy/googlesqlite"
)

// TestPersistedSQLUDFCalledFromNewConnection creates a dataset-qualified
// SQL UDF in one request and calls it in later ones, the way the
// bigquery-emulator serves each request: a dedicated db.Conn with the
// project name path set, one transaction per request. The later calls run
// on a fresh connection of the same database and on a database reopened
// from the file, so the UDF comes from the persisted catalog rather than
// from the creating connection. The expected rows are the AddFourAndDivide
// example of the upstream user-defined functions documentation.
func TestPersistedSQLUDFCalledFromNewConnection(t *testing.T) {
	f, err := os.CreateTemp("", "persisted-udf-*.db")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	defer os.Remove(f.Name())
	dsn := "file:" + f.Name() + "?cache=shared"
	ctx := context.Background()

	request := func(t *testing.T, db *sql.DB, fn func(tx *sql.Tx) error) {
		t.Helper()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if err := conn.Raw(func(c any) error {
			return c.(*googlesqlite.Conn).SetNamePath([]string{"proj"})
		}); err != nil {
			t.Fatal(err)
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := fn(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	db, err := sql.Open("googlesqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	request(t, db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "CREATE FUNCTION dataset1.AddFourAndDivide(x INT64, y INT64) RETURNS FLOAT64 AS ((x + 4) / y)")
		return err
	})

	type row struct {
		val int64
		out float64
	}
	want := []row{{2, 3.0}, {3, 3.5}, {5, 4.5}, {8, 6.0}}
	queries := []string{
		"SELECT val, dataset1.AddFourAndDivide(val, 2) FROM UNNEST([2, 3, 5, 8]) AS val ORDER BY val",
		"SELECT val, proj.dataset1.AddFourAndDivide(val, 2) FROM UNNEST([2, 3, 5, 8]) AS val ORDER BY val",
		"SELECT val, `proj.dataset1.AddFourAndDivide`(val, 2) FROM UNNEST([2, 3, 5, 8]) AS val ORDER BY val",
		// Arithmetic around the call and a FLOAT64 result compared exactly.
		"SELECT val, dataset1.AddFourAndDivide(val, 1) / 2 FROM UNNEST([2, 3, 5, 8]) AS val ORDER BY val",
	}
	check := func(t *testing.T, db *sql.DB) {
		for _, q := range queries {
			request(t, db, func(tx *sql.Tx) error {
				rows, err := tx.QueryContext(ctx, q)
				if err != nil {
					t.Errorf("%s: %v", q, err)
					return nil
				}
				defer rows.Close()
				var got []row
				for rows.Next() {
					var r row
					if err := rows.Scan(&r.val, &r.out); err != nil {
						return err
					}
					got = append(got, r)
				}
				if err := rows.Err(); err != nil {
					t.Errorf("%s: %v", q, err)
					return nil
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %v, want %v", q, got, want)
				}
				return nil
			})
		}
	}
	t.Run("new connection", func(t *testing.T) { check(t, db) })
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := sql.Open("googlesqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	t.Run("reopened database", func(t *testing.T) { check(t, reopened) })
}
