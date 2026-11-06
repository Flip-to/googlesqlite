package googlesqlite_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/goccy/googlesqlite"
)

// TestCatalogSyncCostDoesNotGrowWithSpecs covers the per-statement catalog
// sync. Every analyzed statement re-reads googlesqlite_catalog rows changed
// since the last sync; with no index on updatedAt that was a full scan that
// paged in every stored spec, so a trivial SELECT got slower the more (and
// wider) tables the database held. The BigQuery emulator, loaded with real
// production schemas, spent about half its CPU there (about 250 ms per
// request). With an index the unchanged-catalog case reads no spec at all.
func TestCatalogSyncCostDoesNotGrowWithSpecs(t *testing.T) {
	dir, err := os.MkdirTemp("", "catalog_sync")
	if err != nil {
		t.Fatal(err)
	}
	// Best effort: on Windows the driver may still hold the file at cleanup.
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	db, err := sql.Open("googlesqlite", "file:"+filepath.Join(dir, "c.db")+"?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	selectOne := func() time.Duration {
		const n = 20
		start := time.Now()
		for i := 0; i < n; i++ {
			var v int64
			if err := db.QueryRow("SELECT 1").Scan(&v); err != nil {
				t.Fatal(err)
			}
		}
		return time.Since(start) / n
	}
	selectOne() // warm up the analyzer
	empty := selectOne()

	// 60 tables of 250 columns: the size of the emulator's production schemas.
	for ti := 0; ti < 60; ti++ {
		cols := make([]string, 250)
		for ci := range cols {
			cols[ci] = fmt.Sprintf("column_with_a_long_descriptive_name_%03d STRING", ci)
		}
		if _, err := db.Exec(fmt.Sprintf("CREATE TABLE wide_%02d (%s)", ti, strings.Join(cols, ", "))); err != nil {
			t.Fatal(err)
		}
	}
	selectOne()
	loaded := selectOne()
	t.Logf("SELECT 1: %v with an empty catalog, %v with 60 wide tables", empty, loaded)
	if loaded > 4*empty+2*time.Millisecond {
		t.Fatalf("SELECT 1 took %v with 60 wide tables in the catalog vs %v empty; the per-statement catalog sync should not read unchanged specs", loaded, empty)
	}
}
