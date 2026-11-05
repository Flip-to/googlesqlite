package googlesqlite_test

import (
	"reflect"
	"testing"

	"github.com/goccy/googlesqlite"
)

// TestCreateTableReportsPartitioningAndClustering: the spec of a created table carries its
// PARTITION BY / CLUSTER BY, so a server can report them as tables.get timePartitioning,
// rangePartitioning and clustering (dbt-bigquery compares them before it replaces a table).
func TestCreateTableReportsPartitioningAndClustering(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)

	cases := []struct {
		name    string
		query   string
		part    *googlesqlite.PartitionInfo
		cluster []string
	}{
		{
			name:    "ctas by column and cluster by two columns",
			query:   "CREATE TABLE `p.d.a` PARTITION BY d CLUSTER BY id, k AS SELECT 1 AS id, 'x' AS k, DATE '2026-01-01' AS d",
			part:    &googlesqlite.PartitionInfo{Field: "d", Type: "DAY"},
			cluster: []string{"id", "k"},
		},
		{
			name:  "column list, month truncation",
			query: "CREATE TABLE `p.d.b` (id INT64, d DATE) PARTITION BY DATE_TRUNC(d, MONTH)",
			part:  &googlesqlite.PartitionInfo{Field: "d", Type: "MONTH"},
		},
		{
			name:  "timestamp hour",
			query: "CREATE TABLE `p.d.c` (ts TIMESTAMP) PARTITION BY TIMESTAMP_TRUNC(ts, HOUR)",
			part:  &googlesqlite.PartitionInfo{Field: "ts", Type: "HOUR"},
		},
		{
			name:  "range",
			query: "CREATE TABLE `p.d.r` (n INT64) PARTITION BY RANGE_BUCKET(n, GENERATE_ARRAY(0, 100, 10))",
			part:  &googlesqlite.PartitionInfo{Field: "n", Range: true, RangeStart: 0, RangeEnd: 100, RangeInterval: 10},
		},
		{
			name:  "unpartitioned",
			query: "CREATE TABLE `p.d.u` (n INT64)",
		},
	}
	for _, tc := range cases {
		cc := changedCatalogOf(t, ctx, db, tc.query)
		if len(cc.Table.Added) != 1 {
			t.Fatalf("%s: table not reported: %+v", tc.name, cc.Table)
		}
		spec := cc.Table.Added[0]
		if got := spec.Partitioning(); !reflect.DeepEqual(got, tc.part) {
			t.Errorf("%s: Partitioning() = %+v, want %+v", tc.name, got, tc.part)
		}
		if !reflect.DeepEqual(spec.ClusterBy, tc.cluster) {
			t.Errorf("%s: ClusterBy = %v, want %v", tc.name, spec.ClusterBy, tc.cluster)
		}
	}
}
