package compliancetest

import (
	"regexp"
	"strings"
)

// Construct is a SQL construct a downstream dbt project depends on.
// Failing cases that use one are reported first.
type Construct struct {
	Name  string
	Match func(sql, header string) bool
}

func re(p string) func(sql, header string) bool {
	r := regexp.MustCompile(p)
	return func(sql, _ string) bool { return r.MatchString(sql) }
}

// DBTConstructs is the construct list used to prioritise divergences.
var DBTConstructs = []Construct{
	{"SAFE_DIVIDE", re(`(?i)\bSAFE_DIVIDE\s*\(`)},
	{"LEFT JOIN UNNEST", re(`(?i)\bLEFT\s+(OUTER\s+)?JOIN\s+UNNEST\b`)},
	{"QUALIFY", re(`(?i)\bQUALIFY\b`)},
	{"FORMAT %T", re(`(?i)\bFORMAT\s*\(\s*(r?["'])[^"']*%[-+ #0-9.]*T`)},
	{"HLL_COUNT.*", re(`(?i)\bHLL_COUNT\.`)},
	{"ARRAY_AGG ORDER BY / LIMIT", arrayAggOrderLimit},
	{"GENERATE_DATE_ARRAY", re(`(?i)\bGENERATE_DATE_ARRAY\s*\(`)},
	{"IS DISTINCT FROM", re(`(?i)\bIS\s+(NOT\s+)?DISTINCT\s+FROM\b`)},
	{"window frames", re(`(?i)\bOVER\s*(\(|\w)[\s\S]*\b(ROWS|RANGE)\s+(BETWEEN|UNBOUNDED|CURRENT|\d)`)},
	{"EXCEPT DISTINCT", re(`(?i)\bEXCEPT\s+DISTINCT\b`)},
	{"TO_JSON_STRING", re(`(?i)\bTO_JSON_STRING\s*\(`)},
	{"FARM_FINGERPRINT", re(`(?i)\bFARM_FINGERPRINT\s*\(`)},
	{"APPROX_QUANTILES", re(`(?i)\bAPPROX_QUANTILES\s*\(`)},
	{"NUMERIC arithmetic", func(sql, header string) bool {
		return regexp.MustCompile(`(?i)\b(BIG)?NUMERIC\b`).MatchString(sql + " " + header)
	}},
	{"DATE_TRUNC", re(`(?i)\bDATE_TRUNC\s*\(`)},
	{"TIMESTAMP_SUB", re(`(?i)\bTIMESTAMP_SUB\s*\(`)},
	{"COALESCE", re(`(?i)\bCOALESCE\s*\(`)},
	{"CASE", re(`(?i)\bCASE\b`)},
	{"LIKE", re(`(?i)\bLIKE\b`)},
	{"SPLIT", re(`(?i)\bSPLIT\s*\(`)},
	{"REGEXP_*", re(`(?i)\bREGEXP_\w+\s*\(`)},
}

var arrayAggRe = regexp.MustCompile(`(?i)\bARRAY_AGG\s*\(`)

func arrayAggOrderLimit(sql, _ string) bool {
	for _, loc := range arrayAggRe.FindAllStringIndex(sql, -1) {
		depth := 1
		i := loc[1]
		for ; i < len(sql) && depth > 0; i++ {
			switch sql[i] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		body := strings.ToUpper(sql[loc[1]:i])
		if strings.Contains(body, "ORDER BY") || strings.Contains(body, "LIMIT") {
			return true
		}
	}
	return false
}

// MatchConstructs returns the names of every construct the SQL uses.
func MatchConstructs(sql, header string) []string {
	var out []string
	for _, c := range DBTConstructs {
		if c.Match(sql, header) {
			out = append(out, c.Name)
		}
	}
	return out
}
