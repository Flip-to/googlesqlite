package window

import (
	"math"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// Window SUM / AVG / MIN / MAX for argument types SQLite's built-ins
// cannot handle correctly: DOUBLE (NaN is stored as TEXT, and a
// subtraction-based sliding SUM turns inf - inf into NaN) and
// NUMERIC / BIGNUMERIC (stored as encoded TEXT, so SQLite sums them
// as 0 and orders them as text). INT64 and STRING keep the built-ins.

// frameValues is the ordered list of values in the current frame;
// nil entries are NULL rows.
type frameValues struct {
	values []value.Value
}

func (f *frameValues) step(args ...any) (value.Value, error) {
	values, err := value.ConvertArgs(args...)
	if err != nil {
		return nil, err
	}
	values, _ = helper.ParseOptions(values...)
	values, _ = parseWindowOptions(values...)
	var v value.Value
	if len(values) > 0 {
		v = values[0]
	}
	f.values = append(f.values, v)
	return v, nil
}

func (f *frameValues) popFront() value.Value {
	if len(f.values) == 0 {
		return nil
	}
	v := f.values[0]
	f.values = f.values[1:]
	return v
}

type sumWindowNative struct {
	frame frameValues
	sum   helper.Summer
}

func NewSumWindowNative() func() any { return func() any { return &sumWindowNative{} } }

func (a *sumWindowNative) Step(args ...any) error {
	v, err := a.frame.step(args...)
	if err != nil {
		return err
	}
	return a.sum.Add(v)
}

func (a *sumWindowNative) Inverse(_ ...any) error { return a.sum.Remove(a.frame.popFront()) }

func (a *sumWindowNative) Done() (any, error) {
	v, err := a.sum.Sum("SUM")
	if err != nil || v == nil {
		return nil, err
	}
	return value.EncodeValue(v)
}

type avgWindowNative struct {
	frame frameValues
	sum   helper.Summer
}

func NewAvgWindowNative() func() any { return func() any { return &avgWindowNative{} } }

func (a *avgWindowNative) Step(args ...any) error {
	v, err := a.frame.step(args...)
	if err != nil {
		return err
	}
	return a.sum.Add(v)
}

func (a *avgWindowNative) Inverse(_ ...any) error { return a.sum.Remove(a.frame.popFront()) }

func (a *avgWindowNative) Done() (any, error) {
	v, err := a.sum.Avg()
	if err != nil || v == nil {
		return nil, err
	}
	return value.EncodeValue(v)
}

// minMaxWindowNative recomputes the extreme over the frame at Done.
// Per aggregate_functions.md MIN / MAX return NaN if any input is NaN.
type minMaxWindowNative struct {
	frame frameValues
	isMax bool
}

func NewMinWindowNative() func() any { return func() any { return &minMaxWindowNative{} } }
func NewMaxWindowNative() func() any { return func() any { return &minMaxWindowNative{isMax: true} } }

func (a *minMaxWindowNative) Step(args ...any) error {
	_, err := a.frame.step(args...)
	return err
}

func (a *minMaxWindowNative) Inverse(_ ...any) error { a.frame.popFront(); return nil }

func (a *minMaxWindowNative) Done() (any, error) {
	var best value.Value
	for _, v := range a.frame.values {
		if v == nil {
			continue
		}
		if f, ok := v.(value.FloatValue); ok && math.IsNaN(float64(f)) {
			return value.EncodeValue(v)
		}
		if best == nil {
			best = v
			continue
		}
		var better bool
		var err error
		if a.isMax {
			better, err = v.GT(best)
		} else {
			better, err = v.LT(best)
		}
		if err != nil {
			return nil, err
		}
		if better {
			best = v
		}
	}
	if best == nil {
		return nil, nil
	}
	return value.EncodeValue(best)
}
