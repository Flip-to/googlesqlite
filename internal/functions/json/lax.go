package json

import (
	"math"
	"strconv"
	"strings"

	"github.com/goccy/go-json"
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// LAX_* are the forgiving-cast variants of JSON value-extraction
// functions: rather than raising on type mismatch, they return NULL.
// Per the BigQuery doc, LAX_* applied to a JSON `null` returns NULL,
// and applied to a value of a different concrete JSON type returns
// NULL.
//
// Each variant accepts a single JSON-typed argument. The analyzer
// admits these once `LanguageFeatureFeatureJsonLaxValueExtractionFunctions`
// is enabled (see internal/analyzer.go newAnalyzerOptions).

// jsonScalarFromValue decodes a JSON-typed Value into a primitive Go
// scalar (int64, float64, bool, string) plus a flag indicating
// whether the JSON was a primitive at all. Objects, arrays, and the
// JSON literal `null` return ok=false.
func jsonScalarFromValue(v value.Value) (any, bool) {
	jv, ok := v.(value.JsonValue)
	if !ok {
		return nil, false
	}
	body := string(jv)
	if body == "null" || body == "" {
		return nil, false
	}
	var raw any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, false
	}
	return raw, raw != nil
}

func laxInt64(raw any) (value.Value, bool) {
	switch v := raw.(type) {
	case bool:
		if v {
			return value.IntValue(1), true
		}
		return value.IntValue(0), true
	case float64:
		return roundToInt64(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return value.IntValue(i), true
		}
		if f, err := v.Float64(); err == nil {
			return roundToInt64(f)
		}
	case string:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return value.IntValue(i), true
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return roundToInt64(f)
		}
	}
	return nil, false
}

// roundToInt64 rounds half away from zero, as LAX_INT64 does
// (LAX_INT64(JSON '3.5') is 4, LAX_INT64(JSON '"+1.5"') is 2), and
// reports false when the result is outside the INT64 range
// (LAX_INT64(JSON '1e100') is NULL). json_functions.md, LAX_INT64;
// both verified on BigQuery.
func roundToInt64(f float64) (value.Value, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, false
	}
	r := math.Round(f)
	if r < math.MinInt64 || r >= math.MaxInt64 {
		return nil, false
	}
	return value.IntValue(int64(r)), true
}

// laxFloat64 converts JSON numbers and numeric strings. A JSON boolean
// is not converted: LAX_FLOAT64(JSON 'true') is NULL
// (json_functions.md, LAX_FLOAT64; verified on BigQuery).
func laxFloat64(raw any) (value.Value, bool) {
	switch v := raw.(type) {
	case float64:
		return value.FloatValue(v), true
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return value.FloatValue(f), true
		}
	case string:
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return value.FloatValue(f), true
		}
	}
	return nil, false
}

func laxBool(raw any) (value.Value, bool) {
	switch v := raw.(type) {
	case bool:
		return value.BoolValue(v), true
	case float64:
		return value.BoolValue(v != 0), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return value.BoolValue(i != 0), true
		}
	case string:
		// Only "true" and "false" convert, case-insensitively: "TRue"
		// is true and "1" is NULL (json_functions.md, LAX_BOOL /
		// LAX_BOOL_ARRAY; verified on BigQuery).
		switch {
		case strings.EqualFold(v, "true"):
			return value.BoolValue(true), true
		case strings.EqualFold(v, "false"):
			return value.BoolValue(false), true
		}
	}
	return nil, false
}

func laxString(raw any) (value.Value, bool) {
	switch v := raw.(type) {
	case string:
		return value.StringValue(v), true
	case bool:
		if v {
			return value.StringValue("true"), true
		}
		return value.StringValue("false"), true
	case float64:
		return value.StringValue(strconv.FormatFloat(v, 'g', -1, 64)), true
	case json.Number:
		return value.StringValue(string(v)), true
	}
	return nil, false
}

func bindLax(args []value.Value, conv func(any) (value.Value, bool)) (value.Value, error) {
	if helper.ExistsNull(args) {
		return nil, nil
	}
	if len(args) != 1 {
		return nil, nil
	}
	raw, ok := jsonScalarFromValue(args[0])
	if !ok {
		return nil, nil
	}
	out, ok := conv(raw)
	if !ok {
		return nil, nil
	}
	return out, nil
}

func BindLaxInt64(args ...value.Value) (value.Value, error) {
	return bindLax(args, laxInt64)
}

func BindLaxFloat64(args ...value.Value) (value.Value, error) {
	return bindLax(args, laxFloat64)
}

func BindLaxBool(args ...value.Value) (value.Value, error) {
	return bindLax(args, laxBool)
}

func BindLaxString(args ...value.Value) (value.Value, error) {
	return bindLax(args, laxString)
}
