package hll

import (
	"fmt"

	"github.com/DataDog/go-hll"

	"github.com/goccy/googlesqlite/internal/value"
)

func init() {
	_ = hll.Defaults(hll.Settings{
		Log2m:             15,
		Regwidth:          8,
		ExplicitThreshold: hll.AutoExplicitThreshold,
		SparseEnabled:     true,
	})
}

// HLL_COUNT_EXTRACT is registered as a scalar function (not an
// aggregate) — it accepts a serialized sketch and returns its
// cardinality.
func HLL_COUNT_EXTRACT(sketch []byte) (value.Value, error) {
	if isZetaSketch(sketch) {
		s, err := parseZetaSketch(sketch)
		if err != nil {
			return nil, fmt.Errorf("%w in HLL_COUNT.EXTRACT", err)
		}
		return value.IntValue(s.cardinality()), nil
	}
	_, raw := untagSketch(sketch)
	h, err := hll.FromBytes(raw)
	if err != nil {
		return nil, err
	}
	return value.IntValue(h.Cardinality()), nil
}
