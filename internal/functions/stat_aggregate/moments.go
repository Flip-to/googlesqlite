package stat_aggregate

import (
	"math"
	"math/big"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// series collects the non-NULL inputs of a statistical aggregate.
// NUMERIC and BIGNUMERIC inputs are also kept as exact rationals so the
// moments can be computed without losing precision; DOUBLE inputs use
// a scaled two-pass algorithm so extreme values do not overflow.
type series struct {
	f     []float64
	r     []*big.Rat
	exact bool
}

func (s *series) add(v value.Value) error {
	f, err := v.ToFloat64()
	if err != nil {
		return err
	}
	if len(s.f) == 0 {
		s.exact = true
	}
	if n, ok := v.(*value.NumericValue); ok && s.exact {
		r, err := n.ToRat()
		if err != nil {
			return err
		}
		s.r = append(s.r, r)
	} else {
		s.exact = false
	}
	s.f = append(s.f, f)
	return nil
}

func (s *series) n() int { return len(s.f) }

func (s *series) nonFinite() bool {
	for _, x := range s.f {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return true
		}
	}
	return false
}

// scaled returns the inputs divided by their largest magnitude, and
// that magnitude. Working on values in [-1, 1] keeps sums of squares
// from overflowing.
func (s *series) scaled() ([]float64, float64) {
	var m float64
	for _, x := range s.f {
		m = math.Max(m, math.Abs(x))
	}
	if m == 0 {
		return make([]float64, len(s.f)), 0
	}
	out := make([]float64, len(s.f))
	for i, x := range s.f {
		out[i] = x / m
	}
	return out, m
}

func meanOf(xs []float64) float64 {
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func ratMean(rs []*big.Rat) *big.Rat {
	sum := new(big.Rat)
	for _, r := range rs {
		sum.Add(sum, r)
	}
	return sum.Quo(sum, new(big.Rat).SetInt64(int64(len(rs))))
}

// comoment returns sum((x-mean(x))*(y-mean(y))) for scaled DOUBLE
// inputs, or exactly for rational inputs.
func comomentFloat(xs, ys []float64) float64 {
	mx, my := meanOf(xs), meanOf(ys)
	var sum float64
	for i := range xs {
		sum += (xs[i] - mx) * (ys[i] - my)
	}
	return sum
}

func comomentRat(xs, ys []*big.Rat) *big.Rat {
	mx, my := ratMean(xs), ratMean(ys)
	sum := new(big.Rat)
	for i := range xs {
		dx := new(big.Rat).Sub(xs[i], mx)
		dy := new(big.Rat).Sub(ys[i], my)
		sum.Add(sum, dx.Mul(dx, dy))
	}
	return sum
}

func ratFloat(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}

// variance returns VAR_POP (ddof 0) or VAR_SAMP (ddof 1). It is NULL
// when fewer than ddof+1 values were seen, per
// statistical_aggregate_functions.md.
func (s *series) variance(ddof int, sqrt bool) value.Value {
	n := s.n()
	if n == 0 || n-ddof <= 0 {
		return nil
	}
	if s.nonFinite() {
		return value.FloatValue(math.NaN())
	}
	if s.exact {
		v := ratFloat(new(big.Rat).Quo(comomentRat(s.r, s.r), new(big.Rat).SetInt64(int64(n-ddof))))
		if sqrt {
			v = math.Sqrt(v)
		}
		return value.FloatValue(v)
	}
	ys, scale := s.scaled()
	v := comomentFloat(ys, ys) / float64(n-ddof)
	if sqrt {
		return value.FloatValue(math.Sqrt(v) * scale)
	}
	return value.FloatValue(v * scale * scale)
}

// pairs collects the (x, y) rows where both inputs are non-NULL.
type pairs struct {
	x, y series
}

func (p *pairs) add(x, y value.Value) error {
	if x == nil || y == nil {
		return nil
	}
	if err := p.x.add(x); err != nil {
		return err
	}
	return p.y.add(y)
}

func (p *pairs) covariance(ddof int) value.Value {
	n := p.x.n()
	if n == 0 || n-ddof <= 0 {
		return nil
	}
	if p.x.nonFinite() || p.y.nonFinite() {
		return value.FloatValue(math.NaN())
	}
	if p.x.exact && p.y.exact {
		return value.FloatValue(ratFloat(new(big.Rat).Quo(comomentRat(p.x.r, p.y.r), new(big.Rat).SetInt64(int64(n-ddof)))))
	}
	xs, sx := p.x.scaled()
	ys, sy := p.y.scaled()
	return value.FloatValue(comomentFloat(xs, ys) / float64(n-ddof) * sx * sy)
}

func (p *pairs) correlation() value.Value {
	n := p.x.n()
	if n < 2 {
		return nil
	}
	if p.x.nonFinite() || p.y.nonFinite() {
		return value.FloatValue(math.NaN())
	}
	var cxy, cxx, cyy float64
	if p.x.exact && p.y.exact {
		cxy = ratFloat(comomentRat(p.x.r, p.y.r))
		cxx = ratFloat(comomentRat(p.x.r, p.x.r))
		cyy = ratFloat(comomentRat(p.y.r, p.y.r))
	} else {
		// Scaling cancels out of the correlation coefficient.
		xs, _ := p.x.scaled()
		ys, _ := p.y.scaled()
		cxy, cxx, cyy = comomentFloat(xs, ys), comomentFloat(xs, xs), comomentFloat(ys, ys)
	}
	if cxx == 0 || cyy == 0 {
		return value.FloatValue(math.NaN())
	}
	return value.FloatValue(cxy / (math.Sqrt(cxx) * math.Sqrt(cyy)))
}

type VAR_POP struct{ s series }

func (f *VAR_POP) Step(v value.Value, _ *helper.Option) error {
	if v == nil {
		return nil
	}
	return f.s.add(v)
}
func (f *VAR_POP) Done() (value.Value, error) { return f.s.variance(0, false), nil }

type VAR_SAMP struct{ s series }

func (f *VAR_SAMP) Step(v value.Value, _ *helper.Option) error {
	if v == nil {
		return nil
	}
	return f.s.add(v)
}
func (f *VAR_SAMP) Done() (value.Value, error) { return f.s.variance(1, false), nil }

type STDDEV_POP struct{ s series }

func (f *STDDEV_POP) Step(v value.Value, _ *helper.Option) error {
	if v == nil {
		return nil
	}
	return f.s.add(v)
}
func (f *STDDEV_POP) Done() (value.Value, error) { return f.s.variance(0, true), nil }

type STDDEV_SAMP struct{ s series }

func (f *STDDEV_SAMP) Step(v value.Value, _ *helper.Option) error {
	if v == nil {
		return nil
	}
	return f.s.add(v)
}
func (f *STDDEV_SAMP) Done() (value.Value, error) { return f.s.variance(1, true), nil }

type COVAR_POP struct{ p pairs }

func (f *COVAR_POP) Step(x, y value.Value, _ *helper.Option) error { return f.p.add(x, y) }
func (f *COVAR_POP) Done() (value.Value, error)                    { return f.p.covariance(0), nil }

type COVAR_SAMP struct{ p pairs }

func (f *COVAR_SAMP) Step(x, y value.Value, _ *helper.Option) error { return f.p.add(x, y) }
func (f *COVAR_SAMP) Done() (value.Value, error)                    { return f.p.covariance(1), nil }

type CORR struct{ p pairs }

func (f *CORR) Step(x, y value.Value, _ *helper.Option) error { return f.p.add(x, y) }
func (f *CORR) Done() (value.Value, error)                    { return f.p.correlation(), nil }
