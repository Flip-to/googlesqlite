package math

import (
	"fmt"
	"math"

	"github.com/goccy/googlesqlite/internal/value"
)

// EUCLIDEAN_DISTANCE returns the L2 distance between two
// equal-length ARRAY<FLOAT64> vectors. NULL vectors propagate.
func EUCLIDEAN_DISTANCE(args ...value.Value) (value.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("EUCLIDEAN_DISTANCE: invalid number of arguments: got %d, want 2", len(args))
	}
	if args[0] == nil || args[1] == nil {
		return nil, nil
	}
	a, b, err := vectorPair(args[0], args[1])
	if err != nil {
		return nil, err
	}
	if len(a) != len(b) {
		return nil, fmt.Errorf("EUCLIDEAN_DISTANCE: vector lengths differ (%d vs %d)", len(a), len(b))
	}
	var sum float64
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return value.FloatValue(math.Sqrt(sum)), nil
}

// vectorFromValue extracts an ARRAY<FLOAT64> as a Go slice. Used
// by every vector-distance function.
func vectorFromValue(v value.Value) ([]float64, error) {
	arr, err := v.ToArray()
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(arr.Values))
	for i, e := range arr.Values {
		if e == nil {
			return nil, fmt.Errorf("vector element %d is NULL", i)
		}
		f, err := e.ToFloat64()
		if err != nil {
			return nil, err
		}
		out[i] = f
	}
	return out, nil
}

// vectorPair extracts two vectors for a distance function. Dense
// vectors are ARRAY<FLOAT64>; sparse vectors are
// ARRAY<STRUCT<key INT64|STRING, value FLOAT64>>, aligned on the union of
// their keys with missing entries as 0 and repeated keys an error
// (mathematical_functions.md EUCLIDEAN_DISTANCE; array_functions.test,
// euclidian_distance_shuffled_input_strkey).
func vectorPair(x, y value.Value) ([]float64, []float64, error) {
	if isSparseVector(x) || isSparseVector(y) {
		mx, keys, err := sparseVector(x, nil)
		if err != nil {
			return nil, nil, err
		}
		my, keys, err := sparseVector(y, keys)
		if err != nil {
			return nil, nil, err
		}
		a := make([]float64, len(keys))
		b := make([]float64, len(keys))
		for i, k := range keys {
			a[i], b[i] = mx[k], my[k]
		}
		return a, b, nil
	}
	a, err := vectorFromValue(x)
	if err != nil {
		return nil, nil, err
	}
	b, err := vectorFromValue(y)
	if err != nil {
		return nil, nil, err
	}
	return a, b, nil
}

func isSparseVector(v value.Value) bool {
	arr, err := v.ToArray()
	if err != nil {
		return false
	}
	for _, e := range arr.Values {
		if e != nil {
			_, ok := e.(*value.StructValue)
			return ok
		}
	}
	return false
}

func sparseVector(v value.Value, keys []string) (map[string]float64, []string, error) {
	arr, err := v.ToArray()
	if err != nil {
		return nil, nil, err
	}
	out := make(map[string]float64, len(arr.Values))
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		seen[k] = true
	}
	for i, e := range arr.Values {
		st, ok := e.(*value.StructValue)
		if !ok || len(st.Values) != 2 || st.Values[0] == nil || st.Values[1] == nil {
			return nil, nil, fmt.Errorf("vector element %d is NULL or not a key/value pair", i)
		}
		k := fmt.Sprintf("%T:%v", st.Values[0], st.Values[0].Interface())
		if _, dup := out[k]; dup {
			return nil, nil, fmt.Errorf("vector has a repeated key")
		}
		f, err := st.Values[1].ToFloat64()
		if err != nil {
			return nil, nil, err
		}
		out[k] = f
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return out, keys, nil
}
