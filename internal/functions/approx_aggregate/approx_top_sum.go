package approx_aggregate

import (
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

type APPROX_TOP_SUM struct {
	once     sync.Once
	valueMap map[value.Value]*value.StructValue
	num      int64
}

func (f *APPROX_TOP_SUM) Step(v, weight value.Value, num int64, opt *helper.Option) error {
	if num > maxApproxTopNumber {
		return fmt.Errorf("The second argument to APPROX_TOP_SUM function cannot be greater than %d", maxApproxTopNumber) //nolint:staticcheck // BigQuery's error text
	}
	f.once.Do(func() {
		f.valueMap = map[value.Value]*value.StructValue{}
		f.num = num
	})
	// approximate_aggregate_functions.md APPROX_TOP_SUM: negative and
	// NaN weights are an error.
	if weight != nil {
		w, err := weight.ToFloat64()
		if err != nil {
			return err
		}
		if w < 0 || math.IsNaN(w) {
			return fmt.Errorf("APPROX_TOP_SUM does not support negative or NaN weights in the second argument; got %v", weight)
		}
	}
	val, exists := f.valueMap[v]
	if exists {
		if weight != nil {
			var sum value.Value
			if val.Values[1] == nil {
				sum = weight
			} else {
				if a, ok := val.Values[1].(value.IntValue); ok {
					if b, ok := weight.(value.IntValue); ok {
						r := a + b
						if (b > 0 && r < a) || (b < 0 && r > a) {
							return fmt.Errorf("int64 overflow: %d + %d", int64(a), int64(b))
						}
					}
				}
				added, err := val.Values[1].Add(weight)
				if err != nil {
					return err
				}
				sum = added
			}
			val.Values[1] = sum
			val.M["sum"] = sum
		}
	} else {
		f.valueMap[v] = &value.StructValue{
			Keys:   []string{"value", "sum"},
			Values: []value.Value{v, weight},
			M: map[string]value.Value{
				"value": v,
				"sum":   weight,
			},
		}
	}
	return nil
}

func (f *APPROX_TOP_SUM) Done() (value.Value, error) {
	if len(f.valueMap) == 0 {
		return nil, nil
	}
	values := make([]*value.StructValue, 0, len(f.valueMap))
	for _, v := range f.valueMap {
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Values[1] == nil {
			return false
		}
		if values[j].Values[1] == nil {
			return true
		}
		cond, _ := values[i].Values[1].GT(values[j].Values[1])
		return cond
	})
	ret := &value.ArrayValue{}
	// Fewer distinct values than requested returns all of them
	// (approx_aggregation.test approx_top_*_big_count_small_*_input).
	n := min(f.num, int64(len(values)))
	for _, v := range values[:n] {
		ret.Values = append(ret.Values, v)
	}
	return ret, nil
}
