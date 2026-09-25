package googlesqlite_test

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

// docsClassFile triages every reference-docs example that does not
// pass as documented. TestDocsExamples reads it to decide where each
// such example goes:
//
//   - skip classes (la_timezone, not_in_bigquery, nondeterministic,
//     docs_artifact): emitted to testdata/specs with the documented
//     expectation and a `skip:` reason, so the case documents the
//     upstream Example without running;
//   - bigquery: the docs contradict real BigQuery; the case is emitted
//     to testdata/specs with BigQuery's answer (bigquery_rows or
//     bigquery_error) and a comment citing both;
//   - analyzer, driver_bug: emitted to testdata/specs_pending with a
//     `pending:` reason.
//
// Examples missing from the file are "unclassified" and go to
// testdata/specs_pending.
const docsClassFile = "testdata/docs_examples/classification.yaml"

type docsClass struct {
	Class  string `yaml:"class"`
	Reason string `yaml:"reason"`
	// BigQueryRows / BigQueryError hold BigQuery's answer for class
	// bigquery, in the spec runner's row format.
	BigQueryRows  [][]any `yaml:"bigquery_rows,omitempty"`
	BigQueryError string  `yaml:"bigquery_error,omitempty"`
}

type docsDisposition int

const (
	dispPending docsDisposition = iota
	dispSkip
	dispBigQuery
)

var docsClassDisposition = map[string]docsDisposition{
	"la_timezone":      dispSkip,
	"not_in_bigquery":  dispSkip,
	"nondeterministic": dispSkip,
	"docs_artifact":    dispSkip,
	"bigquery":         dispBigQuery,
	"analyzer":         dispPending,
	"driver_bug":       dispPending,
}

func loadDocsClasses() (map[string]docsClass, error) {
	data, err := os.ReadFile(docsClassFile)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]docsClass{}, nil
		}
		return nil, err
	}
	var f struct {
		Cases map[string]docsClass `yaml:"cases"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", docsClassFile, err)
	}
	for id, c := range f.Cases {
		if _, ok := docsClassDisposition[c.Class]; !ok {
			return nil, fmt.Errorf("%s: %s: unknown class %q", docsClassFile, id, c.Class)
		}
		if strings.TrimSpace(c.Reason) == "" {
			return nil, fmt.Errorf("%s: %s: missing reason", docsClassFile, id)
		}
		if c.Class == "bigquery" && c.BigQueryRows == nil && c.BigQueryError == "" {
			return nil, fmt.Errorf("%s: %s: class bigquery needs bigquery_rows or bigquery_error", docsClassFile, id)
		}
	}
	return f.Cases, nil
}

// docsVerifiedFile lists the examples whose query was run on BigQuery
// (with the bq CLI) and whose BigQuery answer agrees with the case's
// expectation; each emitted case gets a "Verified on BigQuery" note.
const docsVerifiedFile = "testdata/docs_examples/bigquery_verified.yaml"

func loadDocsVerified() (map[string]string, error) {
	data, err := os.ReadFile(docsVerifiedFile)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var f struct {
		Runs []struct {
			Date string   `yaml:"date"`
			IDs  []string `yaml:"ids"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", docsVerifiedFile, err)
	}
	out := map[string]string{}
	for _, r := range f.Runs {
		for _, id := range r.IDs {
			out[id] = r.Date
		}
	}
	return out, nil
}

// bigQueryCase replaces the documented expectation with BigQuery's
// answer and reports whether the driver already produces it.
func bigQueryCase(c yamlCase, cl docsClass, r *docsResult) (yamlCase, bool, error) {
	bc := c
	bc.Note = strings.TrimSpace("Follows BigQuery, not the docs: " + cl.Reason + "\n" + c.Note)
	if cl.BigQueryError != "" {
		// "*" accepts any error: BigQuery's wording differs from the
		// driver's, but both reject the query.
		contains := cl.BigQueryError
		if contains == "*" {
			contains = ""
		}
		bc.Expected = yamlExpected{Error: &yamlError{Contains: contains}}
		return bc, r.GotError != "" && strings.Contains(r.GotError, contains), nil
	}
	bc.Expected = yamlExpected{Rows: cl.BigQueryRows, Unordered: c.Expected.Unordered}
	if r.GotError != "" || r.gotRaw == nil && len(cl.BigQueryRows) > 0 {
		return bc, false, nil
	}
	ok, err := specRunnerAgrees(bc, r.gotRaw)
	return bc, ok, err
}
