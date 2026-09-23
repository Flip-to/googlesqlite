package googlesqlite_test

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestStructAnonymousFields covers structs with unnamed fields
// (flipto-dbt probe S36). BigQuery keeps every anonymous field as a
// separate, positional field: its REST API labels them _field_1,
// _field_2, and TO_JSON_STRING prints each one with an empty key. The
// driver scans a STRUCT as a positional []any, so anonymous fields
// must neither collapse nor pick up generated names such as _field_0.
//
// Every expected value was checked on BigQuery (2026-09-23) with the
// same SQL text.
func TestStructAnonymousFields(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=struct_anonymous_fields")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tests := []struct {
		name  string
		query string
		want  any
	}{
		// BigQuery: [{_field_1: 1, _field_2: 2}].
		{"array_select_as_struct", `SELECT ARRAY(SELECT AS STRUCT 1, 2)`, []any{[]any{int64(1), int64(2)}}},
		{"struct_literal", `SELECT STRUCT(1, 2)`, []any{int64(1), int64(2)}},
		{"mixed_named_anonymous", `SELECT (SELECT AS STRUCT 1 AS a, 2)`, []any{int64(1), int64(2)}},
		{"tuple", `SELECT (1, 'x')`, []any{int64(1), "x"}},
		{"nested", `SELECT STRUCT(1, STRUCT(2, 3))`, []any{int64(1), []any{int64(2), int64(3)}}},
		{"named_unchanged", `SELECT STRUCT(1 AS x, 2 AS y)`, []any{int64(1), int64(2)}},
		{"array_select_as_struct_rows", `SELECT ARRAY(SELECT AS STRUCT x, x + 1 FROM UNNEST([1, 2]) x)`, []any{[]any{int64(1), int64(2)}, []any{int64(2), int64(3)}}},
		// BigQuery TO_JSON_STRING renders anonymous fields with "" keys.
		{"json_array_select_as_struct", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT 1, 2))`, `[{"":1,"":2}]`},
		{"json_struct_literal", `SELECT TO_JSON_STRING(STRUCT(1, 2))`, `{"":1,"":2}`},
		{"json_mixed", `SELECT TO_JSON_STRING((SELECT AS STRUCT 1 AS a, 2))`, `{"a":1,"":2}`},
		{"json_tuple", `SELECT TO_JSON_STRING((1, 'x'))`, `{"":1,"":"x"}`},
		{"json_nested", `SELECT TO_JSON_STRING(STRUCT(1, STRUCT(2, 3)))`, `{"":1,"":{"":2,"":3}}`},
		{"json_array_of_structs", `SELECT TO_JSON_STRING([STRUCT(1, 2), STRUCT(3, 4)])`, `[{"":1,"":2},{"":3,"":4}]`},
		{"json_named_unchanged", `SELECT TO_JSON_STRING(STRUCT(1 AS x, 2 AS y))`, `{"x":1,"y":2}`},
		{"json_cast_to_named", `SELECT TO_JSON_STRING(CAST((1, 2) AS STRUCT<x INT64, y INT64>))`, `{"x":1,"y":2}`},
		// FORMAT %T.
		{"format_t_array", `SELECT FORMAT('%T', ARRAY(SELECT AS STRUCT 1, 2))`, "[(1, 2)]"},
		{"format_t_tuple", `SELECT FORMAT('%T', STRUCT(1, 'x'))`, `(1, "x")`},
		// Positional semantics.
		{"equality", `SELECT STRUCT(1, 2) = STRUCT(1, 2)`, true},
		{"cast_field_access", `SELECT CAST(STRUCT(1, 2) AS STRUCT<x INT64, y INT64>).y`, int64(2)},
		{"unnest_typed_tuples", `SELECT (SELECT SUM(s.y) FROM UNNEST(ARRAY<STRUCT<x INT64, y INT64>>[(1, 2), (3, 4)]) s)`, int64(6)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got any
			if err := db.QueryRow(tt.query).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if d := cmp.Diff(tt.want, got); d != "" {
				t.Errorf("%s: (-want +got)\n%s", tt.query, d)
			}
		})
	}

	// Both anonymous fields stay in the column type metadata.
	rows, err := db.Query(`SELECT STRUCT(1, 2)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cts, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var typ struct {
		Name       string `json:"name"`
		FieldTypes []struct {
			Name string `json:"name"`
		} `json:"fieldTypes"`
	}
	if err := json.Unmarshal([]byte(cts[0].DatabaseTypeName()), &typ); err != nil {
		t.Fatal(err)
	}
	if typ.Name != "STRUCT<INT64, INT64>" || len(typ.FieldTypes) != 2 {
		t.Errorf("column type = %s", cts[0].DatabaseTypeName())
	}
}
