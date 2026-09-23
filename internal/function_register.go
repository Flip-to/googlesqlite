package internal

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/goccy/go-json"
	sqlite3 "github.com/ncruces/go-sqlite3"

	"github.com/goccy/googlesqlite/internal/functions/window"
	"github.com/goccy/googlesqlite/internal/sqlitex"
	"github.com/goccy/googlesqlite/internal/value"
)

type nameAndFunc struct {
	Name string
	Func any
	// NonDeterministic, when true, drops SQLITE_DETERMINISTIC at
	// registration so SQLite re-evaluates the call per row.
	NonDeterministic bool
}

var (
	funcMapMu          sync.RWMutex
	registerFuncOnce   sync.Once
	normalFuncMap      = map[string][]*nameAndFunc{}
	aggregateFuncMap   = map[string][]*nameAndFunc{}
	windowFuncMap      = map[string][]*nameAndFunc{}
	currentTimeFuncMap = map[string]struct{}{
		"current_date":      {},
		"current_datetime":  {},
		"current_time":      {},
		"current_timestamp": {},
	}
)

func RegisterFunctions(conn *sqlite3.Conn) error {
	funcMapMu.RLock()
	defer funcMapMu.RUnlock()

	var onceErr error
	registerFuncOnce.Do(func() {
		for _, info := range normalFuncs {
			setupNormalFuncMap(info)
		}
		for _, info := range aggregateFuncs {
			setupAggregateFuncMap(info)
		}
		for _, info := range windowFuncs {
			setupWindowFuncMap(info)
		}
		// Replace the predecessor's per-row full-scan emulation for
		// the window forms of these aggregators with native
		// incremental implementations. The formatter emits these
		// through OVER syntax in customNativeWindowFuncMap, so SQLite
		// drives Step/Done over the active frame instead of the
		// O(N²) correlated subquery the predecessor required.
		windowFuncMap["array_agg"] = []*nameAndFunc{
			{Name: "googlesqlite_window_array_agg", Func: window.NewArrayAggWindowNative()},
		}
		windowFuncMap["string_agg"] = []*nameAndFunc{
			{Name: "googlesqlite_window_string_agg", Func: window.NewStringAggWindowNative()},
		}
		windowFuncMap["countif"] = []*nameAndFunc{
			{Name: "googlesqlite_window_countif", Func: window.NewCountifWindowNative()},
		}
		windowFuncMap["count_star"] = []*nameAndFunc{
			{Name: "googlesqlite_window_count_star", Func: window.NewCountStarWindowNative()},
		}
		windowFuncMap["any_value"] = []*nameAndFunc{
			{Name: "googlesqlite_window_any_value", Func: window.NewAnyValueWindowNative()},
		}
		windowFuncMap["corr"] = []*nameAndFunc{
			{Name: "googlesqlite_window_corr", Func: window.NewCorrWindowNative()},
		}
		windowFuncMap["covar_pop"] = []*nameAndFunc{
			{Name: "googlesqlite_window_covar_pop", Func: window.NewCovarPopWindowNative()},
		}
		windowFuncMap["covar_samp"] = []*nameAndFunc{
			{Name: "googlesqlite_window_covar_samp", Func: window.NewCovarSampWindowNative()},
		}
		windowFuncMap["stddev_pop"] = []*nameAndFunc{
			{Name: "googlesqlite_window_stddev_pop", Func: window.NewStddevPopWindowNative()},
		}
		windowFuncMap["stddev_samp"] = []*nameAndFunc{
			{Name: "googlesqlite_window_stddev_samp", Func: window.NewStddevSampWindowNative()},
		}
		windowFuncMap["var_pop"] = []*nameAndFunc{
			{Name: "googlesqlite_window_var_pop", Func: window.NewVarPopWindowNative()},
		}
		windowFuncMap["var_samp"] = []*nameAndFunc{
			{Name: "googlesqlite_window_var_samp", Func: window.NewVarSampWindowNative()},
		}
		// DISTINCT-aware window aggregators (SUM/COUNT/AVG). These
		// don't replace existing entries — they're in addition,
		// keyed under their custom registration names so they're
		// looked up by the formatter's distinctAwareNativeWindowFuncs
		// path rather than via predecessor name.
		// Typed SUM / AVG / MIN / MAX for DOUBLE and NUMERIC arguments;
		// see internal/functions/window/typed.go.
		windowFuncMap["first_value_ignore_nulls"] = []*nameAndFunc{
			{Name: "googlesqlite_window_first_value_ignore_nulls", Func: window.NewFirstValueIgnoreNullsWindowNative()},
		}
		windowFuncMap["last_value_ignore_nulls"] = []*nameAndFunc{
			{Name: "googlesqlite_window_last_value_ignore_nulls", Func: window.NewLastValueIgnoreNullsWindowNative()},
		}
		windowFuncMap["nth_value_ignore_nulls"] = []*nameAndFunc{
			{Name: "googlesqlite_window_nth_value_ignore_nulls", Func: window.NewNthValueIgnoreNullsWindowNative()},
		}
		windowFuncMap["sum_typed"] = []*nameAndFunc{
			{Name: "googlesqlite_window_typed_sum", Func: window.NewSumWindowNative()},
		}
		windowFuncMap["avg_typed"] = []*nameAndFunc{
			{Name: "googlesqlite_window_typed_avg", Func: window.NewAvgWindowNative()},
		}
		windowFuncMap["min_typed"] = []*nameAndFunc{
			{Name: "googlesqlite_window_typed_min", Func: window.NewMinWindowNative()},
		}
		windowFuncMap["max_typed"] = []*nameAndFunc{
			{Name: "googlesqlite_window_typed_max", Func: window.NewMaxWindowNative()},
		}
		windowFuncMap["sum_distinct"] = []*nameAndFunc{
			{Name: "googlesqlite_window_sum_distinct", Func: window.NewSumDistinctWindowNative()},
		}
		windowFuncMap["count_distinct"] = []*nameAndFunc{
			{Name: "googlesqlite_window_count_distinct", Func: window.NewCountDistinctWindowNative()},
		}
		windowFuncMap["avg_distinct"] = []*nameAndFunc{
			{Name: "googlesqlite_window_avg_distinct", Func: window.NewAvgDistinctWindowNative()},
		}
		windowFuncMap["percentile_cont"] = []*nameAndFunc{
			{Name: "googlesqlite_window_percentile_cont", Func: window.NewPercentileContWindowNative()},
		}
		windowFuncMap["percentile_disc"] = []*nameAndFunc{
			{Name: "googlesqlite_window_percentile_disc", Func: window.NewPercentileDiscWindowNative()},
		}
		// Native window forms for the boolean / bitwise aggregators
		// that previously fell through to the predecessor's
		// correlated-subquery emulation.
		windowFuncMap["logical_or"] = []*nameAndFunc{
			{Name: "googlesqlite_window_logical_or", Func: window.NewLogicalOrWindowNative()},
		}
		windowFuncMap["logical_and"] = []*nameAndFunc{
			{Name: "googlesqlite_window_logical_and", Func: window.NewLogicalAndWindowNative()},
		}
		windowFuncMap["bit_and"] = []*nameAndFunc{
			{Name: "googlesqlite_window_bit_and", Func: window.NewBitAndAggWindowNative()},
		}
		windowFuncMap["bit_or"] = []*nameAndFunc{
			{Name: "googlesqlite_window_bit_or", Func: window.NewBitOrAggWindowNative()},
		}
		windowFuncMap["bit_xor"] = []*nameAndFunc{
			{Name: "googlesqlite_window_bit_xor", Func: window.NewBitXorAggWindowNative()},
		}
		windowFuncMap["array_concat_agg"] = []*nameAndFunc{
			{Name: "googlesqlite_window_array_concat_agg", Func: window.NewArrayConcatAggWindowNative()},
		}
	})
	if onceErr != nil {
		return onceErr
	}

	deterministic := sqlitex.FunctionFlags{Deterministic: true}

	if err := sqlitex.RegisterFunc(conn, "googlesqlite_decode_array", func(v any) (string, error) {
		decoded, err := DecodeValue(v)
		if err != nil {
			return "", err
		}
		if decoded == nil {
			return "[]", nil
		}
		array, err := decoded.ToArray()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteByte('[')
		for i, elem := range array.Values {
			if i > 0 {
				b.WriteByte(',')
			}
			if f, ok := elem.(value.FloatValue); ok && math.IsInf(float64(f), 0) {
				if f > 0 {
					b.WriteString("9e999")
				} else {
					b.WriteString("-9e999")
				}
				continue
			}
			v, err := value.EncodeElement(elem)
			if err != nil {
				return "", err
			}
			e, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			b.Write(e)
		}
		b.WriteByte(']')
		return b.String(), nil
	}, deterministic); err != nil {
		return fmt.Errorf("failed to register decode_array function: %w", err)
	}

	// googlesqlite_order_class ranks NULL (0), NaN (1) and other values
	// (2) so DOUBLE keys order as GoogleSQL does; see floatOrderClassKey.
	if err := sqlitex.RegisterFunc(conn, "googlesqlite_order_class", func(v any) (any, error) {
		decoded, err := DecodeValue(v)
		if err != nil {
			return nil, err
		}
		if decoded == nil {
			return int64(0), nil
		}
		if f, ok := decoded.(value.FloatValue); ok && math.IsNaN(float64(f)) {
			return int64(1), nil
		}
		return int64(2), nil
	}, deterministic); err != nil {
		return fmt.Errorf("failed to register order_class function: %w", err)
	}

	if err := sqlitex.RegisterFunc(conn, "googlesqlite_group_by", func(v any) (any, error) {
		decoded, err := DecodeValue(v)
		if err != nil {
			return "", err
		}
		if decoded == nil {
			return nil, nil
		}
		switch decoded.(type) {
		case *value.StructValue, *value.ArrayValue:
			// SQLite cannot hold a composite value, so group on its
			// encoding: equal values encode identically.
			return v, nil
		case *value.IntervalValue:
			// Equal intervals can be written differently
			// (INTERVAL 1 MONTH = INTERVAL 30 DAY); group on the
			// normalised key. The selected column keeps its own text.
			return value.DistinctKey(decoded)
		}
		return decoded.Interface(), nil
	}, deterministic); err != nil {
		return fmt.Errorf("failed to register group_by function: %w", err)
	}

	if err := sqlitex.RegisterCollation(conn, "googlesqlite_collate", func(a, b string) int {
		va, _ := DecodeValue(a)
		vb, _ := DecodeValue(b)
		eq, _ := va.EQ(vb)
		if eq {
			return 0
		}
		cond, _ := va.GT(vb)
		if cond {
			return 1
		}
		return -1
	}); err != nil {
		return fmt.Errorf("failed to register collate function: %w", err)
	}

	for _, values := range normalFuncMap {
		for _, v := range values {
			flags := deterministic
			if v.NonDeterministic {
				flags = sqlitex.FunctionFlags{}
			}
			if err := sqlitex.RegisterFunc(conn, v.Name, v.Func, flags); err != nil {
				return fmt.Errorf("failed to register function %s: %w", v.Name, err)
			}
		}
	}
	for _, values := range aggregateFuncMap {
		for _, v := range values {
			if err := sqlitex.RegisterAggregator(conn, v.Name, v.Func, deterministic); err != nil {
				return fmt.Errorf("failed to register aggregate function %s: %w", v.Name, err)
			}
		}
	}
	// Window-aggregate functions register through CreateWindowFunction
	// so they participate in native OVER frame iteration. Functions
	// that the formatter still routes via the predecessor's
	// correlated-subquery emulation (DISTINCT, IGNORE NULLS,
	// non-native ARRAY_AGG/STDDEV/etc.) keep working because
	// CreateWindowFunction also accepts plain aggregate-shaped
	// instances.
	for _, values := range windowFuncMap {
		for _, v := range values {
			if err := sqlitex.RegisterWindow(conn, v.Name, v.Func, deterministic); err != nil {
				return fmt.Errorf("failed to register window function %s: %w", v.Name, err)
			}
		}
	}
	return nil
}

func setupNormalFuncMap(info *funcInfo) {
	normalFuncMap[info.Name] = append(normalFuncMap[info.Name], &nameAndFunc{
		Name: fmt.Sprintf("googlesqlite_%s", info.Name),
		Func: func(args ...any) (any, error) {
			values, err := value.ConvertArgs(args...)
			if err != nil {
				return nil, err
			}
			ret, err := info.BindFunc(values...)
			if err != nil {
				return nil, err
			}
			return EncodeValue(ret)
		},
		NonDeterministic: info.NonDeterministic,
	}, &nameAndFunc{
		Name: fmt.Sprintf("googlesqlite_safe_%s", info.Name),
		Func: func(args ...any) (any, error) {
			values, err := value.ConvertArgs(args...)
			if err != nil {
				return nil, err
			}
			ret, err := info.BindFunc(values...)
			if err != nil {
				// Note, this should only suppress semantic errors based on the
				// input data. See
				// https://github.com/google/googlesql/blob/master/docs/resolved_ast.md#resolvedfunctioncallbase
				return nil, nil
			}
			return EncodeValue(ret)
		},
		NonDeterministic: info.NonDeterministic,
	})
}

func setupAggregateFuncMap(info *aggregateFuncInfo) {
	aggregateFuncMap[info.Name] = append(aggregateFuncMap[info.Name], &nameAndFunc{
		Name: fmt.Sprintf("googlesqlite_%s", info.Name),
		Func: info.BindFunc(),
	})
}

func setupWindowFuncMap(info *windowFuncInfo) {
	windowFuncMap[info.Name] = append(windowFuncMap[info.Name], &nameAndFunc{
		Name: fmt.Sprintf("googlesqlite_window_%s", info.Name),
		Func: info.BindFunc(),
	})
}
