package hll

import (
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/DataDog/go-hll"

	"github.com/goccy/googlesqlite/internal/value"
)

// Sketches produced by GoogleSQL / BigQuery HLL_COUNT.INIT are
// serialized AggregatorStateProto messages (type 112,
// HYPERLOGLOG_PLUS_UNIQUE) carrying a HyperLogLogPlusUniqueStateProto
// in extension field 112:
//
//	AggregatorStateProto
//	  1: type             (always 112)
//	  2: num_values
//	  3: encoding_version (2)
//	  4: value_type
//	  112: HyperLogLogPlusUniqueStateProto
//	    2: sparse_size
//	    3: precision_or_num_buckets
//	    4: sparse_precision_or_num_buckets
//	    5: data         (normal representation, one byte per register)
//	    6: sparse_data  (sorted sparse values, varint difference encoded)
//	    7: legacy value type (1 = STRING, 2 = INT64, 3 = UINT64)
//
// The driver's own HLL_COUNT.INIT still produces go-hll sketches;
// MERGE / MERGE_PARTIAL / EXTRACT accept either format (but never a mix
// of the two in one aggregation). The layout above and the re-encoding
// on merge (legacy value type moved to field 4, sparse_size / values
// recomputed) were checked against BigQuery.

var errIncompatibleSketch = errors.New("Invalid or incompatible sketch") //nolint:staticcheck // BigQuery's error text

type zetaSketch struct {
	numValues       int64
	valueType       int64
	precision       int64
	sparsePrecision int64
	sparse          []uint64
	dense           []byte
}

func isZetaSketch(b []byte) bool {
	return len(b) >= 2 && b[0] == 0x08 && b[1] == 0x70
}

func readVarint(b []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < len(b) && i < 10; i++ {
		x |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return x, i + 1, nil
		}
	}
	return 0, 0, errIncompatibleSketch
}

func appendVarint(b []byte, x uint64) []byte {
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}

type protoField struct {
	num    uint64
	varint uint64
	bytes  []byte
	isLen  bool
}

func parseProto(b []byte) ([]protoField, error) {
	var fields []protoField
	for len(b) > 0 {
		key, n, err := readVarint(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		f := protoField{num: key >> 3}
		switch key & 7 {
		case 0:
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			f.varint = v
			b = b[n:]
		case 2:
			l, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			b = b[n:]
			if uint64(len(b)) < l {
				return nil, errIncompatibleSketch
			}
			f.bytes = b[:l]
			f.isLen = true
			b = b[l:]
		default:
			return nil, errIncompatibleSketch
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// legacyValueTypes maps the HyperLogLogPlusUniqueStateProto legacy value
// type (field 7) onto AggregatorStateProto.value_type.
var legacyValueTypes = map[uint64]int64{1: 11, 2: 8, 3: 7}

func parseZetaSketch(b []byte) (*zetaSketch, error) {
	fields, err := parseProto(b)
	if err != nil {
		return nil, err
	}
	s := &zetaSketch{}
	var state []byte
	for _, f := range fields {
		switch f.num {
		case 1:
			if f.varint != 112 {
				return nil, errIncompatibleSketch
			}
		case 2:
			s.numValues = int64(f.varint)
		case 3:
			if f.varint != 2 {
				return nil, errIncompatibleSketch
			}
		case 4:
			s.valueType = int64(f.varint)
		case 112:
			if !f.isLen {
				return nil, errIncompatibleSketch
			}
			state = f.bytes
		}
	}
	if state == nil {
		return nil, errIncompatibleSketch
	}
	inner, err := parseProto(state)
	if err != nil {
		return nil, err
	}
	var legacyType uint64
	for _, f := range inner {
		switch f.num {
		case 3:
			s.precision = int64(f.varint)
		case 4:
			s.sparsePrecision = int64(f.varint)
		case 5:
			s.dense = append([]byte(nil), f.bytes...)
		case 6:
			var prev uint64
			for data := f.bytes; len(data) > 0; {
				d, n, err := readVarint(data)
				if err != nil {
					return nil, err
				}
				data = data[n:]
				prev += d
				s.sparse = append(s.sparse, prev)
			}
		case 7:
			legacyType = f.varint
		}
	}
	if s.valueType == 0 {
		s.valueType = legacyValueTypes[legacyType]
	}
	if s.precision < 10 || s.precision > 24 {
		return nil, errIncompatibleSketch
	}
	if s.dense != nil && len(s.dense) != 1<<s.precision {
		return nil, errIncompatibleSketch
	}
	return s, nil
}

// merge folds o into s. Sketches of different value types or
// precisions cannot be merged. Only sparse-with-sparse and
// dense-with-dense merges are supported: converting sparse values into
// normal-precision registers needs the GoogleSQL sparse encoding,
// which this package does not implement.
func (s *zetaSketch) merge(o *zetaSketch) error {
	if s.valueType != 0 && o.valueType != 0 && s.valueType != o.valueType {
		return errIncompatibleSketch
	}
	if s.precision != o.precision || s.sparsePrecision != o.sparsePrecision {
		return errIncompatibleSketch
	}
	if (s.dense != nil) != (o.dense != nil) {
		return fmt.Errorf("%w: merging sparse and normal HLL++ sketches is not supported", errIncompatibleSketch)
	}
	if s.valueType == 0 {
		s.valueType = o.valueType
	}
	s.numValues += o.numValues
	if s.dense != nil {
		for i, r := range o.dense {
			s.dense[i] = max(s.dense[i], r)
		}
		return nil
	}
	merged := make([]uint64, 0, len(s.sparse)+len(o.sparse))
	merged = append(merged, s.sparse...)
	merged = append(merged, o.sparse...)
	slices.Sort(merged)
	s.sparse = slices.Compact(merged)
	return nil
}

func (s *zetaSketch) bytes() []byte {
	var inner []byte
	if s.dense == nil {
		inner = appendVarint(append(inner, 0x10), uint64(len(s.sparse)))
	}
	inner = appendVarint(append(inner, 0x18), uint64(s.precision))
	inner = appendVarint(append(inner, 0x20), uint64(s.sparsePrecision))
	if s.dense != nil {
		inner = appendVarint(append(inner, 0x2a), uint64(len(s.dense)))
		inner = append(inner, s.dense...)
	} else {
		var data []byte
		var prev uint64
		for _, v := range s.sparse {
			data = appendVarint(data, v-prev)
			prev = v
		}
		inner = appendVarint(append(inner, 0x32), uint64(len(data)))
		inner = append(inner, data...)
	}
	out := []byte{0x08, 0x70}
	out = appendVarint(append(out, 0x10), uint64(s.numValues))
	out = append(out, 0x18, 0x02)
	if s.valueType != 0 {
		out = appendVarint(append(out, 0x20), uint64(s.valueType))
	}
	out = append(out, 0x82, 0x07)
	out = appendVarint(out, uint64(len(inner)))
	return append(out, inner...)
}

// cardinality estimates the distinct count. A sparse sketch uses linear
// counting over its 2^sparse_precision buckets; a normal sketch uses
// the HyperLogLog estimate with linear counting for small ranges.
func (s *zetaSketch) cardinality() int64 {
	if s.dense == nil {
		m := math.Ldexp(1, int(s.sparsePrecision))
		n := float64(len(s.sparse))
		return int64(math.Round(m * math.Log(m/(m-n))))
	}
	m := float64(len(s.dense))
	var sum float64
	zeros := 0
	for _, r := range s.dense {
		sum += math.Ldexp(1, -int(r))
		if r == 0 {
			zeros++
		}
	}
	alpha := 0.7213 / (1 + 1.079/m)
	e := alpha * m * m / sum
	if e <= 5*m && zeros > 0 {
		e = m * math.Log(m/float64(zeros))
	}
	return int64(math.Round(e))
}

// sketchAccumulator merges a stream of serialized sketches of either
// format.
type sketchAccumulator struct {
	fn     string
	dd     *hll.Hll
	ddType byte
	zeta   *zetaSketch
}

// Driver sketches (go-hll) carry the input type ahead of the go-hll
// bytes, so sketches of different input types are rejected on merge
// as GoogleSQL requires (hll_count.test
// hll_count_merge_incompatible_types / _partial_incompatible_types):
//
//	0xff 'G' <type> <go-hll bytes>
//
// A go-hll serialization never starts with 0xff (its first byte is the
// schema version), so untagged sketches are still accepted.
const (
	sketchTypeInt64 byte = iota + 1
	sketchTypeNumeric
	sketchTypeBigNumeric
	sketchTypeString
	sketchTypeBytes
)

func sketchValueType(v value.Value) byte {
	switch vv := v.(type) {
	case value.IntValue:
		return sketchTypeInt64
	case *value.NumericValue:
		if vv.IsBigNumeric {
			return sketchTypeBigNumeric
		}
		return sketchTypeNumeric
	case value.StringValue:
		return sketchTypeString
	case value.BytesValue:
		return sketchTypeBytes
	}
	return 0
}

func tagSketch(typ byte, b []byte) []byte {
	if typ == 0 {
		return b
	}
	return append([]byte{0xff, 'G', typ}, b...)
}

// untagSketch splits a driver sketch into its input type (0 when
// untagged) and go-hll bytes.
func untagSketch(b []byte) (byte, []byte) {
	if len(b) >= 3 && b[0] == 0xff && b[1] == 'G' {
		return b[2], b[3:]
	}
	return 0, b
}

func (a *sketchAccumulator) add(b []byte) error {
	if isZetaSketch(b) {
		if a.dd != nil {
			return a.incompatible()
		}
		s, err := parseZetaSketch(b)
		if err != nil {
			return a.incompatible()
		}
		if a.zeta == nil {
			a.zeta = s
			return nil
		}
		if err := a.zeta.merge(s); err != nil {
			return fmt.Errorf("%w in %s", err, a.fn)
		}
		return nil
	}
	if a.zeta != nil {
		return a.incompatible()
	}
	typ, b := untagSketch(b)
	h, err := hll.FromBytes(b)
	if err != nil {
		return err
	}
	if a.dd == nil {
		a.dd = &h
		a.ddType = typ
	} else {
		if typ != 0 && a.ddType != 0 && typ != a.ddType {
			return a.incompatible()
		}
		if a.ddType == 0 {
			a.ddType = typ
		}
		a.dd.Union(h)
	}
	return nil
}

func (a *sketchAccumulator) incompatible() error {
	return fmt.Errorf("%w in %s", errIncompatibleSketch, a.fn)
}

func (a *sketchAccumulator) empty() bool { return a.dd == nil && a.zeta == nil }

func (a *sketchAccumulator) bytes() []byte {
	if a.zeta != nil {
		return a.zeta.bytes()
	}
	return tagSketch(a.ddType, a.dd.ToBytes())
}

func (a *sketchAccumulator) cardinality() int64 {
	if a.zeta != nil {
		return a.zeta.cardinality()
	}
	return int64(a.dd.Cardinality())
}
