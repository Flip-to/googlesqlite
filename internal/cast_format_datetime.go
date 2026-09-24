package internal

import (
	"fmt"
	"strings"
	"time"
)

// castFormatElements lists the date/time format elements from
// format-elements.md ("Format date and time as string"), longest first
// so that the tokenizer matches greedily.
var castFormatElements = []string{
	"A.M.", "P.M.", "HH24", "HH12", "MONTH", "YYYY", "RRRR",
	"FF1", "FF2", "FF3", "FF4", "FF5", "FF6", "FF7", "FF8", "FF9",
	"SSSSS", "TZH", "TZM",
	"YYY", "MON", "DDD", "DAY", "RR", "YY", "MM", "DD", "DY", "HH", "MI", "SS", "AM", "PM",
	"Y", "D", "Q",
}

// textCase applies the casing rule for text elements: an all-upper
// element gives upper-case output, a capitalised one gives capitalised
// output, and a lower-case one gives lower-case output.
func textCase(elem, s string) string {
	switch {
	case elem == strings.ToUpper(elem) && len(elem) > 1 && elem[1] >= 'A' && elem[1] <= 'Z':
		return strings.ToUpper(s)
	case elem[0] >= 'A' && elem[0] <= 'Z':
		return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
	}
	return strings.ToLower(s)
}

// castFormatTimeElements are the time-part elements (not valid for DATE).
var castFormatTimeElements = map[string]bool{
	"HH": true, "HH12": true, "HH24": true, "MI": true, "SS": true, "SSSSS": true,
	"AM": true, "PM": true, "A.M.": true, "P.M.": true,
	"FF1": true, "FF2": true, "FF3": true, "FF4": true, "FF5": true,
	"FF6": true, "FF7": true, "FF8": true, "FF9": true,
}

// checkCastFormatElement rejects an element the source type lacks:
// time parts for DATE, date parts for TIME, and TZH / TZM for anything
// but TIMESTAMP (format-elements.md; cast_format_validation.test,
// cast_date_to_string_format_invalid_literal_hh).
func checkCastFormatElement(typeName, elem string) error {
	up := strings.ToUpper(elem)
	isTZ := up == "TZH" || up == "TZM"
	isTime := castFormatTimeElements[up]
	var bad bool
	switch typeName {
	case "DATE":
		bad = isTime || isTZ
	case "DATETIME":
		bad = isTZ
	case "TIME":
		bad = isTZ || !isTime
	}
	if bad {
		return fmt.Errorf("%s does not support '%s'", typeName, up)
	}
	return nil
}

// formatDateTimeElements renders t using CAST ... FORMAT elements.
// typeName is the source type (DATE, DATETIME, TIME or TIMESTAMP).
func formatDateTimeElements(t time.Time, format, typeName string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(format); {
		c := format[i]
		if c == '"' {
			end := strings.IndexByte(format[i+1:], '"')
			if end < 0 {
				return "", fmt.Errorf("CAST: unterminated quoted text in format %q", format)
			}
			out.WriteString(format[i+1 : i+1+end])
			i += end + 2
			continue
		}
		if strings.IndexByte(" -/,.;:", c) >= 0 {
			out.WriteByte(c)
			i++
			continue
		}
		var elem string
		for _, e := range castFormatElements {
			if len(format)-i >= len(e) && strings.EqualFold(format[i:i+len(e)], e) {
				elem = format[i : i+len(e)]
				break
			}
		}
		if elem == "" {
			return "", fmt.Errorf("CAST: invalid format element at %q", format[i:])
		}
		if err := checkCastFormatElement(typeName, elem); err != nil {
			return "", err
		}
		i += len(elem)
		hour12 := t.Hour() % 12
		if hour12 == 0 {
			hour12 = 12
		}
		switch strings.ToUpper(elem) {
		case "YYYY", "RRRR":
			fmt.Fprintf(&out, "%04d", t.Year())
		case "YYY":
			fmt.Fprintf(&out, "%03d", t.Year()%1000)
		case "YY", "RR":
			fmt.Fprintf(&out, "%02d", t.Year()%100)
		case "Y":
			fmt.Fprintf(&out, "%d", t.Year()%10)
		case "Q":
			fmt.Fprintf(&out, "%d", (int(t.Month())-1)/3+1)
		case "MM":
			fmt.Fprintf(&out, "%02d", int(t.Month()))
		case "MON":
			out.WriteString(textCase(elem, t.Month().String()[:3]))
		case "MONTH":
			out.WriteString(textCase(elem, t.Month().String()))
		case "DD":
			fmt.Fprintf(&out, "%02d", t.Day())
		case "DDD":
			fmt.Fprintf(&out, "%03d", t.YearDay())
		case "D":
			fmt.Fprintf(&out, "%d", int(t.Weekday())+1)
		case "DAY":
			out.WriteString(textCase(elem, t.Weekday().String()))
		case "DY":
			out.WriteString(textCase(elem, t.Weekday().String()[:3]))
		case "HH", "HH12":
			fmt.Fprintf(&out, "%02d", hour12)
		case "HH24":
			fmt.Fprintf(&out, "%02d", t.Hour())
		case "MI":
			fmt.Fprintf(&out, "%02d", t.Minute())
		case "SS":
			fmt.Fprintf(&out, "%02d", t.Second())
		case "SSSSS":
			fmt.Fprintf(&out, "%05d", t.Hour()*3600+t.Minute()*60+t.Second())
		case "AM", "PM", "A.M.", "P.M.":
			m := "AM"
			if t.Hour() >= 12 {
				m = "PM"
			}
			if strings.Contains(elem, ".") {
				m = m[:1] + "." + m[1:] + "."
			}
			if elem[0] >= 'a' && elem[0] <= 'z' {
				m = strings.ToLower(m)
			}
			out.WriteString(m)
		case "TZH":
			_, off := t.Zone()
			sign := '+'
			if off < 0 {
				sign, off = '-', -off
			}
			fmt.Fprintf(&out, "%c%02d", sign, off/3600)
		case "TZM":
			_, off := t.Zone()
			if off < 0 {
				off = -off
			}
			fmt.Fprintf(&out, "%02d", off%3600/60)
		default: // FF1 .. FF9
			n := int(elem[2] - '0')
			fmt.Fprintf(&out, "%09d", t.Nanosecond())
			s := out.String()
			out.Reset()
			out.WriteString(s[:len(s)-9+n])
		}
	}
	return out.String(), nil
}
