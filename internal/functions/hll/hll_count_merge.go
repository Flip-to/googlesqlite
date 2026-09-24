package hll

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

type HLL_COUNT_MERGE struct {
	acc sketchAccumulator
}

func (f *HLL_COUNT_MERGE) Step(sketch []byte, opt *helper.Option) error {
	f.acc.fn = "HLL_COUNT.MERGE"
	return f.acc.add(sketch)
}

func (f *HLL_COUNT_MERGE) Done() (value.Value, error) {
	if f.acc.empty() {
		return value.IntValue(0), nil
	}
	return value.IntValue(f.acc.cardinality()), nil
}
