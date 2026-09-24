package internal

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	googlesql "github.com/goccy/go-googlesql"

	"github.com/goccy/googlesqlite/internal/value"
)

// castScalarStrict applies BigQuery's CAST rules for the scalar pairs
// where the generic To* conversions are too lenient or lossy
// (conversion_functions.md, CAST). handled is false when the pair is
// not one of them.
func castScalarStrict(kind googlesql.TypeKind, v value.Value) (out value.Value, handled bool, err error) {
	switch kind {
	case googlesql.TypeKindTypeInt64:
		switch x := v.(type) {
		case value.FloatValue:
			i, err := floatToInt64(float64(x))
			return value.IntValue(i), true, err
		case *value.NumericValue:
			f, _ := x.Rat.Float64()
			i, err := floatToInt64(roundRatHalfAway(x.Rat, 0, f))
			return value.IntValue(i), true, err
		case value.StringValue:
			i, err := parseInt64Literal(string(x))
			return value.IntValue(i), true, err
		}
	case googlesql.TypeKindTypeDouble:
		if x, ok := v.(value.StringValue); ok {
			f, err := parseFloatLiteral(string(x))
			return value.FloatValue(f), true, err
		}
	case googlesql.TypeKindTypeBool:
		switch x := v.(type) {
		case value.IntValue:
			// Any non-zero INT64 is TRUE (flipto-dbt probe cast_as_bool-1821.8).
			return value.BoolValue(x != 0), true, nil
		case value.StringValue:
			// Only "true" / "false" in any case; no surrounding
			// whitespace (flipto-dbt probe cast_as_bool-1821.5).
			switch strings.ToLower(string(x)) {
			case "true":
				return value.BoolValue(true), true, nil
			case "false":
				return value.BoolValue(false), true, nil
			}
			return nil, true, fmt.Errorf("bad bool value: %s", string(x))
		}
	case googlesql.TypeKindTypeNumeric, googlesql.TypeKindTypeBignumeric:
		isBig := kind == googlesql.TypeKindTypeBignumeric
		var r *big.Rat
		switch x := v.(type) {
		case value.StringValue:
			s := strings.TrimSpace(string(x))
			rr, ok := new(big.Rat).SetString(s)
			// big.Rat also takes hex, octal, binary and fractions;
			// NUMERIC takes decimal text only (probe cast_as_numeric-3243.25).
			if !ok || s == "" || !decimalLiteralRe.MatchString(s) {
				return nil, true, fmt.Errorf("invalid NUMERIC value: %s", string(x))
			}
			r = rr
		case value.FloatValue:
			f := float64(x)
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return nil, true, fmt.Errorf("invalid NUMERIC value: %v", f)
			}
			r = new(big.Rat).SetFloat64(f)
		default:
			return nil, false, nil
		}
		scale := 9
		if isBig {
			scale = 38
		}
		r = roundRat(r, scale)
		if !value.CheckNumericRange(r, isBig) {
			return nil, true, fmt.Errorf("numeric overflow: %s", r.FloatString(scale))
		}
		return &value.NumericValue{Rat: r, IsBigNumeric: isBig}, true, nil
	case googlesql.TypeKindTypeUuid:
		// UUID values are stored as their canonical lower-case text.
		if x, ok := v.(value.StringValue); ok {
			u := strings.ToLower(strings.TrimSpace(string(x)))
			if !uuidRe.MatchString(u) {
				return nil, true, fmt.Errorf("invalid UUID: %s", string(x))
			}
			return value.StringValue(u), true, nil
		}
	case googlesql.TypeKindTypeDate:
		if x, ok := v.(value.StringValue); ok {
			t, err := parseDateLiteral(string(x))
			return value.DateValue(t), true, err
		}
	case googlesql.TypeKindTypeDatetime:
		if x, ok := v.(value.StringValue); ok {
			t, zone, err := parseCivilLiteral(string(x))
			if err == nil && zone != "" {
				err = fmt.Errorf("failed to convert %s to time.Time type", string(x))
			}
			// A DATETIME leap second drops its fraction:
			// 12:59:60.123456 is 13:00:00 (civil_time.test
			// cast_from_datetime_to_time, checked on BigQuery). A
			// TIMESTAMP keeps it.
			if m := civilLiteralRe.FindStringSubmatch(strings.TrimSpace(string(x))); m != nil && m[6] == "60" {
				t = t.Truncate(time.Second)
			}
			return value.DatetimeValue(t), true, err
		}
	case googlesql.TypeKindTypeTimestamp:
		if x, ok := v.(value.StringValue); ok {
			t, zone, err := parseCivilLiteral(string(x))
			if err != nil {
				return nil, true, err
			}
			// TIMESTAMP has microsecond precision; more fractional
			// digits are an error (probe cast_as_timestamp-6219.5).
			if m := civilLiteralRe.FindStringSubmatch(strings.TrimSpace(string(x))); m != nil && len(m[7]) > 6 {
				return nil, true, fmt.Errorf("failed to convert %s to time.Time type", string(x))
			}
			loc, err := zoneLocation(zone)
			if err != nil {
				return nil, true, err
			}
			// Reinterpret the civil time in the zone (UTC by default).
			// A time in a DST gap keeps the offset in effect before the
			// transition, as BigQuery does: 02:30 America/New_York on
			// 2024-03-10 is 07:30 UTC (probe cast_as_string-6504.35).
			// Go's time.Date picks the offset after it.
			wall := t
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
			if t.Hour() != wall.Hour() || t.Minute() != wall.Minute() || t.Day() != wall.Day() {
				_, before := t.Add(-2 * time.Hour).Zone()
				t = wall.Add(-time.Duration(before) * time.Second)
			}
			return value.TimestampValue(t.UTC()), true, nil
		}
	}
	return nil, false, nil
}

// floatToInt64 rounds half away from zero and rejects NaN and values
// outside INT64 (CAST(2.5 AS INT64) is 3).
func floatToInt64(f float64) (int64, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("illegal conversion of non-finite floating point number to an integer: %v", f)
	}
	r := math.Round(f)
	if r < -9223372036854775808 || r >= 9223372036854775808 {
		return 0, fmt.Errorf("int64 out of range: %v", f)
	}
	return int64(r), nil
}

func roundRatHalfAway(r *big.Rat, scale int, _ float64) float64 {
	f, _ := roundRat(r, scale).Float64()
	return f
}

// roundRat rounds r to scale decimal places, halves away from zero.
func roundRat(r *big.Rat, scale int) *big.Rat {
	m := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(m))
	num, den := scaled.Num(), scaled.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	twice := new(big.Int).Mul(new(big.Int).Abs(rem), big.NewInt(2))
	if twice.Cmp(den) >= 0 {
		if num.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return new(big.Rat).SetFrac(q, m)
}

// parseInt64Literal accepts optional surrounding whitespace, a sign and
// a hexadecimal 0x prefix.
func parseInt64Literal(s string) (int64, error) {
	t := strings.TrimSpace(s)
	neg := false
	body := t
	if strings.HasPrefix(body, "-") || strings.HasPrefix(body, "+") {
		neg = body[0] == '-'
		body = body[1:]
	}
	base := 10
	if strings.HasPrefix(strings.ToLower(body), "0x") {
		base, body = 16, body[2:]
	}
	if body == "" || strings.ContainsAny(body, "+- \t") {
		return 0, fmt.Errorf("bad int64 value: %s", s)
	}
	u, err := strconv.ParseUint(body, base, 64)
	if err != nil {
		return 0, fmt.Errorf("bad int64 value: %s", s)
	}
	if neg {
		if u > 1<<63 {
			return 0, fmt.Errorf("int64 out of range: %s", s)
		}
		return -int64(u), nil
	}
	if u > 1<<63-1 {
		return 0, fmt.Errorf("int64 out of range: %s", s)
	}
	return int64(u), nil
}

func parseFloatLiteral(s string) (float64, error) {
	t := strings.TrimSpace(s)
	switch strings.ToLower(strings.TrimPrefix(t, "+")) {
	case "nan":
		return math.NaN(), nil
	case "inf", "infinity":
		return math.Inf(1), nil
	case "-inf", "-infinity":
		return math.Inf(-1), nil
	}
	// A 0x-prefixed integer is accepted (probe cast_as_float64-5980.20).
	if body := strings.TrimLeft(t, "+-"); len(body) > 2 && strings.EqualFold(body[:2], "0x") && len(t)-len(body) <= 1 {
		u, err := strconv.ParseUint(body[2:], 16, 64)
		if err != nil {
			return 0, fmt.Errorf("bad double value: %s", s)
		}
		f := float64(u)
		if t[0] == '-' {
			f = -f
		}
		return f, nil
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0, fmt.Errorf("bad double value: %s", s)
	}
	return f, nil
}

var decimalLiteralRe = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var dateLiteralRe = regexp.MustCompile(`^(\d{1,4})-(\d{1,2})-(\d{1,2})$`)

// parseDateLiteral accepts YYYY-[M]M-[D]D (with surrounding
// whitespace) and rejects out-of-range fields and trailing time parts.
func parseDateLiteral(s string) (time.Time, error) {
	m := dateLiteralRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}, fmt.Errorf("failed to convert %s to time.Time type", s)
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != mo || t.Day() != d || y < 1 {
		return time.Time{}, fmt.Errorf("failed to convert %s to time.Time type", s)
	}
	return t, nil
}

var civilLiteralRe = regexp.MustCompile(`^(\d{1,4})-(\d{1,2})-(\d{1,2})(?:[ Tt](\d{1,2}):(\d{1,2}):(\d{1,2})(?:\.(\d{1,9}))?)?\s*(.*)$`)

// parseCivilLiteral parses YYYY-[M]M-[D]D[( |T)[H]H:[M]M:[S]S[.F]]
// followed by an optional time zone, as CAST(STRING AS DATETIME /
// TIMESTAMP) accepts (conversion_functions.md). It returns the civil
// time in UTC and the unparsed zone text.
func parseCivilLiteral(s string) (time.Time, string, error) {
	m := civilLiteralRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}, "", fmt.Errorf("failed to convert %s to time.Time type", s)
	}
	n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
	y, mo, d := n(1), n(2), n(3)
	h, mi, sec, nanos := 0, 0, 0, 0
	if m[4] != "" {
		h, mi, sec = n(4), n(5), n(6)
	}
	if m[7] != "" {
		nanos, _ = strconv.Atoi((m[7] + "000000000")[:9])
	}
	// A leap second (:60) is accepted and rolls into the next minute.
	if y < 1 || mo < 1 || mo > 12 || h > 23 || mi > 59 || sec > 60 {
		return time.Time{}, "", fmt.Errorf("failed to convert %s to time.Time type", s)
	}
	t := time.Date(y, time.Month(mo), d, h, mi, sec, nanos, time.UTC)
	if dayOf := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC); dayOf.Day() != d {
		return time.Time{}, "", fmt.Errorf("failed to convert %s to time.Time type", s)
	}
	return t, strings.TrimSpace(m[8]), nil
}

var zoneOffsetRe = regexp.MustCompile(`^([+-])(\d{1,2})(?::?(\d{2}))?$`)

// zoneLocation resolves a time zone suffix: empty (UTC, the default),
// UTC / Z, a +HH[:MM] offset, or an IANA name such as America/New_York.
func zoneLocation(zone string) (*time.Location, error) {
	switch strings.ToUpper(zone) {
	case "", "UTC", "Z":
		return time.UTC, nil
	}
	if m := zoneOffsetRe.FindStringSubmatch(zone); m != nil {
		h, _ := strconv.Atoi(m[2])
		mins := 0
		if m[3] != "" {
			mins, _ = strconv.Atoi(m[3])
		}
		off := h*3600 + mins*60
		if m[1] == "-" {
			off = -off
		}
		return time.FixedZone(zone, off), nil
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return nil, fmt.Errorf("invalid time zone: %s", zone)
	}
	return loc, nil
}
