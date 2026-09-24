package internal

import "context"

// safeFilterProbesKey carries the collector a SubqueryExprNode in
// safe-evaluation mode uses to learn about the filters inside it: each
// entry is a query that returns a row when a filter condition
// evaluated to a deferred-error marker.
type safeFilterProbesKey struct{}

func withSafeFilterProbes(ctx context.Context, dst *[]string) context.Context {
	return context.WithValue(ctx, safeFilterProbesKey{}, dst)
}

func safeFilterProbes(ctx context.Context) *[]string {
	dst, _ := ctx.Value(safeFilterProbesKey{}).(*[]string)
	return dst
}
