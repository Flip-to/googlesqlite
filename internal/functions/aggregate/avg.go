package aggregate

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// AVG averages exactly; see helper.Summer.
type AVG struct {
	s helper.Summer
}

func (f *AVG) Step(v value.Value, _ *helper.Option) error {
	return f.s.Add(v)
}

func (f *AVG) Done() (value.Value, error) {
	return f.s.Avg()
}
