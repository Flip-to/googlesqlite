package approx_aggregate

import (
	"fmt"
	"sort"
	"sync"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// maxApproxTopNumber is the largest `number` APPROX_TOP_COUNT /
// APPROX_TOP_SUM accept (approx_aggregation.test
// approx_top_*_invalid_huge_count).
const maxApproxTopNumber = 100000

type APPROX_TOP_COUNT struct {
	once     sync.Once
	valueMap map[string]*value.StructValue
	num      int64
}

func (f *APPROX_TOP_COUNT) Step(v value.Value, num int64, opt *helper.Option) error {
	if num > maxApproxTopNumber {
		return fmt.Errorf("The second argument to APPROX_TOP_COUNT function cannot be greater than %d", maxApproxTopNumber) //nolint:staticcheck // BigQuery's error text
	}
	f.once.Do(func() {
		f.valueMap = map[string]*value.StructValue{}
		f.num = num
	})
	key := "null"
	if v != nil {
		k, err := value.DistinctKey(v)
		if err != nil {
			return err
		}
		key = k
	}
	val, exists := f.valueMap[key]
	if exists {
		cur, _ := val.Values[1].ToInt64()
		val.Values[1] = value.IntValue(cur + 1)
		val.M["count"] = value.IntValue(cur + 1)
	} else {
		f.valueMap[key] = &value.StructValue{
			Keys:   []string{"value", "count"},
			Values: []value.Value{v, value.IntValue(1)},
			M: map[string]value.Value{
				"value": v,
				"count": value.IntValue(1),
			},
		}
	}
	return nil
}

func (f *APPROX_TOP_COUNT) Done() (value.Value, error) {
	if len(f.valueMap) == 0 {
		return nil, nil
	}
	values := make([]*value.StructValue, 0, len(f.valueMap))
	for _, v := range f.valueMap {
		values = append(values, v)
	}
	sort.Slice(values, func(i, j int) bool {
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
