package json

import (
	"fmt"
	"strings"

	"github.com/goccy/go-json"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func JSON_VALUE_ARRAY(v, path string) (value.Value, error) {
	p, err := createPath(path)
	if err != nil {
		return nil, err
	}
	if p.UsedSingleQuotePathSelector() {
		return nil, fmt.Errorf("JSON_VALUE_ARRAY: doesn't use single quote path selector")
	}
	if !json.Valid([]byte(v)) {
		// invalid json content is ignored.
		return nil, nil
	}
	extracted, err := p.Extract([]byte(v))
	if err != nil || len(extracted) == 0 {
		return nil, nil
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(extracted[0], &elems); err != nil || elems == nil {
		return nil, nil
	}
	// Each number keeps its source text: [1, 2.50] is ["1", "2.50"]
	// (flipto-dbt probe json_value_array-8983.2).
	ret := &value.ArrayValue{}
	for _, raw := range elems {
		text := strings.TrimSpace(string(raw))
		if text != "" && (text[0] == '{' || text[0] == '[') {
			return nil, nil
		}
		elem, err := scalarFromRawJSON(raw)
		if err != nil {
			return nil, err
		}
		ret.Values = append(ret.Values, elem)
	}
	return ret, nil
}

var BindJsonValueArray = helper.Scalar2(func(a, b value.Value) (value.Value, error) {
	v, err := a.ToString()
	if err != nil {
		return nil, err
	}
	path, err := b.ToString()
	if err != nil {
		return nil, err
	}
	return JSON_VALUE_ARRAY(v, path)
})
