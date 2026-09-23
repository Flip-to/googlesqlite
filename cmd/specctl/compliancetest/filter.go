package compliancetest

import (
	"regexp"
	"sort"
	"strings"
)

// BigQueryFeatures lists the GoogleSQL LanguageFeature names (without
// the FEATURE_ prefix) that BigQuery supports. A case runs only when
// every required feature appears here. The list is explicit on
// purpose: a feature that is in neither this map nor
// NonBigQueryFeatures is reported as "unclassified" rather than
// silently dropped.
var BigQueryFeatures = map[string]bool{
	"ADDITIONAL_STRING_FUNCTIONS":            true,
	"AGGREGATION_THRESHOLD":                  true,
	"ALIASES_FOR_STRING_AND_DATE_FUNCTIONS":  true,
	"ANALYSIS_CONSTANT_INTERVAL_CONSTRUCTOR": true,
	"ANALYSIS_CONSTANT_PIVOT_COLUMN":         true,
	"ANALYTIC_FUNCTIONS":                     true,
	"ANNOTATION_FRAMEWORK":                   true,
	"BIGNUMERIC_TYPE":                        true,
	"BY_NAME":                                true,
	"CIVIL_TIME":                             true,
	"COLLATION_IN_EXPLICIT_CAST":             true,
	"COLLATION_SUPPORT":                      true,
	"CORRESPONDING":                          true,
	"CORRESPONDING_FULL":                     true,
	"CREATE_TABLE_FUNCTION":                  true,
	"DATE_TIME_CONSTRUCTORS":                 true,
	"DML_UPDATE_WITH_JOIN":                   true,
	"ENCRYPTION":                             true,
	"ENFORCE_CONDITIONAL_EVALUATION":         true,
	"ENFORCE_MICROS_MODE_IN_INTERVAL_TYPE":   true,
	"EXTENDED_DATE_TIME_SIGNATURES":          true,
	"FORMAT_IN_CAST":                         true,
	"GEOGRAPHY":                              true,
	"GROUPING_BUILTIN":                       true,
	"GROUPING_SETS":                          true,
	"GROUP_BY_ALL":                           true,
	"GROUP_BY_ROLLUP":                        true,
	"GROUP_BY_STRUCT":                        true,
	"HAVING_IN_AGGREGATE":                    true,
	"INTERVAL_TYPE":                          true,
	"IS_DISTINCT":                            true,
	"JSON_ARRAY_FUNCTIONS":                   true,
	"JSON_CONSTRUCTOR_FUNCTIONS":             true,
	"JSON_KEYS_FUNCTION":                     true,
	"JSON_LAX_VALUE_EXTRACTION_FUNCTIONS":    true,
	"JSON_MORE_VALUE_EXTRACTION_FUNCTIONS":   true,
	"JSON_MUTATOR_FUNCTIONS":                 true,
	"JSON_TYPE":                              true,
	"JSON_VALUE_EXTRACTION_FUNCTIONS":        true,
	"LIKE_ANY_SOME_ALL":                      true,
	"LIKE_ANY_SOME_ALL_ARRAY":                true,
	"LIMIT_IN_AGGREGATE":                     true,
	"LITERAL_CONCATENATION":                  true,
	"MATCH_RECOGNIZE":                        true,
	"NAMED_ARGUMENTS":                        true,
	"NULLS_FIRST_LAST_IN_ORDER_BY":           true,
	"NULL_HANDLING_MODIFIER_IN_AGGREGATE":    true,
	"NULL_HANDLING_MODIFIER_IN_ANALYTIC":     true,
	"NUMERIC_TYPE":                           true,
	"ORDER_BY_COLLATE":                       true,
	"ORDER_BY_IN_AGGREGATE":                  true,
	"PARAMETERIZED_TYPES":                    true,
	"PIPES":                                  true,
	"PIPE_CALL_INPUT_TABLE":                  true,
	"PIPE_WITH":                              true,
	"PIVOT":                                  true,
	"QUALIFY":                                true,
	"RANGE_TYPE":                             true,
	"ROUND_WITH_ROUNDING_MODE":               true,
	"SAFE_FUNCTION_CALL":                     true,
	"SELECT_STAR_EXCEPT_REPLACE":             true,
	"TABLE_VALUED_FUNCTIONS":                 true,
	"TEMPLATE_FUNCTIONS":                     true,
	"UNPIVOT":                                true,
	"WEEK_WITH_WEEKDAY":                      true,
	"WITH_ON_SUBQUERY":                       true,
	"WITH_RECURSIVE":                         true,
}

// NonBigQueryFeatures maps features BigQuery does not expose (or that
// only exist in other GoogleSQL dialects) to the reason the case is
// skipped. Cases requiring any of these are skipped with that reason.
var NonBigQueryFeatures = map[string]string{}

func init() {
	groups := map[string][]string{
		"SQL graph (GQL) is not part of the BigQuery dialect under test": {
			"SQL_GRAPH", "SQL_GRAPH_ADVANCED_QUERY", "SQL_GRAPH_BOUNDED_PATH_QUANTIFICATION",
			"SQL_GRAPH_PATH_TYPE", "SQL_GRAPH_PATH_MODE", "SQL_GRAPH_EXPOSE_GRAPH_ELEMENT",
			"SQL_GRAPH_CHEAPEST_PATH", "SQL_GRAPH_PATH_SEARCH_PREFIX_PATH_COUNT",
			"SQL_GRAPH_SET_OPERATION_PROPAGATION_MODE", "SQL_GRAPH_DYNAMIC_LABEL_PROPERTIES_IN_DDL",
			"SQL_GRAPH_DYNAMIC_ELEMENT_TYPE", "SQL_GRAPH_UNBOUNDED_PATH_QUANTIFICATION",
			"SQL_GRAPH_RETURN_EXTENSIONS", "SQL_GRAPH_CALL", "SQL_GRAPH_DYNAMIC_MULTI_LABEL_NODES",
			"SQL_GRAPH_SAFE_SAME_ALL_DIFFERENT", "GROUP_BY_GRAPH_PATH",
		},
		"proto / enum types are not BigQuery types": {
			"PROTO_EXTENSIONS_WITH_NEW", "PROTO_EXTENSIONS_WITH_SET", "PROTO_MAPS", "PROTO_DEFAULT_IF_NULL",
			"EXTRACT_FROM_PROTO", "REPLACE_FIELDS", "REPLACE_FIELDS_ALLOW_MULTI_ONEOF", "FILTER_FIELDS",
			"BRACED_PROTO_CONSTRUCTORS", "ENUM_VALUE_DESCRIPTOR_PROTO",
		},
		"type not available in BigQuery": {
			"UUID_TYPE", "MAP_TYPE", "VECTOR_TYPE", "TIMESTAMP_PICOS", "TIMESTAMP_NANOS", "TIMESTAMP_PRECISION",
		},
		"anonymization / differential privacy output is noise-dependent and uses non-BigQuery syntax": {
			"ANONYMIZATION", "ANONYMIZATION_THRESHOLDING", "DIFFERENTIAL_PRIVACY",
			"DIFFERENTIAL_PRIVACY_REPORT_FUNCTIONS", "DIFFERENTIAL_PRIVACY_MIN_PRIVACY_UNITS_PER_GROUP",
			"DIFFERENTIAL_PRIVACY_PUBLIC_GROUPS", "DIFFERENTIAL_PRIVACY_PER_AGGREGATION_BUDGET",
			"DIFFERENTIAL_PRIVACY_MAX_ROWS_CONTRIBUTED", "DIFFERENTIAL_PRIVACY_NESTED",
			"PIPE_AGGREGATE_WITH_DIFFERENTIAL_PRIVACY",
		},
		"language feature not in BigQuery": {
			"GENERAL_QUANTIFIED_COMPARISONS", "INLINE_LAMBDA_ARGUMENT", "SAFE_FUNCTION_CALL_WITH_LAMBDA_ARGS",
			"GROUP_BY_ARRAY", "GROUP_BY_ARRAYinordertopassthecompliacetest", "ARRAY_ZIP", "ARRAY_FIND_FUNCTIONS",
			"MULTILEVEL_AGGREGATION", "MULTILEVEL_AGGREGATION_ON_UDAS", "ARRAY_ELEMENTS_WITH_SET",
			"TUMBLE_HOP_TVFS", "ARRAY_ORDERING", "AGGREGATE_FILTERING", "LIKE_ANY_SOME_ALL_SUBQUERY",
			"KLL_WEIGHTS", "KLL_QUANTILES_EXTRACT_RELATIVE_RANK", "DECLARATIVE_TYPE_FRAMEWORK",
			"WITH_GROUP_ROWS", "ARRAY_EQUALITY", "WITH_RECURSIVE_DEPTH_MODIFIER",
			"TYPE_MODIFIERS_IN_EXPLICIT_CONSTRUCTORS_AND_UDF", "RADIANS_DEGREES_FUNCTIONS",
			"PIPE_RECURSIVE_UNION", "LIMIT_OFFSET_EXPRESSIONS", "LIMIT_ALL",
			"BITWISE_AGGREGATE_BYTES_SIGNATURES", "UNNEST_AND_FLATTEN_ARRAYS", "TIME_BUCKET_FUNCTIONS",
			"ARRAY_OF_ARRAY", "TYPEOF_FUNCTION", "TYPE_ANNOTATIONS_ON_SQL_FUNCTION_ARGUMENTS",
			"ENABLE_MEASURES", "JSON_ARRAY_VALUE_EXTRACTION_FUNCTIONS", "NESTED_UPDATE_DELETE_WITH_OFFSET",
			"CAST_TO_JSON_TYPE", "WITH_EXPRESSION", "ARRAY_DISTINCT", "ARRAY_AGGREGATION_FUNCTIONS",
			"PIPE_TEE", "PIPE_FORK", "JSON_TYPE_COMPARISON", "JSON_TYPE_COMPARISON_COERCION",
			"PARSE_TIMESTAMP_WITH_PRECISION_AND_TIMEZONE", "MULTIWAY_UNNEST", "RELAXED_WITH_RECURSIVE",
			"PIPE_CREATE_TABLE", "FIRST_AND_LAST_N", "BIT_CAST_BYTES_FUNCTIONS", "SINGLE_TABLE_NAME_ARRAY_PATH",
			"MULTI_GROUPING_SETS", "JSON_QUERY_LAX", "JSON_FLATTEN_FUNCTION", "CHAINED_FUNCTION_CALLS",
			"CAST_DIFFERENT_ARRAY_TYPES", "CONCAT_MIXED_TYPES", "BETWEEN_UINT64_INT64",
			"TOP_LEVEL_TABLE_STATEMENTS", "STRUCT_POSITIONAL_ACCESSOR", "ANALYSIS_CONSTANT_STRUCT_POSITIONAL_ACCESSOR",
			"L2_NORM", "L1_NORM", "DOT_PRODUCT", "MANHATTAN_DISTANCE", "ANY_STRING_TEMPLATED_ARGUMENT",
			"PIPE_INSERT", "ENABLE_CONSTANT_EXPRESSION_IN_JSON_PATH", "DML_RETURNING", "CAST_OPERATORS",
			"BARE_ARRAY_ACCESS", "ALIGN_OPERATOR", "STRATIFIED_RESERVOIR_TABLESAMPLE", "PIPE_NAMED_WINDOWS",
			"MATCH_MAKE_STRUCT_IN_GROUP_BY", "LATERAL_JOIN", "LATERAL_COLUMN_REFERENCES",
			"JSON_STRICT_NUMBER_PARSING", "IMPLICIT_COERCION_STRING_LITERAL_TO_BYTES",
			"FUNCTION_ARGUMENTS_WITH_DEFAULTS", "PIPE_STATIC_DESCRIBE", "PIPE_IF", "PIPE_DESCRIBE",
			"PIPE_ASSERT", "JSON_SUBFIELDS_WITH_SET", "JSON_CONTAINS_FUNCTION", "ALLOW_CONSECUTIVE_ON",
			"ADDITIONAL_DATE_TIME_FUNCTIONS", "CORRELATED_REFS_IN_NESTED_DML",
		},
		"storage-engine primary-key semantics do not apply to BigQuery": {
			"DISALLOW_PRIMARY_KEY_UPDATES", "DISALLOW_NULL_PRIMARY_KEYS", "DISALLOW_LEGACY_UNICODE_COLLATION",
		},
		"sampling output is nondeterministic": {
			"TABLESAMPLE",
		},
	}
	for reason, feats := range groups {
		for _, f := range feats {
			NonBigQueryFeatures[f] = reason
		}
	}
}

// nonBigQueryTypeRe matches type names that exist only in the
// internal GoogleSQL product mode (BigQuery runs PRODUCT_EXTERNAL).
var nonBigQueryTypeRe = regexp.MustCompile(`(?i)\b(INT32|UINT32|UINT64|FLOAT32|PROTO|ENUM|UUID|MAP|GRAPH_ELEMENT|GRAPH_PATH|TOKENLIST)\s*<|(?i)\b(INT32|UINT32|UINT64|FLOAT32|UUID|TOKENLIST|FLOAT)\b|googlesql_test\.`)

// SkipReason returns a non-empty skip reason when the case should not
// run against a BigQuery-dialect emulator.
func SkipReason(features, forbidden []string, sql, expectedHeader string) string {
	var unsup, unclassified []string
	for _, f := range features {
		if BigQueryFeatures[f] {
			continue
		}
		if r, ok := NonBigQueryFeatures[f]; ok {
			unsup = append(unsup, f+" ("+r+")")
			continue
		}
		unclassified = append(unclassified, f)
	}
	sort.Strings(unsup)
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		return "unclassified feature: " + strings.Join(unclassified, ",")
	}
	if len(unsup) > 0 {
		return "feature not in BigQuery: " + unsup[0]
	}
	for _, f := range forbidden {
		if BigQueryFeatures[f] {
			return "case expects feature " + f + " disabled, but BigQuery enables it"
		}
	}
	if m := nonBigQueryTypeRe.FindString(expectedHeader); m != "" {
		return "non-BigQuery type in result: " + strings.TrimSpace(strings.TrimRight(m, "<"))
	}
	if m := nonBigQueryTypeRe.FindString(stripStringLiterals(sql)); m != "" {
		return "non-BigQuery type in SQL: " + strings.TrimSpace(strings.TrimRight(m, "<"))
	}
	return ""
}

// stripStringLiterals blanks quoted literals so type-name detection
// does not fire on string contents.
func stripStringLiterals(s string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			b.WriteString("''")
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
