package hll

import (
	"sync"

	"github.com/DataDog/go-hll"
	"github.com/spaolacci/murmur3"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// zeroHashSubstitute stands in for a zero murmur3 hash.
const zeroHashSubstitute uint64 = 0x9e3779b97f4a7c15

type HLL_COUNT_INIT struct {
	once      sync.Once
	hll       *hll.Hll
	valueType byte
}

func (f *HLL_COUNT_INIT) Step(input value.Value, precision int64, opt *helper.Option) (e error) {
	// NULL inputs are skipped; with no non-NULL input the sketch is NULL.
	if input == nil {
		return nil
	}
	f.once.Do(func() {
		log2m, err := helper.SafeInt(precision)
		if err != nil {
			e = err
			return
		}
		h, err := hll.NewHll(hll.Settings{Log2m: log2m})
		if err != nil {
			e = err
		}
		f.hll = &h
	})
	var v uint64
	if f.valueType == 0 {
		f.valueType = sketchValueType(input)
	}
	switch input.(type) {
	case value.IntValue:
		s, err := input.ToString()
		if err != nil {
			return err
		}
		v = murmur3.Sum64([]byte(s))
	case *value.NumericValue:
		b, err := input.ToBytes()
		if err != nil {
			return err
		}
		v = murmur3.Sum64(b)
	case value.StringValue:
		s, err := input.ToString()
		if err != nil {
			return err
		}
		v = murmur3.Sum64([]byte(s))
	case value.BytesValue:
		b, err := input.ToBytes()
		if err != nil {
			return err
		}
		v = murmur3.Sum64(b)
	}
	// go-hll ignores a zero hash, and murmur3 of '' (or b'') is zero;
	// BigQuery counts '' as a distinct value (flipto-dbt probe
	// hll_count_init-1736.4).
	if v == 0 {
		v = zeroHashSubstitute
	}
	f.hll.AddRaw(v)
	return nil
}

func (f *HLL_COUNT_INIT) Done() (value.Value, error) {
	if f.hll == nil {
		return nil, nil
	}
	return value.BytesValue(tagSketch(f.valueType, f.hll.ToBytes())), nil
}
