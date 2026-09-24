package longtail

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/goccy/googlesqlite/internal/value"
)

// TypeParamSpec describes the length limits a parameterized cast target
// imposes: MaxLength applies to a STRING(L) / BYTES(L) value, Children
// to the element of an ARRAY (one child) or the fields of a STRUCT
// (one child per field; nil entries carry no limit).
type TypeParamSpec struct {
	MaxLength int64            `json:"n,omitempty"`
	Children  []*TypeParamSpec `json:"c,omitempty"`
}

// BindCheckTypeParameters validates a cast result against the type
// parameters of its target type, e.g. CAST(x AS STRING(5)) fails when
// x is longer than 5 characters. It returns the value unchanged.
func BindCheckTypeParameters(args ...value.Value) (value.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("CHECK_TYPE_PARAMETERS: invalid number of arguments: got %d, want 2", len(args))
	}
	if args[0] == nil || args[1] == nil {
		return args[0], nil
	}
	raw, err := args[1].ToString()
	if err != nil {
		return nil, err
	}
	var spec TypeParamSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return nil, fmt.Errorf("CHECK_TYPE_PARAMETERS: invalid spec: %w", err)
	}
	if err := checkTypeParams(args[0], &spec); err != nil {
		return nil, err
	}
	return args[0], nil
}

func checkTypeParams(v value.Value, spec *TypeParamSpec) error {
	if v == nil || spec == nil {
		return nil
	}
	switch vv := v.(type) {
	case value.StringValue:
		if spec.MaxLength > 0 {
			if n := int64(utf8.RuneCountInString(string(vv))); n > spec.MaxLength {
				return fmt.Errorf("STRING(%d) has maximum length %d but got a value with length %d", spec.MaxLength, spec.MaxLength, n)
			}
		}
	case value.BytesValue:
		if spec.MaxLength > 0 {
			if n := int64(len(vv)); n > spec.MaxLength {
				return fmt.Errorf("BYTES(%d) has maximum length %d but got a value with length %d", spec.MaxLength, spec.MaxLength, n)
			}
		}
	case *value.ArrayValue:
		if len(spec.Children) == 1 {
			for _, e := range vv.Values {
				if err := checkTypeParams(e, spec.Children[0]); err != nil {
					return err
				}
			}
		}
	case *value.StructValue:
		for i, f := range vv.Values {
			if i < len(spec.Children) {
				if err := checkTypeParams(f, spec.Children[i]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
