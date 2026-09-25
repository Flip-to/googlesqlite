package internal

import (
	"math"
	"regexp"
	"strings"

	"github.com/goccy/googlesqlite/internal/value"
)

// The analyzer folds a -0.0 literal to +0, including inside ARRAY and
// STRUCT literals it builds as one constant. restoreNegativeZeros puts
// the sign back by reading the literal's source text: every FLOAT64
// zero element whose own text starts with '-' becomes -0.0 (flipto-dbt
// probes format_t_struct-1232.7 and to_json_string-5209.h1).
func restoreNegativeZeros(v value.Value, image string) value.Value {
	image = strings.TrimSpace(image)
	switch x := v.(type) {
	case value.FloatValue:
		if x == 0 && strings.HasPrefix(image, "-") {
			return value.FloatValue(math.Copysign(0, -1))
		}
	case *value.ArrayValue:
		body, ok := literalBody(image, "ARRAY", '[', ']')
		if !ok {
			return v
		}
		parts := splitTopLevel(body)
		if len(parts) != len(x.Values) {
			return v
		}
		for i, p := range parts {
			x.Values[i] = restoreNegativeZeros(x.Values[i], p)
		}
	case *value.StructValue:
		body, ok := literalBody(image, "STRUCT", '(', ')')
		if !ok {
			return v
		}
		parts := splitTopLevel(body)
		if len(parts) != len(x.Values) {
			return v
		}
		for i, p := range parts {
			p = structFieldAliasRe.ReplaceAllString(p, "")
			nv := restoreNegativeZeros(x.Values[i], p)
			x.Values[i] = nv
			if i < len(x.Keys) && x.M != nil {
				if _, exists := x.M[x.Keys[i]]; exists {
					x.M[x.Keys[i]] = nv
				}
			}
		}
	}
	return v
}

var structFieldAliasRe = regexp.MustCompile(`(?i)\s+AS\s+[A-Za-z_][A-Za-z0-9_]*\s*$|(?i)\s+AS\s+` + "`[^`]*`" + `\s*$`)

// literalBody strips an optional keyword and <type> prefix and returns
// the text between the outer open / close characters.
func literalBody(image, keyword string, open, closeCh byte) (string, bool) {
	s := image
	if len(s) >= len(keyword) && strings.EqualFold(s[:len(keyword)], keyword) {
		s = strings.TrimSpace(s[len(keyword):])
		if strings.HasPrefix(s, "<") {
			depth := 0
			end := -1
			for i := 0; i < len(s); i++ {
				switch s[i] {
				case '<':
					depth++
				case '>':
					depth--
					if depth == 0 {
						end = i
					}
				}
				if end >= 0 {
					break
				}
			}
			if end < 0 {
				return "", false
			}
			s = strings.TrimSpace(s[end+1:])
		}
	}
	if len(s) < 2 || s[0] != open || s[len(s)-1] != closeCh {
		return "", false
	}
	return s[1 : len(s)-1], true
}

// splitTopLevel splits a literal list on commas outside quotes and
// brackets.
func splitTopLevel(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\'', '"', '`':
			i = skipQuoted(s, i)
		case '(', '[', '<', '{':
			depth++
		case ')', ']', '>', '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if strings.TrimSpace(s[start:]) != "" || len(parts) > 0 {
		parts = append(parts, s[start:])
	}
	return parts
}

// skipQuoted returns the index of the quote closing the literal that
// starts at i (backslash escapes honoured).
func skipQuoted(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case q:
			return j
		}
	}
	return len(s)
}
