package json

import (
	"bytes"
	"fmt"

	gjson "github.com/goccy/go-json"

	"github.com/goccy/googlesqlite/internal/value"
)

// JSON_FLATTEN returns every non-array value that is either the input
// itself or reachable from it through one or more consecutively nested
// arrays, as an ARRAY<JSON>. `[[[1]], 2, [3]]` gives `[1, 2, 3]`; an
// array inside an object is left alone (`{"a": [[1]]}` gives
// `[{"a":[[1]]}]`). json_functions.md, JSON_FLATTEN; verified on
// BigQuery.
func JSON_FLATTEN(args ...value.Value) (value.Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return nil, fmt.Errorf("JSON_FLATTEN: invalid number of arguments: got %d, want between 1 and 2", len(args))
	}
	if args[0] == nil {
		return nil, nil
	}
	body, err := args[0].ToJSON()
	if err != nil {
		return nil, err
	}
	dec := gjson.NewDecoder(bytes.NewReader([]byte(body)))
	dec.UseNumber()
	var node any
	if err := dec.Decode(&node); err != nil {
		return nil, err
	}
	out := &value.ArrayValue{}
	var walk func(e any) error
	walk = func(e any) error {
		if arr, ok := e.([]any); ok {
			for _, ee := range arr {
				if err := walk(ee); err != nil {
					return err
				}
			}
			return nil
		}
		b, err := gjson.Marshal(e)
		if err != nil {
			return err
		}
		out.Values = append(out.Values, value.JsonValue(b))
		return nil
	}
	if err := walk(node); err != nil {
		return nil, err
	}
	return out, nil
}
