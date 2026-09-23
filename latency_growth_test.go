package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	_ "github.com/goccy/googlesqlite"
)

// latencyGrowthStatements returns the i-th statement of a bounded,
// deterministic mix of distinct GoogleSQL statements resembling a
// long-running probe workload: scalar probes with varying literals,
// SAFE casts, failing queries, UNNEST/STRUCT/JSON, temp functions and
// temp tables that are created and dropped again.
func latencyGrowthStatements(i int) []string {
	n := strconv.Itoa(i)
	switch i % 10 {
	case 0:
		return []string{fmt.Sprintf("SELECT %d + 1, CONCAT('a', '%d'), LENGTH('%d')", i, i, i)}
	case 1:
		return []string{fmt.Sprintf("SELECT SAFE_CAST('%dx' AS INT64), SAFE_CAST('%d' AS FLOAT64)", i, i)}
	case 2:
		return []string{fmt.Sprintf("SELECT no_such_function_%d(1)", i)} // fails
	case 3:
		return []string{fmt.Sprintf("SELECT x * %d FROM UNNEST([1, 2, 3]) AS x", i)}
	case 4:
		return []string{fmt.Sprintf("SELECT STRUCT(%d AS a, 'b%d' AS b).a", i, i)}
	case 5:
		return []string{fmt.Sprintf(`SELECT JSON_VALUE(JSON '{"k": %d}', '$.k'), TO_JSON_STRING(STRUCT(%d AS v))`, i, i)}
	case 6:
		return []string{
			"CREATE TEMP FUNCTION f_" + n + "(x INT64) AS (x + " + n + ")",
			"SELECT f_" + n + "(1)",
		}
	case 7:
		return []string{
			"CREATE TEMP TABLE t_" + n + " AS SELECT " + n + " AS v",
			"SELECT v FROM t_" + n,
			"DROP TABLE t_" + n,
		}
	case 8:
		return []string{fmt.Sprintf("SELECT CAST('bad%d' AS INT64)", i)} // runtime error
	default:
		return []string{fmt.Sprintf("SELECT FORMAT_DATE('%%Y', DATE '2020-01-%02d'), REGEXP_CONTAINS('a%d', r'a\\d+')", i%28+1, i)}
	}
}

func runStmt(ctx context.Context, conn *sql.Conn, q string) {
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return
	}
	for rows.Next() {
	}
	_ = rows.Close()
}

func timeSelect1(ctx context.Context, t *testing.T, conn *sql.Conn) time.Duration {
	start := time.Now()
	var v int64
	if err := conn.QueryRowContext(ctx, "SELECT 1").Scan(&v); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}
	return time.Since(start)
}

func heapNoGC() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

func median(ds []time.Duration) time.Duration {
	c := append([]time.Duration(nil), ds...)
	sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
	return c[len(c)/2]
}

// TestLatencyStaysFlatAcrossManyStatements guards against per-statement
// state accumulating on a single connection (catalog entries, analyzer
// state, caches keyed by query text, leaked rows on error paths): after
// thousands of distinct statements, `SELECT 1` must not be much slower
// and the heap must not have grown without bound. Temp-object cleanup
// and DROP rebuild the wasm SimpleCatalog; before retired catalogs were
// reclaimed promptly this workload grew the heap from ~100 MB to over
// 1 GB (see Catalog.releaseRetiredCatalogs).
func TestLatencyStaysFlatAcrossManyStatements(t *testing.T) {
	if testing.Short() {
		t.Skip("long-running latency probe")
	}
	total := 3000
	if v, err := strconv.Atoi(os.Getenv("GOOGLESQLITE_LATENCY_STMTS")); err == nil && v > 0 {
		total = v
	}
	const every = 10 // one timed SELECT 1 per `every` workload statements

	db, err := sql.Open("googlesqlite", ":memory:")
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

	// heap forces a GC only at the two checkpoints: a long-running
	// server never calls runtime.GC itself, so the workload must not
	// get periodic collections the real deployment would not.
	heap := func() uint64 {
		runtime.GC()
		return heapNoGC()
	}
	var lat []time.Duration
	deadline := time.Now().Add(45 * time.Second)
	heapStart, gorStart := uint64(0), 0
	for i := 0; i < total && time.Now().Before(deadline); i++ {
		for _, q := range latencyGrowthStatements(i) {
			runStmt(ctx, conn, q)
		}
		if i%every == 0 {
			lat = append(lat, timeSelect1(ctx, t, conn))
		}
		if i == 200 {
			heapStart, gorStart = heap(), runtime.NumGoroutine()
		}
		if i%500 == 0 {
			t.Logf("stmt %5d: select1=%v heap=%dKiB goroutines=%d", i, lat[len(lat)-1], heapNoGC()/1024, runtime.NumGoroutine())
		}
	}
	const window = 50
	if len(lat) < 2*window+window {
		t.Skipf("only %d samples collected before deadline", len(lat))
	}
	first, last := median(lat[:window]), median(lat[len(lat)-window:])
	heapEnd, gorEnd := heap(), runtime.NumGoroutine()
	t.Logf("samples=%d select1 median first=%v last=%v heap start=%dKiB end=%dKiB goroutines %d->%d",
		len(lat), first, last, heapStart/1024, heapEnd/1024, gorStart, gorEnd)
	if last > 3*first+2*time.Millisecond {
		t.Errorf("SELECT 1 latency grew: first median %v, last median %v", first, last)
	}
	if heapStart > 0 && heapEnd > 2*heapStart+64<<20 {
		t.Errorf("heap grew: %d KiB -> %d KiB", heapStart/1024, heapEnd/1024)
	}
	if gorEnd > gorStart+20 {
		t.Errorf("goroutines grew: %d -> %d", gorStart, gorEnd)
	}
}
