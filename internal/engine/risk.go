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

		// A T-SQL batch needs no semicolons, so one statement may hold
		// several: rate it by the riskiest keyword it contains.
		if d.Dialect() == DialectMSSQL {
			switch _, r := tsqlKeyword(stmt); r {
			case RiskDDL:
				return RiskDDL
			case RiskWrite:
				risk = RiskWrite
			}
		}

		switch {
		case ddlKeywords[kw]:
			return RiskDDL
		case dynamicKeywords[kw] && containsDDL(strings.ToUpper(stmt)):
			// The code runs out of sight of the keyword check; DDL written
			// in it, in a string or a dollar-quoted body, counts as DDL.
			return RiskDDL
		case kw == "EXPLAIN" && explainRuns(stmt, d.Dialect()):
			// EXPLAIN ANALYZE executes the statement it explains.
			upper := strings.ToUpper(codeOnly(stmt, d.Dialect()))
			if containsDDL(upper) {
				return RiskDDL
			}

			for _, w := range []string{"INSERT", "UPDATE", "DELETE", "MERGE"} {
				if containsWord(upper, w) {
					risk = RiskWrite
				}
			}
		case kw == "SELECT" && d.Dialect() == DialectPostgres && containsWord(strings.ToUpper(codeOnly(stmt, d.Dialect())), "INTO"):
			// SELECT ... INTO creates a table in PostgreSQL.
			return RiskDDL
		case readOnlyKeywords[kw] && d.CheckReadOnly(stmt) == nil && !writesInCTE(stmt, kw):
		default:
			risk = RiskWrite
		}
	}

	return risk
}

// dynamicKeywords start statements that run code given as text or a block.
var dynamicKeywords = map[string]bool{
	"DO": true, "EXECUTE": true, "EXEC": true, "PREPARE": true, "CALL": true, "BEGIN": true, "DECLARE": true,
}

func containsDDL(upper string) bool {
	for w := range ddlKeywords {
		if containsWord(upper, w) {
			return true
		}
	}

	return false
}

// explainRuns reports an EXPLAIN that executes its statement, such as
// EXPLAIN ANALYZE in PostgreSQL and MySQL.
func explainRuns(stmt string, dialect Dialect) bool {
	upper := strings.ToUpper(codeOnly(stmt, dialect))

	return containsWord(upper, "ANALYZE") || containsWord(upper, "ANALYSE")
}

// writesInCTE reports a WITH statement that modifies data.
func writesInCTE(stmt, kw string) bool {
	if kw != "WITH" {
		return false
	}

	s := strings.ToUpper(stmt)

	return containsWord(s, "INSERT") || containsWord(s, "UPDATE") || containsWord(s, "DELETE") || containsWord(s, "MERGE")
}
