package engine

import "strings"

// Risk classifies a statement for confirmation prompts.
type Risk string

const (
	RiskRead  Risk = "read"
	RiskWrite Risk = "write"
	RiskDDL   Risk = "ddl"
)

var ddlKeywords = map[string]bool{
	"CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true, "RENAME": true,
	"GRANT": true, "REVOKE": true, "COMMENT": true, "REINDEX": true, "VACUUM": true,
	"CLUSTER": true, "OPTIMIZE": true, "ATTACH": true, "DETACH": true,
}

// Classify returns the highest risk among the statements of a script.
func Classify(d Driver, script string) Risk {
	risk := RiskRead

	for _, stmt := range SplitScript(d, script) {
		kw := FirstKeyword(stmt, d.Dialect())

		switch {
		case ddlKeywords[kw]:
			return RiskDDL
		case readOnlyKeywords[kw] && d.CheckReadOnly(stmt) == nil && !writesInCTE(stmt, kw):
		default:
			risk = RiskWrite
		}
	}

	return risk
}

// writesInCTE reports a WITH statement that modifies data.
func writesInCTE(stmt, kw string) bool {
	if kw != "WITH" {
		return false
	}

	s := strings.ToUpper(stmt)

	return containsWord(s, "INSERT") || containsWord(s, "UPDATE") || containsWord(s, "DELETE") || containsWord(s, "MERGE")
}
