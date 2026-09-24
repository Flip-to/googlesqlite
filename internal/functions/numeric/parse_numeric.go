package numeric

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func PARSE_NUMERIC(numeric string) (value.Value, error) {
	r, err := parseNumericString(numeric, 9, false)
	if err != nil {
		return nil, err
	}
	return &value.NumericValue{Rat: r}, nil
}

// parseNumericString implements the input rules of PARSE_NUMERIC /
// PARSE_BIGNUMERIC (docs/third_party/googlesql-docs/conversion_functions.md):
// whitespace anywhere except between digits, commas in the integer part,
// a single sign before or after the number, an optional exponent, and
// rounding half away from zero to `scale` fractional digits. Values
// outside the target type's range are an error.
func parseNumericString(s string, scale int, isBig bool) (*big.Rat, error) {
	invalid := func() (*big.Rat, error) {
		return nil, fmt.Errorf("invalid numeric value: %q", s)
	}
	var (
		sign        byte
		intPart     strings.Builder
		fracPart    strings.Builder
		expPart     strings.Builder
		sawIntChars bool // any digit or comma before the point
		sawPoint    bool
		sawExp      bool
		sawNumber   bool // the number has started
		numberDone  bool // whitespace after the number was seen
		lastDigit   bool // previous non-space char was a digit
		spaceAfter  bool // whitespace after a digit
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			if lastDigit {
				spaceAfter = true
			}
			if sawNumber {
				numberDone = true
			}
			continue
		case c >= '0' && c <= '9':
			if numberDone || spaceAfter {
				return invalid()
			}
			sawNumber = true
			switch {
			case sawExp:
				expPart.WriteByte(c)
			case sawPoint:
				fracPart.WriteByte(c)
			default:
				intPart.WriteByte(c)
				sawIntChars = true
			}
			lastDigit = true
			continue
		case c == ',':
			if numberDone || sawPoint || sawExp {
				return invalid()
			}
			sawNumber = true
			sawIntChars = true
		case c == '.':
			if numberDone || sawPoint || sawExp {
				return invalid()
			}
			sawNumber = true
			sawPoint = true
		case c == 'e' || c == 'E':
			if numberDone || sawExp || !sawNumber {
				return invalid()
			}
			sawExp = true
			// An optional sign directly follows the exponent marker.
			if i+1 < len(s) && (s[i+1] == '+' || s[i+1] == '-') {
				expPart.WriteByte(s[i+1])
				i++
			}
		case c == '+' || c == '-':
			if sign != 0 {
				return invalid()
			}
			if sawNumber {
				numberDone = true
			}
			sign = c
		default:
			return invalid()
		}
		lastDigit = false
		spaceAfter = false
	}
	if !sawNumber {
		return invalid()
	}
	if sawIntChars && intPart.Len() == 0 {
		return invalid()
	}
	if intPart.Len() == 0 && fracPart.Len() == 0 {
		return invalid()
	}
	if sawExp {
		e := strings.TrimLeft(expPart.String(), "+-")
		if e == "" {
			return invalid()
		}
	}
	lit := intPart.String()
	if lit == "" {
		lit = "0"
	}
	if fracPart.Len() > 0 {
		lit += "." + fracPart.String()
	}
	if sawExp {
		lit += "e" + expPart.String()
	}
	r, ok := new(big.Rat).SetString(lit)
	if !ok {
		return invalid()
	}
	r = roundHalfAwayFromZero(r, scale)
	if sign == '-' {
		r.Neg(r)
	}
	if !value.CheckNumericRange(r, isBig) {
		return nil, fmt.Errorf("numeric value out of range: %q", s)
	}
	return r, nil
}

func roundHalfAwayFromZero(r *big.Rat, scale int) *big.Rat {
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	v := new(big.Rat).Mul(new(big.Rat).Abs(r), new(big.Rat).SetInt(pow))
	q, rem := new(big.Int).QuoRem(v.Num(), v.Denom(), new(big.Int))
	if new(big.Int).Mul(rem, big.NewInt(2)).Cmp(v.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	out := new(big.Rat).SetFrac(q, pow)
	if r.Sign() < 0 {
		out.Neg(out)
	}
	return out
}

var BindParseNumeric = helper.Scalar1(func(a value.Value) (value.Value, error) {
	numeric, err := a.ToString()
	if err != nil {
		return nil, err
	}
	return PARSE_NUMERIC(numeric)
})
