package engine

import (
	"strings"
	"unicode"
)

// Dialect selects the lexical rules of the splitter.
type Dialect int

const (
	DialectPostgres Dialect = iota
	DialectMySQL
	DialectMSSQL
	DialectClickHouse
	// DialectStandard has no backslash escapes and no nested comments:
	// Trino and most ODBC targets.
	DialectStandard
	DialectSQLite
	DialectOracle
)

// Split cuts a script into single statements. It understands quoted strings,
// quoted identifiers, line and block comments and, for PostgreSQL, dollar
// quoting, so semicolons inside any of them do not end a statement. Empty
// statements are dropped.
func Split(script string, dialect Dialect) []string {
	ranges := splitRanges(script, dialect)
	out := make([]string, 0, len(ranges))

	for _, r := range ranges {
		out = append(out, strings.TrimSpace(script[r[0]:r[1]]))
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// splitRanges returns the [start, end) offsets of each statement, without
// its semicolon.
func splitRanges(script string, dialect Dialect) [][2]int {
	var (
		out   [][2]int
		start int
		i     int
		n     = len(script)
	)

	flush := func(end int) {
		stmt := strings.TrimSpace(script[start:end])
		if stmt != "" && !onlyComments(stmt, dialect) {
			out = append(out, [2]int{start, end})
		}
	}

	for i < n {
		c := script[i]

		switch {
		case (c == 'q' || c == 'Q') && dialect == DialectOracle && i+2 < n && script[i+1] == '\'' && (i == 0 || !isWordChar(rune(script[i-1]))):
			i = skipOracleQuote(script, i)
		case c == '\'' || c == '"' || (c == '`' && (dialect == DialectMySQL || dialect == DialectClickHouse || dialect == DialectSQLite)):
			i = skipQuoted(script, i, c, backslashEscapes(script, i, c, dialect))
		case c == '[' && (dialect == DialectMSSQL || dialect == DialectSQLite):
			i = skipBracket(script, i)
		case c == '-' && i+1 < n && script[i+1] == '-':
			i = skipLine(script, i)
		case c == '#' && dialect == DialectMySQL:
			i = skipLine(script, i)
		case c == '/' && i+1 < n && script[i+1] == '*':
			i = skipBlock(script, i, nestedComments(dialect))
		case c == '$' && dialect == DialectPostgres:
			if tag, ok := dollarTag(script, i); ok {
				end := strings.Index(script[i+len(tag):], tag)
				if end < 0 {
					i = n
				} else {
					i += len(tag) + end + len(tag)
				}
			} else {
				i++
			}
		case c == ';':
			flush(i)
			i++
			start = i
		default:
			i++
		}
	}

	flush(n)

	return out
}

// backslashEscapes reports whether a backslash escapes the next character in
// the string opened at i: always in MySQL strings, and in PostgreSQL only in
// E'...' escape strings.
func backslashEscapes(s string, i int, quote byte, dialect Dialect) bool {
	switch dialect {
	case DialectMySQL, DialectClickHouse:
		return quote != '`'
	case DialectMSSQL, DialectStandard, DialectSQLite, DialectOracle:
		return false
	}

	if quote != '\'' || i == 0 || (s[i-1] != 'E' && s[i-1] != 'e') {
		return false
	}

	return i < 2 || !isWordChar(rune(s[i-2]))
}

func skipQuoted(s string, i int, quote byte, backslash bool) int {
	i++
	for i < len(s) {
		c := s[i]
		if backslash && c == '\\' {
			i += 2

			continue
		}

		if c == quote {
			if i+1 < len(s) && s[i+1] == quote {
				i += 2

				continue
			}

			return i + 1
		}

		i++
	}

	return len(s)
}

// skipOracleQuote skips an Oracle q'<delim>...<delim>' literal, where the
// closing delimiter pairs with the opening one for brackets.
func skipOracleQuote(s string, i int) int {
	open := s[i+2]
	closer := open

	switch open {
	case '[':
		closer = ']'
	case '(':
		closer = ')'
	case '{':
		closer = '}'
	case '<':
		closer = '>'
	}

	end := strings.Index(s[i+3:], string(closer)+"'")
	if end < 0 {
		return len(s)
	}

	return i + 3 + end + 2
}

// skipBracket skips a [quoted] identifier; "]]" escapes a bracket.
func skipBracket(s string, i int) int {
	i++
	for i < len(s) {
		if s[i] == ']' {
			if i+1 < len(s) && s[i+1] == ']' {
				i += 2

				continue
			}

			return i + 1
		}

		i++
	}

	return len(s)
}

func nestedComments(dialect Dialect) bool {
	return dialect == DialectPostgres || dialect == DialectMSSQL
}

func skipLine(s string, i int) int {
	end := strings.IndexByte(s[i:], '\n')
	if end < 0 {
		return len(s)
	}

	return i + end + 1
}

func skipBlock(s string, i int, nested bool) int {
	depth := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			if depth == 0 || nested {
				depth++
			}

			i += 2

			continue
		}

		if i+1 < len(s) && s[i] == '*' && s[i+1] == '/' {
			depth--
			i += 2

			if depth == 0 {
				return i
			}

			continue
		}

		i++
	}

	return len(s)
}

// dollarTag reports the $tag$ that opens a dollar-quoted string at i.
func dollarTag(s string, i int) (string, bool) {
	if i > 0 {
		prev := rune(s[i-1])
		if unicode.IsLetter(prev) || unicode.IsDigit(prev) || prev == '_' {
			return "", false
		}
	}

	j := i + 1
	for j < len(s) && s[j] != '$' {
		c := rune(s[j])
		if !unicode.IsLetter(c) && c != '_' && (j == i+1 || !unicode.IsDigit(c)) {
			return "", false
		}

		j++
	}

	if j >= len(s) {
		return "", false
	}

	return s[i : j+1], true
}

func onlyComments(stmt string, dialect Dialect) bool {
	return stripLeading(stmt, dialect) == ""
}

// stripLeading removes leading whitespace, comments and opening parentheses.
func stripLeading(s string, dialect Dialect) string {
	for {
		s = strings.TrimLeftFunc(s, unicode.IsSpace)

		switch {
		case strings.HasPrefix(s, "--"):
			s = s[skipLine(s, 0):]
		case dialect == DialectMySQL && strings.HasPrefix(s, "#"):
			s = s[skipLine(s, 0):]
		case strings.HasPrefix(s, "/*"):
			s = s[skipBlock(s, 0, nestedComments(dialect)):]
		case strings.HasPrefix(s, "("):
			s = s[1:]
		default:
			return s
		}
	}
}

// FirstKeyword returns the upper-cased first word of a statement.
func FirstKeyword(stmt string, dialect Dialect) string {
	s := stripLeading(stmt, dialect)

	end := strings.IndexFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && r != '_'
	})
	if end < 0 {
		end = len(s)
	}

	return strings.ToUpper(s[:end])
}

var (
	// readOnlyKeywords may run on a read-only grant. The read-only
	// transaction is the real guard; this list stops statements that would
	// leave it, such as DDL in MySQL, which commits implicitly.
	readOnlyKeywords = map[string]bool{
		"SELECT": true, "WITH": true, "SHOW": true, "EXPLAIN": true,
		"DESCRIBE": true, "DESC": true, "VALUES": true, "TABLE": true,
	}

	// transactionKeywords are never allowed: the engine owns the transaction,
	// and ending it early would run the rest of the script outside of it.
	transactionKeywords = map[string]bool{
		"BEGIN": true, "START": true, "COMMIT": true, "ROLLBACK": true, "END": true,
		"ABORT": true, "SAVEPOINT": true, "RELEASE": true, "XA": true,
	}

	rowKeywords = map[string]bool{
		"SELECT": true, "WITH": true, "SHOW": true, "EXPLAIN": true, "DESCRIBE": true,
		"DESC": true, "VALUES": true, "TABLE": true, "PRAGMA": true, "CALL": true,
	}
)

// returnsRows guesses whether a statement produces a result set. DML with a
// RETURNING clause does.
func returnsRows(stmt, keyword string) bool {
	if rowKeywords[keyword] {
		return true
	}

	switch keyword {
	case "INSERT", "UPDATE", "DELETE", "MERGE":
		return containsWord(strings.ToUpper(stmt), "RETURNING")
	}

	return false
}

func containsWord(s, word string) bool {
	for idx := 0; ; {
		j := strings.Index(s[idx:], word)
		if j < 0 {
			return false
		}

		j += idx
		before := j == 0 || !isWordChar(rune(s[j-1]))
		after := j+len(word) >= len(s) || !isWordChar(rune(s[j+len(word)]))

		if before && after {
			return true
		}

		idx = j + len(word)
	}
}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// codeOnly returns the statement with string literals, quoted identifiers
// and comments replaced by spaces, so word checks only see code.
func codeOnly(s string, dialect Dialect) string {
	b := []byte(s)
	i, n := 0, len(s)

	blank := func(from, to int) {
		for k := from; k < to && k < n; k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
	}

	for i < n {
		c := s[i]

		var end int

		switch {
		case (c == 'q' || c == 'Q') && dialect == DialectOracle && i+2 < n && s[i+1] == '\'' && (i == 0 || !isWordChar(rune(s[i-1]))):
			end = skipOracleQuote(s, i)
		case c == '\'' || c == '"' || (c == '`' && (dialect == DialectMySQL || dialect == DialectClickHouse || dialect == DialectSQLite)):
			end = skipQuoted(s, i, c, backslashEscapes(s, i, c, dialect))
		case c == '[' && (dialect == DialectMSSQL || dialect == DialectSQLite):
			end = skipBracket(s, i)
		case c == '-' && i+1 < n && s[i+1] == '-':
			end = skipLine(s, i)
		case c == '#' && dialect == DialectMySQL:
			end = skipLine(s, i)
		case c == '/' && i+1 < n && s[i+1] == '*':
			end = skipBlock(s, i, nestedComments(dialect))
		default:
			i++

			continue
		}

		blank(i, end)
		i = end
	}

	return string(b)
}
