package bit

import (
	"fmt"

	"github.com/goccy/googlesqlite/internal/value"
)

// BIT_RIGHT_SHIFT is a logical shift (operators.md, bitwise operators):
// no sign extension, so -7 >> 1 is 9223372036854775804. Shifting by 64
// or more gives 0, and a negative shift is an error.
func BIT_RIGHT_SHIFT(a, b value.Value) (value.Value, error) {
	va, err := a.ToInt64()
	if err != nil {
		return nil, err
	}
	vb, err := b.ToInt64()
	if err != nil {
		return nil, err
	}
	if vb < 0 {
		return nil, fmt.Errorf("bitwise shift by a negative number of bits: %d", vb)
	}
	if vb >= 64 {
		return value.IntValue(0), nil
	}
	return value.IntValue(int64(uint64(va) >> uint(vb))), nil
}
