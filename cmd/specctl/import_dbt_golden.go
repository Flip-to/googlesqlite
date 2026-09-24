package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func init() {
	register(command{
		name:    "import-dbt-golden",
		summary: "convert a flipto-dbt emulator_diff run (verdicts.json) into testdata/dbt_golden/probes.jsonl",
		run: func(_ context.Context, args []string) error {
			return runImportDBTGolden(args)
		},
	})
}

// dbtGoldenField mirrors a BigQuery schema field as the harness recorded
// it. Mode is kept only when it is REPEATED: BigQuery reports every
// query column NULLABLE, so that bit carries no signal.
type dbtGoldenField struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Mode   string            `json:"mode,omitempty"`
	Fields []*dbtGoldenField `json:"fields,omitempty"`
}

// dbtGoldenAnswer is the recorded BigQuery answer: schema and rows in
// the harness's normalized value form, or an error message.
type dbtGoldenAnswer struct {
	Error  string            `json:"error,omitempty"`
	Schema []*dbtGoldenField `json:"schema,omitempty"`
	Rows   json.RawMessage   `json:"rows,omitempty"`
}

type dbtGoldenProbe struct {
	ID       string          `json:"id"`
	SQL      string          `json:"sql"`
	BigQuery dbtGoldenAnswer `json:"bigquery"`
}

// dbtGoldenForbidden matches text that would tie a probe to a real
// project: project, dataset and table names, qualified table paths and
// quoted identifiers. Probes that match are not shipped.
var dbtGoldenForbidden = regexp.MustCompile(
	"(?i)flipto|rt_pipeline|snowplow|spacetime|lookup_tables|ondemand|`|@[a-z0-9-]+\\.|\\b(FROM|JOIN)\\s+[a-z_][a-z0-9_-]+\\.[a-z_]")

var (
	// "POST (or GET) https://bigquery.googleapis.com/...: <message>" from the client.
	dbtGoldenPostPrefix = regexp.MustCompile(`^(POST|GET) https?://\S+: `)
	// "; reason: invalidQuery, location: query, message: ... Job ID: ..."
	dbtGoldenReasonTail = regexp.MustCompile(`; reason: .*$`)
	dbtGoldenJobTail    = regexp.MustCompile(`(?s)\s*(Location: |Job ID: |\n).*$`)
)

func cleanDBTGoldenError(msg string) string {
	msg = dbtGoldenPostPrefix.ReplaceAllString(msg, "")
	msg = dbtGoldenReasonTail.ReplaceAllString(msg, "")
	msg = dbtGoldenJobTail.ReplaceAllString(msg, "")
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "error"
	}
	return msg
}

func convertDBTGoldenFields(in []*dbtGoldenField) []*dbtGoldenField {
	out := make([]*dbtGoldenField, 0, len(in))
	for _, f := range in {
		g := &dbtGoldenField{Name: f.Name, Type: f.Type, Fields: convertDBTGoldenFields(f.Fields)}
		if strings.EqualFold(f.Mode, "REPEATED") {
			g.Mode = "REPEATED"
		}
		if len(g.Fields) == 0 {
			g.Fields = nil
		}
		out = append(out, g)
	}
	return out
}

// runImportDBTGolden reads <src>/verdicts.json written by the flipto-dbt
// harness (scripts/emulator_diff/run.py report) and writes one JSON
// object per line: probe id, SQL and the BigQuery answer. Emulator
// results, verdicts, construct inventory counts and job metadata are
// dropped; only the BigQuery answer is authoritative.
func runImportDBTGolden(args []string) error {
	fs := flag.NewFlagSet("import-dbt-golden", flag.ContinueOnError)
	src := fs.String("src", "", "emulator_diff output directory containing verdicts.json")
	out := fs.String("out", "", "output file (default testdata/dbt_golden/probes.jsonl)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *src == "" {
		return errors.New("--src is required")
	}
	if *out == "" {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		*out = filepath.Join(root, "testdata", "dbt_golden", "probes.jsonl")
	}
	raw, err := os.ReadFile(filepath.Join(*src, "verdicts.json"))
	if err != nil {
		return err
	}
	var in []struct {
		ID       string `json:"id"`
		SQL      string `json:"sql"`
		BigQuery struct {
			Error  *string           `json:"error"`
			Schema []*dbtGoldenField `json:"schema"`
			Rows   json.RawMessage   `json:"rows"`
		} `json:"bigquery"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	sort.Slice(in, func(i, j int) bool { return in[i].ID < in[j].ID })

	var buf strings.Builder
	var excluded []string
	seen := map[string]bool{}
	for _, p := range in {
		if seen[p.ID] {
			return fmt.Errorf("duplicate probe id %q", p.ID)
		}
		seen[p.ID] = true
		probe := dbtGoldenProbe{ID: p.ID, SQL: p.SQL}
		switch {
		case p.BigQuery.Error != nil:
			probe.BigQuery.Error = cleanDBTGoldenError(*p.BigQuery.Error)
		case p.BigQuery.Rows != nil:
			probe.BigQuery.Schema = convertDBTGoldenFields(p.BigQuery.Schema)
			probe.BigQuery.Rows = p.BigQuery.Rows
		default:
			excluded = append(excluded, p.ID+" (no BigQuery answer)")
			continue
		}
		// Marshal compacts the rows, dropping the harness's pretty-printing.
		var line bytes.Buffer
		enc := json.NewEncoder(&line)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(probe); err != nil {
			return err
		}
		if dbtGoldenForbidden.Match(line.Bytes()) {
			excluded = append(excluded, p.ID+" (project-specific text)")
			continue
		}
		buf.Write(line.Bytes())
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*out, []byte(buf.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %d probes to %s\n", len(in)-len(excluded), *out)
	for _, e := range excluded {
		fmt.Printf("excluded: %s\n", e)
	}
	return nil
}
