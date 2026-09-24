package internal

import (
	"encoding/json"
	"regexp"
	"strconv"

	"github.com/goccy/go-googlesql"

	"github.com/goccy/googlesqlite/internal/functions/longtail"
)

var maxLengthParamRe = regexp.MustCompile(`max_length\s*[:=]\s*(\d+)`)

// castTypeParamSpec returns the length limits declared by a cast
// target's type parameters (STRING(L) / BYTES(L), also nested in ARRAY
// and STRUCT) as a JSON spec for googlesqlite_check_type_parameters, or
// "" when the target declares none.
func castTypeParamSpec(node *googlesql.ResolvedCast) string {
	params, _ := node.TypeParameters()
	if params == nil || typeParamsEmpty(params) {
		if mods, _ := node.TypeModifiers(); mods != nil {
			params, _ = mods.TypeParameters()
		}
	}
	if params == nil || typeParamsEmpty(params) {
		return ""
	}
	spec := typeParamSpec(params)
	if spec == nil {
		return ""
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return ""
	}
	return string(b)
}

func typeParamsEmpty(p *googlesql.TypeParameters) bool {
	empty, err := p.IsEmpty()
	return err != nil || empty
}

func typeParamSpec(p *googlesql.TypeParameters) *longtail.TypeParamSpec {
	if p == nil || typeParamsEmpty(p) {
		return nil
	}
	if isStr, _ := p.IsStringTypeParameters(); isStr {
		dbg, _ := p.DebugString()
		m := maxLengthParamRe.FindStringSubmatch(dbg)
		if m == nil {
			return nil
		}
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || n <= 0 {
			return nil
		}
		return &longtail.TypeParamSpec{MaxLength: n}
	}
	children, _ := p.ChildList()
	if len(children) == 0 {
		return nil
	}
	spec := &longtail.TypeParamSpec{Children: make([]*longtail.TypeParamSpec, len(children))}
	any := false
	for i, c := range children {
		spec.Children[i] = typeParamSpec(c)
		any = any || spec.Children[i] != nil
	}
	if !any {
		return nil
	}
	return spec
}
