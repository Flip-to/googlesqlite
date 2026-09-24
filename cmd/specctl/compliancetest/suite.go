package compliancetest

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// SuiteCase is a Case enriched with file-level context.
type SuiteCase struct {
	Case
	File  string // base name, e.g. "strings.test"
	Index int    // 1-based position in the file
	// Prepare is true for [prepare_database] setup statements.
	Prepare bool
	// AllFeatures is Features plus any [default required_features=...]
	// declared earlier in the file.
	AllFeatures []string
	Forbidden   []string
	Params      []Param
	// ParamErr is set when [parameters=...] could not be parsed.
	ParamErr string
}

// Param is one `[parameters=<expr> as <name>, ...]` entry.
type Param struct {
	Name string
	Expr string
}

// LoadSuiteFile parses a compliance .test file into SuiteCases.
func LoadSuiteFile(path string) ([]SuiteCase, error) {
	cases, err := ParseFile(path)
	if err != nil {
		return nil, err
	}
	base := filepath.Base(path)
	var defaults []string
	out := make([]SuiteCase, 0, len(cases))
	for i, c := range cases {
		if d, ok := c.Attrs["default required_features"]; ok {
			defaults = append(defaults, splitCSV(d)...)
		}
		sc := SuiteCase{Case: c, File: base, Index: i + 1}
		_, sc.Prepare = c.Attrs["prepare_database"]
		sc.AllFeatures = dedup(append(append([]string{}, defaults...), c.Features...))
		if f, ok := c.Attrs["forbidden_features"]; ok {
			sc.Forbidden = splitCSV(f)
		}
		if p, ok := c.Attrs["parameters"]; ok {
			params, err := ParseParams(p)
			if err != nil {
				sc.ParamErr = err.Error()
			}
			sc.Params = params
		}
		out = append(out, sc)
	}
	return out, nil
}

func dedup(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0]
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

var asRe = regexp.MustCompile(`(?i)\s+as\s+`)

// ParseParams parses the body of a [parameters=...] attribute.
func ParseParams(s string) ([]Param, error) {
	var out []Param
	for _, part := range splitTopLevelCommas(s) {
		// The alias follows the last top-level AS; an inner CAST(x AS T)
		// sits inside parentheses and is skipped. AS is optional, in
		// which case the alias is the last top-level word.
		var chosen []int
		for _, l := range asRe.FindAllStringIndex(part, -1) {
			if depthAt(part, l[0]) == 0 {
				chosen = l
			}
		}
		if chosen == nil {
			for i := len(part) - 1; i > 0; i-- {
				if strings.ContainsRune(" \t\n", rune(part[i])) && depthAt(part, i) == 0 {
					chosen = []int{i, i + 1}
					break
				}
			}
		}
		if chosen == nil {
			return nil, fmt.Errorf("parameter %q has no alias", part)
		}
		out = append(out, Param{
			Expr: strings.TrimSpace(part[:chosen[0]]),
			Name: strings.TrimSpace(part[chosen[1]:]),
		})
	}
	return out, nil
}

func depthAt(s string, pos int) int {
	depth := 0
	var quote byte
	for i := 0; i < pos && i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '(', '[', '<', '{':
			depth++
		case ')', ']', '>', '}':
			depth--
		}
	}
	return depth
}

// SubstituteParams inlines each @name reference as (expr). String
// literals and @@system variables are left untouched.
func SubstituteParams(sql string, params []Param) string {
	if len(params) == 0 {
		return sql
	}
	byName := map[string]string{}
	for _, p := range params {
		byName[strings.ToLower(p.Name)] = p.Expr
	}
	var b strings.Builder
	var quote byte
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		if quote != 0 {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(sql) {
				i++
				b.WriteByte(sql[i])
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			b.WriteByte(c)
			continue
		}
		if c == '@' && i+1 < len(sql) && sql[i+1] != '@' && (i == 0 || sql[i-1] != '@') {
			j := i + 1
			for j < len(sql) && (isIdent(sql[j])) {
				j++
			}
			if expr, ok := byName[strings.ToLower(sql[i+1:j])]; ok {
				if plainLiteralRe.MatchString(expr) {
					// Some grammar positions accept only a literal or a
					// parameter (quantifier bounds `{@lo, @hi}`,
					// `COLLATE @param`), so a plain literal is inlined
					// without the parentheses.
					b.WriteString(expr)
				} else {
					b.WriteString("(" + expr + ")")
				}
				i = j - 1
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// plainLiteralRe matches an unsigned integer, a quoted string without
// escapes, or TRUE / FALSE.
var plainLiteralRe = regexp.MustCompile(`^(?i:[0-9]+|"[^"\\]*"|'[^'\\]*'|true|false)$`)

func isIdent(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

var createTableRe = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?(?:TEMP(?:ORARY)?\s+)?(?:TABLE\s+FUNCTION|AGGREGATE\s+FUNCTION|TABLE|VIEW|FUNCTION|PROPERTY\s+GRAPH)\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z_][\w.]*|` + "`[^`]+`" + `)`)

// CreatedObject returns the object name a setup statement creates.
func CreatedObject(sql string) string {
	m := createTableRe.FindStringSubmatch(sql)
	if m == nil {
		return ""
	}
	return strings.Trim(m[1], "`")
}
