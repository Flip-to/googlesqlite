package googlesqlite_test

import (
	"database/sql"
	"strings"
	"testing"
)

const cloneChainLimitMessage = "Cannot have more than 3 chained clones or snapshots. Please use a deep copy to reset the chain."

type tableTypeRow struct {
	typ                                    string
	baseCatalog, baseSchema, baseTableName sql.NullString
}

func tableTypeOf(t *testing.T, db *sql.DB, dataset, table string) tableTypeRow {
	t.Helper()
	var r tableTypeRow
	err := db.QueryRow("SELECT table_type, base_table_catalog, base_table_schema, base_table_name FROM `p`.`"+
		dataset+"`.INFORMATION_SCHEMA.TABLES WHERE table_name = '"+table+"'").
		Scan(&r.typ, &r.baseCatalog, &r.baseSchema, &r.baseTableName)
	if err != nil {
		t.Fatalf("INFORMATION_SCHEMA.TABLES row of %s.%s: %v", dataset, table, err)
	}
	return r
}

// TestInformationSchemaTablesCloneColumns: a clone is table_type CLONE and names its source in
// base_table_catalog / base_table_schema / base_table_name; a base table has NULL there (as in BigQuery).
func TestInformationSchemaTablesCloneColumns(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db,
		"CREATE TABLE `p.src.t0` (id INT64)",
		"CREATE TABLE `p.dst.t1` CLONE `p.src.t0`",
	)
	base := tableTypeOf(t, db, "src", "t0")
	if base.typ != "BASE TABLE" || base.baseCatalog.Valid || base.baseSchema.Valid || base.baseTableName.Valid {
		t.Fatalf("base table: got %+v, want BASE TABLE with NULL base_table_*", base)
	}
	clone := tableTypeOf(t, db, "dst", "t1")
	if clone.typ != "CLONE" || clone.baseCatalog.String != "p" || clone.baseSchema.String != "src" || clone.baseTableName.String != "t0" {
		t.Fatalf("clone: got %+v, want CLONE of p.src.t0", clone)
	}
}

// TestCreateTableCloneChainLimit: a 4th chained clone fails with BigQuery's message, a deep copy
// (COPY) resets the chain, and IF NOT EXISTS on an existing clone is still a no-op at the limit.
func TestCreateTableCloneChainLimit(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db,
		"CREATE TABLE `p.d.t0` (id INT64)",
		"INSERT INTO `p.d.t0` (id) VALUES (1)",
		"CREATE TABLE `p.d.t1` CLONE `p.d.t0`",
		"CREATE TABLE `p.d.t2` CLONE `p.d.t1`",
		"CREATE TABLE `p.d.t3` CLONE `p.d.t2`",
	)
	_, err := db.ExecContext(ctx, "CREATE TABLE `p.d.t4` CLONE `p.d.t3`")
	if err == nil || !strings.Contains(err.Error(), cloneChainLimitMessage) {
		t.Fatalf("4th chained clone: err = %v, want %q", err, cloneChainLimitMessage)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `p`.`d`.INFORMATION_SCHEMA.TABLES WHERE table_name = 't4'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("the refused clone must not exist: n=%d err=%v", n, err)
	}
	// CREATE OR REPLACE is refused the same way.
	if _, err := db.ExecContext(ctx, "CREATE OR REPLACE TABLE `p.d.t4` CLONE `p.d.t3`"); err == nil || !strings.Contains(err.Error(), cloneChainLimitMessage) {
		t.Fatalf("CREATE OR REPLACE at the limit: err = %v", err)
	}
	// IF NOT EXISTS on an existing clone is a no-op even when a new clone would exceed the limit.
	mustExecGaps(t, ctx, db, "CREATE TABLE IF NOT EXISTS `p.d.t3` CLONE `p.d.t3`")

	// A deep copy resets the chain: it is a BASE TABLE and can be cloned again.
	mustExecGaps(t, ctx, db,
		"CREATE TABLE `p.d.t4c` COPY `p.d.t3`",
		"CREATE TABLE `p.d.t5` CLONE `p.d.t4c`",
	)
	if got := tableTypeOf(t, db, "d", "t4c"); got.typ != "BASE TABLE" || got.baseTableName.Valid {
		t.Fatalf("copy: got %+v, want BASE TABLE with NULL base_table_*", got)
	}
	if got := tableTypeOf(t, db, "d", "t5"); got.typ != "CLONE" || got.baseTableName.String != "t4c" {
		t.Fatalf("clone of the copy: got %+v", got)
	}
}

// TestCreateTableCopy: CREATE TABLE [IF NOT EXISTS] ... COPY <table> is a deep copy that keeps rows,
// columns, PARTITION BY, CLUSTER BY and OPTIONS (statement OPTIONS override), and is independent of the source.
func TestCreateTableCopy(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db,
		"CREATE TABLE `p.src.t` (id INT64 NOT NULL, d DATE, v STRING) PARTITION BY d CLUSTER BY v OPTIONS(description='src', labels=[('a','b')])",
		"INSERT INTO `p.src.t` (id, d, v) VALUES (1, DATE '2026-10-01', 'a'), (2, DATE '2026-10-02', 'b')",
	)
	cc := changedCatalogOf(t, ctx, db, "CREATE TABLE IF NOT EXISTS `p.dst.t_copy` COPY `p.src.t` OPTIONS(description='copy')")
	if len(cc.Table.Added) != 1 || strings.Join(cc.Table.Added[0].NamePath, ".") != "p.dst.t_copy" {
		t.Fatalf("expected p.dst.t_copy reported as added, got %+v", cc.Table)
	}
	added := cc.Table.Added[0]
	if len(added.Columns) != 3 || !added.Columns[0].IsNotNull {
		t.Fatalf("copy must keep the column definitions, got %+v", added.Columns)
	}
	if added.PartitionBy != "d" || len(added.ClusterBy) != 1 || added.ClusterBy[0] != "v" {
		t.Fatalf("copy must keep partitioning and clustering, got %q %v", added.PartitionBy, added.ClusterBy)
	}
	if got, _ := added.Option("description"); got != "copy" {
		t.Fatalf("statement OPTIONS must override the source's, description = %q", got)
	}
	if got := added.Labels(); got["a"] != "b" {
		t.Fatalf("copy must keep the source's other options (labels), got %v", got)
	}
	if len(added.CloneOf) != 0 {
		t.Fatalf("a deep copy is not a clone, CloneOf = %v", added.CloneOf)
	}
	count := func() (n int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `p.dst.t_copy`").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := count(); got != 2 {
		t.Fatalf("copy has %d rows, want 2", got)
	}
	mustExecGaps(t, ctx, db, "INSERT INTO `p.src.t` (id, d, v) VALUES (3, DATE '2026-10-03', 'c')")
	mustExecGaps(t, ctx, db, "CREATE TABLE IF NOT EXISTS `p.dst.t_copy` COPY `p.src.t`")
	if got := count(); got != 2 {
		t.Fatalf("copy changed with its source or IF NOT EXISTS replaced it: %d rows, want 2", got)
	}
	mustExecGaps(t, ctx, db, "CREATE OR REPLACE TABLE `p.dst.t_copy` COPY `p.src.t`")
	if got := count(); got != 3 {
		t.Fatalf("CREATE OR REPLACE ... COPY: %d rows, want 3", got)
	}
	mustExecGaps(t, ctx, db, "DROP TABLE `p.src.t`")
	if got := count(); got != 3 {
		t.Fatalf("copy lost its data when the source was dropped: %d rows", got)
	}
}
