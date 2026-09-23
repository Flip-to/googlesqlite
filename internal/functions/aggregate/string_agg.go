package aggregate

import (
	"strings"
	"sync"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

type STRING_AGG struct {
	values []*helper.OrderedValue
	delim  string
	// isBytes is set when aggregating BYTES; the result is then BYTES.
	isBytes bool
	opt     *helper.Option
	once    sync.Once
}

func (f *STRING_AGG) Step(v value.Value, delim string, opt *helper.Option) error {
	if v == nil {
		return nil
	}
	f.once.Do(func() {
		// The binder supplies "," when the delimiter is omitted; an
		// explicit empty delimiter is kept.
		f.delim = delim
		f.opt = opt
	})
	if _, ok := v.(value.BytesValue); ok {
		f.isBytes = true
	}
	f.values = append(f.values, &helper.OrderedValue{
		OrderBy: opt.OrderBy,
		Value:   v,
	})
	return nil
}

func (f *STRING_AGG) Done() (value.Value, error) {
	f.values = helper.SortAggregatedValues(f.values, f.opt)

	if f.opt != nil && f.opt.Limit != nil {
		minLen := min(*f.opt.Limit, int64(len(f.values)))
		f.values = f.values[:minLen]
	}
	values := make([]string, 0, len(f.values))

	foundNotNilValue := false
	for _, v := range f.values {
		text, err := value.RawText(v.Value)
		if err != nil {
			return nil, err
		}
		foundNotNilValue = true
		values = append(values, text)
	}
	if !foundNotNilValue {
		return nil, nil
	}
	if f.isBytes {
		return value.BytesValue(strings.Join(values, f.delim)), nil
	}
	return value.StringValue(strings.Join(values, f.delim)), nil
}
