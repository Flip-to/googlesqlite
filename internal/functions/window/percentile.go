package window

import (
	"fmt"
	"math"
	"math/big"
	"sort"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// percentileWindow is the shared accumulator for PERCENTILE_CONT and
// PERCENTILE_DISC over OVER. Step buffers per-row (value, percentile)
// pairs; only the first non-NULL percentile is captured because
// BigQuery requires it to be a constant. Done sorts the active
// values and computes the requested percentile.
type percentileWindow struct {
	values     []value.Value
	null       []bool
	pct        float64
	pctValue   value.Value
	pctSet     bool
	ignoreNull bool
	once       bool
}

func (p *percentileWindow) absorbStep(stepArgs ...any) error {
	values, err := value.ConvertArgs(stepArgs...)
	if err != nil {
		return err
	}
	values, opt := helper.ParseOptions(values...)
	values, _ = parseWindowOptions(values...)
	if !p.once {
		p.ignoreNull = opt.IgnoreNulls
		p.once = true
	}
	if len(values) < 2 {
		return fmt.Errorf("percentile: requires (value, percentile) arguments")
	}
	if !p.pctSet && values[1] != nil {
		f, err := values[1].ToFloat64()
		if err != nil {
			return err
		}
		if f < 0 || f > 1 {
			return fmt.Errorf("percentile: value must be in [0, 1]; got %v", f)
		}
		p.pct = f
		p.pctValue = values[1]
		p.pctSet = true
	}
	if values[0] == nil {
		p.values = append(p.values, nil)
		p.null = append(p.null, true)
	} else {
		p.values = append(p.values, values[0])
		p.null = append(p.null, false)
	}
	return nil
}

func (p *percentileWindow) popFront() {
	if len(p.values) == 0 {
		return
	}
	p.values = p.values[1:]
	p.null = p.null[1:]
}

// activeSorted returns the active values in BigQuery PERCENTILE_*
// sort order. When IGNORE NULLS (the default the formatter emits)
// is set, NULLs are dropped. Under RESPECT NULLS they are placed at
// the front of the sorted slice.
func (p *percentileWindow) activeSorted() ([]value.Value, error) {
	var nulls, nonNulls []value.Value
	for i, v := range p.values {
		if p.null[i] {
			if !p.ignoreNull {
				nulls = append(nulls, nil)
			}
			continue
		}
		nonNulls = append(nonNulls, v)
	}
	// NaN orders before every other non-NULL value.
	sort.SliceStable(nonNulls, func(i, j int) bool {
		iNaN, jNaN := isNaNValue(nonNulls[i]), isNaNValue(nonNulls[j])
		if iNaN || jNaN {
			return iNaN && !jNaN
		}
		c, err := nonNulls[i].LT(nonNulls[j])
		if err != nil {
			return false
		}
		return c
	})
	return append(nulls, nonNulls...), nil
}

// PERCENTILE_CONT: continuous percentile.
type percentileContWindow struct{ percentileWindow }

func NewPercentileContWindowNative() func() any {
	return func() any { return &percentileContWindow{} }
}
func (a *percentileContWindow) Step(args ...any) error { return a.absorbStep(args...) }
func (a *percentileContWindow) Inverse(_ ...any) error { a.popFront(); return nil }
func (a *percentileContWindow) Done() (any, error) {
	xs, err := a.activeSorted()
	if err != nil {
		return nil, err
	}
	if len(xs) == 0 {
		return nil, nil
	}
	if !a.pctSet {
		return nil, nil
	}
	return percentileContValue(xs, a.pctValue)
}

// PERCENTILE_DISC: discrete percentile.
type percentileDiscWindow struct{ percentileWindow }

func NewPercentileDiscWindowNative() func() any {
	return func() any { return &percentileDiscWindow{} }
}
func (a *percentileDiscWindow) Step(args ...any) error { return a.absorbStep(args...) }
func (a *percentileDiscWindow) Inverse(_ ...any) error { a.popFront(); return nil }
func (a *percentileDiscWindow) Done() (any, error) {
	xs, err := a.activeSorted()
	if err != nil {
		return nil, err
	}
	if len(xs) == 0 {
		return nil, nil
	}
	if !a.pctSet {
		return nil, nil
	}
	// Smallest value v such that ceil(pct * n) values are <= v.
	// Equivalent to taking the value at index ceil(pct * n) - 1 when
	// 1-indexed, or floor(pct * (n-1)) zero-indexed; BigQuery uses
	// `index = ceil(pct * n) - 1` which matches.
	n := len(xs)
	idx := max(int(math.Ceil(a.pct*float64(n)))-1, 0)
	if idx >= n {
		idx = n - 1
	}
	// PERCENTILE_DISC returns the value at the picked position
	// unchanged; only PERCENTILE_CONT requires a numeric coercion
	// for interpolation.
	return value.EncodeValue(xs[idx])
}

func isNaNValue(v value.Value) bool {
	f, ok := v.(value.FloatValue)
	return ok && math.IsNaN(float64(f))
}

// percentileContValue interpolates PERCENTILE_CONT over the sorted
// values xs (NULL entries first under RESPECT NULLS). The position
// pct * (n - 1) and its fraction are computed exactly, as the
// GoogleSQL reference implementation does, so a percentile such as
// 0.8750000000000001 is not rounded onto a neighbouring row.
//
//   - DOUBLE: lo * (1 - f) + hi * f, which keeps -inf / +inf at the
//     ends instead of producing NaN from inf - inf
//     (aggregate_percentile_cont.test,
//     aggregate_percentile_cont_interpolation).
//   - NUMERIC / BIGNUMERIC: exact lo + (hi - lo) * f, rounded half
//     away from zero to the type's scale
//     (aggregate_percentile_cont_numeric / _bignumeric).
//
// A NULL at either interpolation end yields the other end; NULL at
// both yields NULL.
func percentileContValue(xs []value.Value, pct value.Value) (any, error) {
	n := len(xs)
	if n == 0 || pct == nil {
		return nil, nil
	}
	pr, err := pct.ToRat()
	if err != nil {
		return nil, err
	}
	pos := new(big.Rat).Mul(pr, new(big.Rat).SetInt64(int64(n-1)))
	lo := new(big.Int).Quo(pos.Num(), pos.Denom()) // pos >= 0, so Quo floors
	frac := new(big.Rat).Sub(pos, new(big.Rat).SetInt(lo))
	loIdx := int(lo.Int64())
	hiIdx := loIdx
	if frac.Sign() > 0 {
		hiIdx = loIdx + 1
	}
	if hiIdx >= n {
		hiIdx = n - 1
	}
	loVal, hiVal := xs[loIdx], xs[hiIdx]
	if frac.Sign() == 0 || loIdx == hiIdx {
		hiVal = loVal
	}
	switch {
	case loVal == nil && hiVal == nil:
		return nil, nil
	case loVal == nil:
		loVal = hiVal
	case hiVal == nil:
		hiVal = loVal
	}
	if nv, ok := loVal.(*value.NumericValue); ok {
		a := nv.Rat
		b, err := hiVal.ToRat()
		if err != nil {
			return nil, err
		}
		r := new(big.Rat).Sub(b, a)
		r.Mul(r, frac)
		r.Add(r, a)
		scale := 9
		if nv.IsBigNumeric {
			scale = 38
		}
		rounded, _ := new(big.Rat).SetString(r.FloatString(scale))
		return value.EncodeValue(&value.NumericValue{Rat: rounded, IsBigNumeric: nv.IsBigNumeric})
	}
	a, err := loVal.ToFloat64()
	if err != nil {
		return nil, err
	}
	if frac.Sign() == 0 {
		return value.EncodeValue(value.FloatValue(a))
	}
	b, err := hiVal.ToFloat64()
	if err != nil {
		return nil, err
	}
	f, _ := frac.Float64()
	g, _ := new(big.Rat).Sub(big.NewRat(1, 1), frac).Float64()
	return value.EncodeValue(value.FloatValue(a*g + b*f))
}
