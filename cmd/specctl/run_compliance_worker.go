package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/goccy/googlesqlite/cmd/specctl/compliancetest"
)

var workerDBSeq int64

// workerLine is one JSON line a worker streams to its parent.
type workerLine struct {
	Start  int         `json:"start,omitempty"`
	Result *caseResult `json:"result,omitempty"`
}

// runWorker runs one file in-process, streaming a start marker before
// each case and the result after it. Setup statements before start are
// replayed silently so later cases see their tables.
func runWorker(ctx context.Context, path string, start int, timeout time.Duration) error {
	enc := json.NewEncoder(os.Stdout)
	base := filepath.Base(path)
	cases, err := compliancetest.LoadSuiteFile(path)
	if err != nil {
		return enc.Encode(workerLine{Result: &caseResult{File: base, Status: statusSkip, SkipReason: "runner: cannot parse file: " + err.Error()}})
	}
	fr := &fileRunner{path: path, timeout: timeout, dbSeq: &workerDBSeq, setup: setupState{failed: map[string]string{}}}
	if err := fr.open(ctx); err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer fr.close()
	for _, c := range cases {
		if c.Index < start {
			if c.Prepare {
				fr.runCase(ctx, c)
			}
			continue
		}
		if verboseLog {
			fmt.Fprintf(os.Stderr, "start %s #%d %s\n", base, c.Index, c.Name)
		}
		if err := enc.Encode(workerLine{Start: c.Index}); err != nil {
			return err
		}
		r := fr.runCase(ctx, c)
		if err := enc.Encode(workerLine{Result: &r}); err != nil {
			return err
		}
	}
	return nil
}

// runFileIsolated runs a file in child processes. A timed-out query can
// hold a driver-wide lock and wedge every later statement in the same
// process, so the watchdog kills a silent child, records a timeout for
// the current case and resumes with the next case in a fresh process.
func runFileIsolated(ctx context.Context, path string, timeout time.Duration, workerArgs []string) []caseResult {
	base := filepath.Base(path)
	cases, _ := compliancetest.LoadSuiteFile(path)
	byIndex := map[int]compliancetest.SuiteCase{}
	for _, c := range cases {
		byIndex[c.Index] = c
	}
	limit := timeout + 30*time.Second
	var out []caseResult
	start := 1
	for attempt := 0; attempt < 500; attempt++ {
		args := append([]string{"run-compliance", "-worker", path, "-start", fmt.Sprint(start)}, workerArgs...)
		cmd := exec.CommandContext(ctx, os.Args[0], args...)
		var stderr strings.Builder
		cmd.Stderr = &limitedWriter{w: &stderr, n: 4096}
		stdout, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			return append(out, caseResult{File: base, Status: statusError, Kind: kindDriverError, Reason: "worker: " + err.Error()})
		}
		lines := make(chan workerLine)
		go func() {
			defer close(lines)
			sc := bufio.NewScanner(stdout)
			sc.Buffer(make([]byte, 1<<20), 1<<26)
			for sc.Scan() {
				var l workerLine
				if json.Unmarshal(sc.Bytes(), &l) == nil {
					lines <- l
				}
			}
		}()
		current, killed := 0, false
		watchdog := time.NewTimer(limit)
	read:
		for {
			select {
			case l, ok := <-lines:
				if !ok {
					break read
				}
				watchdog.Reset(limit)
				if l.Result != nil {
					out = append(out, *l.Result)
					current = 0
				} else {
					current = l.Start
				}
			case <-watchdog.C:
				killed = true
				_ = cmd.Process.Kill()
				for range lines {
				}
				break read
			}
		}
		watchdog.Stop()
		werr := cmd.Wait()
		if current == 0 && !killed {
			if werr != nil {
				out = append(out, caseResult{File: base, Status: statusError, Kind: kindDriverError, Reason: "worker failed: " + oneLine(stderr.String())})
			}
			return out
		}
		if current == 0 {
			// Killed while opening or replaying setup.
			return append(out, caseResult{File: base, Status: statusError, Kind: kindTimeout, Reason: "worker wedged before any case"})
		}
		c := byIndex[current]
		r := caseResult{File: base, Index: current, Name: c.Name, Features: c.AllFeatures, Prepare: c.Prepare,
			SQL: compliancetest.SubstituteParams(c.SQL, c.Params), Expected: compliancetest.FirstSection(c.Expected)}
		r.Constructs = compliancetest.MatchConstructs(r.SQL, firstLineOf(r.Expected))
		r.Status = statusError
		if killed {
			r.Kind, r.Emulator = kindTimeout, fmt.Sprintf("no result after %s; worker process killed", limit)
		} else {
			r.Kind, r.Emulator = kindPanic, "worker process exited: "+oneLine(stderr.String())
		}
		r.Score = kindScore[r.Kind]
		out = append(out, r)
		start = current + 1
	}
	return out
}

// limitedWriter keeps at most n bytes of a child's stderr.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		_, _ = l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}
