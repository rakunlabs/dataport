package engine

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Unbounded returns the statements of a script that change every row of a
// table: an UPDATE or DELETE without a WHERE of its own. A WHERE inside a
// subquery does not count. These run only once the user confirms them, on
// every connection, as DBeaver and DataGrip ask, so a statement cut short
// or a forgotten WHERE cannot change a whole table by mistake.
//
// UPDATE and DELETE are told from the words that only contain them by what
// follows: a statement goes on with its table (DELETE FROM t, UPDATE t SET,
// DELETE t FROM, UPDATE ONLY t, UPDATE OR REPLACE t), while ON DELETE
// CASCADE, FOR UPDATE, DO UPDATE SET, GRANT UPDATE ON, THEN UPDATE SET and
// t.update do not. Definitions (CREATE, ALTER, GRANT …) do not run their
// bodies, so their statements are not checked.
func Unbounded(d Driver, script string) []string {
	var out []string

	dialect := d.Dialect()

	for _, stmt := range SplitScript(d, script) {
		if unboundedIn(stmt, dialect) {
			out = append(out, stmt)
		}
	}

	return out
}

// Statements that define code rather than run it.
var definitions = map[string]bool{
	"CREATE": true, "ALTER": true, "GRANT": true, "REVOKE": true, "COMMENT": true,
	"DROP": true, "RECREATE": true,
}

// Words that begin another statement in a T-SQL batch, which needs no
// semicolons; one ends an open UPDATE or DELETE before its WHERE. Only
// checked at the top level and outside CASE … END.
var tsqlStatements = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true,
	"EXEC": true, "EXECUTE": true, "DECLARE": true, "IF": true, "ELSE": true, "BEGIN": true,
	"END": true, "PRINT": true, "RETURN": true, "GO": true, "WHILE": true,
	"TRUNCATE": true, "CREATE": true, "DROP": true, "ALTER": true, "SET": true,
	"GRANT": true, "REVOKE": true, "USE": true, "RAISERROR": true, "THROW": true,
	"COMMIT": true, "ROLLBACK": true, "WAITFOR": true, "BREAK": true, "CONTINUE": true,
}

func unboundedIn(stmt string, dialect Dialect) bool {
	// DO $$ … $$ runs its body at once: check the body's statements.
	if dialect == DialectPostgres && FirstKeyword(stmt, dialect) == "DO" {
		for _, body := range dollarBodies(stmt) {
			for _, inner := range splitBody(body) {
				if unboundedIn(inner, dialect) {
					return true
				}
			}
		}

		return false
	}

	tsql := dialect == DialectMSSQL

	// A T-SQL batch may hold many statements and definitions; elsewhere a
	// definition is the whole statement.
	if !tsql && definitions[FirstKeyword(stmt, dialect)] {
		return false
	}

	toks := tokens(maskLiterals(stmt, dialect))

	var (
		depth int
		// CASE … END blocks open at each depth, whose END closes them
		// rather than a statement.
		cases = map[int]int{}
		// The depth of an UPDATE or DELETE whose WHERE has not come yet,
		// or -1. One in a CTE (WITH d AS (DELETE …)) is at depth 1.
		open = -1
		// The UPDATE's own SET comes before its WHERE; a later SET is the
		// next T-SQL statement (SET @x = 1).
		awaitSet bool
		// A T-SQL definition (CREATE PROCEDURE …) runs nothing until the
		// batch ends; its statements are skipped.
		defining bool
	)

	at := func(i int) string {
		if i >= 0 && i < len(toks) {
			return toks[i]
		}

		return ""
	}

	// before is the word before toks[i] within its statement: in a T-SQL
	// batch a line that starts a statement has none, so "SET NOCOUNT ON" /
	// "DELETE FROM t" is not read as ON DELETE.
	starts := map[int]bool{}

	before := func(i int) string {
		if starts[i] {
			return ""
		}

		return at(i - 1)
	}

	for i := 0; i < len(toks); i++ {
		t := toks[i]

		switch t {
		case "(":
			depth++

			continue
		case ")":
			// A data-changing CTE closes without its WHERE.
			if open >= 0 && depth == open {
				return true
			}

			delete(cases, depth)

			if depth > 0 {
				depth--
			}

			continue
		case "CASE":
			cases[depth]++

			continue
		case "END":
			if cases[depth] > 0 {
				cases[depth]--

				continue
			}
		}

		// A word followed by "(" is a function: IF(…), INSERT(…), TRUNCATE(…).
		if at(i+1) == "(" && t != "UPDATE" && t != "DELETE" && t != "WHERE" {
			continue
		}

		if tsql && depth == 0 && cases[0] == 0 && tsqlStatements[t] && !continuesTSQL(t, at(i-1), at(i+1)) {
			starts[i] = true

			if open >= 0 && !(t == "SET" && awaitSet) {
				return true
			}

			// A new statement of the batch. A procedure, function or
			// trigger takes the rest of the batch as its body, which does
			// not run now; any other statement ends such a definition only
			// at GO.
			switch {
			case t == "GO":
				defining = false
			case (t == "CREATE" || t == "ALTER") && routineDefinition(at(i+1), at(i+2), at(i+3)):
				defining = true
			}
		}

		switch {
		case open >= 0 && depth == open && t == "WHERE":
			open = -1
		case open >= 0 && depth == open && t == "SET" && awaitSet:
			awaitSet = false
		case open >= 0 && depth == open && t == ";":
			return true
		case (t == "UPDATE" || t == "DELETE") && !defining && changesRows(t, at(i+1), at(i+2), before(i)):
			if open >= 0 {
				return true
			}

			open = depth
			awaitSet = t == "UPDATE"
		}
	}

	return open >= 0
}

// routineDefinition tells CREATE / ALTER [OR ALTER] PROCEDURE, FUNCTION,
// TRIGGER or VIEW, whose body lasts to the end of a T-SQL batch.
func routineDefinition(words ...string) bool {
	for _, w := range words {
		switch w {
		case "OR", "ALTER":
			continue
		case "PROC", "PROCEDURE", "FUNCTION", "TRIGGER", "VIEW":
			return true
		}

		return false
	}

	return false
}

// continuesTSQL tells a T-SQL statement word that is part of the statement
// before it: ON DELETE, FOR UPDATE, GRANT UPDATE, DO UPDATE, INSERT … SELECT,
// UNION SELECT, ELSE inside IF, a SET after UPDATE … (handled by awaitSet).
func continuesTSQL(word, prev, next string) bool {
	// UPDATE or DELETE that goes on with its table starts a statement,
	// whatever ended the one before (SET NOCOUNT ON / DELETE FROM t).
	if (word == "UPDATE" || word == "DELETE") && changesRows(word, next, "", "") {
		return false
	}

	switch prev {
	case "ON", "FOR", "GRANT", "REVOKE", "DENY", "OF", "AFTER", "INSTEAD", "THEN", ",", "(":
		return word == "UPDATE" || word == "DELETE" || word == "INSERT" || word == "SELECT" || word == "EXECUTE" || word == "EXEC"
	case "UNION", "ALL", "EXCEPT", "INTERSECT", "AS", "EXISTS", "IN", "=":
		return word == "SELECT"
	case "INTO", "USING":
		return word == "SELECT"
	}

	return false
}

// changesRows tells an UPDATE or DELETE statement from the same word inside
// another one, by the words after it (next, then) and the word before.
func changesRows(word, next, then, prev string) bool {
	// UPDATE STATISTICS, ON UPDATE CASCADE, FOR NO KEY UPDATE.
	switch next {
	case "", ";", ")", ",", "STATISTICS", "CASCADE", "RESTRICT", "NO", "SET", "CURRENT_TIMESTAMP", "NOW", "ON", "TO", "OF", "SKIP", "NOWAIT":
		// "UPDATE SET" is DO UPDATE SET / THEN UPDATE SET: an action, not
		// a statement.
		return false
	}

	// GRANT UPDATE, DELETE ON t.
	if then == "ON" && (next == "," || prev == "," || prev == "GRANT" || prev == "REVOKE") {
		return false
	}

	switch prev {
	case "GRANT", "REVOKE", "FOR", "ON", "DO", "THEN", "BEFORE", "AFTER", "OF", "INSTEAD", "KEY":
		// FOR UPDATE OF t, ON DELETE SET NULL, AFTER UPDATE ON t, THEN
		// DELETE (MERGE), ON DUPLICATE KEY UPDATE a = 1.
		return word == "DELETE" && prev == "THEN" && next == "FROM"
	}

	// The table or FROM must follow: a name, a quoted name or a keyword
	// that leads to one.
	r, _ := utf8.DecodeRuneInString(next)

	return unicode.IsLetter(r) || r == '_' || r == '@' || r == '#'
}

// dollarBodies returns the text inside the $tag$ … $tag$ strings of a
// statement, outside comments and other strings.
func dollarBodies(s string) []string {
	var out []string

	for i := 0; i < len(s); {
		switch c := s[i]; {
		case c == '\'' || c == '"':
			i = skipQuoted(s, i, c, backslashEscapes(s, i, c, DialectPostgres))
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			i = skipLine(s, i)
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i = skipBlock(s, i, true)
		case c == '$':
			tag, ok := dollarTag(s, i)
			if !ok {
				i++

				continue
			}

			from := i + len(tag)
			j := strings.Index(s[from:], tag)

			if j < 0 {
				out = append(out, s[from:])

				return out
			}

			out = append(out, s[from:from+j])
			i = from + j + len(tag)
		default:
			i++
		}
	}

	return out
}

// splitBody cuts a PL/pgSQL body into its statements at ";". BEGIN, IF …
// THEN, LOOP and the like stay on the statement after them, which is
// enough to find an UPDATE or DELETE and its WHERE.
func splitBody(body string) []string {
	var out []string

	for _, stmt := range Split(body, DialectPostgres) {
		// Drop the block words in front: BEGIN DELETE …, THEN DELETE ….
		toks := strings.Fields(stmt)
		for len(toks) > 0 {
			switch strings.ToUpper(toks[0]) {
			case "BEGIN", "DECLARE", "THEN", "ELSE", "LOOP", "END", "IF", "ELSIF", "EXCEPTION":
				toks = toks[1:]

				continue
			}

			break
		}

		if len(toks) > 0 {
			out = append(out, strings.Join(toks, " "))
		}
	}

	return out
}

// maskLiterals returns the statement with comments removed and every
// string, quoted name and dollar-quoted body replaced by "_", so words in
// them are not read as keywords while a quoted table name still reads as a
// name.
func maskLiterals(s string, dialect Dialect) string {
	var b strings.Builder

	i, n := 0, len(s)

	for i < n {
		c := s[i]

		end := -1
		value := true

		switch {
		case (c == 'q' || c == 'Q') && dialect == DialectOracle && i+2 < n && s[i+1] == '\'' && (i == 0 || !isWordChar(rune(s[i-1]))):
			end = skipOracleQuote(s, i)
		case c == '\'' || c == '"' || (c == '`' && (dialect == DialectMySQL || dialect == DialectClickHouse || dialect == DialectSQLite)):
			end = skipQuoted(s, i, c, backslashEscapes(s, i, c, dialect))
		case c == '[' && (dialect == DialectMSSQL || dialect == DialectSQLite):
			end = skipBracket(s, i)
		case c == '$' && dialect == DialectPostgres:
			if tag, ok := dollarTag(s, i); ok {
				if j := strings.Index(s[i+len(tag):], tag); j >= 0 {
					end = i + len(tag) + j + len(tag)
				} else {
					end = n
				}
			}
		case c == '-' && i+1 < n && s[i+1] == '-', c == '#' && dialect == DialectMySQL:
			end, value = skipLine(s, i), false
		case c == '/' && i+1 < n && s[i+1] == '*':
			end, value = skipBlock(s, i, nestedComments(dialect)), false
		}

		if end < 0 {
			b.WriteByte(c)
			i++

			continue
		}

		if value {
			b.WriteString(" _ ")
		} else {
			b.WriteByte(' ')
		}

		i = end
	}

	return b.String()
}

// tokens cuts code into upper-cased words and single signs. A word after a
// dot is a name (t.update), so it is kept with the dot. Letters of any
// script make words.
func tokens(code string) []string {
	var out []string

	s := strings.ToUpper(code)

	isWord := func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '@' || r == '#' || r == '$'
	}

	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])

		switch {
		case unicode.IsSpace(r):
			i += size
		case isWord(r):
			j := i
			for j < len(s) {
				r2, n := utf8.DecodeRuneInString(s[j:])
				if !isWord(r2) {
					break
				}

				j += n
			}

			w := s[i:j]
			if len(out) > 0 && out[len(out)-1] == "." {
				w = "." + w
				out = out[:len(out)-1]
			}

			out = append(out, w)
			i = j
		default:
			out = append(out, string(r))
			i += size
		}
	}

	return out
}
