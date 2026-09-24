package internal

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"

	"github.com/goccy/go-json"
	sqlite3 "github.com/ncruces/go-sqlite3"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	approx "github.com/goccy/googlesqlite/internal/functions/approx_aggregate"
	"github.com/goccy/googlesqlite/internal/functions/hll"
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
		// MATCH_RECOGNIZE row pattern matcher; see match_recognize.go.
		windowFuncMap["match_recognize"] = []*nameAndFunc{
			{Name: "googlesqlite_match_recognize", Func: newMatchRecognizeWindow},
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
		// PERCENTILE_CONT / PERCENTILE_DISC as plain aggregates
		// (aggregate_percentile_cont.test) share the window natives.
		aggregateFuncMap["percentile_cont"] = []*nameAndFunc{
			{Name: "googlesqlite_percentile_cont", Func: window.NewPercentileContWindowNative()},
		}
		aggregateFuncMap["percentile_disc"] = []*nameAndFunc{
			{Name: "googlesqlite_percentile_disc", Func: window.NewPercentileDiscWindowNative()},
		}
		// HLL_COUNT.* in OVER context. The plain aggregates have no
		// Inverse, so sqlitex.RegisterWindow wraps them in the buffered
		// adapter that replays the active frame on each Value.
		windowFuncMap["hll_count_init"] = []*nameAndFunc{
			{Name: "googlesqlite_window_hll_count_init", Func: hll.BindHllCountInit()},
		}
		windowFuncMap["hll_count_merge"] = []*nameAndFunc{
			{Name: "googlesqlite_window_hll_count_merge", Func: hll.BindHllCountMerge()},
		}
		windowFuncMap["hll_count_merge_partial"] = []*nameAndFunc{
			{Name: "googlesqlite_window_hll_count_merge_partial", Func: hll.BindHllCountMergePartial()},
		}
		// APPROX_* aggregates in OVER context, replayed over the active
		// frame like HLL_COUNT.* (analytic_approx_*.test *_basic).
		windowFuncMap["approx_count_distinct"] = []*nameAndFunc{
			{Name: "googlesqlite_window_approx_count_distinct", Func: approx.BindApproxCountDistinct()},
		}
		windowFuncMap["approx_quantiles"] = []*nameAndFunc{
			{Name: "googlesqlite_window_approx_quantiles", Func: approx.BindApproxQuantiles()},
		}
		windowFuncMap["approx_top_count"] = []*nameAndFunc{
			{Name: "googlesqlite_window_approx_top_count", Func: approx.BindApproxTopCount()},
		}
		windowFuncMap["approx_top_sum"] = []*nameAndFunc{
			{Name: "googlesqlite_window_approx_top_sum", Func: approx.BindApproxTopSum()},
		}
		// Inner aggregates for RANGE frames over typed ORDER BY keys
		// (see internal/functions/window/range_frame.go) whose plain
		// window form is a SQLite built-in.
		windowFuncMap["count_typed"] = []*nameAndFunc{
			{Name: "googlesqlite_window_typed_count", Func: window.NewCountWindowNative()},
		}
		windowFuncMap["first_value_typed"] = []*nameAndFunc{
			{Name: "googlesqlite_window_typed_first_value", Func: window.NewFirstValueWindowNative()},
		}
		windowFuncMap["last_value_typed"] = []*nameAndFunc{
			{Name: "googlesqlite_window_typed_last_value", Func: window.NewLastValueWindowNative()},
		}
		windowFuncMap["nth_value_typed"] = []*nameAndFunc{
			{Name: "googlesqlite_window_typed_nth_value", Func: window.NewNthValueWindowNative()},
		}
		innerCtors := map[string]func() any{}
		for _, values := range windowFuncMap {
			for _, v := range values {
				if ctor, ok := v.Func.(func() any); ok {
					innerCtors[v.Name] = ctor
					continue
				}
				// Plain aggregates (func() *T) are usable as inner
				// functions too; they are rebuilt per frame.
				if rv := reflect.ValueOf(v.Func); rv.Kind() == reflect.Func && rv.Type().NumIn() == 0 && rv.Type().NumOut() == 1 {
					innerCtors[v.Name] = func() any { return rv.Call(nil)[0].Interface() }
				}
			}
		}
		windowFuncMap["range_frame"] = []*nameAndFunc{
			{Name: "googlesqlite_window_range", Func: window.NewRangeFrameWindowNative(func(name string) (func() any, bool) {
				ctor, ok := innerCtors[name]
				return ctor, ok
			})},
		}
	})
	if onceErr != nil {
		return onceErr
	}

	deterministic := sqlitex.FunctionFlags{Deterministic: true}

	// googlesqlite_int64_sum_combine(hi, lo) rebuilds a window SUM over
	// INT64 from the sums of the high (x >> 32) and low (x & 0xffffffff)
	// 32-bit halves, which SQLite's sum never overflows on. Only the
	// final total is range-checked, so intermediate overflow in a
	// running sum is not an error (analytic_sum.test,
	// analytic_sum_int64_overflow_3).
	// The googlesqlite_safe_ variant backs SAFE.SUM(x) OVER (...): an
	// overflowing total yields NULL instead of an error
	// (safe_function.test, safe_analytic_agg_func).
	for _, safe := range []bool{false, true} {
		name := "googlesqlite_int64_sum_combine"
		if safe {
			name = "googlesqlite_safe_int64_sum_combine"
		}
		if err := sqlitex.RegisterFunc(conn, name, func(hi, lo any) (any, error) {
			h, ok1 := hi.(int64)
			l, ok2 := lo.(int64)
			if !ok1 || !ok2 {
				return nil, nil
			}
			h += l >> 32
			l &= 0xffffffff
			if h > math.MaxInt32 || h < math.MinInt32 {
				if safe {
					return nil, nil
				}
				return nil, fmt.Errorf("int64 overflow: SUM result exceeds INT64 range")
			}
			return h<<32 | l, nil
		}, deterministic); err != nil {
			return err
		}
	}
	// googlesqlite_raise_deferred(x) raises the error carried by a
	// deferred-error marker and passes any other value through;
	// googlesqlite_deferred_to_null(x) turns the marker into NULL for
	// IFERROR / ISERROR / NULLIFERROR (see value.DeferredError).
	if err := sqlitex.RegisterFunc(conn, "googlesqlite_raise_deferred", func(v any) (any, error) {
		if de, ok := value.AsDeferredError(v); ok {
			return nil, de
		}
		return v, nil
	}, deterministic); err != nil {
		return err
	}
	if err := sqlitex.RegisterFunc(conn, "googlesqlite_deferred_to_null", func(v any) (any, error) {
		if _, ok := value.AsDeferredError(v); ok {
			return nil, nil
		}
		return v, nil
	}, deterministic); err != nil {
		return err
	}
	if err := sqlitex.RegisterFunc(conn, "googlesqlite_is_deferred_error", func(v any) (any, error) {
		_, ok := value.AsDeferredError(v)
		return ok, nil
	}, deterministic); err != nil {
		return err
	}
	// ERROR(msg) inside IFERROR / ISERROR / NULLIFERROR.
	if err := sqlitex.RegisterFunc(conn, "googlesqlite_make_deferred_error", func(v any) (any, error) {
		msg := "ERROR"
		if decoded, err := value.DecodeValue(v); err == nil && decoded != nil {
			if s, err := decoded.ToString(); err == nil {
				msg = s
			}
		}
		return value.EncodeDeferredError(fmt.Errorf("%s", msg)), nil
	}, deterministic); err != nil {
		return err
	}
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
		case value.FloatValue:
			// SQLite stores a NaN REAL as NULL, which would merge the
			// NaN group into the NULL group; GoogleSQL groups all NaNs
			// together, apart from NULL (grouping_sets_queries.test,
			// grouping_sets_with_alias).
			if math.IsNaN(float64(decoded.(value.FloatValue))) {
				return "NaN", nil
			}
		}
		return decoded.Interface(), nil
	}, deterministic); err != nil {
		return fmt.Errorf("failed to register group_by function: %w", err)
	}

	if err := sqlitex.RegisterCollation(conn, "googlesqlite_collate", func(a, b string) int {
		if a == b {
			// Encodings are canonical: byte-equal values are peers. This
			// also makes NaN a peer of NaN, as GoogleSQL ordering requires.
			return 0
		}
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
	}, &nameAndFunc{
		// Deferred variant for aggregate arguments: an error becomes a
		// deferred-error marker that the aggregate carries to the point
		// where its value is used (see value.DeferredError).
		Name: fmt.Sprintf("googlesqlite_deferred_%s", info.Name),
		Func: func(args ...any) (any, error) {
			for _, a := range args {
				if _, ok := value.AsDeferredError(a); ok {
					return a, nil
				}
			}
			values, err := value.ConvertArgs(args...)
			if err != nil {
				return value.EncodeDeferredError(err), nil
			}
			ret, err := info.BindFunc(values...)
			if err != nil {
				return value.EncodeDeferredError(err), nil
			}
			return EncodeValue(ret)
		},
		NonDeterministic: info.NonDeterministic,
	})
}

func setupAggregateFuncMap(info *aggregateFuncInfo) {
	bind := info.BindFunc()
	aggregateFuncMap[info.Name] = append(aggregateFuncMap[info.Name], &nameAndFunc{
		Name: fmt.Sprintf("googlesqlite_%s", info.Name),
		Func: bind,
	}, &nameAndFunc{
		// SAFE.<aggregate>(...) turns an error raised while
		// accumulating or finishing into NULL
		// (safe_function.test, safe_agg_func_group_by).
		Name: fmt.Sprintf("googlesqlite_safe_%s", info.Name),
		Func: func() *safeAggregator { return &safeAggregator{inner: bind()} },
	})
}

// safeAggregator wraps an aggregate so that any error becomes a NULL
// result for the group.
type safeAggregator struct {
	inner  *helper.Aggregator
	failed bool
}

func (a *safeAggregator) Step(args ...any) error {
	if a.failed {
		return nil
	}
	if err := a.inner.Step(args...); err != nil {
		a.failed = true
	}
	return nil
}

func (a *safeAggregator) Done() (any, error) {
	if a.failed {
		return nil, nil
	}
	v, err := a.inner.Done()
	if err != nil {
		return nil, nil
	}
	if _, ok := value.AsDeferredError(v); ok {
		return nil, nil
	}
	return v, nil
}

func setupWindowFuncMap(info *windowFuncInfo) {
	bind := info.BindFunc()
	windowFuncMap[info.Name] = append(windowFuncMap[info.Name], &nameAndFunc{
		Name: fmt.Sprintf("googlesqlite_window_%s", info.Name),
		Func: bind,
	}, &nameAndFunc{
		Name: fmt.Sprintf("googlesqlite_safe_window_%s", info.Name),
		Func: func() *safeWindowAggregator { return &safeWindowAggregator{inner: bind()} },
	})
}

// safeWindowAggregator is the SAFE.<aggregate>(...) OVER (...) analogue
// of safeAggregator.
type safeWindowAggregator struct {
	inner  *window.WindowAggregator
	failed bool
}

func (a *safeWindowAggregator) Step(args ...any) error {
	if a.failed {
		return nil
	}
	if err := a.inner.Step(args...); err != nil {
		a.failed = true
	}
	return nil
}

func (a *safeWindowAggregator) Done() (any, error) {
	if a.failed {
		return nil, nil
	}
	v, err := a.inner.Done()
	if err != nil {
		return nil, nil
	}
	return v, nil
}
