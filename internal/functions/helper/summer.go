package helper

import (
	"fmt"
	"math"
	"math/big"

	"github.com/goccy/googlesqlite/internal/value"
)

// floatSumPrec is enough bits to hold any sum of float64 values
// exactly (exponents span 2^-1074..2^1023), so adding and later
// removing a value (a shrinking window frame) never loses precision.
const floatSumPrec = 2200

// floatFastLimit bounds the float64 fast path well below MaxFloat64 so
// a compensated sum cannot overflow before switching to big.Float.
const floatFastLimit = 1e300

// Summer accumulates SUM / AVG exactly, following the GoogleSQL rules:
//   - INT64 sums are exact and fail with "int64 overflow";
//   - DOUBLE sums are NaN if any input is NaN or both infinities occur,
//     otherwise an infinity if one occurs, otherwise the exact sum
//     rounded once, failing with "double overflow" if it is out of
//     range;
//   - NUMERIC / BIGNUMERIC sums are exact and fail with "numeric
//     overflow" outside the type's range.
//
// Values can be removed again (Remove) for sliding window frames.
type Summer struct {
	n            int64
	isFloat      bool
	isNumeric    bool
	isBigNumeric bool
	i            big.Int
	// Finite DOUBLE inputs use Neumaier-compensated float64 sums (fsum,
	// fcomp) until a magnitude nears overflow; then f takes over as an
	// exact big.Float so huge intermediate sums neither overflow nor
	// lose the small terms.
	fsum, fcomp float64
	f           *big.Float
	r           big.Rat
	nan         int64
	posInf      int64
	negInf      int64
}

func (s *Summer) Count() int64 { return s.n }

func (s *Summer) Add(v value.Value) error { return s.update(v, 1) }

func (s *Summer) Remove(v value.Value) error { return s.update(v, -1) }

func (s *Summer) update(v value.Value, sign int64) error {
	if v == nil {
		return nil
	}
	s.n += sign
	switch x := v.(type) {
	case value.IntValue:
		d := big.NewInt(int64(x))
		if sign < 0 {
			d.Neg(d)
		}
		s.i.Add(&s.i, d)
	case *value.NumericValue:
		s.isNumeric = true
		s.isBigNumeric = s.isBigNumeric || x.IsBigNumeric
		d := new(big.Rat).Set(x.Rat)
		if sign < 0 {
			d.Neg(d)
		}
		s.r.Add(&s.r, d)
	default:
		f, err := v.ToFloat64()
		if err != nil {
			return err
		}
		s.isFloat = true
		switch {
		case math.IsNaN(f):
			s.nan += sign
		case math.IsInf(f, 1):
			s.posInf += sign
		case math.IsInf(f, -1):
			s.negInf += sign
		default:
			if sign < 0 {
				f = -f
			}
			if s.f == nil && math.Abs(f) < floatFastLimit && math.Abs(s.fsum) < floatFastLimit {
				t := s.fsum + f
				if math.Abs(s.fsum) >= math.Abs(f) {
					s.fcomp += (s.fsum - t) + f
				} else {
					s.fcomp += (f - t) + s.fsum
				}
				s.fsum = t
				break
			}
			if s.f == nil {
				s.f = new(big.Float).SetPrec(floatSumPrec).SetFloat64(s.fsum)
				s.f.Add(s.f, new(big.Float).SetFloat64(s.fcomp))
				s.fsum, s.fcomp = 0, 0
			}
			s.f.Add(s.f, new(big.Float).SetPrec(floatSumPrec).SetFloat64(f))
		}
	}
	return nil
}

// Sum returns the sum, or NULL when no non-NULL value is present.
func (s *Summer) Sum(name string) (value.Value, error) {
	if s.n == 0 {
		return nil, nil
	}
	switch {
	case s.isFloat:
		if special, ok := s.floatSpecial(); ok {
			return value.FloatValue(special), nil
		}
		f := s.finiteFloat()
		if math.IsInf(f, 0) {
			return nil, fmt.Errorf("double overflow: %s", name)
		}
		return value.FloatValue(f), nil
	case s.isNumeric:
		if !value.CheckNumericRange(&s.r, s.isBigNumeric) {
			return nil, fmt.Errorf("numeric overflow: %s", name)
		}
		return &value.NumericValue{Rat: new(big.Rat).Set(&s.r), IsBigNumeric: s.isBigNumeric}, nil
	}
	if !s.i.IsInt64() {
		return nil, fmt.Errorf("int64 overflow: %s", name)
	}
	return value.IntValue(s.i.Int64()), nil
}

// Avg returns the mean: DOUBLE for INT64 and DOUBLE inputs, and the
// input's NUMERIC type otherwise. The mean of in-range values is always
// in range, so AVG never overflows.
func (s *Summer) Avg() (value.Value, error) {
	if s.n == 0 {
		return nil, nil
	}
	n := big.NewInt(s.n)
	switch {
	case s.isFloat:
		if special, ok := s.floatSpecial(); ok {
			return value.FloatValue(special), nil
		}
		if s.f == nil {
			return value.FloatValue((s.fsum + s.fcomp) / float64(s.n)), nil
		}
		q := new(big.Float).SetPrec(floatSumPrec).Quo(s.f, new(big.Float).SetInt(n))
		f, _ := q.Float64()
		return value.FloatValue(f), nil
	case s.isNumeric:
		q := new(big.Rat).Quo(&s.r, new(big.Rat).SetInt(n))
		return &value.NumericValue{Rat: q, IsBigNumeric: s.isBigNumeric}, nil
	}
	q := new(big.Rat).SetFrac(new(big.Int).Set(&s.i), n)
	f, _ := q.Float64()
	return value.FloatValue(f), nil
}

func (s *Summer) floatSpecial() (float64, bool) {
	switch {
	case s.nan > 0 || (s.posInf > 0 && s.negInf > 0):
		return math.NaN(), true
	case s.posInf > 0:
		return math.Inf(1), true
	case s.negInf > 0:
		return math.Inf(-1), true
	}
	return 0, false
}

func (s *Summer) finiteFloat() float64 {
	if s.f == nil {
		return s.fsum + s.fcomp + float64FromBig(&s.i)
	}
	// Integer inputs mixed into a DOUBLE sum are exact in s.i.
	total := new(big.Float).SetPrec(floatSumPrec).Set(s.f)
	total.Add(total, new(big.Float).SetInt(&s.i))
	f, _ := total.Float64()
	return f
}

func float64FromBig(i *big.Int) float64 {
	f, _ := new(big.Float).SetInt(i).Float64()
	return f
}
