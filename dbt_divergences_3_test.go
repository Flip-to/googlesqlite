package googlesqlite_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// TestDBTDivergences3 replays the silent divergences left by the full
// flipto-dbt differential run of 2026-09-24 (emulator vs the recorded
// real-BigQuery answers, dbt-validation.md rows 1 to 20). Every want is
// the recorded BigQuery answer; the name carries the probe id from
// verdicts.json (or minimal.json).
//
// Scalar probes run with literal arguments and with the arguments read
// from a STRUCT field, so the analyzer cannot fold them.
func TestDBTDivergences3(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=dbt_divergences_3")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const bigF = "179769313486231570000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
	const fmtWidth = `FORMAT('[%5d][%-5d][%05d][%+d][%8s][%-8s]', $1, $1, $1, $1, $2, $2)`
	const fmtFEG = `FORMAT('%t|%.2f|%e|%g|%5.1f', $1, $1, $1, $1, $1)`

	type scalar struct {
		name string
		expr string
		args []string
		want any
	}
	scalars := []scalar{
		// Row 10: CAST(DATE AS DATETIME).
		{"cast_as_datetime-2057.6", `CAST(CAST($1 AS DATETIME) AS STRING)`, []string{`DATE '2024-02-29'`}, "2024-02-29 00:00:00"},
		{"cast_as_datetime-2057.7", `CAST(CAST($1 AS DATETIME) AS STRING)`, []string{`DATE '2023-12-31'`}, "2023-12-31 00:00:00"},
		{"cast_as_datetime-2057.9", `CAST(CAST($1 AS DATETIME) AS STRING)`, []string{`DATE '2023-05-31'`}, "2023-05-31 00:00:00"},
		// Rows 11 to 14: SAFE_CAST.
		{"cast_as_timestamp-6219.5", `SAFE_CAST($1 AS TIMESTAMP)`, []string{`'2024-01-01 10:00:00.1234567'`}, nil},
		{"cast_as_bool-1821.5", `SAFE_CAST($1 AS BOOL)`, []string{`' true'`}, nil},
		{"cast_as_bool-1821.8", `SAFE_CAST($1 AS BOOL)`, []string{`-7`}, true},
		{"cast_as_bool-1821.9", `SAFE_CAST($1 AS BOOL)`, []string{`3`}, true},
		{"cast_as_bool-1821.10", `SAFE_CAST($1 AS BOOL)`, []string{`9223372036854775807`}, true},
		{"cast_as_bool-1821.11", `SAFE_CAST($1 AS BOOL)`, []string{`-9223372036854775808`}, true},
		{"cast_as_float64-5980.20", `SAFE_CAST($1 AS FLOAT64)`, []string{`'0x10'`}, 16.0},
		{"cast_as_numeric-3243.25", `SAFE_CAST($1 AS NUMERIC)`, []string{`'0x10'`}, nil},
		// Rows 1 and 2: FORMAT width and flags.
		{"format_width_flags-8665.0", fmtWidth, []string{`0`, `''`}, "[    0][0    ][00000][+0][        ][        ]"},
		{"format_width_flags-8665.1", fmtWidth, []string{`1`, `'A_b%c'`}, "[    1][1    ][00001][+1][   A_b%c][A_b%c   ]"},
		{"format_width_flags-8665.2", fmtWidth, []string{`-7`, `"it's"`}, "[   -7][-7   ][-0007][-7][    it's][it's    ]"},
		{"format_width_flags-8665.5", fmtWidth, []string{`-9223372036854775808`, `' pad '`}, "[-9223372036854775808][-9223372036854775808][-9223372036854775808][-9223372036854775808][    pad ][ pad    ]"},
		{"format_width_flags-8665.7", fmtWidth, []string{`0`, `'ABC'`}, "[    0][0    ][00000][+0][     ABC][ABC     ]"},
		{"format_width_flags-8665.8", fmtWidth, []string{`1`, `'x.y-z'`}, "[    1][1    ][00001][+1][   x.y-z][x.y-z   ]"},
		{"format_width_flags-8665.9", fmtWidth, []string{`-7`, `'123'`}, "[   -7][-7   ][-0007][-7][     123][123     ]"},
		{"format_width_flags-8665.11", fmtWidth, []string{`9223372036854775807`, `'abc'`}, "[9223372036854775807][9223372036854775807][9223372036854775807][+9223372036854775807][     abc][abc     ]"},
		// Rows 3 to 5: FORMAT of non-finite, huge and negative-zero floats.
		{"format_t_f_e_g-3249.5", fmtFEG, []string{`1.7976931348623157e308`}, "1.7976931348623157e+308|" + bigF + ".00|1.797693e+308|1.79769e+308|" + bigF + ".0"},
		{"format_t_f_e_g-3249.6", fmtFEG, []string{`CAST('NaN' AS FLOAT64)`}, "nan|nan|nan|nan|  nan"},
		{"format_t_f_e_g-3249.7", fmtFEG, []string{`CAST('+inf' AS FLOAT64)`}, "inf|inf|inf|inf|  inf"},
		{"format_t_f_e_g-3249.8", fmtFEG, []string{`-0.0`}, "-0.0|-0.00|-0.000000e+00|-0| -0.0"},
		{"format_t-5416.15", `FORMAT('%T', $1)`, []string{`-0.0`}, "-0.0"},
		{"format_t_struct-1232.7", `FORMAT('%T', STRUCT($1 AS a, $2 AS b))`, []string{`'a\nb'`, `-0.0`}, `("a\nb", -0.0)`},
		{"to_json_string-5209.15", `TO_JSON_STRING($1)`, []string{`-0.0`}, "-0"},
		{"cast_as_string-6504.15", `CAST($1 AS STRING)`, []string{`-0.0`}, "-0"},
		// Row 6: TO_JSON_STRING quotes NUMERIC.
		{"to_json_string-5209.19", `TO_JSON_STRING($1)`, []string{`NUMERIC '2.5'`}, `"2.5"`},
		{"to_json_string-5209.20", `TO_JSON_STRING($1)`, []string{`NUMERIC '-1.005'`}, `"-1.005"`},
		{"to_json_string-5209.21", `TO_JSON_STRING($1)`, []string{`NUMERIC '0.000000001'`}, `"0.000000001"`},
		{"to_json_string-5209.22", `TO_JSON_STRING($1)`, []string{`NUMERIC '99999999999999999999999999999.999999999'`}, `"99999999999999999999999999999.999999999"`},
		// Rows 7 to 9: JSON number text.
		{"safe_parse_json-2825.1", `TO_JSON_STRING(SAFE.PARSE_JSON($1))`, []string{`'{"a": "it\\u0027s"}'`}, `{"a":"it's"}`},
		{"safe_parse_json-2825.2", `TO_JSON_STRING(SAFE.PARSE_JSON($1))`, []string{`'[1, 2.50, "3"]'`}, `[1,2.5,"3"]`},
		{"json_value_array-8983.0", `ARRAY_TO_STRING(JSON_VALUE_ARRAY($1, '$.b'), ',')`, []string{`'{"a": 1, "b": [1.0, "x"], "c": null}'`}, "1.0,x"},
		{"json_value_array-8983.2", `ARRAY_TO_STRING(JSON_VALUE_ARRAY($1, '$'), ',')`, []string{`'[1, 2.50, "3"]'`}, "1,2.50,3"},
		// Row 19: SAFE.LOG of a domain error is NULL.
		{"safe_prefix-2502.h0_log", `SAFE.LOG($1)`, []string{`-1`}, nil},
		// Row 20: ROUND of NUMERIC overflows.
		{"round-0868.27", `ROUND($1, $2)`, []string{`NUMERIC '99999999999999999999999999999.999999999'`, `0`}, errPrefix("numeric overflow")},
		{"round-0868.39", `ROUND($1)`, []string{`NUMERIC '99999999999999999999999999999.999999999'`}, errPrefix("numeric overflow")},
	}

	type whole struct {
		name  string
		query string
		want  [][]any
	}
	const nanArr = `UNNEST([1.0, CAST('NaN' AS FLOAT64)])`
	const nanArrIn = `UNNEST(ARRAY(SELECT AS STRUCT v AS x FROM UNNEST([1.0, CAST('NaN' AS FLOAT64)]) AS v)) AS s, UNNEST([s.x])`
	const hllArr = `UNNEST(['b', 'a', NULL, 'a', ''])`
	const hllArrIn = `UNNEST(ARRAY(SELECT AS STRUCT v AS x FROM UNNEST(['b', 'a', NULL, 'a', '']) AS v)) AS s, UNNEST([s.x])`
	quant := func(from string) string {
		return `SELECT ARRAY_TO_STRING(ARRAY(SELECT CAST(q AS STRING) FROM UNNEST(qs) q WITH OFFSET o ORDER BY o), ',') FROM (SELECT APPROX_QUANTILES(x, 2) qs FROM ` + from + ` AS x)`
	}
	wholes := []whole{
		// Row 15.
		{"max_by-8303.6.direct", `SELECT MAX_BY(x, x) FROM ` + nanArr + ` AS x`, [][]any{{nil}}},
		{"max_by-8303.6.in", `SELECT MAX_BY(x, x) FROM ` + nanArrIn + ` AS x`, [][]any{{nil}}},
		{"max_by-8303.6.group", `SELECT g, MAX_BY(x, x) FROM ` + nanArr + ` AS x WITH OFFSET o CROSS JOIN UNNEST([MOD(o, 2)]) AS g GROUP BY g ORDER BY g`, [][]any{{int64(0), 1.0}, {int64(1), nil}}},
		{"min_by-6477.6.direct", `SELECT MIN_BY(x, x) FROM ` + nanArr + ` AS x`, [][]any{{nil}}},
		{"min_by-6477.6.in", `SELECT MIN_BY(x, x) FROM ` + nanArrIn + ` AS x`, [][]any{{nil}}},
		{"min_by-6477.6.group", `SELECT g, MIN_BY(x, x) FROM ` + nanArr + ` AS x WITH OFFSET o CROSS JOIN UNNEST([MOD(o, 2)]) AS g GROUP BY g ORDER BY g`, [][]any{{int64(0), 1.0}, {int64(1), nil}}},
		// Row 16.
		{"approx_quantiles-6302.6.direct", quant(nanArr), [][]any{{"nan,nan,1"}}},
		{"approx_quantiles-6302.6.in", quant(nanArrIn), [][]any{{"nan,nan,1"}}},
		// Row 17.
		{"hll_count_init-1736.4.direct", `SELECT HLL_COUNT.EXTRACT(HLL_COUNT.INIT(x)) FROM ` + hllArr + ` AS x`, [][]any{{int64(3)}}},
		{"hll_count_init-1736.4.in", `SELECT HLL_COUNT.EXTRACT(HLL_COUNT.INIT(x)) FROM ` + hllArrIn + ` AS x`, [][]any{{int64(3)}}},
		{"hll_count_init-1736.4.group", `SELECT g, HLL_COUNT.EXTRACT(HLL_COUNT.INIT(x)) FROM ` + hllArr + ` AS x WITH OFFSET o CROSS JOIN UNNEST([MOD(o, 2)]) AS g GROUP BY g ORDER BY g`, [][]any{{int64(0), int64(2)}, {int64(1), int64(1)}}},
		// Row 18 (probe and minimal current_ts_stable).
		{"current_timestamp-2918.h0", `SELECT CURRENT_TIMESTAMP() > TIMESTAMP '2026-01-01', CURRENT_TIMESTAMP() = CURRENT_TIMESTAMP()`, [][]any{{true, true}}},
		{"current_ts_stable", `SELECT CURRENT_TIMESTAMP() = CURRENT_TIMESTAMP()`, [][]any{{true}}},
		// Rows 5 and 19.
		{"to_json_string-5209.h1", `SELECT TO_JSON_STRING(STRUCT(0.1 AS a, 1e-7 AS b, 123456789.125 AS c, -0.0 AS d, 1.5e300 AS e))`, [][]any{{`{"a":0.1,"b":1e-07,"c":123456789.125,"d":-0,"e":1.5e+300}`}}},
		// JSON numbers that are doubles keep a fraction or exponent
		// (checked on BigQuery 2026-09-24).
		{"to_json_string_parse_json_doubles", `SELECT TO_JSON_STRING(PARSE_JSON('{"a":1.0,"b":2.50,"c":1e2,"d":1e20,"e":1.5e300,"f":-0.0,"g":123456789012.0,"h":1e15,"i":1e16,"j":0.000001,"k":1e-7}'))`, [][]any{{`{"a":1.0,"b":2.5,"c":100.0,"d":1e+20,"e":1.5e+300,"f":-0.0,"g":123456789012.0,"h":1e+15,"i":1e+16,"j":1e-06,"k":1e-07}`}}},
		{"to_json_string_json_value_array", `SELECT TO_JSON_STRING(JSON_VALUE_ARRAY('[1.0, 2.50]'))`, [][]any{{`["1.0","2.50"]`}}},
		{"format_f_large", `SELECT FORMAT('%f', 123456789012345678.0), FORMAT('%f', 1e17), FORMAT('%f', 1e16)`, [][]any{{"123456789012345680.000000", "100000000000000000.000000", "10000000000000000.000000"}}},
		{"max_min_by_nan_key", `SELECT MAX_BY(x, y), MIN_BY(x, y) FROM UNNEST([STRUCT(1 AS x, CAST('NaN' AS FLOAT64) AS y), (2, 1.0)])`, [][]any{{nil, nil}}},
		{"safe_prefix-2502.h0", `SELECT SAFE.SUBSTR('abc', 1, -1), SAFE.DATE(2024, 13, 1), SAFE.LOG(-1), SAFE.PARSE_DATE('%Y', 'x')`, [][]any{{nil, nil, nil, nil}}},
	}

	check := func(t *testing.T, query string, want [][]any) {
		t.Helper()
		rows, err := db.Query(query)
		var got [][]any
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				row := make([]any, len(want[0]))
				ptrs := make([]any, len(row))
				for i := range row {
					ptrs[i] = &row[i]
				}
				if err = rows.Scan(ptrs...); err != nil {
					break
				}
				got = append(got, row)
			}
			if err == nil {
				err = rows.Err()
			}
		}
		if p, ok := want[0][0].(errPrefix); ok {
			if err == nil || !strings.Contains(err.Error(), string(p)) {
				t.Errorf("%s: err = %v, want error containing %q", query, err, string(p))
			}
			return
		}
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s: got %d rows %v, want %d", query, len(got), got, len(want))
		}
		for r := range want {
			for i := range want[r] {
				if !sameValue(got[r][i], want[r][i]) {
					t.Errorf("%s: row %d column %d = %#v, want %#v", query, r+1, i+1, got[r][i], want[r][i])
				}
			}
		}
	}

	// BigQuery folds a literal CAST(-0.0 AS STRING) to "0" (checked on
	// BigQuery 2026-09-24); from a column it is "-0", the probe's answer.
	literalWant := map[string]any{"cast_as_string-6504.15": "0"}
	for _, c := range scalars {
		direct, field := c.expr, c.expr
		var fields []string
		for i, a := range c.args {
			ph := fmt.Sprintf("$%d", i+1)
			direct = strings.ReplaceAll(direct, ph, a)
			field = strings.ReplaceAll(field, ph, fmt.Sprintf("s.a%d", i+1))
			fields = append(fields, fmt.Sprintf("%s AS a%d", a, i+1))
		}
		c := c
		t.Run(c.name+"/literal", func(t *testing.T) {
			want := c.want
			if w, ok := literalWant[c.name]; ok {
				want = w
			}
			check(t, "SELECT "+direct, [][]any{{want}})
		})
		t.Run(c.name+"/column", func(t *testing.T) {
			check(t, fmt.Sprintf("SELECT %s FROM (SELECT STRUCT(%s) AS s)", field, strings.Join(fields, ", ")), [][]any{{c.want}})
		})
	}
	for _, c := range wholes {
		c := c
		t.Run(c.name, func(t *testing.T) { check(t, c.query, c.want) })
	}
}
