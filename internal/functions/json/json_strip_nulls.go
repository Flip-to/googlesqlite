package json

import (
	"github.com/goccy/go-json"
	"github.com/goccy/googlesqlite/internal/value"
)

// JSON_STRIP_NULLS recursively removes JSON nulls from objects and,
// when includeArrays is set, from arrays. With removeEmpty, containers
// left empty are removed as well (arrays only when includeArrays is
// set). When nothing is left the result is JSON null
// (json_functions.md, JSON_STRIP_NULLS).
func JSON_STRIP_NULLS(v string, path string, includeArrays, removeEmpty bool) (value.Value, error) {
	var node any
	if err := json.Unmarshal([]byte(v), &node); err != nil {
		return nil, err
	}
	var result any
	if path == "" || path == "$" {
		stripped, drop := stripNulls(node, includeArrays, removeEmpty)
		if drop {
			return value.JsonValue("null"), nil
		}
		result = stripped
	} else {
		segs, err := parseJSONPath(path)
		if err != nil {
			return nil, err
		}
		result = stripNullsAt(node, segs, includeArrays, removeEmpty)
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return value.JsonValue(string(out)), nil
}

// stripNullsAt strips the subtree addressed by segs; a subtree that
// strips to nothing becomes JSON null.
func stripNullsAt(node any, segs []pathSegment, includeArrays, removeEmpty bool) any {
	if len(segs) == 0 {
		stripped, drop := stripNulls(node, includeArrays, removeEmpty)
		if drop {
			return nil
		}
		return stripped
	}
	seg := segs[0]
	switch v := node.(type) {
	case map[string]any:
		if child, ok := v[seg.name]; ok && !seg.arrayIndex {
			v[seg.name] = stripNullsAt(child, segs[1:], includeArrays, removeEmpty)
		}
		return v
	case []any:
		if seg.arrayIndex && seg.index >= 0 && seg.index < len(v) {
			v[seg.index] = stripNullsAt(v[seg.index], segs[1:], includeArrays, removeEmpty)
		}
		return v
	}
	return node
}

// stripNulls returns the stripped value and whether it should itself
// be removed from its parent.
func stripNulls(node any, includeArrays, removeEmpty bool) (any, bool) {
	switch v := node.(type) {
	case nil:
		return nil, true
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			cleaned, drop := stripNulls(child, includeArrays, removeEmpty)
			if drop {
				continue
			}
			out[k] = cleaned
		}
		return out, removeEmpty && len(out) == 0
	case []any:
		out := make([]any, 0, len(v))
		for _, child := range v {
			cleaned, drop := stripNulls(child, includeArrays, removeEmpty)
			if drop {
				if includeArrays {
					continue
				}
				// Nulls inside arrays are kept; an emptied container
				// in an array keeps its (now empty) form.
				if child == nil {
					out = append(out, nil)
				} else {
					out = append(out, cleaned)
				}
				continue
			}
			out = append(out, cleaned)
		}
		return out, removeEmpty && includeArrays && len(out) == 0
	default:
		return v, false
	}
}

// BindJsonStripNulls handles JSON_STRIP_NULLS(json [, path]
// [, include_arrays] [, remove_empty]); the formatter envelopes the
// BOOL arguments so they arrive as BoolValue.
func BindJsonStripNulls(args ...value.Value) (value.Value, error) {
	if len(args) == 0 || args[0] == nil {
		return nil, nil
	}
	s, err := args[0].ToString()
	if err != nil {
		return nil, err
	}
	var path string
	includeArrays, removeEmpty := true, false
	var bools []value.Value
	for _, a := range args[1:] {
		if a == nil {
			// A NULL json_path / include_arrays / remove_empty returns
			// json_expr unchanged.
			return args[0], nil
		}
		switch x := a.(type) {
		case value.StringValue:
			path = string(x)
		default:
			bools = append(bools, a)
		}
	}
	if len(bools) > 0 {
		if b, err := bools[0].ToBool(); err == nil {
			includeArrays = b
		}
	}
	if len(bools) > 1 {
		if b, err := bools[1].ToBool(); err == nil {
			removeEmpty = b
		}
	}
	return JSON_STRIP_NULLS(s, path, includeArrays, removeEmpty)
}
