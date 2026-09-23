package string

import (
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// CONTAINS_SUBSTR is the BigQuery substring search: both sides are
// NFKC-normalised and case-folded before matching (string_functions.md),
// so CONTAINS_SUBSTR('Ⅸ', 'IX') is TRUE (U+2168 is ROMAN NUMERAL
// NINE, NFKC "IX").
//
// Returns NULL if either operand is NULL. Returns BOOL otherwise.
func CONTAINS_SUBSTR(haystack, needle string) (value.Value, error) {
	return value.BoolValue(strings.Contains(normalizeFold(haystack), normalizeFold(needle))), nil
}

var caseFolder = cases.Fold()

func normalizeFold(s string) string {
	return caseFolder.String(norm.NFKC.String(s))
}

var BindContainsSubstr = helper.Scalar2(func(a, b value.Value) (value.Value, error) {
	hay, err := a.ToString()
	if err != nil {
		return nil, err
	}
	needle, err := b.ToString()
	if err != nil {
		return nil, err
	}
	return CONTAINS_SUBSTR(hay, needle)
})
