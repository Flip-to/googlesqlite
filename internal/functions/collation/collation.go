// Package collation implements the runtime half of GoogleSQL
// collation support (collation-concepts.md). The analyzer attaches a
// collation (for example `und:ci`) to every collation-sensitive
// function call; the formatter then lowers those calls onto the
// helpers in this package:
//
//   - KEY(v, spec) maps a STRING (or an ARRAY / STRUCT of STRING) to a
//     sort key whose binary order and equality match the collation.
//     Comparisons, IN, CASE, GROUP BY, DISTINCT, joins and ORDER BY
//     run over the keys.
//   - PACK(v, spec) / UNPACK(v) prefix the original string with its
//     key so MIN / MAX / PERCENTILE_DISC and friends pick the
//     collation-wise extremum but still return the original value.
//   - REPLACE / SPLIT / STRPOS / INSTR / STARTS_WITH / ENDS_WITH / LIKE
//     perform collation-aware substring matching.
package collation

import (
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/goccy/googlesqlite/internal/value"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// spec is a parsed collation specification.
type spec struct {
	binary bool
	tag    language.Tag
	ci     bool
}

func parseSpec(s string) (spec, error) {
	if s == "" || s == "binary" {
		return spec{binary: true}, nil
	}
	parts := strings.SplitN(s, ":", 2)
	if parts[0] == "binary" {
		// orderby_collate_queries.test orderby_collate_binary_cs_is_an_error.
		return spec{}, fmt.Errorf("COLLATE has invalid collation name '%s':binary cannot be combined with a suffix", s)
	}
	sp := spec{tag: language.Make(parts[0])}
	if len(parts) == 2 {
		switch parts[1] {
		case "ci":
			sp.ci = true
		case "cs":
		default:
			return spec{}, fmt.Errorf("COLLATE: unsupported collation attribute %s", parts[1])
		}
	}
	return sp, nil
}

// collator pools: a *collate.Collator is not safe for concurrent use.
var pools sync.Map // map[string]*sync.Pool

func (sp spec) newCollator() *collate.Collator {
	if sp.ci {
		return collate.New(sp.tag, collate.IgnoreCase)
	}
	return collate.New(sp.tag)
}

func (sp spec) pool() *sync.Pool {
	name := sp.tag.String()
	if sp.ci {
		name += ":ci"
	}
	if p, ok := pools.Load(name); ok {
		return p.(*sync.Pool)
	}
	p := &sync.Pool{New: func() any { return sp.newCollator() }}
	actual, _ := pools.LoadOrStore(name, p)
	return actual.(*sync.Pool)
}

// keyOf returns the hex-encoded collation key of s. Hex keeps the
// binary order of the raw key while staying valid UTF-8.
func (sp spec) keyOf(s string) string {
	if sp.binary {
		return s
	}
	p := sp.pool()
	c := p.Get().(*collate.Collator)
	defer p.Put(c)
	var buf collate.Buffer
	return hex.EncodeToString(c.KeyFromString(&buf, s))
}

func specArg(v value.Value) (spec, error) {
	if v == nil {
		return spec{binary: true}, nil
	}
	s, err := v.ToString()
	if err != nil {
		return spec{}, err
	}
	return parseSpec(s)
}

// KEY maps v to its collation key. Non-string leaves pass through
// unchanged so the helper can wrap any comparison operand.
func KEY(args ...value.Value) (value.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("collation_key: expected 2 arguments")
	}
	sp, err := specArg(args[1])
	if err != nil {
		return nil, err
	}
	if sp.binary {
		return args[0], nil
	}
	return keyValue(args[0], sp), nil
}

func keyValue(v value.Value, sp spec) value.Value {
	switch x := v.(type) {
	case value.StringValue:
		return value.StringValue(sp.keyOf(string(x)))
	case *value.ArrayValue:
		out := &value.ArrayValue{Values: make([]value.Value, len(x.Values))}
		for i, e := range x.Values {
			out.Values[i] = keyValue(e, sp)
		}
		return out
	case *value.StructValue:
		out := &value.StructValue{Keys: x.Keys, Values: make([]value.Value, len(x.Values)), M: map[string]value.Value{}}
		for i, e := range x.Values {
			out.Values[i] = keyValue(e, sp)
			if i < len(x.Keys) {
				out.M[x.Keys[i]] = out.Values[i]
			}
		}
		return out
	}
	return v
}

// packPrefix marks a packed (key, original) string. The key is hex
// so it never contains the \x01 separator, and \x01 sorts below every
// hex digit so a shorter key still sorts first.
const packPrefix = "\x00\x01gsqlcoll:"

// PACK returns key + "\x01" + original so that binary comparison of
// packed values follows the collation.
func PACK(args ...value.Value) (value.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("collation_pack: expected 2 arguments")
	}
	sp, err := specArg(args[1])
	if err != nil {
		return nil, err
	}
	if sp.binary {
		return args[0], nil
	}
	return packValue(args[0], sp), nil
}

func packValue(v value.Value, sp spec) value.Value {
	switch x := v.(type) {
	case value.StringValue:
		return value.StringValue(packPrefix + sp.keyOf(string(x)) + "\x01" + string(x))
	case *value.ArrayValue:
		out := &value.ArrayValue{Values: make([]value.Value, len(x.Values))}
		for i, e := range x.Values {
			out.Values[i] = packValue(e, sp)
		}
		return out
	}
	return v
}

// SplitPacked reports the key and original of a packed string.
func SplitPacked(s string) (key, orig string, ok bool) {
	if !strings.HasPrefix(s, packPrefix) {
		return "", "", false
	}
	rest := s[len(packPrefix):]
	i := strings.IndexByte(rest, '\x01')
	if i < 0 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// UNPACK reverses PACK, recursing into arrays and structs.
func UNPACK(args ...value.Value) (value.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("collation_unpack: expected 1 argument")
	}
	return Unpack(args[0]), nil
}

// Unpack strips the PACK envelope from v.
func Unpack(v value.Value) value.Value {
	switch x := v.(type) {
	case value.StringValue:
		if _, orig, ok := SplitPacked(string(x)); ok {
			return value.StringValue(orig)
		}
	case *value.ArrayValue:
		out := &value.ArrayValue{Values: make([]value.Value, len(x.Values))}
		for i, e := range x.Values {
			out.Values[i] = Unpack(e)
		}
		return out
	case *value.StructValue:
		out := &value.StructValue{Keys: x.Keys, Values: make([]value.Value, len(x.Values)), M: map[string]value.Value{}}
		for i, e := range x.Values {
			out.Values[i] = Unpack(e)
			if i < len(x.Keys) {
				out.M[x.Keys[i]] = out.Values[i]
			}
		}
		return out
	}
	return v
}

// matcher finds collation-equal substrings over rune offsets.
type matcher struct {
	sp    spec
	runes []rune
	// byteOff[i] is the byte offset of rune i.
	byteOff []int
	src     string
}

func newMatcher(sp spec, s string) *matcher {
	m := &matcher{sp: sp, src: s}
	for i, r := range s {
		m.runes = append(m.runes, r)
		m.byteOff = append(m.byteOff, i)
	}
	m.byteOff = append(m.byteOff, len(s))
	return m
}

func (m *matcher) sub(i, j int) string { return m.src[m.byteOff[i]:m.byteOff[j]] }

// matchAt returns the smallest rune end j >= i such that
// src[i:j] is collation-equal to key, or -1. When anchorEnd is set
// only j == len(runes) qualifies.
func (m *matcher) matchAt(i int, key string, anchorEnd bool) int {
	n := len(m.runes)
	if anchorEnd {
		if m.sp.keyOf(m.sub(i, n)) == key {
			return n
		}
		return -1
	}
	for j := i; j <= n; j++ {
		if m.sp.keyOf(m.sub(i, j)) == key {
			return j
		}
	}
	return -1
}

// find returns the leftmost match of key starting at rune >= from,
// as rune offsets (start, end), or (-1, -1).
func (m *matcher) find(from int, key string) (int, int) {
	for i := from; i <= len(m.runes); i++ {
		if j := m.matchAt(i, key, false); j >= 0 {
			if j == i && i < len(m.runes) && key != "" {
				// A non-empty key never matches an empty span.
				continue
			}
			return i, j
		}
	}
	return -1, -1
}

func stringArgs(name string, args []value.Value, n int) ([]string, spec, bool, error) {
	if len(args) != n+1 {
		return nil, spec{}, false, fmt.Errorf("%s: invalid number of arguments", name)
	}
	sp, err := specArg(args[n])
	if err != nil {
		return nil, spec{}, false, err
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		if args[i] == nil {
			return nil, sp, true, nil
		}
		s, err := args[i].ToString()
		if err != nil {
			return nil, sp, false, err
		}
		out[i] = s
	}
	return out, sp, false, nil
}

// REPLACE(original, from, to, spec).
func REPLACE(args ...value.Value) (value.Value, error) {
	s, sp, isNull, err := stringArgs("REPLACE", args, 3)
	if err != nil || isNull {
		return nil, err
	}
	if sp.binary {
		return value.StringValue(strings.ReplaceAll(s[0], s[1], s[2])), nil
	}
	key := sp.keyOf(s[1])
	if s[1] == "" || key == "" {
		return value.StringValue(s[0]), nil
	}
	m := newMatcher(sp, s[0])
	var b strings.Builder
	pos := 0
	for {
		st, en := m.find(pos, key)
		if st < 0 {
			break
		}
		b.WriteString(m.sub(pos, st))
		b.WriteString(s[2])
		pos = en
	}
	b.WriteString(m.sub(pos, len(m.runes)))
	return value.StringValue(b.String()), nil
}

// SPLIT(value, delimiter, spec).
func SPLIT(args ...value.Value) (value.Value, error) {
	s, sp, isNull, err := stringArgs("SPLIT", args, 2)
	if err != nil || isNull {
		return nil, err
	}
	key := sp.keyOf(s[1])
	out := &value.ArrayValue{}
	if s[1] == "" || key == "" || sp.binary {
		var parts []string
		if s[1] == "" {
			if s[0] == "" {
				parts = []string{""}
			} else {
				for _, r := range s[0] {
					parts = append(parts, string(r))
				}
			}
		} else {
			parts = strings.Split(s[0], s[1])
		}
		for _, p := range parts {
			out.Values = append(out.Values, value.StringValue(p))
		}
		return out, nil
	}
	m := newMatcher(sp, s[0])
	pos := 0
	for {
		st, en := m.find(pos, key)
		if st < 0 {
			break
		}
		out.Values = append(out.Values, value.StringValue(m.sub(pos, st)))
		pos = en
	}
	out.Values = append(out.Values, value.StringValue(m.sub(pos, len(m.runes))))
	return out, nil
}

// STRPOS(value, subvalue, spec) — 1-based character position.
func STRPOS(args ...value.Value) (value.Value, error) {
	s, sp, isNull, err := stringArgs("STRPOS", args, 2)
	if err != nil || isNull {
		return nil, err
	}
	if sp.binary {
		i := strings.Index(s[0], s[1])
		if i < 0 {
			return value.IntValue(0), nil
		}
		return value.IntValue(utf8.RuneCountInString(s[0][:i]) + 1), nil
	}
	m := newMatcher(sp, s[0])
	st, _ := m.find(0, sp.keyOf(s[1]))
	return value.IntValue(st + 1), nil
}

// INSTR(value, subvalue [, position [, occurrence]], spec).
func INSTR(args ...value.Value) (value.Value, error) {
	if len(args) < 3 || len(args) > 5 {
		return nil, fmt.Errorf("INSTR: invalid number of arguments")
	}
	for _, a := range args[:len(args)-1] {
		if a == nil {
			return nil, nil
		}
	}
	sp, err := specArg(args[len(args)-1])
	if err != nil {
		return nil, err
	}
	src, err := args[0].ToString()
	if err != nil {
		return nil, err
	}
	sub, err := args[1].ToString()
	if err != nil {
		return nil, err
	}
	position, occurrence := int64(1), int64(1)
	if len(args) >= 4 {
		if position, err = args[2].ToInt64(); err != nil {
			return nil, err
		}
	}
	if len(args) == 5 {
		if occurrence, err = args[3].ToInt64(); err != nil {
			return nil, err
		}
	}
	if position == 0 {
		return nil, fmt.Errorf("INSTR: position must not be 0")
	}
	if occurrence <= 0 {
		return nil, fmt.Errorf("INSTR: occurrence must be positive, got %d", occurrence)
	}
	m := newMatcher(sp, src)
	key := sp.keyOf(sub)
	n := int64(len(m.runes))
	isMatch := func(i int64) bool {
		j := m.matchAt(int(i), key, false)
		return j >= 0 && (j > int(i) || key == "")
	}
	var found int64
	if position > 0 {
		if position > n {
			return value.IntValue(0), nil
		}
		for i := position - 1; i < n; i++ {
			if isMatch(i) {
				found++
				if found == occurrence {
					return value.IntValue(i + 1), nil
				}
			}
		}
		return value.IntValue(0), nil
	}
	start := n + position
	if start < 0 {
		return value.IntValue(0), nil
	}
	for i := start; i >= 0; i-- {
		if isMatch(i) {
			found++
			if found == occurrence {
				return value.IntValue(i + 1), nil
			}
		}
	}
	return value.IntValue(0), nil
}

// STARTS_WITH(value, prefix, spec).
func STARTS_WITH(args ...value.Value) (value.Value, error) {
	s, sp, isNull, err := stringArgs("STARTS_WITH", args, 2)
	if err != nil || isNull {
		return nil, err
	}
	if sp.binary {
		return value.BoolValue(strings.HasPrefix(s[0], s[1])), nil
	}
	m := newMatcher(sp, s[0])
	return value.BoolValue(m.matchAt(0, sp.keyOf(s[1]), false) >= 0), nil
}

// ENDS_WITH(value, suffix, spec).
func ENDS_WITH(args ...value.Value) (value.Value, error) {
	s, sp, isNull, err := stringArgs("ENDS_WITH", args, 2)
	if err != nil || isNull {
		return nil, err
	}
	if sp.binary {
		return value.BoolValue(strings.HasSuffix(s[0], s[1])), nil
	}
	m := newMatcher(sp, s[0])
	key := sp.keyOf(s[1])
	for i := len(m.runes); i >= 0; i-- {
		if m.matchAt(i, key, true) >= 0 {
			return value.BoolValue(true), nil
		}
	}
	return value.BoolValue(false), nil
}

// LIKE(value, pattern, spec). With a non-binary collation only '%'
// wildcards are allowed; '_' raises an error as in the reference
// implementation (googlesql/public/functions/like.cc).
func LIKE(args ...value.Value) (value.Value, error) {
	s, sp, isNull, err := stringArgs("LIKE", args, 2)
	if err != nil || isNull {
		return nil, err
	}
	segs, err := likeSegments(s[1], !sp.binary)
	if err != nil {
		return nil, err
	}
	return value.BoolValue(likeMatch(sp, s[0], segs)), nil
}

// likeSegments splits a LIKE pattern at unescaped '%'.
func likeSegments(pattern string, collated bool) ([]string, error) {
	var segs []string
	var cur strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '\\':
			if i+1 >= len(pattern) {
				return nil, fmt.Errorf("LIKE pattern ends with a backslash")
			}
			i++
			cur.WriteByte(pattern[i])
		case '_':
			if collated {
				return nil, fmt.Errorf("LIKE pattern has '_' which is not allowed when its operands have collation: %s", pattern)
			}
			cur.WriteByte(c)
		case '%':
			segs = append(segs, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	segs = append(segs, cur.String())
	return segs, nil
}

func likeMatch(sp spec, s string, segs []string) bool {
	if sp.binary {
		// Only reached for the binary collation, where no '_' was
		// treated specially: fall back to plain segment matching.
		if len(segs) == 1 {
			return s == segs[0]
		}
		if !strings.HasPrefix(s, segs[0]) {
			return false
		}
		rest := s[len(segs[0]):]
		last := segs[len(segs)-1]
		for _, seg := range segs[1 : len(segs)-1] {
			i := strings.Index(rest, seg)
			if i < 0 {
				return false
			}
			rest = rest[i+len(seg):]
		}
		return len(rest) >= len(last) && strings.HasSuffix(rest, last)
	}
	m := newMatcher(sp, s)
	n := len(m.runes)
	if len(segs) == 1 {
		return m.matchAt(0, sp.keyOf(segs[0]), true) >= 0
	}
	pos := 0
	if first := sp.keyOf(segs[0]); first != "" {
		j := m.matchAt(0, first, false)
		if j < 0 {
			return false
		}
		pos = j
	}
	for _, seg := range segs[1 : len(segs)-1] {
		key := sp.keyOf(seg)
		if key == "" {
			continue
		}
		_, en := m.find(pos, key)
		if en < 0 {
			return false
		}
		pos = en
	}
	last := sp.keyOf(segs[len(segs)-1])
	if last == "" {
		return true
	}
	for i := n; i >= pos; i-- {
		if m.matchAt(i, last, true) >= 0 {
			return true
		}
	}
	return false
}
