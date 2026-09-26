package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// TestTextRenderingBigQuery pins the text the driver writes for
// INTERVAL, RANGE, GEOGRAPHY and STRING-typed JSON text to the text
// BigQuery writes. Every want is BigQuery's answer, recorded on
// 2026-09-25 with the bq CLI (the probes are literals and UNNEST only;
// the rendering function, TO_JSON_STRING / CAST AS STRING / FORMAT /
// ST_ASTEXT / ST_ASGEOJSON, runs on BigQuery so the text itself is
// compared).
//
// Scalar probes run twice, like TestDBTDifferentialProbes: with literal
// arguments, which the analyzer may constant-fold, and with the
// arguments read from a STRUCT field, which forces the driver's
// runtime. BigQuery returned the same text for both forms.
//
// Not covered here, because the driver cannot reproduce BigQuery yet:
//   - FORMAT('%t' / '%T', x) with a top-level GEOGRAPHY or RANGE x: the
//     go-googlesql analyzer rejects the call ("Invalid type for
//     MakeValueAsStringSetter"); inside a STRUCT it is covered below.
//   - ST_ASGEOJSON of a LINESTRING that BigQuery densifies along the
//     geodesic (it inserts intermediate vertices); the driver keeps the
//     input vertices.
func TestTextRenderingBigQuery(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=text_rendering_bigquery")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	scalars := []struct {
		name string
		expr string   // uses $1, $2, ... for the arguments
		args []string // SQL literals
		want string
	}{
		{"iv_ytos_cast", `CAST($1 AS STRING)`, []string{`INTERVAL '1-2 3 4:5:6.789' YEAR TO SECOND`}, `1-2 3 4:5:6.789`},
		{"iv_ytos_json", `TO_JSON_STRING($1)`, []string{`INTERVAL '1-2 3 4:5:6.789' YEAR TO SECOND`}, `"P1Y2M3DT4H5M6.789S"`},
		{"iv_ytos_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL '1-2 3 4:5:6.789' YEAR TO SECOND`}, `1-2 3 4:5:6.789`},
		{"iv_ytos_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL '1-2 3 4:5:6.789' YEAR TO SECOND`}, `INTERVAL "1-2 3 4:5:6.789" YEAR TO SECOND`},
		{"iv_ytos_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL '1-2 3 4:5:6.789' YEAR TO SECOND`}, `"P1Y2M3DT4H5M6.789S"`},
		{"iv_ytos_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL '1-2 3 4:5:6.789' YEAR TO SECOND`}, `{"v":"P1Y2M3DT4H5M6.789S"}`},
		{"iv_ytos_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL '1-2 3 4:5:6.789' YEAR TO SECOND`}, `["P1Y2M3DT4H5M6.789S"]`},
		{"iv_ytos_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL '1-2 3 4:5:6.789' YEAR TO SECOND`}, `STRUCT(INTERVAL "1-2 3 4:5:6.789" YEAR TO SECOND)`},
		{"iv_zero_cast", `CAST($1 AS STRING)`, []string{`INTERVAL 0 DAY`}, `0-0 0 0:0:0`},
		{"iv_zero_json", `TO_JSON_STRING($1)`, []string{`INTERVAL 0 DAY`}, `"P0Y"`},
		{"iv_zero_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL 0 DAY`}, `0-0 0 0:0:0`},
		{"iv_zero_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL 0 DAY`}, `INTERVAL "0-0 0 0:0:0" YEAR TO SECOND`},
		{"iv_zero_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL 0 DAY`}, `"P0Y"`},
		{"iv_zero_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL 0 DAY`}, `{"v":"P0Y"}`},
		{"iv_zero_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL 0 DAY`}, `["P0Y"]`},
		{"iv_zero_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL 0 DAY`}, `STRUCT(INTERVAL "0-0 0 0:0:0" YEAR TO SECOND)`},
		{"iv_neg_cast", `CAST($1 AS STRING)`, []string{`INTERVAL '-1-2 -3 -4:5:6.5' YEAR TO SECOND`}, `-1-2 -3 -4:5:6.500`},
		{"iv_neg_json", `TO_JSON_STRING($1)`, []string{`INTERVAL '-1-2 -3 -4:5:6.5' YEAR TO SECOND`}, `"P-1Y-2M-3DT-4H-5M-6.5S"`},
		{"iv_neg_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL '-1-2 -3 -4:5:6.5' YEAR TO SECOND`}, `-1-2 -3 -4:5:6.500`},
		{"iv_neg_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL '-1-2 -3 -4:5:6.5' YEAR TO SECOND`}, `INTERVAL "-1-2 -3 -4:5:6.500" YEAR TO SECOND`},
		{"iv_neg_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL '-1-2 -3 -4:5:6.5' YEAR TO SECOND`}, `"P-1Y-2M-3DT-4H-5M-6.5S"`},
		{"iv_neg_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL '-1-2 -3 -4:5:6.5' YEAR TO SECOND`}, `{"v":"P-1Y-2M-3DT-4H-5M-6.5S"}`},
		{"iv_neg_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL '-1-2 -3 -4:5:6.5' YEAR TO SECOND`}, `["P-1Y-2M-3DT-4H-5M-6.5S"]`},
		{"iv_neg_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL '-1-2 -3 -4:5:6.5' YEAR TO SECOND`}, `STRUCT(INTERVAL "-1-2 -3 -4:5:6.500" YEAR TO SECOND)`},
		{"iv_micro_cast", `CAST($1 AS STRING)`, []string{`INTERVAL '0:0:0.000001' HOUR TO SECOND`}, `0-0 0 0:0:0.000001`},
		{"iv_micro_json", `TO_JSON_STRING($1)`, []string{`INTERVAL '0:0:0.000001' HOUR TO SECOND`}, `"PT0.000001S"`},
		{"iv_micro_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL '0:0:0.000001' HOUR TO SECOND`}, `0-0 0 0:0:0.000001`},
		{"iv_micro_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL '0:0:0.000001' HOUR TO SECOND`}, `INTERVAL "0-0 0 0:0:0.000001" YEAR TO SECOND`},
		{"iv_micro_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL '0:0:0.000001' HOUR TO SECOND`}, `"PT0.000001S"`},
		{"iv_micro_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL '0:0:0.000001' HOUR TO SECOND`}, `{"v":"PT0.000001S"}`},
		{"iv_micro_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL '0:0:0.000001' HOUR TO SECOND`}, `["PT0.000001S"]`},
		{"iv_micro_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL '0:0:0.000001' HOUR TO SECOND`}, `STRUCT(INTERVAL "0-0 0 0:0:0.000001" YEAR TO SECOND)`},
		{"iv_negmin_cast", `CAST($1 AS STRING)`, []string{`INTERVAL -90 MINUTE`}, `0-0 0 -1:30:0`},
		{"iv_negmin_json", `TO_JSON_STRING($1)`, []string{`INTERVAL -90 MINUTE`}, `"PT-1H-30M"`},
		{"iv_negmin_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL -90 MINUTE`}, `0-0 0 -1:30:0`},
		{"iv_negmin_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL -90 MINUTE`}, `INTERVAL "0-0 0 -1:30:0" YEAR TO SECOND`},
		{"iv_negmin_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL -90 MINUTE`}, `"PT-1H-30M"`},
		{"iv_negmin_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL -90 MINUTE`}, `{"v":"PT-1H-30M"}`},
		{"iv_negmin_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL -90 MINUTE`}, `["PT-1H-30M"]`},
		{"iv_negmin_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL -90 MINUTE`}, `STRUCT(INTERVAL "0-0 0 -1:30:0" YEAR TO SECOND)`},
		{"iv_mixed_cast", `CAST($1 AS STRING)`, []string{`INTERVAL '1-2 -3 4:0:0' YEAR TO SECOND`}, `1-2 -3 4:0:0`},
		{"iv_mixed_json", `TO_JSON_STRING($1)`, []string{`INTERVAL '1-2 -3 4:0:0' YEAR TO SECOND`}, `"P1Y2M-3DT4H"`},
		{"iv_mixed_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL '1-2 -3 4:0:0' YEAR TO SECOND`}, `1-2 -3 4:0:0`},
		{"iv_mixed_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL '1-2 -3 4:0:0' YEAR TO SECOND`}, `INTERVAL "1-2 -3 4:0:0" YEAR TO SECOND`},
		{"iv_mixed_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL '1-2 -3 4:0:0' YEAR TO SECOND`}, `"P1Y2M-3DT4H"`},
		{"iv_mixed_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL '1-2 -3 4:0:0' YEAR TO SECOND`}, `{"v":"P1Y2M-3DT4H"}`},
		{"iv_mixed_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL '1-2 -3 4:0:0' YEAR TO SECOND`}, `["P1Y2M-3DT4H"]`},
		{"iv_mixed_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL '1-2 -3 4:0:0' YEAR TO SECOND`}, `STRUCT(INTERVAL "1-2 -3 4:0:0" YEAR TO SECOND)`},
		{"iv_negsec_cast", `CAST($1 AS STRING)`, []string{`INTERVAL '-0:0:1.5' HOUR TO SECOND`}, `0-0 0 -0:0:1.500`},
		{"iv_negsec_json", `TO_JSON_STRING($1)`, []string{`INTERVAL '-0:0:1.5' HOUR TO SECOND`}, `"PT-1.5S"`},
		{"iv_negsec_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL '-0:0:1.5' HOUR TO SECOND`}, `0-0 0 -0:0:1.500`},
		{"iv_negsec_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL '-0:0:1.5' HOUR TO SECOND`}, `INTERVAL "0-0 0 -0:0:1.500" YEAR TO SECOND`},
		{"iv_negsec_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL '-0:0:1.5' HOUR TO SECOND`}, `"PT-1.5S"`},
		{"iv_negsec_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL '-0:0:1.5' HOUR TO SECOND`}, `{"v":"PT-1.5S"}`},
		{"iv_negsec_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL '-0:0:1.5' HOUR TO SECOND`}, `["PT-1.5S"]`},
		{"iv_negsec_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL '-0:0:1.5' HOUR TO SECOND`}, `STRUCT(INTERVAL "0-0 0 -0:0:1.500" YEAR TO SECOND)`},
		{"iv_month_cast", `CAST($1 AS STRING)`, []string{`INTERVAL 3 MONTH`}, `0-3 0 0:0:0`},
		{"iv_month_json", `TO_JSON_STRING($1)`, []string{`INTERVAL 3 MONTH`}, `"P3M"`},
		{"iv_month_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL 3 MONTH`}, `0-3 0 0:0:0`},
		{"iv_month_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL 3 MONTH`}, `INTERVAL "0-3 0 0:0:0" YEAR TO SECOND`},
		{"iv_month_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL 3 MONTH`}, `"P3M"`},
		{"iv_month_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL 3 MONTH`}, `{"v":"P3M"}`},
		{"iv_month_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL 3 MONTH`}, `["P3M"]`},
		{"iv_month_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL 3 MONTH`}, `STRUCT(INTERVAL "0-3 0 0:0:0" YEAR TO SECOND)`},
		{"iv_hours_cast", `CAST($1 AS STRING)`, []string{`INTERVAL 400 HOUR`}, `0-0 0 400:0:0`},
		{"iv_hours_json", `TO_JSON_STRING($1)`, []string{`INTERVAL 400 HOUR`}, `"PT400H"`},
		{"iv_hours_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL 400 HOUR`}, `0-0 0 400:0:0`},
		{"iv_hours_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL 400 HOUR`}, `INTERVAL "0-0 0 400:0:0" YEAR TO SECOND`},
		{"iv_hours_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL 400 HOUR`}, `"PT400H"`},
		{"iv_hours_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL 400 HOUR`}, `{"v":"PT400H"}`},
		{"iv_hours_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL 400 HOUR`}, `["PT400H"]`},
		{"iv_hours_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL 400 HOUR`}, `STRUCT(INTERVAL "0-0 0 400:0:0" YEAR TO SECOND)`},
		{"iv_negmonth_cast", `CAST($1 AS STRING)`, []string{`INTERVAL -14 MONTH`}, `-1-2 0 0:0:0`},
		{"iv_negmonth_json", `TO_JSON_STRING($1)`, []string{`INTERVAL -14 MONTH`}, `"P-1Y-2M"`},
		{"iv_negmonth_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL -14 MONTH`}, `-1-2 0 0:0:0`},
		{"iv_negmonth_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL -14 MONTH`}, `INTERVAL "-1-2 0 0:0:0" YEAR TO SECOND`},
		{"iv_negmonth_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL -14 MONTH`}, `"P-1Y-2M"`},
		{"iv_negmonth_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL -14 MONTH`}, `{"v":"P-1Y-2M"}`},
		{"iv_negmonth_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL -14 MONTH`}, `["P-1Y-2M"]`},
		{"iv_negmonth_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL -14 MONTH`}, `STRUCT(INTERVAL "-1-2 0 0:0:0" YEAR TO SECOND)`},
		{"iv_frac_cast", `CAST($1 AS STRING)`, []string{`INTERVAL '0:0:30.120' HOUR TO SECOND`}, `0-0 0 0:0:30.120`},
		{"iv_frac_json", `TO_JSON_STRING($1)`, []string{`INTERVAL '0:0:30.120' HOUR TO SECOND`}, `"PT30.12S"`},
		{"iv_frac_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL '0:0:30.120' HOUR TO SECOND`}, `0-0 0 0:0:30.120`},
		{"iv_frac_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL '0:0:30.120' HOUR TO SECOND`}, `INTERVAL "0-0 0 0:0:30.120" YEAR TO SECOND`},
		{"iv_frac_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL '0:0:30.120' HOUR TO SECOND`}, `"PT30.12S"`},
		{"iv_frac_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL '0:0:30.120' HOUR TO SECOND`}, `{"v":"PT30.12S"}`},
		{"iv_frac_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL '0:0:30.120' HOUR TO SECOND`}, `["PT30.12S"]`},
		{"iv_frac_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL '0:0:30.120' HOUR TO SECOND`}, `STRUCT(INTERVAL "0-0 0 0:0:30.120" YEAR TO SECOND)`},
		{"iv_neghalf_cast", `CAST($1 AS STRING)`, []string{`INTERVAL '-0:0:0.5' HOUR TO SECOND`}, `0-0 0 -0:0:0.500`},
		{"iv_neghalf_json", `TO_JSON_STRING($1)`, []string{`INTERVAL '-0:0:0.5' HOUR TO SECOND`}, `"PT-0.5S"`},
		{"iv_neghalf_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL '-0:0:0.5' HOUR TO SECOND`}, `0-0 0 -0:0:0.500`},
		{"iv_neghalf_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL '-0:0:0.5' HOUR TO SECOND`}, `INTERVAL "0-0 0 -0:0:0.500" YEAR TO SECOND`},
		{"iv_neghalf_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL '-0:0:0.5' HOUR TO SECOND`}, `"PT-0.5S"`},
		{"iv_neghalf_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL '-0:0:0.5' HOUR TO SECOND`}, `{"v":"PT-0.5S"}`},
		{"iv_neghalf_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL '-0:0:0.5' HOUR TO SECOND`}, `["PT-0.5S"]`},
		{"iv_neghalf_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL '-0:0:0.5' HOUR TO SECOND`}, `STRUCT(INTERVAL "0-0 0 -0:0:0.500" YEAR TO SECOND)`},
		{"iv_max_cast", `CAST($1 AS STRING)`, []string{`INTERVAL '10000-0 3660000 87840000:0:0' YEAR TO SECOND`}, `10000-0 3660000 87840000:0:0`},
		{"iv_max_json", `TO_JSON_STRING($1)`, []string{`INTERVAL '10000-0 3660000 87840000:0:0' YEAR TO SECOND`}, `"P10000Y3660000DT87840000H"`},
		{"iv_max_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL '10000-0 3660000 87840000:0:0' YEAR TO SECOND`}, `10000-0 3660000 87840000:0:0`},
		{"iv_max_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL '10000-0 3660000 87840000:0:0' YEAR TO SECOND`}, `INTERVAL "10000-0 3660000 87840000:0:0" YEAR TO SECOND`},
		{"iv_max_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL '10000-0 3660000 87840000:0:0' YEAR TO SECOND`}, `"P10000Y3660000DT87840000H"`},
		{"iv_max_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL '10000-0 3660000 87840000:0:0' YEAR TO SECOND`}, `{"v":"P10000Y3660000DT87840000H"}`},
		{"iv_max_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL '10000-0 3660000 87840000:0:0' YEAR TO SECOND`}, `["P10000Y3660000DT87840000H"]`},
		{"iv_max_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL '10000-0 3660000 87840000:0:0' YEAR TO SECOND`}, `STRUCT(INTERVAL "10000-0 3660000 87840000:0:0" YEAR TO SECOND)`},
		{"iv_negday_time_cast", `CAST($1 AS STRING)`, []string{`INTERVAL '0-0 -5 3:0:0' YEAR TO SECOND`}, `0-0 -5 3:0:0`},
		{"iv_negday_time_json", `TO_JSON_STRING($1)`, []string{`INTERVAL '0-0 -5 3:0:0' YEAR TO SECOND`}, `"P-5DT3H"`},
		{"iv_negday_time_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL '0-0 -5 3:0:0' YEAR TO SECOND`}, `0-0 -5 3:0:0`},
		{"iv_negday_time_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL '0-0 -5 3:0:0' YEAR TO SECOND`}, `INTERVAL "0-0 -5 3:0:0" YEAR TO SECOND`},
		{"iv_negday_time_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL '0-0 -5 3:0:0' YEAR TO SECOND`}, `"P-5DT3H"`},
		{"iv_negday_time_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL '0-0 -5 3:0:0' YEAR TO SECOND`}, `{"v":"P-5DT3H"}`},
		{"iv_negday_time_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL '0-0 -5 3:0:0' YEAR TO SECOND`}, `["P-5DT3H"]`},
		{"iv_negday_time_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL '0-0 -5 3:0:0' YEAR TO SECOND`}, `STRUCT(INTERVAL "0-0 -5 3:0:0" YEAR TO SECOND)`},
		{"iv_negym_posd_cast", `CAST($1 AS STRING)`, []string{`INTERVAL '-0-3 7 0:0:0' YEAR TO SECOND`}, `-0-3 7 0:0:0`},
		{"iv_negym_posd_json", `TO_JSON_STRING($1)`, []string{`INTERVAL '-0-3 7 0:0:0' YEAR TO SECOND`}, `"P-3M7D"`},
		{"iv_negym_posd_fmt_t", `FORMAT('%t', $1)`, []string{`INTERVAL '-0-3 7 0:0:0' YEAR TO SECOND`}, `-0-3 7 0:0:0`},
		{"iv_negym_posd_fmt_T", `FORMAT('%T', $1)`, []string{`INTERVAL '-0-3 7 0:0:0' YEAR TO SECOND`}, `INTERVAL "-0-3 7 0:0:0" YEAR TO SECOND`},
		{"iv_negym_posd_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`INTERVAL '-0-3 7 0:0:0' YEAR TO SECOND`}, `"P-3M7D"`},
		{"iv_negym_posd_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`INTERVAL '-0-3 7 0:0:0' YEAR TO SECOND`}, `{"v":"P-3M7D"}`},
		{"iv_negym_posd_json_array", `TO_JSON_STRING([$1])`, []string{`INTERVAL '-0-3 7 0:0:0' YEAR TO SECOND`}, `["P-3M7D"]`},
		{"iv_negym_posd_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`INTERVAL '-0-3 7 0:0:0' YEAR TO SECOND`}, `STRUCT(INTERVAL "-0-3 7 0:0:0" YEAR TO SECOND)`},
		{"rg_date_cast", `CAST($1 AS STRING)`, []string{`RANGE(DATE '2020-01-01', DATE '2020-01-02')`}, `[2020-01-01, 2020-01-02)`},
		{"rg_date_json", `TO_JSON_STRING($1)`, []string{`RANGE(DATE '2020-01-01', DATE '2020-01-02')`}, `{"start":"2020-01-01","end":"2020-01-02"}`},
		{"rg_date_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`RANGE(DATE '2020-01-01', DATE '2020-01-02')`}, `{"end":"2020-01-02","start":"2020-01-01"}`},
		{"rg_date_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`RANGE(DATE '2020-01-01', DATE '2020-01-02')`}, `{"v":{"start":"2020-01-01","end":"2020-01-02"}}`},
		{"rg_date_json_array", `TO_JSON_STRING([$1])`, []string{`RANGE(DATE '2020-01-01', DATE '2020-01-02')`}, `[{"start":"2020-01-01","end":"2020-01-02"}]`},
		{"rg_date_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`RANGE(DATE '2020-01-01', DATE '2020-01-02')`}, `STRUCT(RANGE<DATE> "[2020-01-01, 2020-01-02)")`},
		{"rg_date_ub_end_cast", `CAST($1 AS STRING)`, []string{`RANGE<DATE> '[2020-01-01, UNBOUNDED)'`}, `[2020-01-01, UNBOUNDED)`},
		{"rg_date_ub_end_json", `TO_JSON_STRING($1)`, []string{`RANGE<DATE> '[2020-01-01, UNBOUNDED)'`}, `{"start":"2020-01-01","end":null}`},
		{"rg_date_ub_end_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`RANGE<DATE> '[2020-01-01, UNBOUNDED)'`}, `{"end":null,"start":"2020-01-01"}`},
		{"rg_date_ub_end_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`RANGE<DATE> '[2020-01-01, UNBOUNDED)'`}, `{"v":{"start":"2020-01-01","end":null}}`},
		{"rg_date_ub_end_json_array", `TO_JSON_STRING([$1])`, []string{`RANGE<DATE> '[2020-01-01, UNBOUNDED)'`}, `[{"start":"2020-01-01","end":null}]`},
		{"rg_date_ub_end_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`RANGE<DATE> '[2020-01-01, UNBOUNDED)'`}, `STRUCT(RANGE<DATE> "[2020-01-01, UNBOUNDED)")`},
		{"rg_date_ub_start_cast", `CAST($1 AS STRING)`, []string{`RANGE<DATE> '[UNBOUNDED, 2020-01-02)'`}, `[UNBOUNDED, 2020-01-02)`},
		{"rg_date_ub_start_json", `TO_JSON_STRING($1)`, []string{`RANGE<DATE> '[UNBOUNDED, 2020-01-02)'`}, `{"start":null,"end":"2020-01-02"}`},
		{"rg_date_ub_start_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`RANGE<DATE> '[UNBOUNDED, 2020-01-02)'`}, `{"end":"2020-01-02","start":null}`},
		{"rg_date_ub_start_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`RANGE<DATE> '[UNBOUNDED, 2020-01-02)'`}, `{"v":{"start":null,"end":"2020-01-02"}}`},
		{"rg_date_ub_start_json_array", `TO_JSON_STRING([$1])`, []string{`RANGE<DATE> '[UNBOUNDED, 2020-01-02)'`}, `[{"start":null,"end":"2020-01-02"}]`},
		{"rg_date_ub_start_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`RANGE<DATE> '[UNBOUNDED, 2020-01-02)'`}, `STRUCT(RANGE<DATE> "[UNBOUNDED, 2020-01-02)")`},
		{"rg_date_ub_both_cast", `CAST($1 AS STRING)`, []string{`RANGE<DATE> '[UNBOUNDED, UNBOUNDED)'`}, `[UNBOUNDED, UNBOUNDED)`},
		{"rg_date_ub_both_json", `TO_JSON_STRING($1)`, []string{`RANGE<DATE> '[UNBOUNDED, UNBOUNDED)'`}, `{"start":null,"end":null}`},
		{"rg_date_ub_both_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`RANGE<DATE> '[UNBOUNDED, UNBOUNDED)'`}, `{"end":null,"start":null}`},
		{"rg_date_ub_both_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`RANGE<DATE> '[UNBOUNDED, UNBOUNDED)'`}, `{"v":{"start":null,"end":null}}`},
		{"rg_date_ub_both_json_array", `TO_JSON_STRING([$1])`, []string{`RANGE<DATE> '[UNBOUNDED, UNBOUNDED)'`}, `[{"start":null,"end":null}]`},
		{"rg_date_ub_both_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`RANGE<DATE> '[UNBOUNDED, UNBOUNDED)'`}, `STRUCT(RANGE<DATE> "[UNBOUNDED, UNBOUNDED)")`},
		{"rg_dt_cast", `CAST($1 AS STRING)`, []string{`RANGE<DATETIME> '[2014-09-27 12:30:00.45, 2016-10-17 11:15:00.33)'`}, `[2014-09-27 12:30:00.450, 2016-10-17 11:15:00.330)`},
		{"rg_dt_json", `TO_JSON_STRING($1)`, []string{`RANGE<DATETIME> '[2014-09-27 12:30:00.45, 2016-10-17 11:15:00.33)'`}, `{"start":"2014-09-27T12:30:00.450","end":"2016-10-17T11:15:00.330"}`},
		{"rg_dt_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`RANGE<DATETIME> '[2014-09-27 12:30:00.45, 2016-10-17 11:15:00.33)'`}, `{"end":"2016-10-17T11:15:00.330","start":"2014-09-27T12:30:00.450"}`},
		{"rg_dt_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`RANGE<DATETIME> '[2014-09-27 12:30:00.45, 2016-10-17 11:15:00.33)'`}, `{"v":{"start":"2014-09-27T12:30:00.450","end":"2016-10-17T11:15:00.330"}}`},
		{"rg_dt_json_array", `TO_JSON_STRING([$1])`, []string{`RANGE<DATETIME> '[2014-09-27 12:30:00.45, 2016-10-17 11:15:00.33)'`}, `[{"start":"2014-09-27T12:30:00.450","end":"2016-10-17T11:15:00.330"}]`},
		{"rg_dt_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`RANGE<DATETIME> '[2014-09-27 12:30:00.45, 2016-10-17 11:15:00.33)'`}, `STRUCT(RANGE<DATETIME> "[2014-09-27 12:30:00.450, 2016-10-17 11:15:00.330)")`},
		{"rg_dt_whole_cast", `CAST($1 AS STRING)`, []string{`RANGE(DATETIME '2020-01-01 00:00:00', DATETIME '2020-01-02 10:11:12')`}, `[2020-01-01 00:00:00, 2020-01-02 10:11:12)`},
		{"rg_dt_whole_json", `TO_JSON_STRING($1)`, []string{`RANGE(DATETIME '2020-01-01 00:00:00', DATETIME '2020-01-02 10:11:12')`}, `{"start":"2020-01-01T00:00:00","end":"2020-01-02T10:11:12"}`},
		{"rg_dt_whole_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`RANGE(DATETIME '2020-01-01 00:00:00', DATETIME '2020-01-02 10:11:12')`}, `{"end":"2020-01-02T10:11:12","start":"2020-01-01T00:00:00"}`},
		{"rg_dt_whole_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`RANGE(DATETIME '2020-01-01 00:00:00', DATETIME '2020-01-02 10:11:12')`}, `{"v":{"start":"2020-01-01T00:00:00","end":"2020-01-02T10:11:12"}}`},
		{"rg_dt_whole_json_array", `TO_JSON_STRING([$1])`, []string{`RANGE(DATETIME '2020-01-01 00:00:00', DATETIME '2020-01-02 10:11:12')`}, `[{"start":"2020-01-01T00:00:00","end":"2020-01-02T10:11:12"}]`},
		{"rg_dt_whole_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`RANGE(DATETIME '2020-01-01 00:00:00', DATETIME '2020-01-02 10:11:12')`}, `STRUCT(RANGE<DATETIME> "[2020-01-01 00:00:00, 2020-01-02 10:11:12)")`},
		{"rg_ts_cast", `CAST($1 AS STRING)`, []string{`RANGE<TIMESTAMP> '[2020-01-01 12:00:00+00, 2020-01-02 00:00:00.123456+00)'`}, `[2020-01-01 12:00:00+00, 2020-01-02 00:00:00.123456+00)`},
		{"rg_ts_json", `TO_JSON_STRING($1)`, []string{`RANGE<TIMESTAMP> '[2020-01-01 12:00:00+00, 2020-01-02 00:00:00.123456+00)'`}, `{"start":"2020-01-01T12:00:00Z","end":"2020-01-02T00:00:00.123456Z"}`},
		{"rg_ts_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`RANGE<TIMESTAMP> '[2020-01-01 12:00:00+00, 2020-01-02 00:00:00.123456+00)'`}, `{"end":"2020-01-02T00:00:00.123456Z","start":"2020-01-01T12:00:00Z"}`},
		{"rg_ts_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`RANGE<TIMESTAMP> '[2020-01-01 12:00:00+00, 2020-01-02 00:00:00.123456+00)'`}, `{"v":{"start":"2020-01-01T12:00:00Z","end":"2020-01-02T00:00:00.123456Z"}}`},
		{"rg_ts_json_array", `TO_JSON_STRING([$1])`, []string{`RANGE<TIMESTAMP> '[2020-01-01 12:00:00+00, 2020-01-02 00:00:00.123456+00)'`}, `[{"start":"2020-01-01T12:00:00Z","end":"2020-01-02T00:00:00.123456Z"}]`},
		{"rg_ts_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`RANGE<TIMESTAMP> '[2020-01-01 12:00:00+00, 2020-01-02 00:00:00.123456+00)'`}, `STRUCT(RANGE<TIMESTAMP> "[2020-01-01 12:00:00+00, 2020-01-02 00:00:00.123456+00)")`},
		{"rg_ts_ub_cast", `CAST($1 AS STRING)`, []string{`RANGE<TIMESTAMP> '[UNBOUNDED, 2020-01-02 00:00:00+00)'`}, `[UNBOUNDED, 2020-01-02 00:00:00+00)`},
		{"rg_ts_ub_json", `TO_JSON_STRING($1)`, []string{`RANGE<TIMESTAMP> '[UNBOUNDED, 2020-01-02 00:00:00+00)'`}, `{"start":null,"end":"2020-01-02T00:00:00Z"}`},
		{"rg_ts_ub_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`RANGE<TIMESTAMP> '[UNBOUNDED, 2020-01-02 00:00:00+00)'`}, `{"end":"2020-01-02T00:00:00Z","start":null}`},
		{"rg_ts_ub_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`RANGE<TIMESTAMP> '[UNBOUNDED, 2020-01-02 00:00:00+00)'`}, `{"v":{"start":null,"end":"2020-01-02T00:00:00Z"}}`},
		{"rg_ts_ub_json_array", `TO_JSON_STRING([$1])`, []string{`RANGE<TIMESTAMP> '[UNBOUNDED, 2020-01-02 00:00:00+00)'`}, `[{"start":null,"end":"2020-01-02T00:00:00Z"}]`},
		{"rg_ts_ub_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`RANGE<TIMESTAMP> '[UNBOUNDED, 2020-01-02 00:00:00+00)'`}, `STRUCT(RANGE<TIMESTAMP> "[UNBOUNDED, 2020-01-02 00:00:00+00)")`},
		{"g_point_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGPOINT(1, 1)`}, `"POINT(1 1)"`},
		{"g_point_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGPOINT(1, 1)`}, `"POINT(1 1)"`},
		{"g_point_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(1, 1)`}, `{"v":"POINT(1 1)"}`},
		{"g_point_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGPOINT(1, 1)`}, `["POINT(1 1)"]`},
		{"g_point_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(1, 1)`}, `STRUCT(ST_GeogFromText("POINT(1 1)"))`},
		{"g_point_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGPOINT(1, 1)`}, `POINT(1 1)`},
		{"g_point_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGPOINT(1, 1)`}, `{ "type": "Point", "coordinates": [1, 1] } `},
		{"g_point_dec_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGPOINT(-122.35, 47.62)`}, `"POINT(-122.35 47.62)"`},
		{"g_point_dec_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGPOINT(-122.35, 47.62)`}, `"POINT(-122.35 47.62)"`},
		{"g_point_dec_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(-122.35, 47.62)`}, `{"v":"POINT(-122.35 47.62)"}`},
		{"g_point_dec_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGPOINT(-122.35, 47.62)`}, `["POINT(-122.35 47.62)"]`},
		{"g_point_dec_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(-122.35, 47.62)`}, `STRUCT(ST_GeogFromText("POINT(-122.35 47.62)"))`},
		{"g_point_dec_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGPOINT(-122.35, 47.62)`}, `POINT(-122.35 47.62)`},
		{"g_point_dec_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGPOINT(-122.35, 47.62)`}, `{ "type": "Point", "coordinates": [-122.35, 47.62] } `},
		{"g_point_small_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGPOINT(0.1, 0.0000001)`}, `"POINT(0.1 1e-07)"`},
		{"g_point_small_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGPOINT(0.1, 0.0000001)`}, `"POINT(0.1 1e-07)"`},
		{"g_point_small_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(0.1, 0.0000001)`}, `{"v":"POINT(0.1 1e-07)"}`},
		{"g_point_small_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGPOINT(0.1, 0.0000001)`}, `["POINT(0.1 1e-07)"]`},
		{"g_point_small_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(0.1, 0.0000001)`}, `STRUCT(ST_GeogFromText("POINT(0.1 1e-07)"))`},
		{"g_point_small_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGPOINT(0.1, 0.0000001)`}, `POINT(0.1 1e-07)`},
		{"g_point_small_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGPOINT(0.1, 0.0000001)`}, `{ "type": "Point", "coordinates": [0.1, 1e-07] } `},
		{"g_point_long_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGPOINT(123.456789012345678, -45.5)`}, `"POINT(123.456789012346 -45.5)"`},
		{"g_point_long_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGPOINT(123.456789012345678, -45.5)`}, `"POINT(123.456789012346 -45.5)"`},
		{"g_point_long_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(123.456789012345678, -45.5)`}, `{"v":"POINT(123.456789012346 -45.5)"}`},
		{"g_point_long_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGPOINT(123.456789012345678, -45.5)`}, `["POINT(123.456789012346 -45.5)"]`},
		{"g_point_long_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(123.456789012345678, -45.5)`}, `STRUCT(ST_GeogFromText("POINT(123.456789012346 -45.5)"))`},
		{"g_point_long_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGPOINT(123.456789012345678, -45.5)`}, `POINT(123.456789012346 -45.5)`},
		{"g_point_long_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGPOINT(123.456789012345678, -45.5)`}, `{ "type": "Point", "coordinates": [123.456789012346, -45.5] } `},
		{"g_line_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('LINESTRING(0 0, 1.5 2.25)')`}, `"LINESTRING(0 0, 1.5 2.25)"`},
		{"g_line_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('LINESTRING(0 0, 1.5 2.25)')`}, `"LINESTRING(0 0, 1.5 2.25)"`},
		{"g_line_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('LINESTRING(0 0, 1.5 2.25)')`}, `{"v":"LINESTRING(0 0, 1.5 2.25)"}`},
		{"g_line_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('LINESTRING(0 0, 1.5 2.25)')`}, `["LINESTRING(0 0, 1.5 2.25)"]`},
		{"g_line_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('LINESTRING(0 0, 1.5 2.25)')`}, `STRUCT(ST_GeogFromText("LINESTRING(0 0, 1.5 2.25)"))`},
		{"g_line_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('LINESTRING(0 0, 1.5 2.25)')`}, `LINESTRING(0 0, 1.5 2.25)`},
		{"g_poly_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))')`}, `"POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))"`},
		{"g_poly_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))')`}, `"POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))"`},
		{"g_poly_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))')`}, `{"v":"POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))"}`},
		{"g_poly_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))')`}, `["POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))"]`},
		{"g_poly_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))')`}, `STRUCT(ST_GeogFromText("POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))"))`},
		{"g_poly_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))')`}, `POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))`},
		{"g_poly_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGFROMTEXT('POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))')`}, `{ "type": "Polygon", "coordinates": [ [ [2, 0], [2, 2], [1, 2], [0, 2], [0, 0], [2, 0] ] ] } `},
		{"g_mpoint_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1, 2 2)')`}, `"MULTIPOINT(1 1, 2 2)"`},
		{"g_mpoint_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1, 2 2)')`}, `"MULTIPOINT(1 1, 2 2)"`},
		{"g_mpoint_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1, 2 2)')`}, `{"v":"MULTIPOINT(1 1, 2 2)"}`},
		{"g_mpoint_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1, 2 2)')`}, `["MULTIPOINT(1 1, 2 2)"]`},
		{"g_mpoint_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1, 2 2)')`}, `STRUCT(ST_GeogFromText("MULTIPOINT(1 1, 2 2)"))`},
		{"g_mpoint_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1, 2 2)')`}, `MULTIPOINT(1 1, 2 2)`},
		{"g_mpoint_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1, 2 2)')`}, `{ "type": "MultiPoint", "coordinates": [ [1, 1], [2, 2] ] } `},
		{"g_mline_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((2 2, 3 4), (5 6, 7 7))')`}, `"MULTILINESTRING((2 2, 3 4), (5 6, 7 7))"`},
		{"g_mline_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((2 2, 3 4), (5 6, 7 7))')`}, `"MULTILINESTRING((2 2, 3 4), (5 6, 7 7))"`},
		{"g_mline_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((2 2, 3 4), (5 6, 7 7))')`}, `{"v":"MULTILINESTRING((2 2, 3 4), (5 6, 7 7))"}`},
		{"g_mline_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((2 2, 3 4), (5 6, 7 7))')`}, `["MULTILINESTRING((2 2, 3 4), (5 6, 7 7))"]`},
		{"g_mline_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((2 2, 3 4), (5 6, 7 7))')`}, `STRUCT(ST_GeogFromText("MULTILINESTRING((2 2, 3 4), (5 6, 7 7))"))`},
		{"g_mline_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((2 2, 3 4), (5 6, 7 7))')`}, `MULTILINESTRING((2 2, 3 4), (5 6, 7 7))`},
		{"g_coll_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))')`}, `"GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))"`},
		{"g_coll_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))')`}, `"GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))"`},
		{"g_coll_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))')`}, `{"v":"GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))"}`},
		{"g_coll_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))')`}, `["GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))"]`},
		{"g_coll_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))')`}, `STRUCT(ST_GeogFromText("GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))"))`},
		{"g_coll_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))')`}, `GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))`},
		{"g_coll2_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))')`}, `"GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))"`},
		{"g_coll2_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))')`}, `"GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))"`},
		{"g_coll2_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))')`}, `{"v":"GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))"}`},
		{"g_coll2_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))')`}, `["GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))"]`},
		{"g_coll2_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))')`}, `STRUCT(ST_GeogFromText("GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))"))`},
		{"g_coll2_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))')`}, `GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))`},
		{"g_empty_point_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('POINT EMPTY')`}, `"GEOMETRYCOLLECTION EMPTY"`},
		{"g_empty_point_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('POINT EMPTY')`}, `"GEOMETRYCOLLECTION EMPTY"`},
		{"g_empty_point_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('POINT EMPTY')`}, `{"v":"GEOMETRYCOLLECTION EMPTY"}`},
		{"g_empty_point_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('POINT EMPTY')`}, `["GEOMETRYCOLLECTION EMPTY"]`},
		{"g_empty_point_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('POINT EMPTY')`}, `STRUCT(ST_GeogFromText("GEOMETRYCOLLECTION EMPTY"))`},
		{"g_empty_point_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('POINT EMPTY')`}, `GEOMETRYCOLLECTION EMPTY`},
		{"g_empty_point_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGFROMTEXT('POINT EMPTY')`}, `{ "type": "GeometryCollection", "geometries": [ ] } `},
		{"g_empty_coll_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION EMPTY')`}, `"GEOMETRYCOLLECTION EMPTY"`},
		{"g_empty_coll_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION EMPTY')`}, `"GEOMETRYCOLLECTION EMPTY"`},
		{"g_empty_coll_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION EMPTY')`}, `{"v":"GEOMETRYCOLLECTION EMPTY"}`},
		{"g_empty_coll_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION EMPTY')`}, `["GEOMETRYCOLLECTION EMPTY"]`},
		{"g_empty_coll_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION EMPTY')`}, `STRUCT(ST_GeogFromText("GEOMETRYCOLLECTION EMPTY"))`},
		{"g_empty_coll_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION EMPTY')`}, `GEOMETRYCOLLECTION EMPTY`},
		{"g_empty_coll_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION EMPTY')`}, `{ "type": "GeometryCollection", "geometries": [ ] } `},
		{"g_mpoly_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('MULTIPOLYGON(((2 0, 2 2, 1 2, 0 2, 0 0, 2 0)))')`}, `"POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))"`},
		{"g_mpoly_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('MULTIPOLYGON(((2 0, 2 2, 1 2, 0 2, 0 0, 2 0)))')`}, `"POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))"`},
		{"g_mpoly_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('MULTIPOLYGON(((2 0, 2 2, 1 2, 0 2, 0 0, 2 0)))')`}, `{"v":"POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))"}`},
		{"g_mpoly_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('MULTIPOLYGON(((2 0, 2 2, 1 2, 0 2, 0 0, 2 0)))')`}, `["POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))"]`},
		{"g_mpoly_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('MULTIPOLYGON(((2 0, 2 2, 1 2, 0 2, 0 0, 2 0)))')`}, `STRUCT(ST_GeogFromText("POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))"))`},
		{"g_mpoly_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('MULTIPOLYGON(((2 0, 2 2, 1 2, 0 2, 0 0, 2 0)))')`}, `POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))`},
		{"g_mpoly_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGFROMTEXT('MULTIPOLYGON(((2 0, 2 2, 1 2, 0 2, 0 0, 2 0)))')`}, `{ "type": "Polygon", "coordinates": [ [ [2, 0], [2, 2], [1, 2], [0, 2], [0, 0], [2, 0] ] ] } `},
		{"g_point_negsmall_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGPOINT(-0.00001, 0.0001)`}, `"POINT(-1e-05 0.0001)"`},
		{"g_point_negsmall_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGPOINT(-0.00001, 0.0001)`}, `"POINT(-1e-05 0.0001)"`},
		{"g_point_negsmall_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(-0.00001, 0.0001)`}, `{"v":"POINT(-1e-05 0.0001)"}`},
		{"g_point_negsmall_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGPOINT(-0.00001, 0.0001)`}, `["POINT(-1e-05 0.0001)"]`},
		{"g_point_negsmall_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(-0.00001, 0.0001)`}, `STRUCT(ST_GeogFromText("POINT(-1e-05 0.0001)"))`},
		{"g_point_negsmall_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGPOINT(-0.00001, 0.0001)`}, `POINT(-1e-05 0.0001)`},
		{"g_point_negsmall_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGPOINT(-0.00001, 0.0001)`}, `{ "type": "Point", "coordinates": [-1e-05, 0.0001] } `},
		{"g_point_third_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGPOINT(1/3, -2/3)`}, `"POINT(0.333333333333333 -0.666666666666667)"`},
		{"g_point_third_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGPOINT(1/3, -2/3)`}, `"POINT(0.333333333333333 -0.666666666666667)"`},
		{"g_point_third_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(1/3, -2/3)`}, `{"v":"POINT(0.333333333333333 -0.666666666666667)"}`},
		{"g_point_third_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGPOINT(1/3, -2/3)`}, `["POINT(0.333333333333333 -0.666666666666667)"]`},
		{"g_point_third_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGPOINT(1/3, -2/3)`}, `STRUCT(ST_GeogFromText("POINT(0.333333333333333 -0.666666666666667)"))`},
		{"g_point_third_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGPOINT(1/3, -2/3)`}, `POINT(0.333333333333333 -0.666666666666667)`},
		{"g_point_third_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGPOINT(1/3, -2/3)`}, `{ "type": "Point", "coordinates": [0.333333333333333, -0.666666666666667] } `},
		{"g_empty_line_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('LINESTRING EMPTY')`}, `"GEOMETRYCOLLECTION EMPTY"`},
		{"g_empty_line_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('LINESTRING EMPTY')`}, `"GEOMETRYCOLLECTION EMPTY"`},
		{"g_empty_line_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('LINESTRING EMPTY')`}, `{"v":"GEOMETRYCOLLECTION EMPTY"}`},
		{"g_empty_line_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('LINESTRING EMPTY')`}, `["GEOMETRYCOLLECTION EMPTY"]`},
		{"g_empty_line_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('LINESTRING EMPTY')`}, `STRUCT(ST_GeogFromText("GEOMETRYCOLLECTION EMPTY"))`},
		{"g_empty_line_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('LINESTRING EMPTY')`}, `GEOMETRYCOLLECTION EMPTY`},
		{"g_empty_line_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGFROMTEXT('LINESTRING EMPTY')`}, `{ "type": "GeometryCollection", "geometries": [ ] } `},
		{"g_empty_poly_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('POLYGON EMPTY')`}, `"GEOMETRYCOLLECTION EMPTY"`},
		{"g_empty_poly_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('POLYGON EMPTY')`}, `"GEOMETRYCOLLECTION EMPTY"`},
		{"g_empty_poly_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('POLYGON EMPTY')`}, `{"v":"GEOMETRYCOLLECTION EMPTY"}`},
		{"g_empty_poly_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('POLYGON EMPTY')`}, `["GEOMETRYCOLLECTION EMPTY"]`},
		{"g_empty_poly_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('POLYGON EMPTY')`}, `STRUCT(ST_GeogFromText("GEOMETRYCOLLECTION EMPTY"))`},
		{"g_empty_poly_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('POLYGON EMPTY')`}, `GEOMETRYCOLLECTION EMPTY`},
		{"g_empty_poly_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGFROMTEXT('POLYGON EMPTY')`}, `{ "type": "GeometryCollection", "geometries": [ ] } `},
		{"g_mpoint1_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1)')`}, `"POINT(1 1)"`},
		{"g_mpoint1_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1)')`}, `"POINT(1 1)"`},
		{"g_mpoint1_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1)')`}, `{"v":"POINT(1 1)"}`},
		{"g_mpoint1_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1)')`}, `["POINT(1 1)"]`},
		{"g_mpoint1_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1)')`}, `STRUCT(ST_GeogFromText("POINT(1 1)"))`},
		{"g_mpoint1_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1)')`}, `POINT(1 1)`},
		{"g_mpoint1_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGFROMTEXT('MULTIPOINT(1 1)')`}, `{ "type": "Point", "coordinates": [1, 1] } `},
		{"g_mline1_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((0 0, 1 1))')`}, `"LINESTRING(0 0, 1 1)"`},
		{"g_mline1_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((0 0, 1 1))')`}, `"LINESTRING(0 0, 1 1)"`},
		{"g_mline1_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((0 0, 1 1))')`}, `{"v":"LINESTRING(0 0, 1 1)"}`},
		{"g_mline1_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((0 0, 1 1))')`}, `["LINESTRING(0 0, 1 1)"]`},
		{"g_mline1_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((0 0, 1 1))')`}, `STRUCT(ST_GeogFromText("LINESTRING(0 0, 1 1)"))`},
		{"g_mline1_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((0 0, 1 1))')`}, `LINESTRING(0 0, 1 1)`},
		{"g_mline1_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGFROMTEXT('MULTILINESTRING((0 0, 1 1))')`}, `{ "type": "LineString", "coordinates": [ [0, 0], [1, 1] ] } `},
		{"g_coll1_json", `TO_JSON_STRING($1)`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(1 1))')`}, `"POINT(1 1)"`},
		{"g_coll1_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(1 1))')`}, `"POINT(1 1)"`},
		{"g_coll1_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(1 1))')`}, `{"v":"POINT(1 1)"}`},
		{"g_coll1_json_array", `TO_JSON_STRING([$1])`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(1 1))')`}, `["POINT(1 1)"]`},
		{"g_coll1_fmt_T_struct", `FORMAT('%T', STRUCT($1 AS v))`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(1 1))')`}, `STRUCT(ST_GeogFromText("POINT(1 1)"))`},
		{"g_coll1_astext", `ST_ASTEXT($1)`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(1 1))')`}, `POINT(1 1)`},
		{"g_coll1_geojson", `ST_ASGEOJSON($1)`, []string{`ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(1 1))')`}, `{ "type": "Point", "coordinates": [1, 1] } `},
		{"js_extract_obj_json", `TO_JSON_STRING(JSON_EXTRACT($1, '$'))`, []string{`'{"a":1}'`}, `"{\"a\":1}"`},
		{"js_extract_obj_json_struct", `TO_JSON_STRING(STRUCT(JSON_EXTRACT($1, '$') AS v))`, []string{`'{"a":1}'`}, `{"v":"{\"a\":1}"}`},
		{"js_extract_obj_fmt_T", `FORMAT('%T', JSON_EXTRACT($1, '$'))`, []string{`'{"a":1}'`}, `'{"a":1}'`},
		{"js_extract_obj_fmt_t", `FORMAT('%t', JSON_EXTRACT($1, '$'))`, []string{`'{"a":1}'`}, `{"a":1}`},
		{"js_extract_obj_to_json", `TO_JSON_STRING(TO_JSON(JSON_EXTRACT($1, '$')))`, []string{`'{"a":1}'`}, `"{\"a\":1}"`},
		{"js_query_str_json", `TO_JSON_STRING(JSON_QUERY($1, '$.a'))`, []string{`'{"a":"x"}'`}, `"\"x\""`},
		{"js_query_str_json_struct", `TO_JSON_STRING(STRUCT(JSON_QUERY($1, '$.a') AS v))`, []string{`'{"a":"x"}'`}, `{"v":"\"x\""}`},
		{"js_query_str_fmt_T", `FORMAT('%T', JSON_QUERY($1, '$.a'))`, []string{`'{"a":"x"}'`}, `'"x"'`},
		{"js_query_str_fmt_t", `FORMAT('%t', JSON_QUERY($1, '$.a'))`, []string{`'{"a":"x"}'`}, `"x"`},
		{"js_query_str_to_json", `TO_JSON_STRING(TO_JSON(JSON_QUERY($1, '$.a')))`, []string{`'{"a":"x"}'`}, `"\"x\""`},
		{"js_query_arr_json", `TO_JSON_STRING(JSON_QUERY($1, '$.a'))`, []string{`'{"a":[1,{"b":2}]}'`}, `"[1,{\"b\":2}]"`},
		{"js_query_arr_json_struct", `TO_JSON_STRING(STRUCT(JSON_QUERY($1, '$.a') AS v))`, []string{`'{"a":[1,{"b":2}]}'`}, `{"v":"[1,{\"b\":2}]"}`},
		{"js_query_arr_fmt_T", `FORMAT('%T', JSON_QUERY($1, '$.a'))`, []string{`'{"a":[1,{"b":2}]}'`}, `'[1,{"b":2}]'`},
		{"js_query_arr_fmt_t", `FORMAT('%t', JSON_QUERY($1, '$.a'))`, []string{`'{"a":[1,{"b":2}]}'`}, `[1,{"b":2}]`},
		{"js_query_arr_to_json", `TO_JSON_STRING(TO_JSON(JSON_QUERY($1, '$.a')))`, []string{`'{"a":[1,{"b":2}]}'`}, `"[1,{\"b\":2}]"`},
		{"js_extract_num_json", `TO_JSON_STRING(JSON_EXTRACT($1, '$.a'))`, []string{`'{"a":6}'`}, `"6"`},
		{"js_extract_num_json_struct", `TO_JSON_STRING(STRUCT(JSON_EXTRACT($1, '$.a') AS v))`, []string{`'{"a":6}'`}, `{"v":"6"}`},
		{"js_extract_num_fmt_T", `FORMAT('%T', JSON_EXTRACT($1, '$.a'))`, []string{`'{"a":6}'`}, `"6"`},
		{"js_extract_num_fmt_t", `FORMAT('%t', JSON_EXTRACT($1, '$.a'))`, []string{`'{"a":6}'`}, `6`},
		{"js_extract_num_to_json", `TO_JSON_STRING(TO_JSON(JSON_EXTRACT($1, '$.a')))`, []string{`'{"a":6}'`}, `"6"`},
		{"js_extract_null_json", `TO_JSON_STRING(JSON_EXTRACT($1, '$.a'))`, []string{`'{"a":null}'`}, `null`},
		{"js_extract_null_json_struct", `TO_JSON_STRING(STRUCT(JSON_EXTRACT($1, '$.a') AS v))`, []string{`'{"a":null}'`}, `{"v":null}`},
		{"js_extract_null_fmt_T", `FORMAT('%T', JSON_EXTRACT($1, '$.a'))`, []string{`'{"a":null}'`}, `NULL`},
		{"js_extract_null_fmt_t", `FORMAT('%t', JSON_EXTRACT($1, '$.a'))`, []string{`'{"a":null}'`}, `NULL`},
		{"js_extract_null_to_json", `TO_JSON_STRING(TO_JSON(JSON_EXTRACT($1, '$.a')))`, []string{`'{"a":null}'`}, `null`},
		{"js_extract_array_json", `TO_JSON_STRING(JSON_EXTRACT_ARRAY($1))`, []string{`'[1,{"b":2},"c"]'`}, `["1","{\"b\":2}","\"c\""]`},
		{"js_extract_array_json_struct", `TO_JSON_STRING(STRUCT(JSON_EXTRACT_ARRAY($1) AS v))`, []string{`'[1,{"b":2},"c"]'`}, `{"v":["1","{\"b\":2}","\"c\""]}`},
		{"js_extract_array_fmt_T", `FORMAT('%T', JSON_EXTRACT_ARRAY($1))`, []string{`'[1,{"b":2},"c"]'`}, `["1", '{"b":2}', '"c"']`},
		{"js_extract_array_fmt_t", `FORMAT('%t', JSON_EXTRACT_ARRAY($1))`, []string{`'[1,{"b":2},"c"]'`}, `[1, {"b":2}, "c"]`},
		{"js_extract_array_to_json", `TO_JSON_STRING(TO_JSON(JSON_EXTRACT_ARRAY($1)))`, []string{`'[1,{"b":2},"c"]'`}, `["1","{\"b\":2}","\"c\""]`},
		{"js_query_array_json", `TO_JSON_STRING(JSON_QUERY_ARRAY($1))`, []string{`'[1,{"b":2},"c"]'`}, `["1","{\"b\":2}","\"c\""]`},
		{"js_query_array_json_struct", `TO_JSON_STRING(STRUCT(JSON_QUERY_ARRAY($1) AS v))`, []string{`'[1,{"b":2},"c"]'`}, `{"v":["1","{\"b\":2}","\"c\""]}`},
		{"js_query_array_fmt_T", `FORMAT('%T', JSON_QUERY_ARRAY($1))`, []string{`'[1,{"b":2},"c"]'`}, `["1", '{"b":2}', '"c"']`},
		{"js_query_array_fmt_t", `FORMAT('%t', JSON_QUERY_ARRAY($1))`, []string{`'[1,{"b":2},"c"]'`}, `[1, {"b":2}, "c"]`},
		{"js_query_array_to_json", `TO_JSON_STRING(TO_JSON(JSON_QUERY_ARRAY($1)))`, []string{`'[1,{"b":2},"c"]'`}, `["1","{\"b\":2}","\"c\""]`},
		{"js_plain_json", `TO_JSON_STRING($1)`, []string{`'{"a":1}'`}, `"{\"a\":1}"`},
		{"js_plain_json_struct", `TO_JSON_STRING(STRUCT($1 AS v))`, []string{`'{"a":1}'`}, `{"v":"{\"a\":1}"}`},
		{"js_plain_fmt_T", `FORMAT('%T', $1)`, []string{`'{"a":1}'`}, `'{"a":1}'`},
		{"js_plain_fmt_t", `FORMAT('%t', $1)`, []string{`'{"a":1}'`}, `{"a":1}`},
		{"js_plain_to_json", `TO_JSON_STRING(TO_JSON($1))`, []string{`'{"a":1}'`}, `"{\"a\":1}"`},
	}

	// Reference-docs examples whose values already agreed with BigQuery
	// but whose TO_JSON_STRING text did not (docs/docs_examples_results.md);
	// the want is BigQuery's text from the 2026-09-25 verification run.
	wholes := []struct {
		name  string
		query string
		want  string
	}{
		{"conversion_functions#9", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT input, CAST(input AS INTERVAL) AS output
FROM UNNEST([
  '1-2 3 10:20:30.456',
  '1-2',
  '10:20:30',
  'P1Y2M3D',
  'PT10H20M30,456S'
]) input
)))`, `[{"input":"1-2 3 10:20:30.456","output":"P1Y2M3DT10H20M30.456S"},{"input":"1-2","output":"P1Y2M"},{"input":"10:20:30","output":"PT10H20M30S"},{"input":"P1Y2M3D","output":"P1Y2M3D"},{"input":"PT10H20M30,456S","output":"PT10H20M30.456S"}]`},
		{"conversion_functions#13", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT CAST(
  '[2020-01-01, 2020-01-02)'
  AS RANGE<DATE>) AS string_to_range
)))`, `[{"string_to_range":{"start":"2020-01-01","end":"2020-01-02"}}]`},
		{"conversion_functions#14", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT CAST(
  '[2014-09-27 12:30:00.45, 2016-10-17 11:15:00.33)'
  AS RANGE<DATETIME>) AS string_to_range
)))`, `[{"string_to_range":{"start":"2014-09-27T12:30:00.450","end":"2016-10-17T11:15:00.330"}}]`},
		{"conversion_functions#16", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT CAST(
  '[UNBOUNDED, 2020-01-02)'
  AS RANGE<DATE>) AS string_to_range
)))`, `[{"string_to_range":{"start":null,"end":"2020-01-02"}}]`},
		{"conversion_functions#17", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT CAST(
  '[2020-01-01, NULL)'
  AS RANGE<DATE>) AS string_to_range
)))`, `[{"string_to_range":{"start":"2020-01-01","end":null}}]`},
		{"data-types#9", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  ST_GEOGFROMTEXT('MULTIPOINT(1 1, 2 2)') AS a,
  ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(1 1), POINT(2 2))') AS b
)))`, `[{"a":"MULTIPOINT(1 1, 2 2)","b":"MULTIPOINT(1 1, 2 2)"}]`},
		{"geography_functions#9", "SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (\nSELECT\n  ST_GEOGPOINT(i, i) AS p,\n  ST_CONTAINS(ST_GEOGFROMTEXT('POLYGON((1 1, 20 1, 10 20, 1 1))'),\n              ST_GEOGPOINT(i, i)) AS `contains`\nFROM UNNEST([0, 1, 10]) AS i\n)))", `[{"p":"POINT(0 0)","contains":false},{"p":"POINT(1 1)","contains":false},{"p":"POINT(10 10)","contains":true}]`},
		{"geography_functions#16", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT ST_ENDPOINT(ST_GEOGFROMTEXT('LINESTRING(1 1, 2 1, 3 2, 3 3)')) last
)))`, `[{"last":"POINT(3 3)"}]`},
		{"geography_functions#20", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT ST_GEOGFROM(FROM_HEX('010100000000000000000000400000000000001040')) AS WKB_format
)))`, `[{"WKB_format":"POINT(2 4)"}]`},
		{"geography_functions#21", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT ST_GEOGFROM('010100000000000000000000400000000000001040') AS WKB_format
)))`, `[{"WKB_format":"POINT(2 4)"}]`},
		{"geography_functions#22", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT ST_GEOGFROM(
  '{ "type": "Polygon", "coordinates": [ [ [2, 0], [2, 2], [1, 2], [0, 2], [0, 0], [2, 0] ] ] }'
) AS GEOJSON_format
)))`, `[{"GEOJSON_format":"POLYGON((2 0, 2 2, 1 2, 0 2, 0 0, 2 0))"}]`},
		{"geography_functions#27", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
WITH example AS(
  SELECT ST_GEOGFROMTEXT('POINT(0 1)') AS geography
  UNION ALL
  SELECT ST_GEOGFROMTEXT('MULTILINESTRING((2 2, 3 4), (5 6, 7 7))')
  UNION ALL
  SELECT ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))')
  UNION ALL
  SELECT ST_GEOGFROMTEXT('GEOMETRYCOLLECTION EMPTY'))
SELECT
  geography AS WKT,
  ST_GEOMETRYTYPE(geography) AS geometry_type_name
FROM example
)))`, `[{"WKT":"POINT(0 1)","geometry_type_name":"ST_Point"},{"WKT":"MULTILINESTRING((2 2, 3 4), (5 6, 7 7))","geometry_type_name":"ST_MultiLineString"},{"WKT":"GEOMETRYCOLLECTION(MULTIPOINT(-1 2, 0 12), LINESTRING(-2 4, 0 6))","geometry_type_name":"ST_GeometryCollection"},{"WKT":"GEOMETRYCOLLECTION EMPTY","geometry_type_name":"ST_GeometryCollection"}]`},
		{"geography_functions#33", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT p, ST_INTERSECTSBOX(p, -90, 0, 90, 20) AS box1,
       ST_INTERSECTSBOX(p, 90, 0, -90, 20) AS box2
FROM UNNEST([ST_GEOGPOINT(10, 10), ST_GEOGPOINT(170, 10),
             ST_GEOGPOINT(30, 30)]) p
)))`, `[{"p":"POINT(10 10)","box1":true,"box2":false},{"p":"POINT(170 10)","box1":false,"box2":true},{"p":"POINT(30 30)","box1":false,"box2":false}]`},
		{"geography_functions#34", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
WITH example AS(
  SELECT ST_GEOGFROMTEXT('POINT(5 0)') AS geography
  UNION ALL
  SELECT ST_GEOGFROMTEXT('LINESTRING(0 1, 4 3, 2 6, 0 1)') AS geography
  UNION ALL
  SELECT ST_GEOGFROMTEXT('LINESTRING(2 6, 1 3, 3 9)') AS geography
  UNION ALL
  SELECT ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))') AS geography
  UNION ALL
  SELECT ST_GEOGFROMTEXT('GEOMETRYCOLLECTION EMPTY'))
SELECT
  geography,
  ST_ISCLOSED(geography) AS is_closed,
FROM example
)))`, `[{"geography":"POINT(5 0)","is_closed":true},{"geography":"LINESTRING(0 1, 4 3, 2 6, 0 1)","is_closed":true},{"geography":"LINESTRING(2 6, 1 3, 3 9)","is_closed":false},{"geography":"GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))","is_closed":false},{"geography":"GEOMETRYCOLLECTION EMPTY","is_closed":false}]`},
		{"geography_functions#35", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
WITH fractions AS (
    SELECT 0 AS fraction UNION ALL
    SELECT 0.5 UNION ALL
    SELECT 1 UNION ALL
    SELECT NULL
  )
SELECT
  fraction,
  ST_LINEINTERPOLATEPOINT(ST_GEOGFROMTEXT('LINESTRING(1 1, 5 5)'), fraction)
    AS point
FROM fractions
)))`, `[{"fraction":0,"point":"POINT(1 1)"},{"fraction":0.5,"point":"POINT(2.99633827268976 3.00182528336078)"},{"fraction":1,"point":"POINT(5 5)"},{"fraction":null,"point":null}]`},
		{"geography_functions#40", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
WITH example AS(
  SELECT ST_GEOGFROMTEXT('POINT(5 0)') AS geography
  UNION ALL
  SELECT ST_GEOGFROMTEXT('MULTIPOINT(0 1, 4 3, 2 6)') AS geography
  UNION ALL
  SELECT ST_GEOGFROMTEXT('GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))') AS geography
  UNION ALL
  SELECT ST_GEOGFROMTEXT('GEOMETRYCOLLECTION EMPTY'))
SELECT
  geography,
  ST_NUMGEOMETRIES(geography) AS num_geometries,
FROM example
)))`, `[{"geography":"POINT(5 0)","num_geometries":1},{"geography":"MULTIPOINT(0 1, 4 3, 2 6)","num_geometries":3},{"geography":"GEOMETRYCOLLECTION(POINT(0 0), LINESTRING(1 2, 2 1))","num_geometries":2},{"geography":"GEOMETRYCOLLECTION EMPTY","num_geometries":0}]`},
		{"geography_functions#41", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
WITH linestring AS (
    SELECT ST_GEOGFROMTEXT('LINESTRING(1 1, 2 1, 3 2, 3 3)') g
)
SELECT ST_POINTN(g, 1) AS first, ST_POINTN(g, -1) AS last,
    ST_POINTN(g, 2) AS second, ST_POINTN(g, -2) AS second_to_last
FROM linestring
)))`, `[{"first":"POINT(1 1)","last":"POINT(3 3)","second":"POINT(2 1)","second_to_last":"POINT(3 2)"}]`},
		{"geography_functions#42", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
WITH example AS
 (SELECT ST_GEOGFROMTEXT('LINESTRING(0 0, 0.05 0, 0.1 0, 0.15 0, 2 0)') AS line)
SELECT
   line AS original_line,
   ST_SIMPLIFY(line, 1) AS simplified_line
FROM example
)))`, `[{"original_line":"LINESTRING(0 0, 0.05 0, 0.1 0, 0.15 0, 2 0)","simplified_line":"LINESTRING(0 0, 2 0)"}]`},
		{"geography_functions#44", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT ST_STARTPOINT(ST_GEOGFROMTEXT('LINESTRING(1 1, 2 1, 3 2, 3 3)')) first
)))`, `[{"first":"POINT(1 1)"}]`},
		{"interval_functions#4", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  JUSTIFY_DAYS(INTERVAL 29 DAY) AS i1,
  JUSTIFY_DAYS(INTERVAL -30 DAY) AS i2,
  JUSTIFY_DAYS(INTERVAL 31 DAY) AS i3,
  JUSTIFY_DAYS(INTERVAL -65 DAY) AS i4,
  JUSTIFY_DAYS(INTERVAL 370 DAY) AS i5
)))`, `[{"i1":"P29D","i2":"P-1M","i3":"P1M1D","i4":"P-2M-5D","i5":"P1Y10D"}]`},
		{"interval_functions#5", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  JUSTIFY_HOURS(INTERVAL 23 HOUR) AS i1,
  JUSTIFY_HOURS(INTERVAL -24 HOUR) AS i2,
  JUSTIFY_HOURS(INTERVAL 47 HOUR) AS i3,
  JUSTIFY_HOURS(INTERVAL -12345 MINUTE) AS i4
)))`, `[{"i1":"PT23H","i2":"P-1D","i3":"P1DT23H","i4":"P-8DT-13H-45M"}]`},
		{"interval_functions#6", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JUSTIFY_INTERVAL(INTERVAL '29 49:00:00' DAY TO SECOND) AS i
)))`, `[{"i":"P1M1DT1H"}]`},
		{"interval_functions#7", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  MAKE_INTERVAL(1, 6, 15) AS i1,
  MAKE_INTERVAL(hour => 10, second => 20) AS i2,
  MAKE_INTERVAL(1, minute => 5, day => 2) AS i3
)))`, `[{"i1":"P1Y6M15D","i2":"PT10H20S","i3":"P1Y2DT5M"}]`},
		{"json_functions#90", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_EXTRACT(
  '{"class": {"students": [{"name": "Jane"}]}}',
  '$') AS json_text_string
)))`, `[{"json_text_string":"{\"class\":{\"students\":[{\"name\":\"Jane\"}]}}"}]`},
		{"json_functions#91", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_EXTRACT(
  '{"class": {"students": []}}',
  '$') AS json_text_string
)))`, `[{"json_text_string":"{\"class\":{\"students\":[]}}"}]`},
		{"json_functions#92", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_EXTRACT(
  '{"class": {"students": [{"name": "John"}, {"name": "Jamie"}]}}',
  '$') AS json_text_string
)))`, `[{"json_text_string":"{\"class\":{\"students\":[{\"name\":\"John\"},{\"name\":\"Jamie\"}]}}"}]`},
		{"json_functions#93", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_EXTRACT(
  '{"class": {"students": [{"name": "Jane"}]}}',
  '$.class.students[0]') AS first_student
)))`, `[{"first_student":"{\"name\":\"Jane\"}"}]`},
		{"json_functions#95", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_EXTRACT(
  '{"class": {"students": [{"name": "John"}, {"name": "Jamie"}]}}',
  '$.class.students[0]') AS first_student
)))`, `[{"first_student":"{\"name\":\"John\"}"}]`},
		{"json_functions#99", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_EXTRACT(
  '{"class": {"students": [{"name": "John"}, {"name": "Jamie"}]}}',
  '$.class.students[1].name') AS second_student
)))`, `[{"second_student":"\"Jamie\""}]`},
		{"json_functions#100", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_EXTRACT(
  '{"class": {"students": [{"name": "Jane"}]}}',
  "$.class['students']") AS student_names
)))`, `[{"student_names":"[{\"name\":\"Jane\"}]"}]`},
		{"json_functions#101", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_EXTRACT(
  '{"class": {"students": []}}',
  "$.class['students']") AS student_names
)))`, `[{"student_names":"[]"}]`},
		{"json_functions#102", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_EXTRACT(
  '{"class": {"students": [{"name": "John"}, {"name": "Jamie"}]}}',
  "$.class['students']") AS student_names
)))`, `[{"student_names":"[{\"name\":\"John\"},{\"name\":\"Jamie\"}]"}]`},
		{"json_functions#115", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_EXTRACT('{"name": "Jakob", "age": "6" }', '$.name') AS json_name,
  JSON_EXTRACT_SCALAR('{"name": "Jakob", "age": "6" }', '$.name') AS scalar_name,
  JSON_EXTRACT('{"name": "Jakob", "age": "6" }', '$.age') AS json_age,
  JSON_EXTRACT_SCALAR('{"name": "Jakob", "age": "6" }', '$.age') AS scalar_age
)))`, `[{"json_name":"\"Jakob\"","scalar_name":"Jakob","json_age":"\"6\"","scalar_age":"6"}]`},
		{"json_functions#116", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_EXTRACT('{"fruits": ["apple", "banana"]}', '$.fruits') AS json_extract,
  JSON_EXTRACT_SCALAR('{"fruits": ["apple", "banana"]}', '$.fruits') AS json_extract_scalar
)))`, `[{"json_extract":"[\"apple\",\"banana\"]","json_extract_scalar":null}]`},
		{"json_functions#155", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  JSON_QUERY('{"class": {"students": [{"name": "Jane"}]}}', '$') AS json_text_string
)))`, `[{"json_text_string":"{\"class\":{\"students\":[{\"name\":\"Jane\"}]}}"}]`},
		{"json_functions#156", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_QUERY('{"class": {"students": []}}', '$') AS json_text_string
)))`, `[{"json_text_string":"{\"class\":{\"students\":[]}}"}]`},
		{"json_functions#157", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  JSON_QUERY(
    '{"class": {"students": [{"name": "John"},{"name": "Jamie"}]}}',
    '$') AS json_text_string
)))`, `[{"json_text_string":"{\"class\":{\"students\":[{\"name\":\"John\"},{\"name\":\"Jamie\"}]}}"}]`},
		{"json_functions#158", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  JSON_QUERY(
    '{"class": {"students": [{"name": "Jane"}]}}',
    '$.class.students[0]') AS first_student
)))`, `[{"first_student":"{\"name\":\"Jane\"}"}]`},
		{"json_functions#160", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  JSON_QUERY(
    '{"class": {"students": [{"name": "John"}, {"name": "Jamie"}]}}',
    '$.class.students[0]') AS first_student
)))`, `[{"first_student":"{\"name\":\"John\"}"}]`},
		{"json_functions#164", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  JSON_QUERY(
    '{"class": {"students": [{"name": "John"}, {"name": "Jamie"}]}}',
    '$.class.students[1].name') AS second_student
)))`, `[{"second_student":"\"Jamie\""}]`},
		{"json_functions#165", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  JSON_QUERY(
    '{"class": {"students": [{"name": "Jane"}]}}',
    '$.class."students"') AS student_names
)))`, `[{"student_names":"[{\"name\":\"Jane\"}]"}]`},
		{"json_functions#166", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  JSON_QUERY(
    '{"class": {"students": []}}',
    '$.class."students"') AS student_names
)))`, `[{"student_names":"[]"}]`},
		{"json_functions#167", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  JSON_QUERY(
    '{"class": {"students": [{"name": "John"}, {"name": "Jamie"}]}}',
    '$.class."students"') AS student_names
)))`, `[{"student_names":"[{\"name\":\"John\"},{\"name\":\"Jamie\"}]"}]`},
		{"json_functions#216", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_QUERY('{"name": "Jakob", "age": "6"}', '$.name') AS json_name,
  JSON_VALUE('{"name": "Jakob", "age": "6"}', '$.name') AS scalar_name,
  JSON_QUERY('{"name": "Jakob", "age": "6"}', '$.age') AS json_age,
  JSON_VALUE('{"name": "Jakob", "age": "6"}', '$.age') AS scalar_age
)))`, `[{"json_name":"\"Jakob\"","scalar_name":"Jakob","json_age":"\"6\"","scalar_age":"6"}]`},
		{"json_functions#217", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT JSON_QUERY('{"fruits": ["apple", "banana"]}', '$.fruits') AS json_query,
  JSON_VALUE('{"fruits": ["apple", "banana"]}', '$.fruits') AS json_value
)))`, `[{"json_query":"[\"apple\",\"banana\"]","json_value":null}]`},
		{"operators#33", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  DATE "2021-05-20" - DATE "2020-04-19" AS date_diff,
  TIMESTAMP "2021-06-01 12:34:56.789" - TIMESTAMP "2021-05-31 00:00:00" AS time_diff
)))`, `[{"date_diff":"P396D","time_diff":"PT36H34M56.789S"}]`},
		{"operators#35", `SELECT TO_JSON_STRING(ARRAY(SELECT AS STRUCT * FROM (
SELECT
  INTERVAL '1:2:3' HOUR TO SECOND * 10 AS mul1,
  INTERVAL 35 SECOND * 4 AS mul2,
  INTERVAL 10 YEAR / 3 AS div1,
  INTERVAL 1 MONTH / 12 AS div2
)))`, `[{"mul1":"PT10H20M30S","mul2":"PT2M20S","div1":"P3Y4M","div2":"P2DT12H"}]`},
	}

	query := func(t *testing.T, q string) string {
		t.Helper()
		var got sql.NullString
		if err := db.QueryRowContext(context.Background(), q).Scan(&got); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if !got.Valid {
			t.Fatalf("%s: NULL", q)
		}
		return got.String
	}
	for _, c := range scalars {
		direct := c.expr
		field := c.expr
		var fields []string
		for i, a := range c.args {
			ph := fmt.Sprintf("$%d", i+1)
			direct = strings.ReplaceAll(direct, ph, a)
			field = strings.ReplaceAll(field, ph, fmt.Sprintf("s.a%d", i+1))
			fields = append(fields, fmt.Sprintf("%s AS a%d", a, i+1))
		}
		t.Run(c.name+"/literal", func(t *testing.T) {
			q := "SELECT " + direct
			if got := query(t, q); got != c.want {
				t.Errorf("%s\n got: %q\nwant: %q", q, got, c.want)
			}
		})
		t.Run(c.name+"/column", func(t *testing.T) {
			q := fmt.Sprintf("SELECT %s FROM (SELECT STRUCT(%s) AS s)", field, strings.Join(fields, ", "))
			if got := query(t, q); got != c.want {
				t.Errorf("%s\n got: %q\nwant: %q", q, got, c.want)
			}
		})
	}
	for _, c := range wholes {
		t.Run(c.name, func(t *testing.T) {
			if got := query(t, c.query); got != c.want {
				t.Errorf("%s\n got: %q\nwant: %q", c.query, got, c.want)
			}
		})
	}
}
