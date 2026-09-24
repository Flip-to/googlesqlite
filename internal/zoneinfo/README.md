# Embedded zoneinfo

`zoneinfo.zip` is an unmodified copy of `$GOROOT/lib/time/zoneinfo.zip`
from the Go distribution (Go 1.26.2, IANA tz code/data release 2025c).
It contains time zone files compiled from the IANA Time Zone Database,
which the IANA states is in the public domain
(https://www.iana.org/time-zones).

It is extracted at runtime only when the GoogleSQL wasm analyzer would
otherwise find no zone files (typically on Windows); see `zoneinfo.go`.
Set `GOOGLESQLITE_WASM_EMBEDDED_ZONEINFO=0` to disable this.

To refresh it, copy the archive from a newer Go toolchain:

    cp "$(go env GOROOT)/lib/time/zoneinfo.zip" internal/zoneinfo/

and update the version noted above. The extraction directory is keyed
by the archive's content hash, so upgraded binaries re-extract.
