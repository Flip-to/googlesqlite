package value

import (
	"errors"
	"strings"
)

// Deferred errors.
//
// GoogleSQL only raises an aggregate's error when the aggregate's value
// is actually used: `IF(1 > 0, SUM(a), SUM(a / 0))` is SUM(a), and
// `IFERROR(SUM(a / 0), -1)` is -1 (conditional_evaluation.test,
// aggregate_in_if; iferror.test,
// iferror_on_aggregate_expressions_error_in_try_with_literal_fallback).
// SQLite computes every aggregate of a group up front, so an error in
// one aggregate's argument would abort the statement even when the
// value is never needed.
//
// The formatter therefore evaluates such aggregate arguments with the
// googlesqlite_deferred_<fn> function variants, which return a
// deferred-error marker instead of raising. The Go aggregator records
// the marker and yields it as its result; every reference to the
// aggregate column is wrapped so the error is raised (or, inside
// IFERROR / ISERROR, turned into NULL) at the point of use.

// deferredErrorPrefix starts every deferred-error marker. The NUL
// byte keeps it distinct from the base64 value envelope and from any
// raw SQL string literal the formatter inlines.
const deferredErrorPrefix = "\x00googlesqlite-deferred-error\x00"

// DeferredError is the error carried by a deferred-error marker.
type DeferredError struct {
	Msg string
}

func (e *DeferredError) Error() string { return e.Msg }

// EncodeDeferredError returns the marker that carries err.
func EncodeDeferredError(err error) string {
	var de *DeferredError
	if errors.As(err, &de) {
		return deferredErrorPrefix + de.Msg
	}
	return deferredErrorPrefix + err.Error()
}

// AsDeferredError reports whether v (a raw SQLite value) is a
// deferred-error marker and returns the carried error.
func AsDeferredError(v any) (*DeferredError, bool) {
	s, ok := v.(string)
	if !ok || !strings.HasPrefix(s, deferredErrorPrefix) {
		return nil, false
	}
	return &DeferredError{Msg: s[len(deferredErrorPrefix):]}, true
}

// IsDeferredError reports whether err came from a deferred-error marker.
func IsDeferredError(err error) bool {
	var de *DeferredError
	return errors.As(err, &de)
}
