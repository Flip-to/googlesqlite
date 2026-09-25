package window

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// Native, frame-driven statistical window aggregators. Step appends
// the row to a buffer, Inverse drops the oldest row, and Done computes
// the result over the frame with the same moments code as the plain
// aggregates (helper.MomentSeries / helper.MomentPairs), so NULL, NaN,
// infinities, NUMERIC precision and minimum row counts behave the same
// in both.

type statWindow struct {
	rows        [][]value.Value // one entry per frame row: its inputs
	distinct    bool
	ignoreNulls bool
	once        bool
}

func (w *statWindow) step(args ...any) error {
	values, err := value.ConvertArgs(args...)
	if err != nil {
		return err
	}
	values, opt := helper.ParseOptions(values...)
	values, _ = parseWindowOptions(values...)
	if !w.once {
		w.distinct = opt.Distinct
		w.ignoreNulls = opt.IgnoreNulls
		w.once = true
	}
	w.rows = append(w.rows, values)
	return nil
}

func (w *statWindow) popFront() {
	if len(w.rows) > 0 {
		w.rows = w.rows[1:]
	}
}

// active returns the frame rows whose inputs are all non-NULL, with
// DISTINCT applied to the first input when requested.
func (w *statWindow) active(n int) ([][]value.Value, error) {
	seen := map[string]struct{}{}
	var out [][]value.Value
	for _, r := range w.rows {
		if len(r) < n {
			continue
		}
		null := false
		for _, v := range r[:n] {
			if v == nil {
				null = true
			}
		}
		if null {
			continue
		}
		if w.distinct {
			key, err := value.DistinctKey(r[0])
			if err != nil {
				return nil, err
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, r[:n])
	}
	return out, nil
}

func (w *statWindow) series() (*helper.MomentSeries, error) {
	rows, err := w.active(1)
	if err != nil {
		return nil, err
	}
	var s helper.MomentSeries
	for _, r := range rows {
		if err := s.Add(r[0]); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

func (w *statWindow) pairs() (*helper.MomentPairs, error) {
	rows, err := w.active(2)
	if err != nil {
		return nil, err
	}
	var p helper.MomentPairs
	for _, r := range rows {
		// CORR(y, x) and COVAR_*(y, x): the argument order does not
		// matter for the result.
		if err := p.Add(r[0], r[1]); err != nil {
			return nil, err
		}
	}
	return &p, nil
}

func encodeStat(v value.Value) (any, error) {
	if v == nil {
		return nil, nil
	}
	return value.EncodeValue(v)
}

type varianceWindow struct {
	statWindow
	ddof int
	sqrt bool
}

func (a *varianceWindow) Step(args ...any) error { return a.step(args...) }
func (a *varianceWindow) Inverse(_ ...any) error { a.popFront(); return nil }
func (a *varianceWindow) Done() (any, error) {
	s, err := a.series()
	if err != nil {
		return nil, err
	}
	return encodeStat(s.Variance(a.ddof, a.sqrt))
}

func NewStddevPopWindowNative() func() any {
	return func() any { return &varianceWindow{ddof: 0, sqrt: true} }
}

func NewStddevSampWindowNative() func() any {
	return func() any { return &varianceWindow{ddof: 1, sqrt: true} }
}

func NewVarPopWindowNative() func() any {
	return func() any { return &varianceWindow{ddof: 0} }
}

func NewVarSampWindowNative() func() any {
	return func() any { return &varianceWindow{ddof: 1} }
}

type covarianceWindow struct {
	statWindow
	ddof int
	corr bool
}

func (a *covarianceWindow) Step(args ...any) error { return a.step(args...) }
func (a *covarianceWindow) Inverse(_ ...any) error { a.popFront(); return nil }
func (a *covarianceWindow) Done() (any, error) {
	p, err := a.pairs()
	if err != nil {
		return nil, err
	}
	if a.corr {
		return encodeStat(p.Correlation())
	}
	return encodeStat(p.Covariance(a.ddof))
}

func NewCorrWindowNative() func() any {
	return func() any { return &covarianceWindow{corr: true} }
}

func NewCovarPopWindowNative() func() any {
	return func() any { return &covarianceWindow{ddof: 0} }
}

func NewCovarSampWindowNative() func() any {
	return func() any { return &covarianceWindow{ddof: 1} }
}

// Names used by the package's unit tests.
type (
	stddevPopWindow  = varianceWindow
	stddevSampWindow = varianceWindow
	varPopWindow     = varianceWindow
	varSampWindow    = varianceWindow
	corrWindow       = covarianceWindow
	covarPopWindow   = covarianceWindow
	covarSampWindow  = covarianceWindow
)
