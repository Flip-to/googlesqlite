package window

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// Native, frame-driven DISTINCT variants of the standard aggregate
// window functions. SQLite's built-in SUM/COUNT/AVG don't accept
// `DISTINCT` inside `OVER (...)`, so the predecessor handled
// `SUM(DISTINCT x) OVER (...)` through its per-output-row
// correlated-subquery emulation. Wiring these custom variants
// through `Conn.CreateWindowFunction` lets the SQLite frame engine
// drive Step / Inverse / Done while we apply DISTINCT semantics in
// Done over the active frame.

// distinctNumericWindow buffers the typed values currently in the
// frame (nil for NULL rows, so Inverse can drop the oldest row). It is
// the shared base for SUM_DISTINCT / COUNT_DISTINCT / AVG_DISTINCT.
type distinctNumericWindow struct {
	values []value.Value
}

func (d *distinctNumericWindow) appendValue(v value.Value) error {
	d.values = append(d.values, v)
	return nil
}

func (d *distinctNumericWindow) popFront() {
	if len(d.values) > 0 {
		d.values = d.values[1:]
	}
}

// activeDistinct returns the distinct non-NULL values in the frame.
// Values compare by type and canonical text, so 1 and 1.0 of different
// types stay apart and NaN equals NaN, as in GROUP BY.
func (d *distinctNumericWindow) activeDistinct() ([]value.Value, error) {
	seen := map[string]struct{}{}
	var out []value.Value
	for _, v := range d.values {
		if v == nil {
			continue
		}
		key, err := value.DistinctKey(v)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out, nil
}

func (d *distinctNumericWindow) summer() (*helper.Summer, error) {
	vals, err := d.activeDistinct()
	if err != nil {
		return nil, err
	}
	var s helper.Summer
	for _, v := range vals {
		if err := s.Add(v); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

// stepArgs strips option markers and forwards the value to appendValue.
func (d *distinctNumericWindow) stepArgs(stepArgs ...any) error {
	values, err := value.ConvertArgs(stepArgs...)
	if err != nil {
		return err
	}
	values, _ = helper.ParseOptions(values...)
	values, _ = parseWindowOptions(values...)
	if len(values) == 0 {
		return d.appendValue(nil)
	}
	return d.appendValue(values[0])
}

// --- SUM(DISTINCT x) OVER ... -------------------------------------

type sumDistinctWindow struct{ distinctNumericWindow }

func NewSumDistinctWindowNative() func() any {
	return func() any { return &sumDistinctWindow{} }
}

func (a *sumDistinctWindow) Step(args ...any) error { return a.stepArgs(args...) }
func (a *sumDistinctWindow) Inverse(_ ...any) error { a.popFront(); return nil }
func (a *sumDistinctWindow) Done() (any, error) {
	s, err := a.summer()
	if err != nil {
		return nil, err
	}
	v, err := s.Sum("SUM")
	if err != nil || v == nil {
		return nil, err
	}
	return value.EncodeValue(v)
}

// --- COUNT(DISTINCT x) OVER ... ------------------------------------

type countDistinctWindow struct{ distinctNumericWindow }

func NewCountDistinctWindowNative() func() any {
	return func() any { return &countDistinctWindow{} }
}

func (a *countDistinctWindow) Step(args ...any) error { return a.stepArgs(args...) }
func (a *countDistinctWindow) Inverse(_ ...any) error { a.popFront(); return nil }
func (a *countDistinctWindow) Done() (any, error) {
	vals, err := a.activeDistinct()
	if err != nil {
		return nil, err
	}
	return int64(len(vals)), nil
}

// --- AVG(DISTINCT x) OVER ... --------------------------------------

type avgDistinctWindow struct{ distinctNumericWindow }

func NewAvgDistinctWindowNative() func() any {
	return func() any { return &avgDistinctWindow{} }
}

func (a *avgDistinctWindow) Step(args ...any) error { return a.stepArgs(args...) }
func (a *avgDistinctWindow) Inverse(_ ...any) error { a.popFront(); return nil }
func (a *avgDistinctWindow) Done() (any, error) {
	s, err := a.summer()
	if err != nil {
		return nil, err
	}
	v, err := s.Avg()
	if err != nil || v == nil {
		return nil, err
	}
	return value.EncodeValue(v)
}
