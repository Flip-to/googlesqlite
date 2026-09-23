package internal

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// castParseTarget identifies the type a string is parsed into by
// CAST(string AS type FORMAT ...).
type castParseTarget int

const (
	castParseDate castParseTarget = iota
	castParseDatetime
	castParseTime
	castParseTimestamp
)

func (t castParseTarget) String() string {
	switch t {
	case castParseDate:
		return "DATE"
	case castParseDatetime:
		return "DATETIME"
	case castParseTime:
		return "TIME"
	}
	return "TIMESTAMP"
}

// castParseElements lists the format elements recognised when parsing
// ("Format string as date and time" in format-elements.md), longest
// first so the tokenizer matches greedily. Elements that exist only in
// the formatting direction are listed so that they are reported as
// unsupported for parsing rather than as unknown.
var castParseElements = []string{
	"Y,YYY", "A.M.", "P.M.", "HH24", "HH12", "MONTH", "YYYY", "RRRR",
	"FF1", "FF2", "FF3", "FF4", "FF5", "FF6", "FF7", "FF8", "FF9",
	"SSSSS", "TZH", "TZM",
	"YYY", "MON", "DDD", "DAY", "RR", "YY", "MM", "DD", "DY", "HH", "MI", "SS", "AM", "PM",
	"Y", "D", "Q",
}

// castParseUnsupported are format elements that are valid when
// formatting but not when parsing.
var castParseUnsupported = map[string]bool{
	"DDD": true, "DAY": true, "DY": true, "D": true, "Q": true, "AM": true, "PM": true,
}

type castParseTokenKind int

const (
	tokElement castParseTokenKind = iota
	tokLiteral
	tokSpace
)

type castParseToken struct {
	kind castParseTokenKind
	text string // upper-cased element name, or literal text
}

// elementPart groups elements by the date/time part they set.
func elementPart(e string) string {
	switch e {
	case "YYYY", "YYY", "YY", "Y", "Y,YYY", "RRRR", "RR":
		return "year"
	case "MM", "MON", "MONTH":
		return "month"
	case "DD":
		return "day"
	case "HH", "HH12", "HH24":
		return "hour"
	case "MI":
		return "minute"
	case "SS":
		return "second"
	case "SSSSS":
		return "secondofday"
	case "A.M.", "P.M.":
		return "meridian"
	case "TZH":
		return "tzh"
	case "TZM":
		return "tzm"
	}
	return "fraction" // FFn
}

func tokenizeCastParseFormat(format string) ([]castParseToken, error) {
	var toks []castParseToken
	for i := 0; i < len(format); {
		c := format[i]
		switch {
		case c == '"':
			var lit strings.Builder
			j := i + 1
			for ; j < len(format) && format[j] != '"'; j++ {
				if format[j] == '\\' && j+1 < len(format) {
					j++
				}
				lit.WriteByte(format[j])
			}
			if j >= len(format) {
				return nil, fmt.Errorf("CAST: unterminated quoted text in format %q", format)
			}
			toks = append(toks, castParseToken{kind: tokLiteral, text: lit.String()})
			i = j + 1
			continue
		case c == ' ':
			for i < len(format) && format[i] == ' ' {
				i++
			}
			toks = append(toks, castParseToken{kind: tokSpace})
			continue
		case strings.IndexByte("-/,.;:'", c) >= 0:
			toks = append(toks, castParseToken{kind: tokLiteral, text: string(c)})
			i++
			continue
		}
		var elem string
		for _, e := range castParseElements {
			if len(format)-i >= len(e) && strings.EqualFold(format[i:i+len(e)], e) {
				elem = e
				break
			}
		}
		if elem == "" {
			return nil, fmt.Errorf("CAST: invalid format element at %q", format[i:])
		}
		if castParseUnsupported[elem] {
			return nil, fmt.Errorf("Format element '%s' is not supported for parsing", elem)
		}
		toks = append(toks, castParseToken{kind: tokElement, text: elem})
		i += len(elem)
	}
	return toks, nil
}

// validateCastParseModel applies the format model rules from
// format-elements.md ("Format model rules").
func validateCastParseModel(toks []castParseToken, target castParseTarget) error {
	seen := map[string]bool{}
	parts := map[string]string{}
	for _, t := range toks {
		if t.kind != tokElement {
			continue
		}
		if seen[t.text] {
			return fmt.Errorf("CAST: format element '%s' appears more than once", t.text)
		}
		seen[t.text] = true
		p := elementPart(t.text)
		if prev, ok := parts[p]; ok {
			return fmt.Errorf("CAST: format elements '%s' and '%s' both set the %s part", prev, t.text, p)
		}
		parts[p] = t.text
		hasDate := target != castParseTime
		hasTime := target != castParseDate
		switch p {
		case "year", "month", "day":
			if !hasDate {
				return fmt.Errorf("CAST: format element '%s' is not allowed when casting to %s", t.text, target)
			}
		case "tzh", "tzm":
			if target != castParseTimestamp {
				return fmt.Errorf("CAST: format element '%s' is not allowed when casting to %s", t.text, target)
			}
		default:
			if !hasTime {
				return fmt.Errorf("CAST: format element '%s' is not allowed when casting to %s", t.text, target)
			}
		}
	}
	hour, hasHour := parts["hour"]
	_, hasMeridian := parts["meridian"]
	if hour == "HH24" && hasMeridian {
		return fmt.Errorf("CAST: format element 'HH24' can't be used with a meridian indicator")
	}
	if hasHour && hour != "HH24" && !hasMeridian {
		return fmt.Errorf("CAST: format element '%s' requires a meridian indicator", hour)
	}
	if hasMeridian && (!hasHour || hour == "HH24") {
		return fmt.Errorf("CAST: a meridian indicator requires the 12-hour format element")
	}
	if _, ok := parts["secondofday"]; ok {
		for _, p := range []string{"hour", "minute", "second", "meridian"} {
			if _, ok := parts[p]; ok {
				return fmt.Errorf("CAST: format element 'SSSSS' can't be used with the %s part", p)
			}
		}
	}
	if _, ok := parts["tzm"]; ok {
		if _, ok := parts["tzh"]; !ok {
			return fmt.Errorf("CAST: format element 'TZM' requires 'TZH'")
		}
	}
	return nil
}

// isNumericElement reports whether a token consumes digits, which
// decides whether the preceding numeric element is delimited.
func isNumericElement(t castParseToken) bool {
	if t.kind != tokElement {
		return false
	}
	switch t.text {
	case "MON", "MONTH", "A.M.", "P.M.", "TZH":
		return false
	}
	return true
}

// parseStringWithFormat implements CAST(string AS DATE / DATETIME /
// TIME / TIMESTAMP FORMAT format [AT TIME ZONE loc]). now supplies the
// current date used for missing year/month parts.
func parseStringWithFormat(s, format string, target castParseTarget, loc *time.Location, now time.Time) (time.Time, error) {
	toks, err := tokenizeCastParseFormat(format)
	if err != nil {
		return time.Time{}, err
	}
	if err := validateCastParseModel(toks, target); err != nil {
		return time.Time{}, err
	}
	in := strings.TrimFunc(s, unicode.IsSpace)
	pos := 0
	fail := func() (time.Time, error) {
		return time.Time{}, fmt.Errorf("CAST: failed to parse %q with format %q", s, format)
	}
	readDigits := func(minN, maxN int) (int, int, bool) {
		v, n := 0, 0
		for n < maxN && pos+n < len(in) && in[pos+n] >= '0' && in[pos+n] <= '9' {
			v = v*10 + int(in[pos+n]-'0')
			n++
		}
		if n < minN {
			return 0, 0, false
		}
		pos += n
		return v, n, true
	}

	nowYear := now.Year()
	year, month, day := nowYear, int(now.Month()), 1
	hour, minute, sec, nsec := 0, 0, 0, 0
	pm, hasMeridian := false, false
	tzSign, tzH, tzM, hasTZ := 1, 0, 0, false

	for i, t := range toks {
		switch t.kind {
		case tokSpace:
			start := pos
			for pos < len(in) {
				r, size := utf8.DecodeRuneInString(in[pos:])
				if !unicode.IsSpace(r) {
					break
				}
				pos += size
			}
			if pos == start {
				return fail()
			}
			continue
		case tokLiteral:
			if !strings.HasPrefix(in[pos:], t.text) {
				return fail()
			}
			pos += len(t.text)
			continue
		}
		delimited := i+1 >= len(toks) || !isNumericElement(toks[i+1])
		num := func(width int, delimitedMax int) (int, int, bool) {
			if delimited {
				return readDigits(1, delimitedMax)
			}
			return readDigits(width, width)
		}
		switch t.text {
		case "YYYY", "RRRR":
			v, _, ok := num(4, 5)
			if !ok {
				return fail()
			}
			year = v
		case "YYY", "YY", "Y":
			w := len(t.text)
			v, _, ok := readDigits(w, w)
			if !ok {
				return fail()
			}
			mod := 1
			for k := 0; k < w; k++ {
				mod *= 10
			}
			year = nowYear/mod*mod + v
		case "Y,YYY":
			hi, _, ok := readDigits(1, 2)
			if !ok || pos >= len(in) || in[pos] != ',' {
				return fail()
			}
			pos++
			lo, _, ok := readDigits(3, 3)
			if !ok {
				return fail()
			}
			year = hi*1000 + lo
		case "RR":
			v, _, ok := readDigits(2, 2)
			if !ok {
				return fail()
			}
			century, cur := nowYear/100*100, nowYear%100
			switch {
			case v < 50 && cur >= 50:
				century += 100
			case v >= 50 && cur < 50:
				century -= 100
			}
			year = century + v
		case "MM":
			v, _, ok := num(2, 2)
			if !ok {
				return fail()
			}
			month = v
		case "MON", "MONTH":
			found := false
			for m := time.January; m <= time.December; m++ {
				name := m.String()
				if t.text == "MON" {
					name = name[:3]
				}
				if len(in)-pos >= len(name) && strings.EqualFold(in[pos:pos+len(name)], name) {
					month = int(m)
					pos += len(name)
					found = true
					break
				}
			}
			if !found {
				return fail()
			}
		case "DD":
			v, _, ok := num(2, 2)
			if !ok {
				return fail()
			}
			day = v
		case "HH", "HH12":
			v, _, ok := num(2, 2)
			if !ok || v < 1 || v > 12 {
				return fail()
			}
			hour = v % 12
		case "HH24":
			v, _, ok := num(2, 2)
			if !ok {
				return fail()
			}
			hour = v
		case "MI":
			v, _, ok := num(2, 2)
			if !ok {
				return fail()
			}
			minute = v
		case "SS":
			v, _, ok := num(2, 2)
			if !ok {
				return fail()
			}
			sec = v
		case "SSSSS":
			v, _, ok := num(5, 5)
			if !ok || v >= 86400 {
				return fail()
			}
			hour, minute, sec = v/3600, v%3600/60, v%60
		case "A.M.", "P.M.":
			if len(in)-pos < 4 {
				return fail()
			}
			switch strings.ToUpper(in[pos : pos+4]) {
			case "A.M.":
				pm = false
			case "P.M.":
				pm = true
			default:
				return fail()
			}
			hasMeridian = true
			pos += 4
		case "TZH":
			if pos >= len(in) {
				return fail()
			}
			switch in[pos] {
			case '+', ' ':
				tzSign = 1
			case '-':
				tzSign = -1
			default:
				return fail()
			}
			pos++
			v, _, ok := num(2, 2)
			if !ok {
				return fail()
			}
			tzH, hasTZ = v, true
		case "TZM":
			v, _, ok := num(2, 2)
			if !ok || v > 59 {
				return fail()
			}
			tzM = v
		default: // FF1 .. FF9
			n := int(t.text[2] - '0')
			v, got, ok := num(n, n)
			if !ok {
				return fail()
			}
			for ; got < 9; got++ {
				v *= 10
			}
			// Sub-microsecond digits are truncated.
			nsec = v / 1000 * 1000
		}
	}
	if pos != len(in) {
		return fail()
	}
	if hasMeridian && pm {
		hour += 12
	}
	if year < 1 || year > 9999 || month < 1 || month > 12 || day < 1 ||
		hour > 23 || minute > 59 || sec > 59 || tzH > 14 {
		return fail()
	}
	if target == castParseTime {
		return time.Date(1970, 1, 1, hour, minute, sec, nsec, time.UTC), nil
	}
	if hasTZ {
		loc = time.FixedZone("", tzSign*(tzH*3600+tzM*60))
	}
	if target != castParseTimestamp {
		loc = time.UTC
	}
	t := time.Date(year, time.Month(month), day, hour, minute, sec, nsec, loc)
	if t.Day() != day || int(t.Month()) != month {
		return fail()
	}
	return t, nil
}
