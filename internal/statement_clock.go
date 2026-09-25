package internal

import (
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"

	"github.com/goccy/googlesqlite/internal/functions/date"
	"github.com/goccy/googlesqlite/internal/functions/datetime"
	timefn "github.com/goccy/googlesqlite/internal/functions/time"
	"github.com/goccy/googlesqlite/internal/functions/timestamp"
)

// StatementClock holds the instant CURRENT_TIMESTAMP, CURRENT_DATETIME,
// CURRENT_DATE and CURRENT_TIME return while a statement runs on one
// SQLite connection. BigQuery evaluates them from one instant per
// statement ("the current time is the same for all calls within a
// statement"); reading the wall clock per call site made
// CURRENT_TIMESTAMP() = CURRENT_TIMESTAMP() false whenever the clock
// ticked between the two calls, which on nanosecond clocks (Linux) is
// almost always.
//
// The instant is read at run time rather than written into the SQL, so
// a view, a SQL function body or a column DEFAULT that calls
// CURRENT_TIMESTAMP still sees the time of the statement that uses it.
// While no statement holds the clock (for example, a prepared statement
// executed on its own), the functions read the wall clock as before.
type StatementClock struct {
	// nanos is the frozen instant in Unix nanoseconds; 0 means unset.
	nanos atomic.Int64
	// token identifies the statement that set nanos, so only that
	// statement's end clears it.
	token atomic.Uint64
	next  atomic.Uint64
}

// Start freezes the clock at now and returns a token for End.
func (c *StatementClock) Start(now time.Time) uint64 {
	if c == nil {
		return 0
	}
	token := c.next.Add(1)
	c.nanos.Store(now.UnixNano())
	c.token.Store(token)
	return token
}

// End unfreezes the clock if the statement identified by token still
// holds it.
func (c *StatementClock) End(token uint64) {
	if c == nil || token == 0 {
		return
	}
	if c.token.CompareAndSwap(token, 0) {
		c.nanos.Store(0)
	}
}

func (c *StatementClock) frozen() (int64, bool) {
	n := c.nanos.Load()
	return n, n != 0
}

var (
	statementClocksMu sync.Mutex
	statementClocks   = map[*sqlite3.Conn]*StatementClock{}
)

// TakeStatementClock returns the clock RegisterFunctions installed on
// conn and forgets it, so the caller owns it for the connection's life.
func TakeStatementClock(conn *sqlite3.Conn) *StatementClock {
	statementClocksMu.Lock()
	defer statementClocksMu.Unlock()
	c := statementClocks[conn]
	delete(statementClocks, conn)
	return c
}

func newStatementClock(conn *sqlite3.Conn) *StatementClock {
	c := &StatementClock{}
	statementClocksMu.Lock()
	statementClocks[conn] = c
	statementClocksMu.Unlock()
	return c
}

// clockBinders are the binders that read the current time. Each takes
// optional (unixNano) first, then an optional time zone.
var clockBinders = map[uintptr]struct{}{
	reflect.ValueOf(timestamp.BindCurrentTimestamp).Pointer(): {},
	reflect.ValueOf(datetime.BindCurrentDatetime).Pointer():   {},
	reflect.ValueOf(date.BindCurrentDate).Pointer():           {},
	reflect.ValueOf(timefn.BindCurrentTime).Pointer():         {},
}

// clockFuncNames are the registered SQLite names backed by clockBinders.
var clockFuncNames = sync.OnceValue(func() map[string]struct{} {
	names := map[string]struct{}{}
	for _, info := range normalFuncs {
		if _, ok := clockBinders[reflect.ValueOf(info.BindFunc).Pointer()]; !ok {
			continue
		}
		for _, v := range normalFuncMap[info.Name] {
			names[v.Name] = struct{}{}
		}
	}
	return names
})

// withStatementClock passes the frozen instant to a clock function
// unless the call already carries one (a time injected through
// WithCurrentTime is formatted as the leading INT64 argument).
func withStatementClock(clock *StatementClock, fn func(args ...any) (any, error)) func(args ...any) (any, error) {
	return func(args ...any) (any, error) {
		if len(args) > 0 {
			if _, ok := args[0].(int64); ok {
				return fn(args...)
			}
		}
		nanos, ok := clock.frozen()
		if !ok {
			return fn(args...)
		}
		withNow := make([]any, 0, len(args)+1)
		withNow = append(withNow, nanos)
		withNow = append(withNow, args...)
		return fn(withNow...)
	}
}
