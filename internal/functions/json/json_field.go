package json

import (
	"fmt"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func JSON_FIELD(v, fieldName string) (value.Value, error) {
	p := &gsqlPath{steps: []pathStep{{name: fieldName}}}
	extracted, err := p.Extract([]byte(v))
	if err != nil {
		return nil, err
	}
	if len(extracted) == 0 {
		return nil, nil
	}
	return value.JsonValue(string(extracted[0])), nil
}

var BindJsonField = helper.Scalar2(func(a, b value.Value) (value.Value, error) {
	jsonValue, err := a.ToString()
	if err != nil {
		return nil, err
	}
	if _, ok := b.(value.StringValue); !ok {
		return nil, fmt.Errorf("JSON field name must be STRING, got %T", b)
	}
	fieldName, err := b.ToString()
	if err != nil {
		return nil, err
	}
	return JSON_FIELD(jsonValue, fieldName)
})
