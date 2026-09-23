package main

import (
	"fmt"
	"sort"
	"strings"
)

type tally struct {
	Pass, Fail, Error, Skip int
}

func (t *tally) add(status string) {
	switch status {
	case statusPass:
		t.Pass++
	case statusFail:
		t.Fail++
	case statusError:
		t.Error++
	case statusSkip:
		t.Skip++
	}
}

func (t tally) run() int { return t.Pass + t.Fail + t.Error }

// skipBucket collapses per-case skip reasons into report buckets.
func skipBucket(reason string) string {
	switch {
	case strings.HasPrefix(reason, "feature not in BigQuery: "):
		r := strings.TrimPrefix(reason, "feature not in BigQuery: ")
		if i := strings.Index(r, " ("); i >= 0 {
			return "feature not in BigQuery: " + strings.TrimSuffix(r[i+2:], ")")
		}
		return reason
	case strings.HasPrefix(reason, "non-BigQuery type in result"):
		return "non-BigQuery type in result"
	case strings.HasPrefix(reason, "non-BigQuery type in SQL"):
		return "non-BigQuery type in SQL"
	case strings.HasPrefix(reason, "depends on "):
		return "depends on a setup object that was skipped or failed"
	case strings.HasPrefix(reason, "case expects feature"):
		return "case expects a BigQuery feature disabled (forbidden_features)"
	case strings.HasPrefix(reason, "non-UTC default_time_zone"):
		return "non-UTC default_time_zone"
	case strings.HasPrefix(reason, "runner: cannot parse expected value"):
		return "runner: cannot parse expected value"
	}
	return reason
}

func renderComplianceSummary(results, failing []caseResult) string {
	var b strings.Builder
	var total tally
	var setup tally
	perFile := map[string]*tally{}
	perFeature := map[string]*tally{}
	skips := map[string]int{}
	var classNotes int
	for _, r := range results {
		if r.Prepare {
			setup.add(r.Status)
			if r.Status == statusSkip {
				skips["(setup) "+skipBucket(r.SkipReason)]++
			}
			continue
		}
		total.add(r.Status)
		if perFile[r.File] == nil {
			perFile[r.File] = &tally{}
		}
		perFile[r.File].add(r.Status)
		feats := r.Features
		if len(feats) == 0 {
			feats = []string{"(no required feature)"}
		}
		for _, f := range feats {
			if perFeature[f] == nil {
				perFeature[f] = &tally{}
			}
			perFeature[f].add(r.Status)
		}
		if r.Status == statusSkip {
			skips[skipBucket(r.SkipReason)]++
		}
		if r.ErrorClassNote != "" {
			classNotes++
		}
	}
	kinds := map[string]int{}
	for _, r := range failing {
		if !r.Prepare {
			kinds[r.Kind]++
		}
	}

	fmt.Fprintf(&b, "## Totals\n\n")
	fmt.Fprintf(&b, "| | cases |\n|---|---:|\n")
	fmt.Fprintf(&b, "| query cases in suite | %d |\n", total.run()+total.Skip)
	fmt.Fprintf(&b, "| run | %d |\n| passed | %d |\n| failed (wrong result, no error) | %d |\n| errored (driver error where rows expected) | %d |\n| skipped | %d |\n", total.run(), total.Pass, total.Fail, total.Error, total.Skip)
	if total.run() > 0 {
		fmt.Fprintf(&b, "| pass rate of run cases | %.1f%% |\n", 100*float64(total.Pass)/float64(total.run()))
	}
	fmt.Fprintf(&b, "\nSetup statements (`[prepare_database]`): %d ok, %d failed, %d skipped.\n", setup.Pass, setup.Error, setup.Skip)
	fmt.Fprintf(&b, "Expected-error cases that passed but where the error phase differs (analysis vs runtime): %d.\n\n", classNotes)

	fmt.Fprintf(&b, "### Failure kinds\n\n| kind | cases | silent |\n|---|---:|---|\n")
	for _, k := range sortedKeysByScore(kinds) {
		silent := "no"
		if kindScore[k] >= kindScore[kindShape] {
			silent = "yes"
		}
		fmt.Fprintf(&b, "| %s | %d | %s |\n", k, kinds[k], silent)
	}

	fmt.Fprintf(&b, "\n### Skipped, by reason\n\n| reason | cases |\n|---|---:|\n")
	for _, k := range sortedByCount(skips) {
		fmt.Fprintf(&b, "| %s | %d |\n", mdEscape(k), skips[k])
	}

	fmt.Fprintf(&b, "\n### dbt construct areas\n\n| construct | run | pass | fail | error |\n|---|---:|---:|---:|---:|\n")
	for _, c := range dbtAreaTallies(results) {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", c.name, c.t.run(), c.t.Pass, c.t.Fail, c.t.Error)
	}

	fmt.Fprintf(&b, "\n### Per feature area (required_features)\n\n| feature | run | pass | fail | error | skip |\n|---|---:|---:|---:|---:|---:|\n")
	for _, f := range sortedTallyKeys(perFeature) {
		t := perFeature[f]
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d |\n", f, t.run(), t.Pass, t.Fail, t.Error, t.Skip)
	}

	fmt.Fprintf(&b, "\n### Per file\n\n| file | run | pass | fail | error | skip |\n|---|---:|---:|---:|---:|---:|\n")
	files := make([]string, 0, len(perFile))
	for f := range perFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		t := perFile[f]
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d |\n", f, t.run(), t.Pass, t.Fail, t.Error, t.Skip)
	}
	return b.String()
}

type areaTally struct {
	name string
	t    tally
}

func dbtAreaTallies(results []caseResult) []areaTally {
	idx := map[string]*tally{}
	for _, r := range results {
		if r.Prepare || r.Status == statusSkip {
			continue
		}
		for _, c := range r.Constructs {
			if idx[c] == nil {
				idx[c] = &tally{}
			}
			idx[c].add(r.Status)
		}
	}
	var out []areaTally
	for k, v := range idx {
		out = append(out, areaTally{k, *v})
	}
	sort.Slice(out, func(a, b int) bool {
		fa, fb := out[a].t.Fail+out[a].t.Error, out[b].t.Fail+out[b].t.Error
		if fa != fb {
			return fa > fb
		}
		return out[a].name < out[b].name
	})
	return out
}

func sortedKeysByScore(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool { return kindScore[keys[a]] > kindScore[keys[b]] })
	return keys
}

func sortedByCount(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		if m[keys[a]] != m[keys[b]] {
			return m[keys[a]] > m[keys[b]]
		}
		return keys[a] < keys[b]
	})
	return keys
}

func sortedTallyKeys(m map[string]*tally) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		fa := m[keys[a]].Fail + m[keys[a]].Error
		fb := m[keys[b]].Fail + m[keys[b]].Error
		if fa != fb {
			return fa > fb
		}
		return keys[a] < keys[b]
	})
	return keys
}

func mdEscape(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
