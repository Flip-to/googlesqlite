package json

import (
	"fmt"
	"strings"

	"github.com/goccy/go-json"
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func JSON_VALUE(v, path string) (value.Value, error) {
	p, err := createPath(path)
	if err != nil {
		return nil, err
	}
	if p.UsedSingleQuotePathSelector() {
		return nil, fmt.Errorf("JSON_VALUE: doesn't use single quote path selector")
	}
	// Invalid JSON input gives NULL, as BigQuery does for STRING input.
	if !json.Valid([]byte(v)) {
		return nil, nil
	}
	extracted, err := p.Extract([]byte(v))
	if err != nil || len(extracted) == 0 {
		return nil, nil
	}
	return scalarFromRawJSON(extracted[0])
}

// scalarFromRawJSON converts a JSON scalar to JSON_VALUE's STRING result,
// keeping a number's original text (JSON_VALUE('{"a": 1.0}', '$.a') is
// '1.0', not '1'). Objects, arrays and null give NULL.
func scalarFromRawJSON(raw json.RawMessage) (value.Value, error) {
	text := strings.TrimSpace(string(raw))
	switch {
	case text == "" || text == "null" || text[0] == '{' || text[0] == '[':
		return nil, nil
	case text[0] == '"':
		var s string
		if err := json.Unmarshal([]byte(text), &s); err != nil {
			return nil, err
		}
		return value.StringValue(s), nil
	}
	return value.StringValue(text), nil
}

var BindJsonValue = helper.Scalar2(func(a, b value.Value) (value.Value, error) {
	v, err := a.ToString()
	if err != nil {
		return nil, err
	}
	path, err := b.ToString()
	if err != nil {
		return nil, err
	}
	return JSON_VALUE(v, path)
})
