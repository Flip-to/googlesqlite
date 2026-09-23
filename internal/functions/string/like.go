package string

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func LIKE(a, b value.Value) (value.Value, error) {
	va, err := value.RawText(a)
	if err != nil {
		return nil, err
	}
	vb, err := value.RawText(b)
	if err != nil {
		return nil, err
	}
	if _, ok := a.(value.BytesValue); ok {
		// BYTES LIKE matches byte by byte: map each byte to one rune so
		// '_' consumes a single byte rather than a UTF-8 character.
		va, vb = bytesAsRunes(va), bytesAsRunes(vb)
	}
	re, err := likeRegexp(vb)
	if err != nil {
		return nil, err
	}
	return value.BoolValue(re.MatchString(va)), nil
}

// likeRegexps caches compiled LIKE patterns: the pattern is almost
// always a literal, so it is the same on every row.
var likeRegexps sync.Map // map[string]*regexp.Regexp

func likeRegexp(pattern string) (*regexp.Regexp, error) {
	if re, ok := likeRegexps.Load(pattern); ok {
		return re.(*regexp.Regexp), nil
	}
	rePattern, err := likePatternToRegexp(pattern)
	if err != nil {
		return nil, err
	}
	// (?s): '%' and '_' match newlines too, as RE2's dot_nl does in the
	// reference implementation.
	re, err := regexp.Compile("(?s)^(?:" + rePattern + ")$")
	if err != nil {
		return nil, err
	}
	likeRegexps.Store(pattern, re)
	return re, nil
}

// likePatternToRegexp translates a LIKE pattern the way
// googlesql/public/functions/like.cc GetRePatternFromLikePattern does:
// '_' matches one character, '%' any run of characters, a backslash
// makes the next character literal, and every other character matches
// itself.
func likePatternToRegexp(pattern string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '\\':
			if i+1 >= len(pattern) {
				return "", fmt.Errorf("LIKE pattern ends with a backslash")
			}
			i++
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		case '_':
			b.WriteByte('.')
		case '%':
			b.WriteString(".*")
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}
	return b.String(), nil
}

// BindLike: per GoogleSQL three-valued logic, LIKE with a NULL operand
// returns NULL, not FALSE — Scalar2 propagates that.
var BindLike = helper.Scalar2(LIKE)

// bytesAsRunes maps every byte to the rune with the same value.
func bytesAsRunes(s string) string {
	r := make([]rune, len(s))
	for i := 0; i < len(s); i++ {
		r[i] = rune(s[i])
	}
	return string(r)
}
