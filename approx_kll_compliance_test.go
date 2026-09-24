package googlesqlite_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	_ "github.com/goccy/googlesqlite"
)

// TestApproxKLLCompliance replays cases from the GoogleSQL compliance
// suite (googlesql/compliance/testdata/<file>.test, [name=<case>]).
// Expected values are the fixtures' own; results are rendered with
// FORMAT('%T') or compared as scalars so each assertion stays exact.
func TestApproxKLLCompliance(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=approx_kll_compliance")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	const aesGcmKeyset = "CJmMp4UJEmQKWAowdHlwZS5nb29nbGVhcGlzLmNvbS9nb29nbGUuY3J5cHRvLnRpbmsuQWVzR2NtS2V5EiIaIP7Xj33VJoXuk9KMRwXsjkuDmo5P40WUSaVtJAzyve80GAEQARiZjKeFCSAB"
	const aesSivKeyset = "CIGYpMkDEoQBCngKMHR5cGUuZ29vZ2xlYXBpcy5jb20vZ29vZ2xlLmNyeXB0by50aW5rLkFlc1NpdktleRJCEkBKAovGAOp+siYkuWvTYKXRW6V/3Z5en87KNgswc+8TKZUzumbh/qVtCYQ8qHo7li/fYDx43MoBYwbfUPhv3//xGAEQARiBmKTJAyAB"

	for _, tc := range []struct {
		name    string // file / case
		query   string
		want    string // "NULL" for SQL NULL
		wantErr string
	}{
		// approx_aggregation.test
		{
			name:  "approx_aggregation/approx_quantiles_fixed_count_1000",
			query: "SELECT ARRAY_LENGTH(APPROX_QUANTILES(x, (1000))) FROM UNNEST([1,2,3,3,2,1]) x",
			want:  "1001",
		},
		{
			name:  "approx_aggregation/approx_top_count_includes_nulls",
			query: "SELECT FORMAT('%T', APPROX_TOP_COUNT(x, 2)) FROM UNNEST([NULL, NULL, NULL, NULL, NULL]) x",
			want:  "[(NULL, 5)]",
		},
		{
			name:  "approx_aggregation/approx_top_count_big_count_small_nonunique_input",
			query: "SELECT ARRAY_LENGTH(APPROX_TOP_COUNT(x, 10000)) <= ARRAY_LENGTH(([1,1,1,2,2,2,3,3,3])) FROM UNNEST(([1,1,1,2,2,2,3,3,3])) x",
			want:  "true",
		},
		{
			name:  "approx_aggregation/approx_top_sum_includes_nulls",
			query: "SELECT FORMAT('%T', APPROX_TOP_SUM(x, x, 2)) FROM UNNEST([NULL, NULL, NULL, NULL, NULL]) x",
			want:  "[(NULL, NULL)]",
		},
		{
			name:  "approx_aggregation/approx_top_sum_big_count_small_unique_input",
			query: "SELECT ARRAY_LENGTH(APPROX_TOP_SUM(x, x, 10000)) <= ARRAY_LENGTH(([1,2,3])) FROM UNNEST(([1,2,3])) x",
			want:  "true",
		},
		{
			name:    "approx_aggregation/approx_top_sum_top_weight_overflow",
			query:   `SELECT FORMAT('%T', APPROX_TOP_SUM("", x, 1)) FROM UNNEST([~(1 << 63), 1]) x`,
			wantErr: "int64 overflow: 9223372036854775807 + 1",
		},
		{
			name:    "approx_aggregation/approx_top_count_invalid_huge_count",
			query:   "SELECT FORMAT('%T', APPROX_TOP_COUNT(x, 100001)) FROM UNNEST([1]) x",
			wantErr: "The second argument to APPROX_TOP_COUNT function cannot be greater than 100000",
		},
		// kll_quantiles_init.test
		{
			name:  "kll_quantiles_init/init_int64_no_input_rows_input",
			query: "SELECT KLL_QUANTILES.INIT_INT64(x) AS sketch FROM UNNEST(ARRAY<INT64>[]) AS x",
			want:  "NULL",
		},
		{
			name:  "kll_quantiles_init/init_double_all_null_input_or_weight_input_precision",
			query: "SELECT KLL_QUANTILES.INIT_DOUBLE(x, 2000) AS sketch FROM UNNEST(ARRAY<DOUBLE>[NULL, NULL, NULL]) AS x",
			want:  "NULL",
		},
		{
			name:    "kll_quantiles_init/init_int64_negative_precision_input_negative_precision",
			query:   "SELECT KLL_QUANTILES.INIT_INT64(x, (-1)) AS sketch FROM UNNEST(ARRAY<INT64>[3, 1, 2]) AS x",
			wantErr: "KLL failed: Provided inv_eps:-1 but inv_eps needs to be >= 1 and <= 200000000.",
		},
		// analytic_array_aggregation.test: ARRAY_AGG keeps NULL elements.
		{
			name: "analytic_array_aggregation/array_agg_with_null_BOOL_analytic",
			// Expected [false, true, true, NULL]; checked element-wise.
			query: "SELECT ARRAY_LENGTH(a) = 4 AND a[OFFSET(0)] = false AND a[OFFSET(1)] AND a[OFFSET(2)] AND a[OFFSET(3)] IS NULL FROM (SELECT ARRAY_AGG(elem) OVER (ORDER BY elem ASC NULLS LAST ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS a FROM UNNEST([true, false, NULL, true]) elem LIMIT 1)",
			want:  "true",
		},
		{
			name:  "analytic_array_aggregation/array_agg_with_null_INT64_analytic",
			query: "SELECT FORMAT('%T', a) FROM (SELECT elem, ARRAY_AGG(elem) OVER (ORDER BY elem RANGE BETWEEN UNBOUNDED PRECEDING AND 2 FOLLOWING) AS a FROM UNNEST([NULL, 1]) elem) WHERE elem IS NULL",
			want:  "[NULL]",
		},
		// compression.test
		{
			name:  "compression/zstd_compress_optional_arg_null",
			query: `SELECT zstd_compress("text", NULL)`,
			want:  "NULL",
		},
		{
			name:    "compression/zstd_compress_string_with_level_too_high",
			query:   `SELECT zstd_compress("text", 23)`,
			wantErr: "ZSTD compression level must be between -5 and 22, but was 23",
		},
		{
			name:    "compression/zstd_decompress_with_invalid_size_limit",
			query:   `SELECT zstd_decompress_to_string(b"(\xb5/\xfd \x05)\x00\x00bytes", size_limit => 0)`,
			wantErr: "ZSTD size limit must be positive, but was 0",
		},
		// aead.test (KeysetTable kt_id 1 / DeterministicKeysetTable kt_id 1)
		{
			name:  "aead/encrypt_decrypt_roundtrip (binary Tink keyset, empty plaintext)",
			query: "SELECT FORMAT('%T', AEAD.DECRYPT_STRING(k, AEAD.ENCRYPT(k, '', 'abc'), 'abc')) FROM (SELECT FROM_BASE64('" + aesGcmKeyset + "') AS k)",
			want:  `""`,
		},
		{
			name:  "aead/deterministic_encrypt_decrypt_roundtrip",
			query: "SELECT DETERMINISTIC_DECRYPT_STRING(k, DETERMINISTIC_ENCRYPT(k, 'plaintext', 'abc'), 'abc') FROM (SELECT FROM_BASE64('" + aesSivKeyset + "') AS k)",
			want:  "plaintext",
		},
		{
			name:  "aead/deterministic_encrypt_same_keyset_different_outputs",
			query: "SELECT DETERMINISTIC_ENCRYPT(k, 'plaintext', 'abc') = DETERMINISTIC_ENCRYPT(k, 'plaintext', 'abc') FROM (SELECT FROM_BASE64('" + aesSivKeyset + "') AS k)",
			want:  "true",
		},
		{
			name:  "aead/encrypt_with_clause",
			query: "WITH T AS (SELECT AEAD.ENCRYPT(FROM_BASE64('" + aesGcmKeyset + "'), 'plaintext', 'abc') AS ciphertext) SELECT ciphertext = ciphertext FROM T",
			want:  "true",
		},
		{
			name:    "aead/decrypt_invalid_string",
			query:   "SELECT AEAD.DECRYPT_STRING(k, AEAD.ENCRYPT(k, b'\\xFF\\xFE', b'abc'), 'abc') FROM (SELECT FROM_BASE64('" + aesGcmKeyset + "') AS k)",
			wantErr: "Decrypted plaintext is not a valid UTF-8 string. To decrypt to BYTES, use AEAD.DECRYPT_BYTES",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got sql.NullString
			err := db.QueryRowContext(ctx, tc.query).Scan(&got)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("%s: err = %v, want containing %q", tc.query, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: %v", tc.query, err)
			}
			s := "NULL"
			if got.Valid {
				s = got.String
			}
			if s != tc.want {
				t.Errorf("%s = %q, want %q", tc.query, s, tc.want)
			}
		})
	}
}

// TestAggregationThresholdCompliance replays aggregation_threshold.test
// count_aggregation_threshold_query_{keep_all,remove_some,remove_all}_results:
// groups with fewer distinct privacy units than `threshold` are dropped.
func TestAggregationThresholdCompliance(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=aggregation_threshold_compliance")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE TestExamTableWithRowId AS
SELECT CAST(1 AS INT64) AS row_id, "Hansen" AS last_name, "P91" AS test_id, CAST(510 AS FLOAT64) AS test_score UNION ALL
SELECT 2, "Wang", "U25", 500 UNION ALL
SELECT 3, "Wang", "C83", 520 UNION ALL
SELECT 4, "Wang", "U25", 460 UNION ALL
SELECT 5, "Hansen", "C83", 420 UNION ALL
SELECT 6, "Hansen", "C83", 560 UNION ALL
SELECT 7, "Devi", "U25", 580 UNION ALL
SELECT 8, "Devi", "P91", 480 UNION ALL
SELECT 9, "Ivanov", "U25", 490 UNION ALL
SELECT 10, "Ivanov", "P91", 540 UNION ALL
SELECT 11, "Silva", "U25", 550`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, tc := range []struct {
		name      string
		threshold int
		want      string
	}{
		{"count_aggregation_threshold_query_keep_all_results", 2, "C83:2,P91:3,U25:4"},
		{"count_aggregation_threshold_query_remove_some_results", 3, "P91:3,U25:4"},
		{"count_aggregation_threshold_query_remove_all_results", 10, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := db.QueryContext(ctx, `SELECT WITH AGGREGATION_THRESHOLD
  OPTIONS(threshold=`+strconv.Itoa(tc.threshold)+`, privacy_unit_column=last_name)
  test_id, COUNT(DISTINCT last_name) AS student_count
FROM TestExamTableWithRowId GROUP BY test_id ORDER BY test_id`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var out []string
			for rows.Next() {
				var id string
				var n int64
				if err := rows.Scan(&id, &n); err != nil {
					t.Fatal(err)
				}
				out = append(out, id+":"+strconv.FormatInt(n, 10))
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(out, ","); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
