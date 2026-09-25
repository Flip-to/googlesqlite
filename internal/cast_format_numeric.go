package internal

import (
	"fmt"
	"math"
	"math/big"
	"strings"
)

// formatNumberElements implements CAST(numeric AS STRING FORMAT fmt) for
// the digit (0, 9), decimal point (., D), group separator (, G), sign
// (S, MI, PR), currency ($, L, C, c) and flag (FM, B) elements of
// format-elements.md, "Format numeric type as string". The output has a
// fixed width aligned on the decimal point; without a sign element one
// leading character is reserved for the sign. The layout rules below
// were checked against BigQuery:
//
//	CAST(12.5 AS STRING FORMAT '99.99')        ' 12.50'
//	CAST(-12 AS STRING FORMAT '$99')           '-$12'
//	CAST(1234567.89 AS STRING FORMAT '$999,999.999')  ' $###,###.###'
//	CAST(0.5 AS STRING FORMAT '9.99')          '  .50'
//	CAST(0 AS STRING FORMAT '999')             '   0'
//	CAST(12 AS STRING FORMAT '999.FM')         '12.'
//
// The exponent (EEEE), V, hexadecimal (X) and Roman numeral (RN)
// elements are rejected rather than ignored.
func formatNumberElements(r *big.Rat, isNaN bool, format string) (string, error) {
	f, err := parseNumberFormat(format)
	if err != nil {
		return "", err
	}
	if isNaN {
		return "", fmt.Errorf("CAST FORMAT: cannot format NaN or infinity")
	}
	neg := r.Sign() < 0
	abs := new(big.Rat).Abs(r)
	// Round half away from zero to the number of fraction digits.
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(f.fracDigits)), nil)
	scaled := new(big.Rat).Mul(abs, new(big.Rat).SetInt(scale))
	q, rem := new(big.Int).QuoRem(scaled.Num(), scaled.Denom(), new(big.Int))
	if new(big.Int).Mul(rem, big.NewInt(2)).Cmp(scaled.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	digits := q.String()
	for len(digits) < f.fracDigits+1 {
		digits = "0" + digits
	}
	intStr := digits[:len(digits)-f.fracDigits]
	fracStr := digits[len(digits)-f.fracDigits:]
	intZero := strings.TrimLeft(intStr, "0") == ""
	if intZero {
		intStr = ""
	} else {
		intStr = strings.TrimLeft(intStr, "0")
	}
	overflow := len(intStr) > f.intDigits

	// The integer part, right to left over the format's integer
	// elements (digits and group separators).
	intOut := make([]rune, len(f.intElems))
	di := len(intStr) - 1
	for i := len(f.intElems) - 1; i >= 0; i-- {
		e := f.intElems[i]
		if e == ',' {
			intOut[i] = ','
			continue
		}
		switch {
		case overflow:
			intOut[i] = '#'
		case di >= 0:
			intOut[i] = rune(intStr[di])
		case f.zeroFrom >= 0 && i >= f.zeroFrom:
			intOut[i] = '0'
		case i == len(f.intElems)-1 && intZero && f.fracDigits == 0:
			// A zero integer part still shows one 0 when there is no
			// fraction to carry the value.
			intOut[i] = '0'
		default:
			intOut[i] = ' '
		}
		di--
	}
	// A separator with no digit printed to its left is blank too.
	for i := 0; i < len(intOut); i++ {
		if intOut[i] != ',' && intOut[i] != ' ' {
			break
		}
		intOut[i] = ' '
	}
	lead := 0
	for lead < len(intOut) && intOut[lead] == ' ' {
		lead++
	}
	var body strings.Builder
	body.WriteString(string(intOut[lead:]))
	if f.hasPoint {
		body.WriteByte('.')
		if overflow {
			body.WriteString(strings.Repeat("#", f.fracDigits))
		} else {
			body.WriteString(fracStr)
		}
	}
	number := body.String()
	if f.fm {
		// FM drops trailing zeros of the fraction (the point stays).
		if f.hasPoint && !overflow {
			number = strings.TrimRight(number, "0")
		}
	}
	var out strings.Builder
	if !f.fm {
		out.WriteString(strings.Repeat(" ", lead))
	}
	switch f.sign {
	case signDefault:
		if neg {
			out.WriteByte('-')
		} else if !f.fm {
			out.WriteByte(' ')
		}
	case signLeadingS:
		if neg {
			out.WriteByte('-')
		} else {
			out.WriteByte('+')
		}
	case signPR:
		if neg {
			out.WriteByte('<')
		} else if !f.fm {
			out.WriteByte(' ')
		}
	}
	out.WriteString(f.currency)
	out.WriteString(number)
	switch f.sign {
	case signTrailingS:
		if neg {
			out.WriteByte('-')
		} else {
			out.WriteByte('+')
		}
	case signMI:
		if neg {
			out.WriteByte('-')
		} else if !f.fm {
			out.WriteByte(' ')
		}
	case signPR:
		if neg {
			out.WriteByte('>')
		} else if !f.fm {
			out.WriteByte(' ')
		}
	}
	s := out.String()
	if f.blankZero && intZero {
		// B: a zero integer part prints as blanks of the same width.
		return strings.Repeat(" ", len([]rune(s))), nil
	}
	return s, nil
}

type numberSign int

const (
	signDefault numberSign = iota
	signLeadingS
	signTrailingS
	signMI
	signPR
)

type numberFormat struct {
	intElems   []rune // '0', '9' and ',' before the decimal point
	intDigits  int
	zeroFrom   int // first '0' in intElems, or -1
	fracDigits int
	hasPoint   bool
	sign       numberSign
	currency   string
	fm         bool
	blankZero  bool
}

func parseNumberFormat(format string) (*numberFormat, error) {
	f := &numberFormat{zeroFrom: -1}
	s := format
	seenDigit := false
	for len(s) > 0 {
		up := strings.ToUpper(s)
		switch {
		case strings.HasPrefix(up, "FM"):
			f.fm = true
			s = s[2:]
		case strings.HasPrefix(up, "MI"):
			if len(s) != 2 {
				return nil, fmt.Errorf("CAST FORMAT: MI must be the last element")
			}
			f.sign = signMI
			s = s[2:]
		case strings.HasPrefix(up, "PR"):
			if len(s) != 2 {
				return nil, fmt.Errorf("CAST FORMAT: PR must be the last element")
			}
			f.sign = signPR
			s = s[2:]
		case strings.HasPrefix(up, "EEEE"), strings.HasPrefix(up, "RN"),
			up[0] == 'V', up[0] == 'X':
			return nil, fmt.Errorf("CAST FORMAT: numeric format element in %q is not supported", format)
		default:
			c := s[0]
			switch c {
			case '0', '9':
				seenDigit = true
				if f.hasPoint {
					f.fracDigits++
				} else {
					if c == '0' && f.zeroFrom < 0 {
						f.zeroFrom = len(f.intElems)
					}
					f.intElems = append(f.intElems, rune(c))
					f.intDigits++
				}
			case '.', 'D', 'd':
				if f.hasPoint {
					return nil, fmt.Errorf("CAST FORMAT: more than one decimal point in %q", format)
				}
				f.hasPoint = true
			case ',', 'G', 'g':
				if f.hasPoint {
					return nil, fmt.Errorf("CAST FORMAT: group separator after the decimal point in %q", format)
				}
				f.intElems = append(f.intElems, ',')
			case 'S', 's':
				if !seenDigit && !f.hasPoint && f.currency == "" {
					f.sign = signLeadingS
				} else {
					f.sign = signTrailingS
				}
			case '$', 'L', 'l':
				f.currency = "$"
			case 'C':
				f.currency = "USD"
			case 'c':
				f.currency = "usd"
			case 'B', 'b':
				f.blankZero = true
			default:
				return nil, fmt.Errorf("CAST FORMAT: invalid numeric format element %q in %q", string(c), format)
			}
			s = s[1:]
		}
	}
	if f.intDigits == 0 && f.fracDigits == 0 {
		return nil, fmt.Errorf("CAST FORMAT: numeric format %q has no digit elements", format)
	}
	return f, nil
}

// floatToRat converts a FLOAT64 to its exact binary value, so 1.005
// formats as the double it is: '9.99' gives ' 1.00' on BigQuery.
func floatToRat(v float64) (*big.Rat, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, false
	}
	return new(big.Rat).SetFloat64(v), true
}
