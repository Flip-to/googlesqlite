package internal

import "testing"

func TestExtractPartitionByExprs(t *testing.T) {
	got := extractPartitionByExprs("CREATE OR REPLACE TABLE IF NOT EXISTS `p.ds.A` (d DATE) PARTITION BY DATE(ts, 'UTC') CLUSTER BY x; CREATE TABLE b (d DATE); CREATE TABLE c PARTITION BY d AS SELECT 1")
	if len(got) != 2 || got["a"] != "DATE(ts, 'UTC')" || got["c"] != "d" {
		t.Fatalf("got %v", got)
	}
}

func TestPartitionSpecFromExpr(t *testing.T) {
	for expr, layout := range map[string]string{"d": "20060102", "DATE(ts)": "20060102", "timestamp_trunc(ts, HOUR)": "2006010215", "DATE_TRUNC(d, MONTH)": "200601", "TIMESTAMP_TRUNC(ts, YEAR)": "2006"} {
		if ps := partitionSpecFromExpr(expr); ps == nil || ps.layout != layout {
			t.Fatalf("%s => %+v", expr, ps)
		}
	}
	for _, expr := range []string{"", "DATE(_PARTITIONTIME)x", "a + b", "TIMESTAMP_TRUNC(ts, WEEK)"} {
		if ps := partitionSpecFromExpr(expr); ps != nil {
			t.Fatalf("%q should be unsupported, got %+v", expr, ps)
		}
	}
	if ps := partitionSpecFromExpr("RANGE_BUCKET(n, GENERATE_ARRAY(0, 100, 10))"); ps == nil || ps.kind != "range" || ps.interval != 10 {
		t.Fatalf("range: %+v", ps)
	}
}
