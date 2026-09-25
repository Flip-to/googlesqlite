package internal

import (
	"runtime"
	"testing"
)

// TestParseAndAnalyzeMemoryIsReleased parses and analyzes many
// statements and checks that the wasm heap stops growing. Every parse
// used to allocate its AST into the long-lived arena of shared parser
// options (and each AnalyzerOptions.GetParserOptions call leaked the
// options copy), so a long-lived process lost a few KiB of wasm memory
// per statement for good; the BigQuery emulator grew by about 145 MiB
// per 1,000 queries.
func TestParseAndAnalyzeMemoryIsReleased(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: runs tens of thousands of statements")
	}
	a, err := NewAnalyzer(NewCatalog(nil))
	if err != nil {
		t.Fatal(err)
	}
	heap := func() uint64 {
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	run := func(n int) {
		for i := 0; i < n; i++ {
			parsed, err := a.parseScript("SELECT 1 AS x, 'v' AS s, [1, 2, 3] AS arr")
			if err != nil {
				t.Fatal(err)
			}
			if i%8 == 0 {
				_, _, err := a.analyzeStatementLocked(parsed.stmts[0], 0, nil, "SELECT 1 AS x, 'v' AS s, [1, 2, 3] AS arr", false)
				if err != nil {
					t.Fatal(err)
				}
			}
			if i%256 == 0 {
				runtime.GC()
			}
		}
	}
	run(2000)
	before := heap()
	run(60000)
	after := heap()
	grown := int64(after) - int64(before)
	t.Logf("heap %d MiB -> %d MiB", before>>20, after>>20)
	// A leak of ~2.4 KiB per parse is ~140 MiB here.
	if grown > 32<<20 {
		t.Fatalf("heap grew by %d MiB over 60,000 statements", grown>>20)
	}
}
