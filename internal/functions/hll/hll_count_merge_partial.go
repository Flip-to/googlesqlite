package hll

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

type HLL_COUNT_MERGE_PARTIAL struct {
	acc sketchAccumulator
}

func (f *HLL_COUNT_MERGE_PARTIAL) Step(sketch []byte, opt *helper.Option) error {
	f.acc.fn = "HLL_COUNT.MERGE_PARTIAL"
	return f.acc.add(sketch)
}

func (f *HLL_COUNT_MERGE_PARTIAL) Done() (value.Value, error) {
	if f.acc.empty() {
		return nil, nil
	}
	return value.BytesValue(f.acc.bytes()), nil
}
