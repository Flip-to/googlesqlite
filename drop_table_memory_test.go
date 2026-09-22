package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"runtime"
	"testing"

	_ "github.com/goccy/googlesqlite"
)

// TestRepeatedDropOfQualifiedTableHeapGrowth runs CREATE/DROP cycles on
// a dataset-qualified table. Every DROP rebuilds the catalog. If the
// rebuild re-creates the dataset's INFORMATION_SCHEMA tables each
// time, the wasm heap grows by tens of MB per cycle and never gives
// the memory back; a long enough run passes 2 GiB and allocation fails
// ("slice bounds out of range" from the wasm bridge).
func TestRepeatedDropOfQualifiedTableHeapGrowth(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: exercises many catalog rebuilds")
	}
	db, err := sql.Open("googlesqlite", ":memory:?_test=repeated_drop_qualified")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	cycle := func(i int) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("CREATE TABLE my_ds.t%d AS SELECT 1 AS k", i)); err != nil {
			t.Fatalf("cycle %d: CREATE: %v", i, err)
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("DROP TABLE my_ds.t%d", i)); err != nil {
			t.Fatalf("cycle %d: DROP: %v", i, err)
		}
	}
	heapInuse := func() uint64 {
		var ms runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&ms)
		return ms.HeapInuse
	}
	const warmup, measured = 10, 40
	for i := 0; i < warmup; i++ {
		cycle(i)
	}
	before := heapInuse()
	for i := warmup; i < warmup+measured; i++ {
		cycle(i)
	}
	grown := int64(heapInuse()) - int64(before)
	// A rebuild that re-creates INFORMATION_SCHEMA costs ~25 MB per
	// cycle (~1 GB over 40). Allow a generous margin for allocator
	// noise; anything near linear growth fails.
	if limit := int64(256 << 20); grown > limit {
		t.Fatalf("heap grew %d MiB over %d CREATE/DROP cycles, want < %d MiB", grown>>20, measured, limit>>20)
	}
}
