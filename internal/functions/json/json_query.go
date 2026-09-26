package json

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/goccy/go-json"
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func JSON_QUERY(v, path string) (value.Value, error) {
	p, err := createPath(path)
	if err != nil {
		return nil, err
	}
	if p.UsedSingleQuotePathSelector() {
		return nil, fmt.Errorf("JSON_QUERY: doesn't use single quote path selector")
	}
	// Invalid JSON input gives NULL, as BigQuery does for STRING input.
	if !json.Valid([]byte(v)) {
		return nil, nil
	}
	extracted, err := p.Extract([]byte(v))
	if err != nil {
		return nil, err
	}
	if len(extracted) == 0 {
		return nil, nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, extracted[0]); err != nil {
		return nil, fmt.Errorf("failed to format json %q: %w", extracted[0], err)
	}
	jsonValue := buf.String()
	if jsonValue == "null" {
		return nil, nil
	}
	return value.JsonValue(jsonValue), nil
}

var BindJsonQuery = helper.Scalar2(func(a, b value.Value) (value.Value, error) {
	v, err := a.ToString()
	if err != nil {
		return nil, err
	}
	path, err := b.ToString()
	if err != nil {
		return nil, err
	}
	out, err := JSON_QUERY(v, path)
	if err != nil {
		return nil, err
	}
	if out != nil {
		return stringResultForStringInput(a, out), nil
	}
	// For JSON input a matched JSON null is JSON 'null', not SQL NULL
	// (json_functions.md JSON_QUERY); STRING input maps it to NULL.
	if _, isJSON := a.(value.JsonValue); isJSON && jsonPathMatchesNull(v, path) {
		return value.JsonValue("null"), nil
	}
	return nil, nil
})

// jsonPathMatchesNull reports whether path selects a JSON null in v.
func jsonPathMatchesNull(v, path string) bool {
	p, err := createPath(path)
	if err != nil {
		return false
	}
	extracted, err := p.Extract([]byte(v))
	if err != nil || len(extracted) == 0 {
		return false
	}
	return strings.TrimSpace(string(extracted[0])) == "null"
}

// stringResultForStringInput turns the JSON text extracted from a
// STRING argument into a STRING value: JSON_QUERY / JSON_EXTRACT of a
// STRING return STRING, so TO_JSON_STRING quotes the text and
// FORMAT('%T') prints a string literal (verified on BigQuery
// 2026-09-25: TO_JSON_STRING(JSON_QUERY('{"a":"x"}', '$.a')) is
// "\"x\"").
func stringResultForStringInput(input, out value.Value) value.Value {
	if _, isJSON := input.(value.JsonValue); isJSON {
		return out
	}
	if j, ok := out.(value.JsonValue); ok {
		return value.StringValue(j)
	}
	return out
}
