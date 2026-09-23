package stat_aggregate

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// Statistical aggregates; the moments are computed by
// helper.MomentSeries and helper.MomentPairs.

type VAR_POP struct{ s helper.MomentSeries }

func (f *VAR_POP) Step(v value.Value, _ *helper.Option) error {
	if v == nil {
		return nil
	}
	return f.s.Add(v)
}
func (f *VAR_POP) Done() (value.Value, error) { return f.s.Variance(0, false), nil }

type VAR_SAMP struct{ s helper.MomentSeries }

func (f *VAR_SAMP) Step(v value.Value, _ *helper.Option) error {
	if v == nil {
		return nil
	}
	return f.s.Add(v)
}
func (f *VAR_SAMP) Done() (value.Value, error) { return f.s.Variance(1, false), nil }

type STDDEV_POP struct{ s helper.MomentSeries }

func (f *STDDEV_POP) Step(v value.Value, _ *helper.Option) error {
	if v == nil {
		return nil
	}
	return f.s.Add(v)
}
func (f *STDDEV_POP) Done() (value.Value, error) { return f.s.Variance(0, true), nil }

type STDDEV_SAMP struct{ s helper.MomentSeries }

func (f *STDDEV_SAMP) Step(v value.Value, _ *helper.Option) error {
	if v == nil {
		return nil
	}
	return f.s.Add(v)
}
func (f *STDDEV_SAMP) Done() (value.Value, error) { return f.s.Variance(1, true), nil }

type COVAR_POP struct{ p helper.MomentPairs }

func (f *COVAR_POP) Step(x, y value.Value, _ *helper.Option) error { return f.p.Add(x, y) }
func (f *COVAR_POP) Done() (value.Value, error)                    { return f.p.Covariance(0), nil }

type COVAR_SAMP struct{ p helper.MomentPairs }

func (f *COVAR_SAMP) Step(x, y value.Value, _ *helper.Option) error { return f.p.Add(x, y) }
func (f *COVAR_SAMP) Done() (value.Value, error)                    { return f.p.Covariance(1), nil }

type CORR struct{ p helper.MomentPairs }

func (f *CORR) Step(x, y value.Value, _ *helper.Option) error { return f.p.Add(x, y) }
func (f *CORR) Done() (value.Value, error)                    { return f.p.Correlation(), nil }
