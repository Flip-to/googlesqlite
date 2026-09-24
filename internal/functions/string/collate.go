package string

import (
	"fmt"
	"strings"

	"github.com/goccy/googlesqlite/internal/value"
	"golang.org/x/text/language"
)

func COLLATE(v, spec string) (value.Value, error) {
	if spec == "" {
		return value.StringValue(v), nil
	}
	splitted := strings.SplitN(spec, ":", 2)
	if len(splitted) == 0 {
		return nil, fmt.Errorf("COLLATE: unexpected spec literal %s", spec)
	}
	tag := language.Make(splitted[0])
	caseInsensitive := false
	if len(splitted) == 2 {
		switch splitted[1] {
		case "ci":
			caseInsensitive = true
		case "cs":
			caseInsensitive = false
		default:
			return nil, fmt.Errorf("COLLATE: unsupported collation attribute %s", splitted[1])
		}
	}
	// COLLATE only attaches a collation annotation; the value itself
	// is unchanged. Collation-sensitive operations are lowered by the
	// formatter onto internal/functions/collation.
	_ = tag
	_ = caseInsensitive
	return value.StringValue(v), nil
}

func BindCollate(args ...value.Value) (value.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("COLLATE: invalid number of arguments: got %d, want 2", len(args))
	}
	if args[0] == nil {
		return nil, nil
	}
	if args[1] == nil {
		return nil, fmt.Errorf("COLLATE: collation_specification must be string literal")
	}
	value, err := args[0].ToString()
	if err != nil {
		return nil, fmt.Errorf("COLLATE: value must be string: %w", err)
	}
	spec, err := args[1].ToString()
	if err != nil {
		return nil, fmt.Errorf("COLLATE: collation_specification must be string literal: %w", err)
	}
	return COLLATE(value, spec)
}
