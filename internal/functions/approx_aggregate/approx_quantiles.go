package approx_aggregate

import (
	"math"
	"sort"
	"sync"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

type APPROX_QUANTILES struct {
	once   sync.Once
	values []value.Value
	num    int64
}

func (f *APPROX_QUANTILES) Step(v value.Value, num int64, opt *helper.Option) error {
	f.once.Do(func() {
		f.num = num
	})
	f.values = append(f.values, v)
	return nil
}

func (f *APPROX_QUANTILES) Done() (value.Value, error) {
	if len(f.values) == 0 {
		return nil, nil
	}
	// Quantiles are read off the sorted input, NULLs first (they only
	// reach here under RESPECT NULLS).
	sort.SliceStable(f.values, func(i, j int) bool {
		a, b := f.values[i], f.values[j]
		if a == nil || b == nil {
			return a == nil && b != nil
		}
		// NaN sorts before every other FLOAT64, as in BigQuery's
		// ordering (flipto-dbt probe approx_quantiles-6302.6).
		if an, bn := isNaN(a), isNaN(b); an || bn {
			return an && !bn
		}
		lt, err := a.LT(b)
		return err == nil && lt
	})
	if f.num == 0 {
		return &value.ArrayValue{Values: []value.Value{f.values[0]}}, nil
	}
	if f.num == 1 {
		return &value.ArrayValue{Values: []value.Value{f.values[0], f.values[len(f.values)-1]}}, nil
	}
	// Integer arithmetic keeps exactly num+1 boundaries; a float
	// step accumulated rounding error and emitted an extra element
	// for num=1000 (approx_aggregation.test approx_quantiles_fixed_count_1000).
	length := int64(len(f.values))
	quantiles := make([]value.Value, 0, f.num+1)
	for i := int64(0); i < f.num; i++ {
		idx := (length*i + f.num - 1) / f.num // ceil(length*i/num)
		if idx > 0 {
			quantiles = append(quantiles, f.values[idx-1])
		} else {
			quantiles = append(quantiles, f.values[idx])
		}
	}
	quantiles = append(quantiles, f.values[len(f.values)-1])
	return &value.ArrayValue{Values: quantiles}, nil
}

func isNaN(v value.Value) bool {
	f, ok := v.(value.FloatValue)
	return ok && math.IsNaN(float64(f))
}
