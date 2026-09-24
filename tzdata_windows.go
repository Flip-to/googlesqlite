package googlesqlite

// Go's own time.LoadLocation has no system zone database on Windows
// unless GOROOT is present, so embed Go's copy there.
import _ "time/tzdata"
