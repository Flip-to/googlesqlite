package internal

import (
	"context"
	"fmt"
	"strings"

	"github.com/goccy/go-googlesql"

	"github.com/goccy/googlesqlite/internal/value"
)

// Collation lowering (collation-concepts.md).
//
// With FEATURE_ANNOTATION_FRAMEWORK / FEATURE_COLLATION_SUPPORT on, the
// analyzer records the collation that governs every
// collation-sensitive call in ResolvedFunctionCallBase.collation_list
// (and in AggregateScan / WindowPartitioning collation lists,
// ResolvedOrderByItem.collation and ResolvedSubqueryExpr.in_collation).
// COLLATE() itself is the identity at runtime; the formatter rewrites
// the sensitive operations onto internal/functions/collation:
//
//   - comparisons and grouping run over googlesqlite_collation_key(x)
//   - MIN / MAX-like functions run over googlesqlite_collation_pack(x)
//     and unpack the result so the original string is returned
//   - substring functions switch to googlesqlite_collate_<fn>(..., spec)

// collationName returns the non-binary collation carried by c
// (searching ARRAY / STRUCT children), or "".
func collationName(c *googlesql.ResolvedCollation) string {
	if c == nil {
		return ""
	}
	if name, err := c.CollationName(); err == nil && name != "" {
		if name == "binary" {
			return ""
		}
		return name
	}
	children, err := c.ChildList()
	if err != nil {
		return ""
	}
	for _, child := range children {
		if name := collationName(child); name != "" {
			return name
		}
	}
	return ""
}

func collationListName(list []*googlesql.ResolvedCollation) string {
	for _, c := range list {
		if name := collationName(c); name != "" {
			return name
		}
	}
	return ""
}

// callCollation returns the non-binary collation of a function call.
func callCollation(node *ResolvedBaseFunctionCallNode) string {
	if node == nil {
		return ""
	}
	list, err := node.CollationList()
	if err != nil {
		return ""
	}
	return collationListName(list)
}

func collationSpecLiteral(spec string) string {
	lit, err := literalFromValue(value.StringValue(spec))
	if err != nil {
		return "'" + strings.ReplaceAll(spec, "'", "''") + "'"
	}
	return lit
}

func collationKeySQL(expr, spec string) string {
	return fmt.Sprintf("googlesqlite_collation_key(%s,%s)", expr, collationSpecLiteral(spec))
}

func collationPackSQL(expr, spec string) string {
	return fmt.Sprintf("googlesqlite_collation_pack(%s,%s)", expr, collationSpecLiteral(spec))
}

func collationUnpackSQL(expr string) string {
	return fmt.Sprintf("googlesqlite_collation_unpack(%s)", expr)
}

// collationKeyFuncs compare every argument under the collation.
var collationKeyFuncs = map[string]bool{
	"$equal":                true,
	"$not_equal":            true,
	"$less":                 true,
	"$less_or_equal":        true,
	"$greater":              true,
	"$greater_or_equal":     true,
	"$between":              true,
	"$is_distinct_from":     true,
	"$is_not_distinct_from": true,
	"$in":                   true,
	"$in_array":             true,
	"array_includes":        true,
	"array_includes_any":    true,
	"array_includes_all":    true,
	"count":                 true,
	"approx_count_distinct": true,
}

// collationPackFuncs select an element by collation order and return
// the original value.
var collationPackFuncs = map[string]bool{
	"min":             true,
	"max":             true,
	"array_min":       true,
	"array_max":       true,
	"percentile_disc": true,
	"greatest":        true,
	"least":           true,
}

// collationDistinctFuncs deduplicate on the collation key when called
// with DISTINCT; helper.Aggregator unpacks the argument.
var collationDistinctFuncs = map[string]bool{
	"array_agg":  true,
	"string_agg": true,
}

// collationSearchFuncs have a collation-aware runtime variant that
// takes the collation spec as its trailing argument.
var collationSearchFuncs = map[string]string{
	"replace":     "googlesqlite_collate_replace",
	"split":       "googlesqlite_collate_split",
	"strpos":      "googlesqlite_collate_strpos",
	"instr":       "googlesqlite_collate_instr",
	"starts_with": "googlesqlite_collate_starts_with",
	"ends_with":   "googlesqlite_collate_ends_with",
	"$like":       "googlesqlite_collate_like",
}

func rawFuncName(node *ResolvedBaseFunctionCallNode) string {
	fn, err := node.Function()
	if err != nil || fn == nil {
		return ""
	}
	name, err := fn.FullName(false)
	if err != nil {
		return ""
	}
	return name
}

// collationNeedsUnpack reports whether the formatted call returns a
// packed value that must be unpacked.
func collationNeedsUnpack(node *ResolvedBaseFunctionCallNode) bool {
	return collationPackFuncs[rawFuncName(node)] && callCollation(node) != ""
}

// collationPackDistinctArg packs the first argument of a collated
// DISTINCT array_agg / string_agg so helper.Aggregator deduplicates on
// the collation key.
func collationPackDistinctArg(node *ResolvedBaseFunctionCallNode, distinct bool, args []string) []string {
	if !distinct || len(args) == 0 || !collationDistinctFuncs[rawFuncName(node)] {
		return args
	}
	spec := callCollation(node)
	if spec == "" {
		return args
	}
	out := append([]string(nil), args...)
	out[0] = collationPackSQL(out[0], spec)
	return out
}

// applyCallCollation rewrites funcName / args for a collated call.
// rawName is the analyzer's function name (e.g. "$equal", "min").
func applyCallCollation(node *ResolvedBaseFunctionCallNode, rawName, funcName string, args []string) (string, []string) {
	spec := callCollation(node)
	if spec == "" {
		return funcName, args
	}
	nArgs := len(m1(node.ArgumentList()))
	switch {
	case rawName == "$case_with_value":
		out := append([]string(nil), args...)
		out[0] = collationKeySQL(out[0], spec)
		for i := 1; i+1 < len(out); i += 2 {
			out[i] = collationKeySQL(out[i], spec)
		}
		return funcName, out
	case collationKeyFuncs[rawName]:
		out := append([]string(nil), args...)
		for i := 0; i < nArgs && i < len(out); i++ {
			out[i] = collationKeySQL(out[i], spec)
		}
		return funcName, out
	case collationPackFuncs[rawName]:
		out := append([]string(nil), args...)
		limit := nArgs
		if rawName == "percentile_disc" {
			limit = 1
		}
		for i := 0; i < limit && i < len(out); i++ {
			out[i] = collationPackSQL(out[i], spec)
		}
		return funcName, out
	}
	if name, ok := collationSearchFuncs[rawName]; ok {
		out := append(append([]string(nil), args...), collationSpecLiteral(spec))
		return name, out
	}
	return funcName, args
}

func (n *FunctionCallNode) FormatSQL(ctx context.Context) (string, error) {
	sql, err := n.formatSQL(ctx)
	if err != nil || n.node == nil || !collationNeedsUnpack(n.node.ResolvedFunctionCallBase) {
		return sql, err
	}
	return collationUnpackSQL(sql), nil
}

func (n *AggregateFunctionCallNode) FormatSQL(ctx context.Context) (string, error) {
	sql, err := n.formatSQL(ctx)
	if err != nil || n.node == nil || !collationNeedsUnpack(n.node.ResolvedFunctionCallBase) {
		return sql, err
	}
	return collationUnpackSQL(sql), nil
}

func (n *AnalyticFunctionCallNode) FormatSQL(ctx context.Context) (string, error) {
	sql, err := n.formatSQL(ctx)
	if err != nil || n.node == nil || !collationNeedsUnpack(n.node.ResolvedFunctionCallBase) {
		return sql, err
	}
	return collationUnpackSQL(sql), nil
}

// columnCollation returns the non-binary top-level collation recorded
// in a resolved column's type annotations, or "".
func columnCollation(col *googlesql.ResolvedColumn) string {
	if col == nil {
		return ""
	}
	am, err := col.TypeAnnotationMap()
	if err != nil {
		return ""
	}
	return annotationMapCollation(am)
}

// annotationMapCollation returns the non-binary top-level collation in
// a type annotation map, or "".
func annotationMapCollation(am *googlesql.AnnotationMap) string {
	if am == nil {
		return ""
	}
	ca, err := googlesql.NewCollationAnnotation()
	if err != nil || ca == nil {
		return ""
	}
	id, err := ca.Id()
	if err != nil {
		return ""
	}
	v, err := am.GetAnnotation(id)
	if err != nil || v == nil {
		return ""
	}
	if ok, _ := v.HasStringValue(); !ok {
		return ""
	}
	name, err := v.StringValue()
	if err != nil || name == "binary" {
		return ""
	}
	return name
}

// formatCollatedSetOperation lowers UNION / INTERSECT / EXCEPT DISTINCT
// whose output has a collated column: rows are compared on the
// collation key instead of SQLite's byte-wise set semantics.
func formatCollatedSetOperation(ctx context.Context, node *googlesql.ResolvedSetOperationScan, queries []string) (string, bool) {
	op := m1(node.OpType())
	switch op {
	case googlesql.ResolvedSetOperationScanEnums_SetOperationTypeUnionDistinct,
		googlesql.ResolvedSetOperationScanEnums_SetOperationTypeIntersectDistinct,
		googlesql.ResolvedSetOperationScanEnums_SetOperationTypeExceptDistinct:
	default:
		return "", false
	}
	outCols := m1(node.ColumnList())
	specs := make([]string, len(outCols))
	collated := false
	for i, c := range outCols {
		specs[i] = columnCollation(c)
		if specs[i] != "" {
			collated = true
		}
	}
	items := m1(node.InputItemList())
	if !collated || len(items) != len(queries) || len(items) == 0 {
		return "", false
	}
	// Normalise every input to positional column names.
	normalized := make([]string, len(items))
	for i, item := range items {
		var cols []string
		for k, c := range m1(item.OutputColumnList()) {
			cols = append(cols, fmt.Sprintf("`%s` AS `__setop_%d`", uniqueColumnName(ctx, c), k))
		}
		normalized[i] = fmt.Sprintf("SELECT %s FROM (%s)", strings.Join(cols, ","), queries[i])
	}
	keyOf := func(alias string, k int) string {
		ref := fmt.Sprintf("%s.`__setop_%d`", alias, k)
		if specs[k] != "" {
			return collationKeySQL(ref, specs[k])
		}
		return ref
	}
	var groupKeys []string
	for k := range outCols {
		groupKeys = append(groupKeys, fmt.Sprintf("googlesqlite_group_by(%s)", keyOf("a", k)))
	}
	var body string
	switch op {
	case googlesql.ResolvedSetOperationScanEnums_SetOperationTypeUnionDistinct:
		body = fmt.Sprintf("SELECT a.* FROM (%s) a", strings.Join(normalized, " UNION ALL "))
	default:
		var conds []string
		for i := 1; i < len(normalized); i++ {
			var eq []string
			for k := range outCols {
				eq = append(eq, fmt.Sprintf("%s IS %s", keyOf("a", k), keyOf("b", k)))
			}
			exists := fmt.Sprintf("EXISTS (SELECT 1 FROM (%s) b WHERE %s)", normalized[i], strings.Join(eq, " AND "))
			if op == googlesql.ResolvedSetOperationScanEnums_SetOperationTypeExceptDistinct {
				exists = "NOT " + exists
			}
			conds = append(conds, exists)
		}
		body = fmt.Sprintf("SELECT a.* FROM (%s) a WHERE %s", normalized[0], strings.Join(conds, " AND "))
	}
	var outs []string
	for k, c := range outCols {
		outs = append(outs, fmt.Sprintf("`__setop_%d` AS `%s`", k, uniqueColumnName(ctx, c)))
	}
	return fmt.Sprintf("SELECT %s FROM (%s GROUP BY %s)", strings.Join(outs, ","), body, strings.Join(groupKeys, ",")), true
}

// jsonValueConstructors build JSON from SQL values; a top-level BOOL
// argument must reach them as BOOL (JSON true/false), not as the
// INTEGER SQLite stores booleans as.
var jsonValueConstructors = map[string]bool{
	"to_json":           true,
	"to_json_string":    true,
	"json_array":        true,
	"json_object":       true,
	"json_set":          true,
	"json_array_insert": true,
	"json_array_append": true,
	"json_strip_nulls":  true,
}

// boolContainerConstructors collect their arguments into an ARRAY
// value (or, for FORMAT, render them by type); a BOOL argument must
// arrive as BOOL rather than the INTEGER SQLite hands over, or the
// element type is lost.
var boolContainerConstructors = map[string]bool{
	"$make_array": true,
	"array_agg":   true,
	"format":      true,
}

// envelopeBoolSQL wraps sql in googlesqlite_bool_envelope when t is
// BOOL, so a column value keeps its BOOL type once it is stored inside
// an ARRAY or STRUCT. Literals are already encoded with their type.
func envelopeBoolSQL(expr googlesql.ResolvedExprNode, t googlesql.Googlesql_TypeNode, sql string) string {
	if t == nil {
		return sql
	}
	if isBool, _ := t.IsBool(); !isBool {
		return sql
	}
	if expr != nil {
		if _, ok := expr.(*googlesql.ResolvedLiteral); ok {
			return sql
		}
	}
	return fmt.Sprintf("googlesqlite_bool_envelope(%s)", sql)
}

func envelopeJSONBoolArgs(node *ResolvedBaseFunctionCallNode, rawName string, args []string) []string {
	if !jsonValueConstructors[rawName] && !boolContainerConstructors[rawName] {
		return args
	}
	argNodes := m1(node.ArgumentList())
	var out []string
	for i, a := range argNodes {
		if i >= len(args) {
			break
		}
		t, err := a.Type()
		if err != nil || t == nil {
			continue
		}
		if w := envelopeBoolSQL(a, t, args[i]); w != args[i] {
			if out == nil {
				out = append([]string(nil), args...)
			}
			out[i] = w
		}
	}
	if out == nil {
		return args
	}
	return out
}

func firstArgIsArray(node *ResolvedBaseFunctionCallNode) bool {
	argNodes := m1(node.ArgumentList())
	if len(argNodes) == 0 {
		return false
	}
	t, err := argNodes[0].Type()
	if err != nil || t == nil {
		return false
	}
	isArray, _ := t.IsArray()
	return isArray
}

// groupByColumnCollation returns the collation of a GROUP BY computed
// column, read from its own annotations, from the expression, or -- when
// the expression references a column the input ProjectScan computes --
// from that computing expression. The UNPIVOT rewriter produces such
// columns: the annotation sits on the expression, not on the column.
func groupByColumnCollation(col googlesql.ResolvedComputedColumnBaseNode, input googlesql.ResolvedScanNode) string {
	if spec := columnCollation(m1(col.Column())); spec != "" {
		return spec
	}
	expr := m1(col.Expr())
	if spec := annotationMapCollation(m1(expr.TypeAnnotationMap())); spec != "" {
		return spec
	}
	ref, ok := expr.(*googlesql.ResolvedColumnRef)
	if !ok {
		return ""
	}
	refCol := m1(ref.Column())
	if spec := columnCollation(refCol); spec != "" {
		return spec
	}
	project, ok := input.(*googlesql.ResolvedProjectScan)
	if !ok {
		return ""
	}
	id := m1(refCol.ColumnId())
	for _, c := range m1(project.ExprList()) {
		if m1(m1(c.Column()).ColumnId()) != id {
			continue
		}
		return annotationMapCollation(m1(m1(c.Expr()).TypeAnnotationMap()))
	}
	return ""
}
