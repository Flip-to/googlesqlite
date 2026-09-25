package internal

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/goccy/go-json"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/functions/window"
	"github.com/goccy/googlesqlite/internal/value"
)

type SQLiteFunction func(...any) (any, error)
type BindFunction func(...value.Value) (value.Value, error)
type aggregateBindFunction func() func() *helper.Aggregator
type windowBindFunction func() func() *window.WindowAggregator

type funcInfo struct {
	Name     string
	BindFunc BindFunction
	// NonDeterministic, when true, registers the underlying SQLite
	// function without the SQLITE_DETERMINISTIC flag. SQLite is then
	// required to re-evaluate the call for every row, instead of
	// constant-folding it. Required for functions like GENERATE_UUID
	// or CURRENT_TIMESTAMP whose result must vary per call.
	NonDeterministic bool
}

type aggregateFuncInfo struct {
	Name     string
	BindFunc aggregateBindFunction
}

type windowFuncInfo struct {
	Name     string
	BindFunc windowBindFunction
}

// BIT_CAST_TO_* are wired through Scalar1 in normalFuncs; no
// per-function bindXxx helper is needed.

// bindBool / bindInt64 are identity passthroughs: they must observe a
// NULL argument and return it unchanged, so they use Scalar1KeepNull
// (arity check only, no NULL short-circuit).
var bindBool = helper.Scalar1KeepNull(func(v value.Value) (value.Value, error) {
	jv, ok := v.(value.JsonValue)
	if !ok {
		return v, nil
	}
	// BOOL(json_expr): only a JSON boolean converts; anything else,
	// including JSON null, is an error (json_functions.md, BOOL).
	switch strings.TrimSpace(string(jv)) {
	case "true":
		return value.BoolValue(true), nil
	case "false":
		return value.BoolValue(false), nil
	}
	return nil, fmt.Errorf("The provided JSON input is not a boolean")
})

var bindInt64 = helper.Scalar1KeepNull(func(v value.Value) (value.Value, error) {
	jv, ok := v.(value.JsonValue)
	if !ok {
		return v, nil
	}
	// INT64(json_expr): a JSON number with a zero fractional part
	// (e.g. 10.0) converts; anything else is an error
	// (json_functions.md, INT64). That includes JSON null: INT64(JSON
	// 'null') raises "The provided JSON input is not an integer" on
	// BigQuery, as the docs say.
	body := strings.TrimSpace(string(jv))
	r, ok := new(big.Rat).SetString(body)
	if !ok || body == "" || body[0] == '"' {
		return nil, fmt.Errorf("The provided JSON input is not an integer")
	}
	if !r.IsInt() || !r.Num().IsInt64() {
		return nil, fmt.Errorf("The provided JSON number: %s cannot be converted to an integer", body)
	}
	return value.IntValue(r.Num().Int64()), nil
})

// bindDouble implements `FLOAT64(json_expr[, wide_number_mode])`.
// Strict: errors on non-numeric JSON. wide_number_mode controls
// rounding behaviour for numbers that don't fit FLOAT64 exactly;
// 'exact' raises, 'round' tolerates the precision loss. Default is
// 'round' per the BigQuery spec.
func bindDouble(args ...value.Value) (value.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("FLOAT64: invalid number of arguments: got %d, want between 1 and 2", len(args))
	}
	if args[0] == nil {
		return nil, nil
	}
	mode := "round"
	if len(args) == 2 && args[1] != nil {
		s, err := args[1].ToString()
		if err != nil {
			return nil, err
		}
		switch s {
		case "exact", "round":
			mode = s
		default:
			return nil, fmt.Errorf("FLOAT64: unexpected wide_number_mode: %s", s)
		}
	}
	jv, ok := args[0].(value.JsonValue)
	if !ok {
		// Already-numeric pass-through: legacy callers feed FLOAT64
		// to coerce non-JSON values; let the conversion happen.
		f, err := args[0].ToFloat64()
		if err != nil {
			return nil, err
		}
		return value.FloatValue(f), nil
	}
	body := strings.TrimSpace(string(jv))
	if body == "" {
		return nil, nil
	}
	if body == "null" {
		// FLOAT64(JSON 'null') is an error, like INT64 and BOOL on a
		// JSON null (json_functions.md, FLOAT64; verified on BigQuery).
		return nil, fmt.Errorf("The provided JSON input is not a number") //nolint:staticcheck // BigQuery's error text
	}
	if body[0] == '"' || body[0] == '{' || body[0] == '[' || body == "true" || body == "false" {
		return nil, fmt.Errorf("FLOAT64: JSON value is not a number")
	}
	f, err := strconv.ParseFloat(body, 64)
	if err != nil {
		return nil, fmt.Errorf("FLOAT64: failed to parse JSON number %q: %w", body, err)
	}
	if mode == "exact" {
		// Re-serialise and compare; round-trip mismatch means we
		// lost precision.
		exact, ok := new(big.Rat).SetString(body)
		if !ok || new(big.Rat).SetFloat64(f).Cmp(exact) != 0 {
			return nil, fmt.Errorf("FLOAT64: number %q cannot be represented as FLOAT64 without loss", body)
		}
	}
	return value.FloatValue(f), nil
}

func bindDistinct(args ...value.Value) (value.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("DISTINCT: invalid number of arguments: got %d, want 0", len(args))
	}
	return helper.DISTINCT()
}

func bindHaving(args ...value.Value) (value.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("HAVING: invalid number of arguments: got %d, want 2", len(args))
	}
	isMax, err := args[1].ToBool()
	if err != nil {
		return nil, err
	}
	return helper.HAVING(args[0], isMax)
}

func bindIgnoreNulls(args ...value.Value) (value.Value, error) {
	if len(args) != 0 {
		return nil, fmt.Errorf("IGNORE_NULLS: invalid number of arguments: got %d, want 0", len(args))
	}
	return helper.IGNORE_NULLS()
}

// bindOrderBy and bindLimit build the ORDER BY / LIMIT aggregate
// option markers. Most aggregates reach SQLite after the analyzer's
// ORDER BY / LIMIT rewrite, but MATCH_RECOGNIZE measures are not
// rewritten, so their aggregates carry the markers directly.
func bindOrderBy(args ...value.Value) (value.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("ORDER_BY: invalid number of arguments: got %d, want 2", len(args))
	}
	isAsc, err := args[1].ToBool()
	if err != nil {
		return nil, err
	}
	return helper.ORDER_BY(args[0], isAsc)
}

func bindLimit(args ...value.Value) (value.Value, error) {
	if len(args) != 1 || args[0] == nil {
		return nil, fmt.Errorf("LIMIT: invalid argument")
	}
	n, err := args[0].ToInt64()
	if err != nil {
		return nil, err
	}
	return helper.LIMIT(n)
}

var bindWindowRowID = helper.Scalar1(func(v value.Value) (value.Value, error) {
	a0, err := v.ToInt64()
	if err != nil {
		return nil, err
	}
	return window.WINDOW_ROWID(a0)
})

func bindEvalJavaScript(args ...value.Value) (value.Value, error) {
	code, err := args[0].ToString()
	if err != nil {
		return nil, err
	}
	encodedType, err := args[1].ToString()
	if err != nil {
		return nil, err
	}
	var typ Type
	if err := json.Unmarshal([]byte(encodedType), &typ); err != nil {
		return nil, fmt.Errorf("EVAL_JAVASCRIPT: failed to decode type information from %s: %w", encodedType, err)
	}
	if len(args) == 2 {
		return EVAL_JAVASCRIPT(code, &typ, nil, nil)
	}
	argNames, err := args[2].ToArray()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(argNames.Values))
	for _, val := range argNames.Values {
		name, err := val.ToString()
		if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return EVAL_JAVASCRIPT(code, &typ, names, args[3:])
}
