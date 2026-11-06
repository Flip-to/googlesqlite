package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "github.com/goccy/googlesqlite"
)

func openDropCostDB(t *testing.T) *sql.DB {
	t.Helper()
	dir, err := os.MkdirTemp("", "drop_cost")
	if err != nil {
		t.Fatal(err)
	}
	// Best effort: on Windows the driver may still hold the file at cleanup.
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	db, err := sql.Open("googlesqlite", "file:"+filepath.Join(dir, "d.db")+"?cache=shared&_pragma=synchronous(off)&_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

func createWideTables(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	for ti := 0; ti < n; ti++ {
		cols := make([]string, 250)
		for ci := range cols {
			cols[ci] = fmt.Sprintf("column_with_a_long_descriptive_name_%03d STRING", ci)
		}
		if _, err := db.Exec(fmt.Sprintf("CREATE TABLE wide_%02d (%s)", ti, strings.Join(cols, ", "))); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDropTableCostWithWideCatalog covers DROP TABLE with many wide
// tables in the catalog. go-googlesql cannot remove a table from a
// SimpleCatalog, so a DROP rebuilds the catalog and re-adds every
// remaining table. Rebuilding each remaining SimpleTable column by
// column through wasm made a DROP of a 2-column table cost ~100 ms with
// 60 tables of 250 columns (the BigQuery emulator's production schemas),
// against ~16 ms with an empty catalog.
func TestDropTableCostWithWideCatalog(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: builds 60 wide tables")
	}
	dropAvg := func(db *sql.DB, prefix string) time.Duration {
		const n = 10
		var total time.Duration
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("%s_%d", prefix, i)
			if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %s (a INT64, b STRING)", name)); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if _, err := db.Exec("DROP TABLE " + name); err != nil {
				t.Fatal(err)
			}
			total += time.Since(start)
		}
		return total / n
	}
	db := openDropCostDB(t)
	dropAvg(db, "warm")
	empty := dropAvg(db, "empty")
	createWideTables(t, db, 60)
	dropAvg(db, "warm2")
	loaded := dropAvg(db, "loaded")
	t.Logf("DROP TABLE: %v with an empty catalog, %v with 60 wide tables", empty, loaded)
	if loaded > 3*empty+10*time.Millisecond {
		t.Fatalf("DROP TABLE took %v with 60 wide tables vs %v empty; a DROP should not rebuild every remaining table", loaded, empty)
	}
}

// TestDropTableCatalogStaysCorrectAcrossCycles checks that reusing table
// handles across catalog rebuilds never exposes a dropped table or a
// stale column set, and that types, functions and TVFs keep resolving.
func TestDropTableCatalogStaysCorrectAcrossCycles(t *testing.T) {
	db := openDropCostDB(t)
	ctx := context.Background()
	exec := func(q string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec("CREATE TABLE ds.keep (id INT64, name STRING)")
	exec("INSERT INTO ds.keep VALUES (1, 'a')")
	exec("CREATE FUNCTION ds.plus1(x INT64) AS (x + 1)")
	exec("CREATE TABLE FUNCTION ds.keep_tvf(n INT64) AS SELECT id, name FROM ds.keep WHERE id >= n")
	for i := 0; i < 30; i++ {
		// Same name every cycle, alternating column sets.
		if i%2 == 0 {
			exec("CREATE TABLE ds.tmp (a INT64, b STRING)")
			exec("INSERT INTO ds.tmp VALUES (1, 'x')")
			var a int64
			var b string
			if err := db.QueryRowContext(ctx, "SELECT a, b FROM ds.tmp").Scan(&a, &b); err != nil {
				t.Fatalf("cycle %d: %v", i, err)
			}
		} else {
			exec("CREATE TABLE ds.tmp (c FLOAT64)")
			if _, err := db.QueryContext(ctx, "SELECT a FROM ds.tmp"); err == nil {
				t.Fatalf("cycle %d: recreated table still exposes the dropped column a", i)
			}
			var n int64
			if err := db.QueryRowContext(ctx, "SELECT COUNT(c) FROM ds.tmp").Scan(&n); err != nil {
				t.Fatalf("cycle %d: %v", i, err)
			}
		}
		exec("DROP TABLE ds.tmp")
		if _, err := db.QueryContext(ctx, "SELECT * FROM ds.tmp"); err == nil {
			t.Fatalf("cycle %d: dropped table still resolves", i)
		}
		var got int64
		if err := db.QueryRowContext(ctx, "SELECT ds.plus1(id) FROM ds.keep").Scan(&got); err != nil || got != 2 {
			t.Fatalf("cycle %d: function after drop: %v %d", i, err, got)
		}
		var name string
		if err := db.QueryRowContext(ctx, "SELECT name FROM ds.keep_tvf(1)").Scan(&name); err != nil || name != "a" {
			t.Fatalf("cycle %d: TVF after drop: %v %q", i, err, name)
		}
		var d string
		if err := db.QueryRowContext(ctx, "SELECT CAST(EXTRACT(YEAR FROM DATE '2024-01-02') AS STRING) FROM ds.keep").Scan(&d); err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		var ok bool
		if err := db.QueryRowContext(ctx, "SELECT CAST(NULL AS google.type.Date) IS NULL").Scan(&ok); err != nil || !ok {
			t.Fatalf("cycle %d: google.type.Date after drop: %v", i, err)
		}
	}
}

// TestDropTableWithWideCatalogHeapBounded runs many CREATE/DROP cycles
// with wide tables in the catalog: cached table handles must be dropped
// with their tables, not accumulate.
func TestDropTableWithWideCatalogHeapBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	db := openDropCostDB(t)
	createWideTables(t, db, 10)
	cycle := func(i int) {
		t.Helper()
		cols := make([]string, 250)
		for ci := range cols {
			cols[ci] = fmt.Sprintf("c%03d STRING", ci)
		}
		if _, err := db.Exec(fmt.Sprintf("CREATE TABLE ds.cyc_%d (%s)", i, strings.Join(cols, ", "))); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(fmt.Sprintf("DROP TABLE ds.cyc_%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	heapInuse := func() uint64 {
		var ms runtime.MemStats
		runtime.GC()
		runtime.GC()
		runtime.ReadMemStats(&ms)
		return ms.HeapInuse
	}
	const warmup, measured = 20, 100
	for i := 0; i < warmup; i++ {
		cycle(i)
	}
	before := heapInuse()
	for i := warmup; i < warmup+measured; i++ {
		cycle(i)
	}
	grown := int64(heapInuse()) - int64(before)
	t.Logf("heap grew %d MiB over %d cycles", grown>>20, measured)
	if limit := int64(256 << 20); grown > limit {
		t.Fatalf("heap grew %d MiB over %d CREATE/DROP cycles, want < %d MiB", grown>>20, measured, limit>>20)
	}
}
