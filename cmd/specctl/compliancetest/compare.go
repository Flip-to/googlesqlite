package compliancetest

import (
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// FloatULPs is the tolerance for DOUBLE comparison, in units in the
// last place. It mirrors the default ULP margin the GoogleSQL
// compliance framework applies to floating point results. FLOAT
// values are rounded to float32 and compared with the same margin.
const FloatULPs = 4

func nan() float64      { return math.NaN() }
func inf(s int) float64 { return math.Inf(s) }

// CompareRows compares the expected row set against driver rows. Each
// driver row is one []any of column values as returned by
// database/sql's Scan into *any. It returns "" on a match, or a short
// human-readable reason for the first difference found.
func CompareRows(exp Val, rows [][]any) string {
	if exp.Kind != "ARRAY" {
		return "expected value is not an ARRAY"
	}
	act := make([]any, len(rows))
	elemIsStruct := exp.Type != nil && exp.Type.Elem != nil && exp.Type.Elem.Kind == "STRUCT"
	for i, r := range rows {
		if elemIsStruct {
			act[i] = r
			continue
		}
		// Value table: a single column per row.
		if len(r) != 1 {
			return fmt.Sprintf("value-table result has %d columns", len(r))
		}
		act[i] = r[0]
	}
	if exp.Null {
		return "expected NULL row set"
	}
	if len(exp.Elems) != len(act) {
		return fmt.Sprintf("row count: expected %d, got %d", len(exp.Elems), len(act))
	}
	if exp.Ordered {
		for i := range exp.Elems {
			if why := Compare(exp.Elems[i], act[i]); why != "" {
				return fmt.Sprintf("row %d: %s", i+1, why)
			}
		}
		return ""
	}
	return matchUnordered(exp.Elems, act, "row")
}

// matchUnordered does a greedy multiset match of expected against
// actual values.
func matchUnordered(exp []Val, act []any, what string) string {
	used := make([]bool, len(act))
	for i, e := range exp {
		found := false
		firstWhy := ""
		for j, a := range act {
			if used[j] {
				continue
			}
			why := Compare(e, a)
			if why == "" {
				used[j] = true
				found = true
				break
			}
			if firstWhy == "" {
				firstWhy = why
			}
		}
		if !found {
			return fmt.Sprintf("no actual %s matches expected %s %d %s (closest diff: %s)", what, what, i+1, clip(e.String(), 200), firstWhy)
		}
	}
	return ""
}

// Compare compares one expected value with one driver value.
func Compare(exp Val, act any) string {
	if exp.Null || act == nil {
		if exp.Null && act == nil {
			return ""
		}
		return fmt.Sprintf("expected %s, got %s", clip(exp.String(), 120), clip(RenderAny(act), 120))
	}
	switch exp.Kind {
	case "ARRAY":
		arr, ok := act.([]any)
		if !ok {
			return fmt.Sprintf("expected ARRAY, got %T %s", act, clip(RenderAny(act), 80))
		}
		if len(arr) != len(exp.Elems) {
			return fmt.Sprintf("array length: expected %d, got %d (expected %s, got %s)", len(exp.Elems), len(arr), clip(exp.String(), 120), clip(RenderAny(act), 120))
		}
		if exp.Ordered || len(arr) <= 1 {
			for i := range exp.Elems {
				if why := Compare(exp.Elems[i], arr[i]); why != "" {
					return fmt.Sprintf("array element %d: %s", i+1, why)
				}
			}
			return ""
		}
		return matchUnordered(exp.Elems, arr, "element")
	case "STRUCT":
		fields, ok := act.([]any)
		if !ok {
			if m, isMap := act.(map[string]any); isMap && exp.Type != nil {
				fields = make([]any, 0, len(m))
				for _, f := range exp.Type.Fields {
					fields = append(fields, m[f.Name])
				}
			} else {
				return fmt.Sprintf("expected STRUCT, got %T", act)
			}
		}
		if len(fields) != len(exp.Elems) {
			return fmt.Sprintf("column count: expected %d, got %d", len(exp.Elems), len(fields))
		}
		for i := range exp.Elems {
			if why := Compare(exp.Elems[i], fields[i]); why != "" {
				name := ""
				if exp.Type != nil && i < len(exp.Type.Fields) && exp.Type.Fields[i].Name != "" {
					name = " (" + exp.Type.Fields[i].Name + ")"
				}
				return fmt.Sprintf("column %d%s: %s", i+1, name, why)
			}
		}
		return ""
	}
	got, err := canonActual(exp, act)
	if err != nil {
		return fmt.Sprintf("expected %s, got %s (%v)", clip(exp.String(), 120), clip(RenderAny(act), 120), err)
	}
	if scalarEqual(exp, got) {
		return ""
	}
	return fmt.Sprintf("expected %s, got %s", clip(exp.String(), 160), clip(got.String(), 160))
}

func canonActual(exp Val, act any) (Val, error) {
	t := exp.Type
	if t == nil {
		t = &Type{Kind: exp.Kind}
	}
	switch x := act.(type) {
	case float64:
		if exp.IsFloat {
			return Val{Kind: t.Kind, Type: t, F: x, IsFloat: true, S: strconv.FormatFloat(x, 'g', -1, 64)}, nil
		}
		return Canon(t, strconv.FormatFloat(x, 'f', -1, 64))
	case float32:
		return Val{Kind: t.Kind, Type: t, F: float64(x), IsFloat: true}, nil
	case int64:
		return Canon(t, strconv.FormatInt(x, 10))
	case uint64:
		return Canon(t, strconv.FormatUint(x, 10))
	case bool:
		return Canon(t, strconv.FormatBool(x))
	case []byte:
		if t.Kind == "BYTES" || t.Kind == "STRING" {
			return Val{Kind: t.Kind, Type: t, S: string(x)}, nil
		}
		return Canon(t, string(x))
	case time.Time:
		switch t.Kind {
		case "DATE":
			return Canon(t, x.Format("2006-01-02"))
		case "DATETIME":
			return Canon(t, x.Format("2006-01-02 15:04:05.999999999"))
		case "TIME":
			return Canon(t, x.Format("15:04:05.999999999"))
		}
		return Canon(t, x.UTC().Format("2006-01-02 15:04:05.999999999+00"))
	case string:
		switch t.Kind {
		case "STRING":
			return Val{Kind: t.Kind, Type: t, S: x}, nil
		case "BYTES":
			// The driver returns BYTES base64-encoded.
			b, err := base64.StdEncoding.DecodeString(x)
			if err != nil {
				return Val{Kind: t.Kind, Type: t, S: x}, nil
			}
			return Val{Kind: t.Kind, Type: t, S: string(b)}, nil
		case "RANGE":
			return canonRange(t, x)
		}
		return Canon(t, x)
	}
	return Canon(t, fmt.Sprint(act))
}

func scalarEqual(exp, got Val) bool {
	if exp.IsFloat && got.IsFloat {
		if exp.Kind == "FLOAT" || exp.Kind == "FLOAT32" {
			return floatEqualULP(float64(float32(exp.F)), float64(float32(got.F)))
		}
		return floatEqualULP(exp.F, got.F)
	}
	return exp.S == got.S
}

func floatEqualULP(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	if a == b {
		return true
	}
	if math.IsInf(a, 0) || math.IsInf(b, 0) {
		return false
	}
	return ulpDistance(a, b) <= FloatULPs
}

func ulpDistance(a, b float64) uint64 {
	ia := orderedBits(a)
	ib := orderedBits(b)
	if ia > ib {
		return uint64(ia - ib)
	}
	return uint64(ib - ia)
}

// orderedBits maps a float64 onto an integer line where adjacent
// representable values differ by one.
func orderedBits(f float64) int64 {
	b := int64(math.Float64bits(f))
	if b < 0 {
		return math.MinInt64 - b
	}
	return b
}

// RenderAny renders a driver value compactly for reports.
func RenderAny(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = RenderAny(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case string:
		return strconv.Quote(x)
	case []byte:
		return "b" + strconv.Quote(string(x))
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	}
	return fmt.Sprint(v)
}

// RenderRows renders driver rows for reports.
func RenderRows(rows [][]any) string {
	parts := make([]string, len(rows))
	for i, r := range rows {
		cells := make([]string, len(r))
		for j, c := range r {
			cells[j] = RenderAny(c)
		}
		parts[i] = "{" + strings.Join(cells, ", ") + "}"
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
