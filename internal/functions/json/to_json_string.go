package json

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/goccy/googlesqlite/internal/value"
)

func TO_JSON_STRING(v value.Value, prettyPrint bool) (value.Value, error) {
	if v == nil {
		// BigQuery surfaces TO_JSON_STRING(NULL) as the JSON null
		// literal rather than SQL NULL.
		return value.StringValue("null"), nil
	}
	s, err := value.EncodeJSONString(v)
	if err != nil {
		return nil, err
	}
	// JSON values keep their input text (PARSE_JSON('{"a": 1}')), so
	// normalise the whole document: compact, or indented with two spaces
	// when pretty_print is true (json_functions.md TO_JSON_STRING).
	var buf bytes.Buffer
	if prettyPrint {
		err = json.Indent(&buf, []byte(s), "", "  ")
	} else {
		err = json.Compact(&buf, []byte(s))
	}
	if err != nil {
		return value.StringValue(s), nil
	}
	return value.StringValue(buf.String()), nil
}

func BindToJsonString(args ...value.Value) (value.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("TO_JSON_STRING: invalid number of arguments: got %d, want 1 or 2", len(args))
	}
	var prettyPrint bool
	if len(args) == 2 {
		if args[1] != nil {
			b, err := args[1].ToBool()
			if err != nil {
				return nil, err
			}
			prettyPrint = b
		}
	}
	return TO_JSON_STRING(args[0], prettyPrint)
}
