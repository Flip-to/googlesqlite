package window

import (
	"fmt"
	"math"
	"math/big"
	"sort"

	"github.com/goccy/googlesqlite/internal/value"
)

// RANGE frames over typed ORDER BY keys.
//
// SQLite's own RANGE frames only work for keys it can do arithmetic
// on: NUMERIC / BIGNUMERIC keys are stored as encoded TEXT, DOUBLE NaN
// is not a REAL, and `key - offset` on INT64 overflows at the ends of
// the domain. For those frames the formatter emits
//
//	googlesqlite_window_range(<inner>, <flags>, <start type>, <start
//	    offset>, <end type>, <end offset>, <key>, <args...>)
//	OVER (PARTITION BY ... ORDER BY ... ROWS BETWEEN CURRENT ROW AND
//	      UNBOUNDED FOLLOWING)
//
// With that frame SQLite steps every row of the partition (in ORDER BY
// order) before the first Value, and inverses exactly one row after
// each output row, so the number of Inverse calls seen so far is the
// index of the current row. The RANGE frame of the current row is then
// located by binary search over the buffered keys, and the inner
// aggregate (any googlesqlite_window_* native with Step / Done, and
// optionally Inverse) is evaluated over that slice of rows.
//
// Semantics follow the GoogleSQL window frame rules
// (docs/third_party/googlesql-docs/window-function-calls.md): the frame
// of a row with key k and `x PRECEDING` starts at the first row whose
// key is at or after k - x in sort order (k + x for DESC). NULL and NaN
// keys stay NULL / NaN under the offset, so their frames are their
// peer groups; +/-inf stay +/-inf. The offset arithmetic is exact
// (big.Rat) for integer and NUMERIC keys, so it never overflows.

// Range frame flags passed as the second argument.
const (
	RangeFrameFlagDesc      = 1
	RangeFrameFlagNullsLast = 2
)

type rangeKey struct {
	null    bool
	nan     bool
	isFloat bool
	f       float64
	r       *big.Rat
}

func toRangeKey(v value.Value) (rangeKey, error) {
	if v == nil {
		return rangeKey{null: true}, nil
	}
	if f, ok := v.(value.FloatValue); ok {
		x := float64(f)
		return rangeKey{isFloat: true, f: x, nan: math.IsNaN(x)}, nil
	}
	r, err := v.ToRat()
	if err != nil {
		return rangeKey{}, err
	}
	return rangeKey{r: r}, nil
}

// compareValues orders two non-NULL keys ascending with NaN first.
func (k rangeKey) compareValues(o rangeKey) int {
	switch {
	case k.nan && o.nan:
		return 0
	case k.nan:
		return -1
	case o.nan:
		return 1
	}
	if k.isFloat || o.isFloat {
		a, b := k.float(), o.float()
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		}
		return 0
	}
	return k.r.Cmp(o.r)
}

func (k rangeKey) float() float64 {
	if k.isFloat {
		return k.f
	}
	f, _ := k.r.Float64()
	return f
}

// shift returns k + sign*offset. NULL, NaN and infinite keys are
// unchanged by the shift.
func (k rangeKey) shift(off rangeKey, sign int) rangeKey {
	if k.null || k.nan {
		return k
	}
	if k.isFloat {
		if math.IsInf(k.f, 0) {
			return k
		}
		o := off.float()
		if sign < 0 {
			o = -o
		}
		x := k.f + o
		return rangeKey{isFloat: true, f: x, nan: math.IsNaN(x)}
	}
	if off.isFloat {
		return rangeKey{isFloat: true, f: k.float()}.shift(off, sign)
	}
	z := new(big.Rat)
	if sign < 0 {
		z.Sub(k.r, off.r)
	} else {
		z.Add(k.r, off.r)
	}
	return rangeKey{r: z}
}

type rangeInner interface {
	Step(...any) error
	Done() (any, error)
}

type rangeInverser interface {
	Inverse(...any) error
}

type rangeFrameWindow struct {
	lookup func(string) (func() any, bool)

	initialized bool
	ctor        func() any
	desc        bool
	nullsLast   bool
	startType   WindowBoundaryType
	endType     WindowBoundaryType
	startOff    rangeKey
	endOff      rangeKey

	keys []rangeKey
	rows [][]any
	cur  int

	inner        rangeInner
	innerS       int
	innerE       int
	innerInverse bool
}

// NewRangeFrameWindowNative returns the constructor of
// googlesqlite_window_range. lookup resolves the inner aggregate's
// registered name to its constructor.
func NewRangeFrameWindowNative(lookup func(string) (func() any, bool)) func() any {
	return func() any { return &rangeFrameWindow{lookup: lookup} }
}

func (w *rangeFrameWindow) init(args []any) error {
	if len(args) < 7 {
		return fmt.Errorf("googlesqlite_window_range: too few arguments")
	}
	head, err := value.ConvertArgs(args[:6]...)
	if err != nil {
		return err
	}
	if head[0] == nil {
		return fmt.Errorf("googlesqlite_window_range: missing inner function")
	}
	name, err := head[0].ToString()
	if err != nil {
		return err
	}
	ctor, ok := w.lookup(name)
	if !ok {
		return fmt.Errorf("googlesqlite_window_range: unknown inner function %s", name)
	}
	w.ctor = ctor
	ints := make([]int64, 0, 3)
	for _, i := range []int{1, 2, 4} {
		if head[i] == nil {
			return fmt.Errorf("googlesqlite_window_range: invalid frame argument")
		}
		n, err := head[i].ToInt64()
		if err != nil {
			return err
		}
		ints = append(ints, n)
	}
	w.desc = ints[0]&RangeFrameFlagDesc != 0
	w.nullsLast = ints[0]&RangeFrameFlagNullsLast != 0
	w.startType = WindowBoundaryType(ints[1])
	w.endType = WindowBoundaryType(ints[2])
	if isOffsetBoundary(w.startType) {
		if w.startOff, err = offsetKey(head[3]); err != nil {
			return err
		}
	}
	if isOffsetBoundary(w.endType) {
		if w.endOff, err = offsetKey(head[5]); err != nil {
			return err
		}
	}
	w.initialized = true
	return nil
}

func isOffsetBoundary(t WindowBoundaryType) bool {
	return t == WindowOffsetPrecedingType || t == WindowOffsetFollowingType
}

func offsetKey(v value.Value) (rangeKey, error) {
	k, err := toRangeKey(v)
	if err != nil {
		return k, err
	}
	if k.null {
		return k, fmt.Errorf("Window frame offset for PRECEDING or FOLLOWING cannot be NULL") //nolint:staticcheck // BigQuery's error text, checked by the compliance fixtures
	}
	if k.nan || (k.isFloat && k.f < 0) || (!k.isFloat && k.r.Sign() < 0) {
		return k, fmt.Errorf("Window frame offset for PRECEDING or FOLLOWING must be non-negative") //nolint:staticcheck // BigQuery's error text, checked by the compliance fixtures
	}
	return k, nil
}

func (w *rangeFrameWindow) Step(args ...any) error {
	if !w.initialized {
		if err := w.init(args); err != nil {
			return err
		}
	}
	keyVals, err := value.ConvertArgs(args[6])
	if err != nil {
		return err
	}
	k, err := toRangeKey(keyVals[0])
	if err != nil {
		return err
	}
	w.keys = append(w.keys, k)
	w.rows = append(w.rows, append([]any(nil), args[7:]...))
	return nil
}

func (w *rangeFrameWindow) Inverse(_ ...any) error {
	w.cur++
	return nil
}

// cmp orders two keys the way the window's ORDER BY sorts them.
func (w *rangeFrameWindow) cmp(a, b rangeKey) int {
	switch {
	case a.null && b.null:
		return 0
	case a.null:
		if w.nullsLast {
			return 1
		}
		return -1
	case b.null:
		if w.nullsLast {
			return -1
		}
		return 1
	}
	c := a.compareValues(b)
	if w.desc {
		return -c
	}
	return c
}

// bound returns the key the given boundary is anchored at.
func (w *rangeFrameWindow) bound(k rangeKey, typ WindowBoundaryType, off rangeKey) rangeKey {
	sign := 1
	if typ == WindowOffsetPrecedingType {
		sign = -1
	}
	if w.desc {
		sign = -sign
	}
	return k.shift(off, sign)
}

func (w *rangeFrameWindow) frame(i int) (int, int) {
	n := len(w.keys)
	k := w.keys[i]
	var s, e int
	switch w.startType {
	case WindowUnboundedPrecedingType:
		s = 0
	case WindowUnboundedFollowingType:
		s = n
	default:
		b := k
		if w.startType != WindowCurrentRowType {
			b = w.bound(k, w.startType, w.startOff)
		}
		s = sort.Search(n, func(j int) bool { return w.cmp(w.keys[j], b) >= 0 })
	}
	switch w.endType {
	case WindowUnboundedFollowingType:
		e = n
	case WindowUnboundedPrecedingType:
		e = 0
	default:
		b := k
		if w.endType != WindowCurrentRowType {
			b = w.bound(k, w.endType, w.endOff)
		}
		e = sort.Search(n, func(j int) bool { return w.cmp(w.keys[j], b) > 0 })
	}
	if e < s {
		e = s
	}
	return s, e
}

func (w *rangeFrameWindow) newInner() (rangeInner, error) {
	inst, ok := w.ctor().(rangeInner)
	if !ok {
		return nil, fmt.Errorf("googlesqlite_window_range: inner function is not an aggregate")
	}
	return inst, nil
}

func (w *rangeFrameWindow) Done() (any, error) {
	if !w.initialized {
		return nil, nil
	}
	if w.cur >= len(w.keys) {
		return nil, nil
	}
	s, e := w.frame(w.cur)
	if w.inner != nil && w.innerInverse && s >= w.innerS && e >= w.innerE && s <= w.innerE {
		for j := w.innerE; j < e; j++ {
			if err := w.inner.Step(w.rows[j]...); err != nil {
				return nil, err
			}
		}
		inv := w.inner.(rangeInverser)
		for j := w.innerS; j < s; j++ {
			if err := inv.Inverse(w.rows[j]...); err != nil {
				return nil, err
			}
		}
	} else {
		inner, err := w.newInner()
		if err != nil {
			return nil, err
		}
		for j := s; j < e; j++ {
			if err := inner.Step(w.rows[j]...); err != nil {
				return nil, err
			}
		}
		w.inner = inner
		_, w.innerInverse = inner.(rangeInverser)
	}
	w.innerS, w.innerE = s, e
	return w.inner.Done()
}
