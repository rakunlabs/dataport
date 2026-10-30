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
// quoting, so semicolons inside any of them do not end a statement, nor do
// those inside a routine's BEGIN … END. Empty statements are dropped.
func Split(script string, dialect Dialect) []string {
	ranges := splitRanges(script, dialect, true)
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
// its semicolon. A routine body that never ends (a missing END) would take
// the rest of the script with it, so then the script is split again at
// every ";", as before routines were known: a broken routine fails on its
// own and the statements after it stay whole.
func splitRanges(script string, dialect Dialect, routines bool) [][2]int {
	var (
		out   [][2]int
		start int
		i     int
		n     = len(script)
		body  = routineBody{dialect: dialect}
	)

	flush := func(end int) {
		stmt := strings.TrimSpace(script[start:end])
		if stmt != "" && !onlyComments(stmt, dialect) {
			out = append(out, [2]int{start, end})
		}
	}

	for i < n {
		c := script[i]

		oracleQuote := (c == 'q' || c == 'Q') && dialect == DialectOracle && i+2 < n && script[i+1] == '\'' && (i == 0 || !isWordChar(rune(script[i-1])))

		if routines && !oracleQuote && isWordStart(script, i) {
			end := wordEnd(script, i)
			body.word(strings.ToUpper(script[i:end]))
			i = end

			continue
		}

		// Signs between words count for the body: IF( is a function, and
		// "END," closes a CASE before the next one.
		if routines && (c == '(' || c == ',' || c == ':') {
			body.sign(c)
		}

		switch {
		case oracleQuote:
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
		case c == ';' && body.open():
			// A ";" inside a routine's BEGIN … END ends one of its
			// statements, not the CREATE.
			body.semicolon()
			i++
		case c == ';':
			flush(i)
			i++
			start = i
			body = routineBody{dialect: dialect}
		default:
			i++
		}
	}

	if body.open() {
		return splitRanges(script, dialect, false)
	}

	flush(n)

	return out
}

// routineBody follows a CREATE of a procedure, function, trigger or event
// to find its BEGIN … END body (MySQL, MariaDB, SQLite, SQL Server,
// PostgreSQL's BEGIN ATOMIC). Its statements end with ";" too, so without
// this the routine would be cut at the first one and sent in pieces.
//
// Only a routine definition opens a body: in CREATE TABLE, CREATE INDEX and
// the like, begin, case or end are names or expressions, and a body there
// could hide the statements after it. Inside the body, CASE … END, IF …
// END IF and loops nest; IF and FOR open a block only where a statement
// starts (not IF(…), IF EXISTS, FOR UPDATE), and BEGIN TRANSACTION is no
// block.
type routineBody struct {
	dialect Dialect
	words   int  // words read in the statement
	create  bool // the statement is a CREATE
	routine bool // … of a procedure, function, trigger or event
	depth   int  // open BEGIN / CASE / IF / LOOP … blocks
	// The last word or ";", and the word before it.
	last string
	// A word whose block is decided by the next word: BEGIN (TRAN …).
	pending string
}

func (b *routineBody) open() bool { return b.depth > 0 }

func (b *routineBody) semicolon() {
	b.settle("")
	b.last = ";"
}

// sign notes punctuation: "(" right after IF makes it a function, and a
// label ("lbl:") starts a statement as ";" does.
func (b *routineBody) sign(c byte) {
	if c == ':' {
		if b.depth > 0 && b.pending == "" {
			b.last = ";"
		}

		return
	}

	if c == '(' && (b.pending == "IF" || b.pending == "FOR") {
		b.pending = ""
	}

	b.settle("(")
	b.last = string(c)
}

var routineKinds = map[string]bool{
	"PROCEDURE": true, "PROC": true, "FUNCTION": true, "TRIGGER": true, "EVENT": true,
}

// Words after which a statement starts inside a body.
var statementStarts = map[string]bool{
	";": true, "BEGIN": true, "THEN": true, "ELSE": true, "DO": true, "LOOP": true, "REPEAT": true, "ATOMIC": true,
}

// kindWords is how many words after CREATE the kind of object may come:
// CREATE OR REPLACE DEFINER = `u`@`h` SQL SECURITY INVOKER PROCEDURE.
const kindWords = 10

func (b *routineBody) word(w string) {
	b.words++
	prev := b.last
	b.last = w

	switch {
	case b.words == 1:
		b.create = w == "CREATE"

		return
	case b.create && !b.routine && b.words <= kindWords && routineKinds[w]:
		// The kind comes before any name or body, so the words before it
		// were modifiers.
		b.routine = true

		return
	case !b.routine:
		return
	}

	b.settle(w)

	switch w {
	case "BEGIN":
		// Decided by the next word: BEGIN TRAN / TRANSACTION / WORK /
		// DISTRIBUTED / DIALOG / CONVERSATION.
		b.pending = "BEGIN"
	case "CASE":
		// CASE … END wherever it is; END CASE closes a CASE statement.
		if b.depth > 0 && prev != "END" {
			b.depth++
		}
	case "LOOP", "REPEAT", "WHILE":
		// MySQL loops end with END LOOP / END REPEAT / END WHILE; T-SQL's
		// WHILE takes a statement or a BEGIN … END that counts on its own.
		if b.depth > 0 && prev != "END" && b.dialect != DialectMSSQL {
			b.depth++
		}
	case "IF", "FOR":
		// IF and FOR open a block where a statement starts.
		if b.depth > 0 && prev != "END" && statementStarts[prev] {
			b.pending = w
		}
	case "END":
		if b.depth > 0 {
			b.depth--
		}
	}
}

// settle decides a pending BEGIN, IF or FOR by the word after it.
func (b *routineBody) settle(next string) {
	p := b.pending
	if p == "" {
		return
	}

	b.pending = ""

	switch p {
	case "BEGIN":
		switch next {
		case "TRAN", "TRANSACTION", "WORK", "DISTRIBUTED", "DIALOG", "CONVERSATION":
			return
		}

		b.depth++
	case "IF":
		// MySQL IF … THEN … END IF; not IF EXISTS / IF NOT EXISTS, and not
		// T-SQL's IF, which has no END IF (its BEGIN counts on its own).
		if b.dialect != DialectMSSQL && next != "EXISTS" && next != "NOT" && next != "" {
			b.depth++
		}
	case "FOR":
		// FOR … DO / LOOP … END FOR; not FOR UPDATE, FOR EACH ROW.
		if next != "UPDATE" && next != "EACH" && next != "SHARE" && next != "XML" && next != "JSON" && next != "BROWSE" && next != "" {
			b.depth++
		}
	}
}

func isWordStart(s string, i int) bool {
	c := s[i]
	if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_') {
		return false
	}

	return i == 0 || !isWordChar(rune(s[i-1])) && s[i-1] != '$' && s[i-1] != '@' && s[i-1] != '.'
}

func wordEnd(s string, i int) int {
	for i < len(s) && (isWordChar(rune(s[i])) || s[i] == '$') {
		i++
	}

	return i
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
