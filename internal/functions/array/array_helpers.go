package array

import (
	"fmt"

	"github.com/goccy/googlesqlite/internal/value"
)

func generateArray(start, end, step value.Value) (value.Value, error) {
	if start == nil || end == nil || step == nil {
		return nil, nil
	}
	isLT, err := start.LTE(end)
	if err != nil {
		return nil, err
	}
	arr := &value.ArrayValue{}
	// A zero step never reaches end; without this check the loop below
	// appends forever and exhausts memory (compliance
	// array_functions.test: "Sequence step cannot be 0.").
	if isZero, err := step.EQ(value.IntValue(0)); err == nil && isZero {
		return nil, fmt.Errorf("sequence step cannot be 0")
	}
	isPositiveStepValue, err := step.GT(value.IntValue(0))
	if err != nil {
		return nil, err
	}
	if isLT && !isPositiveStepValue {
		// start less than end and step is negative value
		return arr, nil
	} else if !isLT && isPositiveStepValue {
		// start greater than end and step is positive value
		return arr, nil
	}
	cur := start
	for {
		if len(arr.Values) >= maxGeneratedArrayLen {
			return nil, fmt.Errorf("GENERATE_ARRAY: result exceeds %d elements", maxGeneratedArrayLen)
		}
		arr.Values = append(arr.Values, cur)
		after, err := cur.Add(step)
		if err != nil {
			return nil, err
		}
		if isLT {
			cond, err := after.LTE(end)
			if err != nil {
				return nil, err
			}
			if !cond {
				break
			}
		} else {
			cond, err := after.GTE(end)
			if err != nil {
				return nil, err
			}
			if !cond {
				break
			}
		}
		cur = after
	}
	return arr, nil
}

// maxGeneratedArrayLen bounds GENERATE_ARRAY and friends so a bad range
// fails with an error instead of consuming all memory.
const maxGeneratedArrayLen = 10_000_000
