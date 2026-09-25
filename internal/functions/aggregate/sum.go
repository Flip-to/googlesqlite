package aggregate

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// SUM sums exactly and reports overflow; see helper.Summer.
type SUM struct {
	s helper.Summer
}

func (f *SUM) Step(v value.Value, _ *helper.Option) error {
	return f.s.Add(v)
}

func (f *SUM) Done() (value.Value, error) {
	return f.s.Sum("SUM")
}
