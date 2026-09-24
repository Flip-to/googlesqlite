package json

import (
	"bytes"
	"fmt"

	"github.com/goccy/go-json"
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func JSON_QUERY_ARRAY(v, path string) (value.Value, error) {
	p, err := createPath(path)
	if err != nil {
		return nil, err
	}
	if p.UsedSingleQuotePathSelector() {
		return nil, fmt.Errorf("JSON_QUERY_ARRAY: doesn't use single quote path selector")
	}
	extracted, err := p.Extract([]byte(v))
	if err != nil {
		return nil, err
	}
	if len(extracted) == 0 {
		return nil, nil
	}
	content := bytes.TrimLeft(extracted[0], " ")
	if len(content) != 0 && content[0] != '[' {
		// not array content
		return nil, nil
	}
	var values []json.RawMessage
	if err := json.Unmarshal(content, &values); err != nil {
		return nil, err
	}
	ret := &value.ArrayValue{}
	for _, val := range values {
		jsonValue := string(val)
		if jsonValue == "null" {
			ret.Values = append(ret.Values, nil)
		} else {
			// Elements come back compacted: '{"c": 2}' is '{"c":2}'
			// (strings.test, json_query_array).
			var buf bytes.Buffer
			if err := json.Compact(&buf, val); err == nil {
				jsonValue = buf.String()
			}
			ret.Values = append(ret.Values, value.JsonValue(jsonValue))
		}
	}
	return ret, nil
}

var BindJsonQueryArray = helper.Scalar2(func(a, b value.Value) (value.Value, error) {
	v, err := a.ToString()
	if err != nil {
		return nil, err
	}
	path, err := b.ToString()
	if err != nil {
		return nil, err
	}
	out, err := JSON_QUERY_ARRAY(v, path)
	if err != nil || out == nil {
		return out, err
	}
	// A JSON null element stays 'null' (JSON 'null' for JSON input, the
	// string "null" for STRING input), never SQL NULL (strings.test,
	// json_query_array; verified against BigQuery).
	keepJSONNullElements(out)
	if _, isJSON := a.(value.JsonValue); !isJSON {
		// STRING input yields ARRAY<STRING>.
		jsonElementsToStrings(out)
	}
	return out, nil
})
