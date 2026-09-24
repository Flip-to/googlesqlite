package internal

import (
	"context"
	"fmt"
	"strings"

	"github.com/goccy/go-googlesql"
)

// Deferred aggregate errors (see value.DeferredError for the model).
//
// An aggregate whose argument contains a function call is evaluated
// with the googlesqlite_deferred_<fn> variants for that chain of calls,
// so an error only becomes a marker the aggregate yields. Every
// reference to such an aggregate column is wrapped in
// googlesqlite_raise_deferred (or googlesqlite_deferred_to_null inside
// IFERROR / ISERROR / NULLIFERROR), so the error is raised exactly when
// the value is used and never escapes into storage or native SQL.

type deferredErrorModeKey struct{}

type deferredAggColumnsKey struct{}

func withDeferredErrorMode(ctx context.Context, on bool) context.Context {
	if !on && !inDeferredErrorMode(ctx) {
		return ctx
	}
	return context.WithValue(ctx, deferredErrorModeKey{}, on)
}

func inDeferredErrorMode(ctx context.Context) bool {
	b, _ := ctx.Value(deferredErrorModeKey{}).(bool)
	return b
}

// withDeferredAggColumns records the deferrable aggregate columns
// produced below scan so references to them get wrapped.
func withDeferredAggColumns(ctx context.Context, scan googlesql.ResolvedScanNode) context.Context {
	var cols map[string]struct{}
	collectDeferredAggColumns(ctx, scan, &cols)
	if len(cols) == 0 {
		return ctx
	}
	if prev, ok := ctx.Value(deferredAggColumnsKey{}).(map[string]struct{}); ok {
		for k := range prev {
			cols[k] = struct{}{}
		}
	}
	return context.WithValue(ctx, deferredAggColumnsKey{}, cols)
}

func collectDeferredAggColumns(ctx context.Context, scan googlesql.ResolvedScanNode, cols *map[string]struct{}) {
	for depth := 0; scan != nil && depth < 16; depth++ {
		switch n := newNode(scan).(type) {
		case *ProjectScanNode:
			scan = m1(n.node.InputScan())
		case *FilterScanNode:
			scan = m1(n.node.InputScan())
		case *OrderByScanNode:
			scan = m1(n.node.InputScan())
		case *LimitOffsetScanNode:
			scan = m1(n.node.InputScan())
		case *AnalyticScanNode:
			scan = m1(n.node.InputScan())
		case *AggregateScanNode:
			for _, agg := range m1(n.node.AggregateList()) {
				call, ok := newNode(m1(agg.Expr())).(*AggregateFunctionCallNode)
				if !ok || !aggregateCallDeferrable(ctx, call) {
					continue
				}
				if *cols == nil {
					*cols = map[string]struct{}{}
				}
				(*cols)[uniqueColumnName(ctx, m1(agg.Column()))] = struct{}{}
			}
			return
		default:
			return
		}
	}
}

// aggregateCallDeferrable reports whether a Go-implemented aggregate
// has an argument that is a function call (the only place an argument
// error can come from).
func aggregateCallDeferrable(ctx context.Context, call *AggregateFunctionCallNode) bool {
	if call == nil || call.node == nil || inSafeEvalMode(ctx) {
		return false
	}
	base := call.node.ResolvedFunctionCallBase
	if m1(base.ErrorMode()) == googlesql.ResolvedFunctionCallBaseEnums_ErrorModeSafeErrorMode {
		return false
	}
	if m1(call.node.HavingModifier()) != nil {
		return false
	}
	name := strings.ReplaceAll(m1(m1(base.Function()).FullName(false)), ".", "_")
	if _, ok := aggregateFuncMap[name]; !ok {
		return false
	}
	if _, udf := funcMapFromContext(ctx)[name]; udf {
		return false
	}
	for _, a := range m1(base.ArgumentList()) {
		if fc, ok := newNode(a).(*FunctionCallNode); ok && functionCallDeferrable(fc.node.ResolvedFunctionCallBase) {
			return true
		}
	}
	return false
}

// deferredSpecialForms are the calls the formatter lowers to native SQL
// (or handles specially); they never take the deferred variant.
var deferredSpecialForms = map[string]bool{
	"from_proto": true, "to_proto": true, "filter_fields": true, "replace_fields": true,
	"proto_modify_map": true, "proto_map_contains_key": true, "enum_value_descriptor_proto": true,
	"iferror": true, "iserror": true, "nulliferror": true, "error": true,
	"ifnull": true, "if": true, "case_no_value": true, "case_with_value": true,
}

// functionCallDeferrable reports whether a scalar call has a
// googlesqlite_deferred_ variant.
func functionCallDeferrable(node *googlesql.ResolvedFunctionCallBase) bool {
	if node == nil {
		return false
	}
	name := strings.ReplaceAll(m1(m1(node.Function()).FullName(false)), ".", "_")
	name = strings.TrimPrefix(name, "$")
	if deferredSpecialForms[name] {
		return false
	}
	if m1(node.ErrorMode()) == googlesql.ResolvedFunctionCallBaseEnums_ErrorModeSafeErrorMode {
		return false
	}
	_, ok := normalFuncMap[name]
	return ok
}

// wrapDeferredAggColumn wraps a reference to a deferrable aggregate
// column.
func wrapDeferredAggColumn(ctx context.Context, colName, sql string) string {
	cols, _ := ctx.Value(deferredAggColumnsKey{}).(map[string]struct{})
	if _, ok := cols[colName]; !ok {
		return sql
	}
	if inSafeEvalMode(ctx) {
		// The enclosing IFERROR / ISERROR / NULLIFERROR detects it.
		return sql
	}
	return fmt.Sprintf("googlesqlite_raise_deferred(%s)", sql)
}

// projectColumnSQL renders a pass-through ProjectScan column.
func projectColumnSQL(ctx context.Context, colName string) string {
	ref := fmt.Sprintf("`%s`", colName)
	if wrapped := wrapDeferredAggColumn(ctx, colName, ref); wrapped != ref {
		return fmt.Sprintf("%s AS `%s`", wrapped, colName)
	}
	return ref
}

// castFuncName names the runtime function for a CAST lowering. Inside
// IFERROR / ISERROR / NULLIFERROR (or a deferred aggregate argument) the
// deferred variant turns a failed conversion into a deferred-error
// marker (iferror.test, try_expr_has_constant_folding_error).
func castFuncName(ctx context.Context, name string) string {
	if inSafeEvalMode(ctx) || inDeferredErrorMode(ctx) {
		return "googlesqlite_deferred_" + name
	}
	return "googlesqlite_" + name
}

func isDeferredSpecialForm(node *googlesql.ResolvedFunctionCallBase) bool {
	if node == nil {
		return false
	}
	name := strings.ReplaceAll(m1(m1(node.Function()).FullName(false)), ".", "_")
	return deferredSpecialForms[strings.TrimPrefix(name, "$")]
}
