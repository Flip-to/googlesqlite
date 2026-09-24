package longtail

import (
	"encoding/json"
	"fmt"
	"math/big"
	"unicode/utf8"

	"github.com/goccy/googlesqlite/internal/value"
)

// TypeParamSpec describes the limits a parameterized cast target
// imposes: MaxLength applies to a STRING(L) / BYTES(L) value,
// Precision / Scale to a NUMERIC(P[, S]) / BIGNUMERIC(P[, S]) value
// (MaxPrecision for BIGNUMERIC(MAX, S)), Children to the element of an
// ARRAY (one child) or the fields of a STRUCT (one child per field;
// nil entries carry no limit).
type TypeParamSpec struct {
	MaxLength    int64            `json:"n,omitempty"`
	Numeric      bool             `json:"num,omitempty"`
	Precision    int64            `json:"p,omitempty"`
	Scale        int64            `json:"s,omitempty"`
	MaxPrecision bool             `json:"pmax,omitempty"`
	Children     []*TypeParamSpec `json:"c,omitempty"`
}

// BindCheckTypeParameters applies the type parameters of a cast target
// to the cast result: CAST(x AS STRING(5)) fails when x is longer than 5
// characters, and CAST(x AS NUMERIC(P, S)) rounds x to S fractional
// digits and fails when the result needs more than P digits.
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
	return applyTypeParams(args[0], &spec)
}

func applyTypeParams(v value.Value, spec *TypeParamSpec) (value.Value, error) {
	if v == nil || spec == nil {
		return v, nil
	}
	switch vv := v.(type) {
	case value.StringValue:
		if spec.MaxLength > 0 {
			if n := int64(utf8.RuneCountInString(string(vv))); n > spec.MaxLength {
				return nil, fmt.Errorf("STRING(%d) has maximum length %d but got a value with length %d", spec.MaxLength, spec.MaxLength, n)
			}
		}
	case value.BytesValue:
		if spec.MaxLength > 0 {
			if n := int64(len(vv)); n > spec.MaxLength {
				return nil, fmt.Errorf("BYTES(%d) has maximum length %d but got a value with length %d", spec.MaxLength, spec.MaxLength, n)
			}
		}
	case *value.NumericValue:
		if spec.Numeric {
			return applyNumericTypeParams(vv, spec)
		}
	case *value.ArrayValue:
		if len(spec.Children) == 1 {
			out := &value.ArrayValue{Values: make([]value.Value, len(vv.Values))}
			for i, e := range vv.Values {
				c, err := applyTypeParams(e, spec.Children[0])
				if err != nil {
					return nil, err
				}
				out.Values[i] = c
			}
			return out, nil
		}
	case *value.StructValue:
		out := *vv
		out.Values = make([]value.Value, len(vv.Values))
		out.M = make(map[string]value.Value, len(vv.Values))
		for i, f := range vv.Values {
			c := f
			if i < len(spec.Children) {
				var err error
				if c, err = applyTypeParams(f, spec.Children[i]); err != nil {
					return nil, err
				}
			}
			out.Values[i] = c
			if i < len(vv.Keys) {
				out.M[vv.Keys[i]] = c
			}
		}
		return &out, nil
	}
	return v, nil
}

// applyNumericTypeParams rounds half away from zero to the scale and
// checks the precision (cast_function.test,
// cast_numeric_type_parameters_invalid_input and
// cast_struct_type_parameters_valid_input).
func applyNumericTypeParams(n *value.NumericValue, spec *TypeParamSpec) (value.Value, error) {
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(spec.Scale), nil)
	scaled := new(big.Rat).Mul(new(big.Rat).Abs(n.Rat), new(big.Rat).SetInt(pow))
	q, rem := new(big.Int).QuoRem(scaled.Num(), scaled.Denom(), new(big.Int))
	if new(big.Int).Mul(rem, big.NewInt(2)).Cmp(scaled.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if !spec.MaxPrecision {
		limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(spec.Precision), nil)
		if q.Cmp(limit) >= 0 {
			name := "NUMERIC"
			if n.IsBigNumeric {
				name = "BIGNUMERIC"
			}
			typ := fmt.Sprintf("%s(%d)", name, spec.Precision)
			if spec.Scale != 0 {
				typ = fmt.Sprintf("%s(%d, %d)", name, spec.Precision, spec.Scale)
			}
			bound := new(big.Rat).SetFrac(new(big.Int).Sub(limit, big.NewInt(1)), pow).FloatString(int(spec.Scale))
			return nil, fmt.Errorf("%s has precision %d and scale %d but got a value that is not in range of [-%s, %s]",
				typ, spec.Precision, spec.Scale, bound, bound)
		}
	}
	r := new(big.Rat).SetFrac(q, pow)
	if n.Sign() < 0 {
		r.Neg(r)
	}
	return &value.NumericValue{Rat: r, IsBigNumeric: n.IsBigNumeric}, nil
}
