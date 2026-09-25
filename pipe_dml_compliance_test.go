package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// TestPipeDMLCompliance covers pipe operators, struct value tables,
// hints, DML assertions, KEYS.* keysets, PIVOT with non-deterministic
// inputs and GROUP BY over IEEE special values. Expected values come
// from the GoogleSQL compliance fixtures under compliance/testdata; the
// file and case name are cited per case. Rows are compared as an
// unordered multiset unless ordered is set; NULL renders as "NULL".
func TestPipeDMLCompliance(t *testing.T) {
	db, err := sql.Open("googlesqlite", ":memory:?_test=pipedmlcompliance")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE KeyValue AS SELECT 1 AS key, "value1" AS value UNION ALL SELECT 2, "value2" UNION ALL SELECT 3, "value1"`,
		`CREATE TABLE t AS SELECT 123 AS i`,
		`CREATE TABLE Table1 AS SELECT CAST(1 AS INT64) AS primary_key, CAST(10 AS INT64) AS value UNION ALL SELECT 2, 20`,
		`CREATE TABLE t1 AS SELECT * FROM UNNEST([
			STRUCT(1 AS rowid, 1 AS x, 100 AS y, 1000 AS z),
			STRUCT(2 AS rowid, 1 AS x, 100 AS y, 1000 AS z),
			STRUCT(10 AS rowid, 2 AS x, 100 AS y, 1000 AS z),
			STRUCT(11 AS rowid, 2 AS x, 101 AS y, 1001 AS z)])`,
		`CREATE TABLE KeysetTable AS SELECT 1 AS kt_id, FROM_BASE64('CJmMp4UJEmQKWAowdHlwZS5nb29nbGVhcGlzLmNvbS9nb29nbGUuY3J5cHRvLnRpbmsuQWVzR2NtS2V5EiIaIP7Xj33VJoXuk9KMRwXsjkuDmo5P40WUSaVtJAzyve80GAEQARiZjKeFCSAB') AS keyset`,
		`CREATE TABLE RawKeyTable AS SELECT 1 AS id, b'0123456789abcdef' AS raw_key_bytes UNION ALL SELECT 2, REPEAT(b'0123456789abcdef', 2) UNION ALL SELECT 3, NULL`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("setup %q: %v", stmt, err)
		}
	}

	for _, tc := range []struct {
		name    string
		sql     string
		cols    []string
		rows    [][]string
		ordered bool
	}{
		// pipe_operators.test
		{name: "pipe_where", sql: "FROM KeyValue\n|> WHERE key=2", cols: []string{"key", "value"}, rows: [][]string{{"2", "value2"}}},
		{name: "pipe_where_window_function", sql: "FROM KeyValue\n|> WHERE RANK() OVER (ORDER BY key) = 2", cols: []string{"key", "value"}, rows: [][]string{{"2", "value2"}}},
		{name: "pipe_having", sql: "FROM KeyValue\n|> AGGREGATE COUNT(*) c GROUP BY value\n|> WHERE c = 2", cols: []string{"value", "c"}, rows: [][]string{{"value1", "2"}}},
		{name: "pipe_having_window_function", sql: "FROM KeyValue\n|> AGGREGATE COUNT(*) c GROUP BY value\n|> WHERE RANK() OVER (ORDER BY c) = 1", cols: []string{"value", "c"}, rows: [][]string{{"value2", "1"}}},
		{name: "pipe_window_in_where", sql: "FROM KeyValue\n|> WHERE RANK() OVER (ORDER BY key) = 3", cols: []string{"key", "value"}, rows: [][]string{{"3", "value1"}}},
		{name: "pipe_with", sql: "select 2 key\n|> WITH kv AS (FROM KeyValue)\n|> JOIN kv USING (key)", cols: []string{"key", "value"}, rows: [][]string{{"2", "value2"}}},
		{name: "pipe_with_unused", sql: "select 2 key\n|> WITH kv AS (FROM KeyValue)", cols: []string{"key"}, rows: [][]string{{"2"}}},
		{
			name: "pipe_with_multi",
			sql: `select null
|> WITH kv AS (FROM KeyValue),
        kv2 AS (FROM kv |> SET key=key+10)
|> WITH kv3 AS (FROM kv2 |> SET key=key+100)
|> WITH kv AS (FROM kv3 |> SET key=key+1000)
|> CROSS JOIN kv`,
			cols: []string{"$col1", "key", "value"},
			rows: [][]string{{"NULL", "1111", "value1"}, {"NULL", "1112", "value2"}, {"NULL", "1113", "value1"}},
		},
		{
			name: "pipe_with_in_subquery",
			sql: `select 2 key
|> WITH kv AS (FROM KeyValue)
|> JOIN (
     FROM kv
     |> WITH kv2 AS (
          FROM KeyValue
          |> EXTEND key+1000 AS k2
        )
     |> JOIN kv2 USING (key)
   ) USING (key)`,
			cols: []string{"key", "value", "value", "k2"},
			rows: [][]string{{"2", "value2", "value2", "1002"}},
		},

		// value_table_queries.test
		{name: "value_table_struct_single_row_column", sql: "SELECT AS STRUCT 1 foo", cols: []string{"foo"}, rows: [][]string{{"1"}}},
		{name: "value_table_struct_single_row", sql: `SELECT AS STRUCT 1 foo, "hey" bar`, cols: []string{"foo", "bar"}, rows: [][]string{{"1", "hey"}}},
		{name: "value_table_struct_union_all", sql: `SELECT AS STRUCT 1 foo, "hey" bar UNION ALL SELECT AS STRUCT 1 foo, "hey" bar`, cols: []string{"foo", "bar"}, rows: [][]string{{"1", "hey"}, {"1", "hey"}}},
		{
			name: "value_table_with_struct_star_filter",
			sql: `WITH
  WithStructValueTable AS
    (SELECT AS STRUCT 55 foo, "fifty five" bar
     UNION ALL
     SELECT AS STRUCT 77 foo, "seventy seven" bar)
SELECT AS STRUCT * FROM WithStructValueTable
WHERE foo = 77`,
			cols: []string{"foo", "bar"},
			rows: [][]string{{"77", "seventy seven"}},
		},

		// hints.test
		{name: "ignore_hints_for_other_engines_2", sql: "select i from t @{ some_other_engine.hint=5 }", cols: []string{"i"}, rows: [][]string{{"123"}}},
		{name: "ignore_invalid_hints_for_other_engines", sql: "select i from t group @{ some_other_engine.num_shards='abc' } by 1", cols: []string{"i"}, rows: [][]string{{"123"}}},

		// pivot.test (RAND() evaluated once per input row)
		{
			name: "pivot_nondeterminstic_input_column",
			sql: `SELECT
  min_x_1 = min_x_2,
  max_x_1 = max_x_2
FROM (SELECT x, RAND() AS r FROM t1)
  PIVOT(MIN(r) AS min, MAX(r) AS max FOR x IN (1 AS x_1, 1 AS x_2))`,
			cols: []string{"$col1", "$col2"},
			rows: [][]string{{"true", "true"}},
		},

		// groupby_queries.test (reduced to the special values involved)
		{
			name: "groupby_positive_infinities",
			sql: `SELECT DOUBLE
from (select IEEE_DIVIDE(1, 0) DOUBLE union all
      select POW(0.1, IEEE_DIVIDE(-1, 0)) union all
      select LOG(IEEE_DIVIDE(1, 0), 1.1) union all
      select CAST("Inf" as DOUBLE)) val
GROUP BY DOUBLE`,
			cols: []string{"DOUBLE"},
			rows: [][]string{{"+Inf"}},
		},
		{
			name: "groupby_negative_infinities",
			sql: `SELECT DOUBLE
from (select IEEE_DIVIDE(-1, 0) DOUBLE union all
      select POW(IEEE_DIVIDE(-1, 0), 3) union all
      select LOG(IEEE_DIVIDE(1, 0), 0.1) union all
      select CAST("-Inf" as DOUBLE)) val
GROUP BY DOUBLE`,
			cols: []string{"DOUBLE"},
			rows: [][]string{{"-Inf"}},
		},
		{
			name: "groupby_nan",
			sql: `SELECT COUNT(*) FROM (SELECT NAN
from (select IEEE_DIVIDE(0, 0) NAN union all
      select LOG(IEEE_DIVIDE(-1, 0), 1) union all
      select LOG(1, IEEE_DIVIDE(1, 0)) union all
      select cast("NAN" as DOUBLE) union all
      select cast("-NAN" as DOUBLE)) val
GROUP BY NAN)`,
			cols: []string{"$col1"},
			rows: [][]string{{"1"}},
		},

		// keys.test
		{
			name: "json_keyset_1",
			sql:  "SELECT kt_id, KEYS.KEYSET_TO_JSON(keyset) FROM KeysetTable",
			cols: []string{"kt_id", "$col2"},
			rows: [][]string{{"1", `{"key":[{"keyData":{"keyMaterialType":"SYMMETRIC","typeUrl":"type.googleapis.com/google.crypto.tink.AesGcmKey","value":"GiD+14991SaF7pPSjEcF7I5Lg5qOT+NFlEmlbSQM8r3vNA=="},"keyId":2427045401,"outputPrefixType":"TINK","status":"ENABLED"}],"primaryKeyId":2427045401}`}},
		},
		{name: "json_keyset_2", sql: "SELECT kt_id, KEYS.KEYSET_FROM_JSON(KEYS.KEYSET_TO_JSON(keyset)) = keyset FROM KeysetTable", cols: []string{"kt_id", "$col2"}, rows: [][]string{{"1", "true"}}},
		{
			name: "json_keyset_3",
			sql:  `SELECT TO_HEX(KEYS.KEYSET_FROM_JSON('{"key":[{"keyId":1234,"outputPrefixType":"TINK","status":"DESTROYED"}],"primaryKeyId":4321}'))`,
			cols: []string{"$col1"},
			// b"\x08\xe1!\x12\x07\x10\x03\x18\xd2\x09 \x01"
			rows: [][]string{{"08e1211207100318d2092001"}},
		},
		{
			name: "keys_raw_bytes_from_table_mixed_types",
			sql: `SELECT
  KEYS.KEYSET_TO_JSON(
    KEYS.ADD_KEY_FROM_RAW_BYTES(
      KEYS.ADD_KEY_FROM_RAW_BYTES(
        KEYS.NEW_KEYSET('AEAD_AES_GCM_256'), 'AES_CBC_PKCS', raw_key_bytes
      ), 'AES_GCM', raw_key_bytes
    )
  ) LIKE '%AesGcmKey%AesCbcPkcs7Key%AesGcmKey%'
FROM RawKeyTable
ORDER BY id`,
			cols:    []string{"$col1"},
			rows:    [][]string{{"true"}, {"true"}, {"NULL"}},
			ordered: true,
		},
		{name: "rotate_keyset_7", sql: "SELECT KEYS.KEYSET_LENGTH(KEYS.ROTATE_KEYSET(b'', 'AEAD_AES_GCM_256'))", cols: []string{"$col1"}, rows: [][]string{{"1"}}},
		{name: "safe_functions_3", sql: `SELECT SAFE.KEYS.KEYSET_TO_JSON(b'123'), SAFE.KEYS.KEYSET_FROM_JSON('{"key":123}')`, cols: []string{"$col1", "$col2"}, rows: [][]string{{"NULL", "NULL"}}},
		{name: "keys_4", sql: "SELECT KEYS.NEW_KEYSET(@key_type)", cols: []string{"$col1"}, rows: [][]string{{"NULL"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var args []any
			if strings.Contains(tc.sql, "@key_type") {
				args = append(args, sql.Named("key_type", nil))
			}
			cols, rows, err := queryStrings(ctx, db, tc.sql, args...)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if strings.Join(cols, ",") != strings.Join(tc.cols, ",") {
				t.Fatalf("columns = %v, want %v", cols, tc.cols)
			}
			if !tc.ordered {
				sortRows(rows)
				sortRows(tc.rows)
			}
			if fmt.Sprint(rows) != fmt.Sprint(tc.rows) {
				t.Fatalf("rows = %v, want %v", rows, tc.rows)
			}
		})
	}

	for _, tc := range []struct {
		name string
		sql  string
		want string
	}{
		// hints.test
		{name: "reject_unknown_hints_statement", sql: "@{ invalid_hint=5 } select i from t", want: "Unsupported hint: invalid_hint"},
		{name: "reject_unknown_hints_select", sql: "select @{ invalid_hint=5 } i from t", want: "Unsupported hint: invalid_hint"},
		{name: "reject_unknown_hints_table", sql: "select i from t @{ invalid_hint=5 }", want: "Unsupported hint: invalid_hint"},
		{name: "reject_unknown_hints_group_by", sql: "select i from t group @{ invalid_hint=5 } by 1", want: "Unsupported hint: invalid_hint"},
		{name: "reject_unknown_hints_order_by", sql: "select i from t order @{ invalid_hint=5 } by 1", want: "Unsupported hint: invalid_hint"},
		{name: "reject_unknown_hints_join", sql: "select t1.i from t t1 join @{ invalid_hint=5 } t t2 on true", want: "Unsupported hint: invalid_hint"},
		{name: "reject_unknown_hints_in", sql: "select i from t where i in @{ invalid_hint=5 } (select 1)", want: "Unsupported hint: invalid_hint"},
		{name: "reject_unknown_hints_exists", sql: "select i from t where exists @{ invalid_hint=5 } (select 1, 2)", want: "Unsupported hint: invalid_hint"},
		{name: "reject_invalid_hints", sql: "select i from t group @{ num_shards='abc' } by 1", want: "Unsupported hint: num_shards"},

		// dml_insert.test
		{name: "assert_rows_modified_failure", sql: "INSERT Table1 (primary_key, value) VALUES (3, 30)\nASSERT_ROWS_MODIFIED 100", want: "ASSERT_ROWS_MODIFIED expected 100 rows modified, but found 1"},
		{name: "insert_ignore_no_primary_key", sql: "INSERT IGNORE Table1 (primary_key, value) VALUES (30, 31)", want: "INSERT OR IGNORE is not allowed because the table does not have a primary key"},
		{name: "insert_replace_no_primary_key", sql: "INSERT REPLACE Table1 (primary_key, value) VALUES (30, 31)", want: "INSERT OR REPLACE is not allowed because the table does not have a primary key"},
		{name: "insert_update_no_primary_key", sql: "INSERT UPDATE Table1 (primary_key, value) VALUES (30, 31)", want: "INSERT OR UPDATE is not allowed because the table does not have a primary key"},
		// dml_delete.test
		{name: "delete_everything_with_failed_assertion", sql: "DELETE Table1 WHERE true ASSERT_ROWS_MODIFIED 100", want: "ASSERT_ROWS_MODIFIED expected 100 rows modified, but found 2"},

		// keys.test
		{name: "keys_bad_key_length_2", sql: "SELECT KEYS.ADD_KEY_FROM_RAW_BYTES(KEYS.NEW_KEYSET('AEAD_AES_GCM_256'), 'AES_GCM', RPAD(raw_key_bytes, 5, b'12')) AS keyset FROM RawKeyTable", want: "Unsupported key size: 5 bytes; expected 16 or 32 bytes."},
		{name: "keys_bad_key_length_3", sql: "SELECT KEYS.ADD_KEY_FROM_RAW_BYTES(KEYS.NEW_KEYSET('AEAD_AES_GCM_256'), 'AES_CBC_PKCS', RPAD(raw_key_bytes, 5, b'12')) AS keyset FROM RawKeyTable", want: "Unsupported key size: 5 bytes; expected 16, 24, or 32 bytes."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := queryStrings(ctx, db, tc.sql)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}

	// A failed ASSERT_ROWS_MODIFIED leaves the table untouched, and a
	// satisfied one applies the change (dml_delete.test).
	_, rows, err := queryStrings(ctx, db, "SELECT COUNT(*) FROM Table1")
	if err != nil || fmt.Sprint(rows) != "[[2]]" {
		t.Fatalf("Table1 after failed assertions: rows=%v err=%v", rows, err)
	}
	if _, err := db.ExecContext(ctx, "DELETE Table1 WHERE primary_key = 2 ASSERT_ROWS_MODIFIED 1"); err != nil {
		t.Fatalf("delete with satisfied assertion: %v", err)
	}
	_, rows, err = queryStrings(ctx, db, "SELECT COUNT(*) FROM Table1")
	if err != nil || fmt.Sprint(rows) != "[[1]]" {
		t.Fatalf("Table1 after satisfied assertion: rows=%v err=%v", rows, err)
	}

	// AEAD round trip over a keyset read from KeysetTable, and through a
	// RAW AES_GCM key added with KEYS.ADD_KEY_FROM_RAW_BYTES.
	_, rows, err = queryStrings(ctx, db, `SELECT AEAD.DECRYPT_STRING(keyset, AEAD.ENCRYPT(keyset, 'abc', 'aad'), 'aad') FROM KeysetTable`)
	if err != nil || fmt.Sprint(rows) != "[[abc]]" {
		t.Fatalf("AEAD round trip: rows=%v err=%v", rows, err)
	}
}

func queryStrings(ctx context.Context, db *sql.DB, q string, args ...any) ([]string, [][]string, error) {
	rs, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		return nil, nil, err
	}
	var out [][]string
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			switch x := v.(type) {
			case nil:
				row[i] = "NULL"
			case []byte:
				row[i] = string(x)
			default:
				row[i] = fmt.Sprint(x)
			}
		}
		out = append(out, row)
	}
	return cols, out, rs.Err()
}

func sortRows(rows [][]string) {
	sort.Slice(rows, func(i, j int) bool {
		return strings.Join(rows[i], "\x00") < strings.Join(rows[j], "\x00")
	})
}
