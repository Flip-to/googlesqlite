package json

import (
	stdjson "encoding/json"
	"fmt"
	"strings"

	"github.com/goccy/googlesqlite/internal/value"
)

// JSON_ARRAY builds a JSON array containing the supplied values
// (one element per argument). NULL arguments become JSON null.
func JSON_ARRAY(args ...value.Value) (value.Value, error) {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if a == nil {
			parts = append(parts, "null")
			continue
		}
		s, err := a.ToJSON()
		if err != nil {
			return nil, err
		}
		parts = append(parts, s)
	}
	return value.JsonValue("[" + strings.Join(parts, ",") + "]"), nil
}

// JSON_OBJECT(key, value, ...) builds a JSON object from
// alternating key/value pairs. Keys must be STRING, values are
// converted via ToJSON. When a key repeats, the first occurrence wins
// (json_functions.md, JSON_OBJECT).
func JSON_OBJECT(args ...value.Value) (value.Value, error) {
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("JSON_OBJECT: needs an even argument count, got %d", len(args))
	}
	keys := make([]value.Value, 0, len(args)/2)
	vals := make([]value.Value, 0, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		keys = append(keys, args[i])
		vals = append(vals, args[i+1])
	}
	return buildJSONObject(keys, vals)
}

func buildJSONObject(keys, vals []value.Value) (value.Value, error) {
	parts := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for i, k := range keys {
		if k == nil {
			return nil, fmt.Errorf("Invalid input to JSON_OBJECT: A key cannot be NULL") //nolint:staticcheck // BigQuery's error text, checked by the compliance fixtures
		}
		key, err := k.ToString()
		if err != nil {
			return nil, err
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		valStr := "null"
		if vals[i] != nil {
			s, err := vals[i].ToJSON()
			if err != nil {
				return nil, err
			}
			valStr = s
		}
		kb, err := stdjson.Marshal(key)
		if err != nil {
			return nil, err
		}
		parts = append(parts, string(kb)+":"+valStr)
	}
	return value.JsonValue("{" + strings.Join(parts, ",") + "}"), nil
}

// JSON_OBJECT_ARRAYS is JSON_OBJECT(ARRAY<STRING> keys, ARRAY<T> values).
func JSON_OBJECT_ARRAYS(args ...value.Value) (value.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("JSON_OBJECT: expected 2 array arguments, got %d", len(args))
	}
	if args[0] == nil {
		return nil, fmt.Errorf("Invalid input to JSON_OBJECT: The keys array cannot be NULL") //nolint:staticcheck // BigQuery's error text, checked by the compliance fixtures
	}
	if args[1] == nil {
		return nil, fmt.Errorf("Invalid input to JSON_OBJECT: The values array cannot be NULL") //nolint:staticcheck // BigQuery's error text, checked by the compliance fixtures
	}
	keys, err := args[0].ToArray()
	if err != nil {
		return nil, err
	}
	vals, err := args[1].ToArray()
	if err != nil {
		return nil, err
	}
	if len(keys.Values) != len(vals.Values) {
		return nil, fmt.Errorf("Invalid input to JSON_OBJECT: The number of keys and values must match") //nolint:staticcheck // BigQuery's error text, checked by the compliance fixtures
	}
	return buildJSONObject(keys.Values, vals.Values)
}
