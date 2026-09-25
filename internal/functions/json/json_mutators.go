package json

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/goccy/go-json"
	"github.com/goccy/googlesqlite/internal/value"
)

// pathSegment represents one segment of a `$.foo.bar[2]` JSON path.
// One of name (object key) or index (array offset, with arrayIndex
// = true) is set per segment.
type pathSegment struct {
	name       string
	index      int
	arrayIndex bool
}

func parseJSONPath(path string) ([]pathSegment, error) {
	if !strings.HasPrefix(path, "$") {
		return nil, fmt.Errorf("invalid JSON path %q: must start with '$'", path)
	}
	rest := path[1:]
	var segs []pathSegment
	for len(rest) > 0 {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			end := 0
			for end < len(rest) && rest[end] != '.' && rest[end] != '[' {
				end++
			}
			if end == 0 {
				return nil, fmt.Errorf("invalid JSON path %q: empty key", path)
			}
			segs = append(segs, pathSegment{name: rest[:end]})
			rest = rest[end:]
		case '[':
			close := strings.IndexByte(rest, ']')
			if close < 0 {
				return nil, fmt.Errorf("invalid JSON path %q: unterminated '['", path)
			}
			idx, err := strconv.Atoi(rest[1:close])
			if err != nil {
				return nil, fmt.Errorf("invalid JSON path %q: bad index", path)
			}
			segs = append(segs, pathSegment{index: idx, arrayIndex: true})
			rest = rest[close+1:]
		default:
			return nil, fmt.Errorf("invalid JSON path %q: unexpected %q", path, rest[0])
		}
	}
	return segs, nil
}

func jsonRemoveAtPath(node any, segs []pathSegment) any {
	if len(segs) == 0 {
		return nil
	}
	seg := segs[0]
	if len(segs) == 1 {
		switch v := node.(type) {
		case map[string]any:
			if !seg.arrayIndex {
				delete(v, seg.name)
			}
			return v
		case []any:
			if seg.arrayIndex && seg.index >= 0 && seg.index < len(v) {
				return append(v[:seg.index], v[seg.index+1:]...)
			}
			return v
		}
		return node
	}
	switch v := node.(type) {
	case map[string]any:
		if seg.arrayIndex {
			return v
		}
		child, ok := v[seg.name]
		if !ok {
			return v
		}
		v[seg.name] = jsonRemoveAtPath(child, segs[1:])
		return v
	case []any:
		if !seg.arrayIndex || seg.index < 0 || seg.index >= len(v) {
			return v
		}
		v[seg.index] = jsonRemoveAtPath(v[seg.index], segs[1:])
		return v
	}
	return node
}

// jsonSetAtPath replaces the value at segs. When createIfMissing is
// set, missing object members are created and arrays are padded with
// JSON null up to the requested index (json_functions.md, JSON_SET);
// otherwise only existing paths are replaced. Paths that do not match
// the document shape (a subscript on an object, a member on an array,
// anything below a scalar) leave the document unchanged.
func jsonSetAtPath(node any, segs []pathSegment, val any, createIfMissing bool) any {
	if len(segs) == 0 {
		return val
	}
	seg := segs[0]
	switch v := node.(type) {
	case map[string]any:
		if seg.arrayIndex {
			return v
		}
		child, ok := v[seg.name]
		if !ok {
			if !createIfMissing {
				return v
			}
			v[seg.name] = jsonBuildMissing(segs[1:], val)
			return v
		}
		v[seg.name] = jsonSetAtPath(child, segs[1:], val, createIfMissing)
		return v
	case []any:
		if !seg.arrayIndex || seg.index < 0 {
			return v
		}
		if seg.index < len(v) {
			v[seg.index] = jsonSetAtPath(v[seg.index], segs[1:], val, createIfMissing)
			return v
		}
		if !createIfMissing {
			return v
		}
		for len(v) < seg.index {
			v = append(v, nil)
		}
		return append(v, jsonBuildMissing(segs[1:], val))
	case nil:
		if !createIfMissing {
			return node
		}
		return jsonBuildMissing(segs, val)
	}
	return node
}

// jsonBuildMissing materialises the containers a JSON_SET path needs.
func jsonBuildMissing(segs []pathSegment, val any) any {
	if len(segs) == 0 {
		return val
	}
	seg := segs[0]
	if seg.arrayIndex {
		if seg.index < 0 {
			return nil
		}
		arr := make([]any, seg.index, seg.index+1)
		return append(arr, jsonBuildMissing(segs[1:], val))
	}
	return map[string]any{seg.name: jsonBuildMissing(segs[1:], val)}
}

// JSON_REMOVE removes one or more path expressions from a JSON value.
func JSON_REMOVE(jsonText string, paths []string) (value.Value, error) {
	var node any
	if err := json.Unmarshal([]byte(jsonText), &node); err != nil {
		return nil, err
	}
	for _, p := range paths {
		segs, err := parseJSONPath(p)
		if err != nil {
			return nil, err
		}
		node = jsonRemoveAtPath(node, segs)
	}
	out, err := json.Marshal(node)
	if err != nil {
		return nil, err
	}
	return value.JsonValue(string(out)), nil
}

// JSON_SET sets one or more (path, value) pairs on a JSON value.
// pairs come in alternating order [path1, val1, path2, val2, ...].
func JSON_SET(jsonText string, pairs []value.Value, createIfMissing bool) (value.Value, error) {
	if len(pairs)%2 != 0 {
		return nil, fmt.Errorf("JSON_SET: path/value pairs must be even, got %d", len(pairs))
	}
	var node any
	if err := json.Unmarshal([]byte(jsonText), &node); err != nil {
		return nil, err
	}
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i] == nil {
			// A NULL json_path skips its pair (json_functions.md JSON_SET).
			continue
		}
		pathStr, err := pairs[i].ToString()
		if err != nil {
			return nil, err
		}
		segs, err := parseJSONPath(pathStr)
		if err != nil {
			return nil, err
		}
		var val any
		if pairs[i+1] != nil {
			raw, err := pairs[i+1].ToJSON()
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal([]byte(raw), &val); err != nil {
				return nil, err
			}
		}
		node = jsonSetAtPath(node, segs, val, createIfMissing)
	}
	out, err := json.Marshal(node)
	if err != nil {
		return nil, err
	}
	return value.JsonValue(string(out)), nil
}

func BindJsonRemove(args ...value.Value) (value.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("JSON_REMOVE: need at least 2 args, got %d", len(args))
	}
	if args[0] == nil {
		return nil, nil
	}
	jsonText, err := args[0].ToString()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(args)-1)
	for _, a := range args[1:] {
		if a == nil {
			// A NULL JSONPath removes nothing (json_queries.test
			// json_remove_null_jsonpath).
			continue
		}
		s, err := a.ToString()
		if err != nil {
			return nil, err
		}
		paths = append(paths, s)
	}
	return JSON_REMOVE(jsonText, paths)
}

func BindJsonSet(args ...value.Value) (value.Value, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("JSON_SET: need at least 3 args, got %d", len(args))
	}
	if args[0] == nil {
		return nil, nil
	}
	jsonText, err := args[0].ToString()
	if err != nil {
		return nil, err
	}
	// GoogleSQL injects the `create_if_missing` named argument
	// (default true) as the trailing BoolValue when callers omit it,
	// so arg count for n path/value pairs is 1 + 2n + 1. Strip the
	// trailing bool back off before pair-pairing.
	pairs := args[1:]
	createIfMissing := true
	if len(pairs)%2 == 1 {
		// trailing arg is the create_if_missing bool injected by the
		// analyzer when the caller omits it.
		last := pairs[len(pairs)-1]
		if last == nil {
			// A NULL create_if_missing ignores the set operation.
			return args[0], nil
		}
		if b, err := last.ToBool(); err == nil {
			createIfMissing = b
		}
		pairs = pairs[:len(pairs)-1]
	}
	return JSON_SET(jsonText, pairs, createIfMissing)
}
