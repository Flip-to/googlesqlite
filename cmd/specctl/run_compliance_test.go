package main

import "testing"

func TestRankFailuresPrefersDBTThenSilent(t *testing.T) {
	rs := []caseResult{
		{Name: "loud-dbt", Score: kindScore[kindDriverError], Constructs: []string{"CASE"}},
		{Name: "silent-other", Score: kindScore[kindWrongValues]},
		{Name: "silent-dbt", Score: kindScore[kindWrongValues], Constructs: []string{"QUALIFY"}},
		{Name: "rowcount-dbt", Score: kindScore[kindRowCount], Constructs: []string{"LIKE"}},
	}
	rankFailures(rs)
	want := []string{"silent-dbt", "rowcount-dbt", "loud-dbt", "silent-other"}
	for i, w := range want {
		if rs[i].Name != w {
			t.Fatalf("position %d = %s, want %s (all: %+v)", i, rs[i].Name, w, rs)
		}
	}
}

func TestSkipBucket(t *testing.T) {
	for in, want := range map[string]string{
		"feature not in BigQuery: SQL_GRAPH (SQL graph (GQL) is not part of the BigQuery dialect under test)": "feature not in BigQuery: SQL graph (GQL) is not part of the BigQuery dialect under test",
		"non-BigQuery type in SQL: INT32": "non-BigQuery type in SQL",
		"depends on t (setup failed: x)":  "depends on a setup object that was skipped or failed",
	} {
		if got := skipBucket(in); got != want {
			t.Errorf("skipBucket(%q) = %q, want %q", in, got, want)
		}
	}
}
