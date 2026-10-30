// Finds the statements of an editor script, with their positions, so the
// editor can put a run button next to each. It follows the server's rules
// closely enough for that: quotes, comments and PostgreSQL dollar quotes do
// not end a statement, and Oracle PL/SQL blocks end at a line with "/".

export interface Statement {
  from: number;
  to: number;
  text: string;
  // The statement ended at a blank line, without a ";" (see split).
  soft?: boolean;
}

function backslashes(dbType: string): boolean {
  return dbType === 'mysql' || dbType === 'mariadb' || dbType === 'clickhouse';
}

// blankLines lets a blank line end a statement, as DBeaver's "Blank line is
// statement delimiter" does; without it only ";" does.
export function statements(src: string, dbType: string, blankLines = true): Statement[] {
  if (dbType === 'oracle') return oracleStatements(src, blankLines);
  return split(src, 0, src.length, dbType, blankLines, true);
}

// separate puts a ";" after every statement that ends at a blank line
// without one, so the server, which splits at ";" only, runs them one by one
// as the editor shows them.
export function separate(src: string, dbType: string, blankLines = true): string {
  const parts: string[] = [];
  let at = 0;
  for (const s of statements(src, dbType, blankLines)) {
    if (!s.soft) continue;
    parts.push(src.slice(at, s.to), ';');
    at = s.to;
  }
  if (!parts.length) return src;
  parts.push(src.slice(at));
  return parts.join('');
}

// statementAt finds the statement a run without a selection means, as DBX
// does: the one the cursor is in, or the one that ends earlier on the same
// line (after its ";"), or the next one when the cursor is in the blank
// space just before it on its line. On a line of its own between
// statements there is none.
export function statementAt(list: Statement[], src: string, pos: number): Statement | undefined {
  const lineStart = src.lastIndexOf('\n', pos - 1) + 1;
  // A statement starts at its first code, after the comments before it; on
  // the line of those comments the cursor is still with the statement that
  // ended there, or with none.
  const inside = list.find((s) => pos >= codeStart(src, s) && pos <= s.to);
  if (inside) return inside;
  let lineEnd = src.indexOf('\n', pos);
  if (lineEnd < 0) lineEnd = src.length;
  // Text after the statement on its last line: its ";" and spaces, or a comment.
  const before = [...list].reverse().find((s) => s.to < pos && s.to >= lineStart);
  if (before) return before;
  const after = list.find((s) => s.from > pos && s.from <= lineEnd && !src.slice(pos, s.from).trim());
  return after;
}

// codeStart is where a statement's code begins, after comments and blank
// lines in front of it.
function codeStart(src: string, s: Statement): number {
  let i = s.from;
  for (;;) {
    while (i < s.to && /\s/.test(src[i])) i++;
    if (src.startsWith('--', i)) {
      const j = src.indexOf('\n', i);
      i = j < 0 || j > s.to ? s.to : j + 1;
    } else if (src.startsWith('/*', i)) {
      const j = src.indexOf('*/', i + 2);
      i = j < 0 || j > s.to ? s.to : j + 2;
    } else return i;
  }
}

// A blank line ends a statement without a ";" only when all of these hold,
// so a long query written with blank lines in it is never cut (DBeaver's
// "Smart" mode works the same way):
//
//   - the blank line is at the top level: not in brackets, a string, a
//     comment, a $$ body or a CREATE … BEGIN … END block;
//   - the next line begins with a word that starts a statement (below), so
//     "delete from t", a blank line, then "where id = 1" stays one statement;
//   - the line before does not leave the statement open: it does not end in
//     a comma, an operator or a word like AND, UNION, AS or FROM;
//   - the statement does not take that word as its own part: the main query
//     of a WITH or EXPLAIN, the SELECT of an INSERT or CREATE … AS, the SET
//     of an UPDATE, the actions of a MERGE or ALTER.
//
// A line without a blank line before it never ends a statement. SQL Server
// is left out: its batches commonly run without semicolons, and DECLARE'd
// variables last only for one batch.
const softStarts = new Set([
  'SELECT', 'WITH', 'CREATE', 'ALTER', 'DROP', 'INSERT', 'UPDATE', 'DELETE', 'MERGE', 'REPLACE', 'TRUNCATE',
  'GRANT', 'REVOKE', 'COMMENT', 'EXPLAIN', 'SHOW', 'DESCRIBE', 'USE', 'SET', 'CALL', 'EXEC', 'EXECUTE',
  'BEGIN', 'COMMIT', 'ROLLBACK', 'DECLARE', 'ANALYZE', 'VACUUM', 'PRAGMA', 'REFRESH', 'COPY', 'ATTACH', 'DETACH',
]);

// After these words or signs the next line belongs to the same statement:
// "UNION ALL" / "SELECT", "CREATE VIEW v AS" / "SELECT", "a," / "update".
const continuationWords = new Set([
  'UNION', 'INTERSECT', 'EXCEPT', 'MINUS', 'ALL', 'DISTINCT', 'THEN', 'ELSE', 'AS', 'AND', 'OR', 'NOT', 'IN',
  'EXISTS', 'BY', 'FROM', 'JOIN', 'WHERE', 'ON', 'INTO', 'SET', 'VALUES', 'WITH', 'RECURSIVE', 'EXPLAIN', 'ANALYZE',
  'VERBOSE', 'IS', 'SELECT', 'CASE', 'WHEN', 'LIKE', 'BETWEEN', 'HAVING', 'KEY', 'GRANT', 'REVOKE', 'AFTER',
  'BEFORE', 'OF', 'INSTEAD', 'FOR', 'EACH', 'ROW', 'TO', 'USING', 'RETURNS', 'RETURN', 'TABLE', 'VIEW', 'DO',
]);
const continuationSigns = ',(=<>+-*/|&.%!';
const cteBodies = new Set(['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'MERGE']);
const explainable = new Set(['SELECT', 'WITH', 'INSERT', 'UPDATE', 'DELETE', 'MERGE', 'CREATE', 'ALTER', 'DROP', 'VALUES', 'EXECUTE']);

// startsStatement tells whether word, at the start of a line, can begin a
// statement: REPLACE( and TRUNCATE( are functions, COMMENT is a statement
// only as COMMENT ON, and WITH only as a CTE (not WITH (NOLOCK) or WITH
// GRANT OPTION).
function startsStatement(src: string, at: number, word: string): boolean {
  if (!softStarts.has(word)) return false;
  const rest = src.slice(at + word.length, at + word.length + 200);
  if ((word === 'REPLACE' || word === 'TRUNCATE') && /^\s*\(/.test(rest)) return false;
  if (word === 'COMMENT' && !/^\s+ON\b/i.test(rest)) return false;
  if (word === 'WITH' && (/^\s+(?:ORDINALITY|TIME\s+ZONE|TIES|CHECK|GRANT|ADMIN|NO\s+DATA|DATA|LOCAL|CASCADED|HOLD|ROLLUP|CUBE)\b/i.test(rest) || !/^\s+(?:RECURSIVE\s+)?[^\s(]+\s*(?:\([^)]*\)\s*)?AS\b/i.test(rest))) return false;
  return true;
}

// RoutineBody follows a CREATE of a procedure, function, trigger or event
// to find its BEGIN … END body, as the server's routineBody does
// (internal/engine/split.go); the two must agree on where statements end.
// Only a routine definition opens a body: in CREATE TABLE or CREATE INDEX,
// begin, case or end are names. Inside, CASE … END, IF … END IF and loops
// nest; IF and FOR open a block only where a statement starts (not IF(…),
// IF EXISTS, FOR UPDATE), and BEGIN TRANSACTION is no block.
const routineKinds = new Set(['PROCEDURE', 'PROC', 'FUNCTION', 'TRIGGER', 'EVENT']);
const bodyStatementStarts = new Set([';', 'BEGIN', 'THEN', 'ELSE', 'DO', 'LOOP', 'REPEAT', 'ATOMIC']);

class RoutineBody {
  words = 0;
  create = false;
  routine = false;
  depth = 0;
  last = '';
  pending = '';
  // The body was opened and has ended: what follows is a new statement.
  closed = false;
  dbType: string;

  constructor(dbType: string) {
    this.dbType = dbType;
  }

  get open() {
    return this.depth > 0;
  }

  semicolon() {
    this.settle('');
    this.last = ';';
  }

  sign(c: string) {
    if (c === ':') {
      if (this.depth > 0 && this.pending === '') this.last = ';';
      return;
    }
    if (c === '(' && (this.pending === 'IF' || this.pending === 'FOR')) this.pending = '';
    this.settle('(');
    this.last = c;
  }

  word(w: string) {
    this.words++;
    const prev = this.last;
    this.last = w;
    if (this.words === 1) {
      this.create = w === 'CREATE';
      return;
    }
    if (this.create && !this.routine && this.words <= 10 && routineKinds.has(w)) {
      this.routine = true;
      return;
    }
    if (!this.routine) return;
    this.settle(w);
    switch (w) {
      case 'BEGIN':
        this.pending = 'BEGIN';
        break;
      case 'CASE':
        if (this.depth > 0 && prev !== 'END') this.depth++;
        break;
      case 'LOOP':
      case 'REPEAT':
      case 'WHILE':
        if (this.depth > 0 && prev !== 'END' && this.dbType !== 'mssql') this.depth++;
        break;
      case 'IF':
      case 'FOR':
        if (this.depth > 0 && prev !== 'END' && bodyStatementStarts.has(prev)) this.pending = w;
        break;
      case 'END':
        if (this.depth > 0) {
          this.depth--;
          if (this.depth === 0) this.closed = true;
        }
        break;
    }
  }

  private settle(next: string) {
    const p = this.pending;
    if (!p) return;
    this.pending = '';
    if (p === 'BEGIN') {
      if (!['TRAN', 'TRANSACTION', 'WORK', 'DISTRIBUTED', 'DIALOG', 'CONVERSATION'].includes(next)) this.depth++;
    } else if (p === 'IF') {
      if (this.dbType !== 'mssql' && next !== 'EXISTS' && next !== 'NOT' && next !== '') this.depth++;
    } else if (p === 'FOR') {
      if (!['UPDATE', 'EACH', 'SHARE', 'XML', 'JSON', 'BROWSE', ''].includes(next)) this.depth++;
    }
  }
}

// routines keeps the ";" inside a routine's BEGIN … END from ending the
// CREATE, as the server's splitter does (internal/engine/split.go). A body
// that is never closed is split at every ";" instead, again as the server
// does, so the statements after it stay whole.
function split(src: string, start: number, end: number, dbType: string, blankLines: boolean, routines = true): Statement[] {
  const out: Statement[] = [];
  const bs = backslashes(dbType);
  const soft = blankLines && dbType !== 'mssql';
  let from = start;
  let i = start;

  // What is known of the statement being read, to tell a soft start from
  // a line that carries it on.
  let started = false;
  let head = ''; // its first word, then the one whose body is being read
  let first = ''; // its first word, kept
  let last = ''; // the last word (upper case) or sign
  let lastEnd = start; // just after the last code, before comments
  let depth = 0;
  let lineStart = true;
  // A line with only spaces came after the last code of the statement.
  let blank = false;
  let lineHasContent = false;
  let cteDone = false;
  let explainDone = false;
  // A routine's BEGIN … END body, whose ";" and lines end no statement.
  let body = new RoutineBody(dbType);
  // INSERT … VALUES is whole; a SELECT after it is a statement of its own.
  let values = false;
  // What a CREATE makes: TABLE, VIEW, TRIGGER…
  let kind = '';
  // A WITH inside the statement (CREATE … AS WITH …) waits for its query.
  let withOpen = false;
  // CREATE TABLE t (…) SELECT … took its query.
  let tableQuery = false;

  const reset = () => {
    started = false;
    head = '';
    first = '';
    last = '';
    depth = 0;
    blank = false;
    cteDone = false;
    explainDone = false;
    body = new RoutineBody(dbType);
    values = false;
    kind = '';
    withOpen = false;
    tableQuery = false;
  };

  const push = (a: number, b: number, isSoft = false) => {
    const text = src.slice(a, b);
    const lead = text.length - text.trimStart().length;
    const trimmed = text.trim();
    if (trimmed && !onlyComments(trimmed)) out.push({ from: a + lead, to: a + lead + trimmed.length, text: trimmed, ...(isSoft ? { soft: true } : {}) });
  };

  // follow notes the main query of a WITH or EXPLAIN, wherever it is, so a
  // later statement after a blank line is not taken for it.
  const follow = (word: string) => {
    if (head === 'WITH' && !cteDone && cteBodies.has(word) && last === ')') {
      cteDone = true;
      head = word;
    } else if (head === 'EXPLAIN' && !explainDone && explainable.has(word)) {
      explainDone = true;
      head = word;
    } else if ((head === 'INSERT' || head === 'REPLACE') && !values && (word === 'SELECT' || word === 'WITH')) {
      head = word;
      cteDone = false;
    } else if (first === 'CREATE' && head === 'CREATE' && withOpen && cteBodies.has(word) && last === ')') {
      head = word;
    }
  };

  const continues = (word: string, at: number): boolean => {
    if (routines && body.open) return true;
    // The rules of the statement's own word go first, so EXPLAIN / SELECT
    // takes its one query and no more.
    if (head === 'WITH' && !cteDone && cteBodies.has(word)) {
      cteDone = true;
      head = word;
      return true;
    }
    if (head === 'EXPLAIN' && !explainDone && explainable.has(word)) {
      explainDone = true;
      head = word;
      return true;
    }
    if (continuationWords.has(last) || (last.length === 1 && continuationSigns.includes(last))) return true;
    if ((head === 'INSERT' || head === 'REPLACE') && !values && (word === 'SELECT' || word === 'WITH')) {
      head = word;
      cteDone = false;
      return true;
    }
    // UPDATE … SET, INSERT … ON CONFLICT DO UPDATE SET (also after its
    // SELECT, and in a WITH or EXPLAIN), ALTER … SET.
    if (word === 'SET' && (['UPDATE', 'INSERT', 'REPLACE', 'ALTER', 'MERGE', 'CREATE'].includes(head) || ['INSERT', 'REPLACE', 'MERGE', 'ALTER'].includes(first) || last === 'UPDATE')) return true;
    // CREATE TABLE t (…) SELECT … (MySQL), ALTER TABLE … ATTACH / REPLACE
    // PARTITION, SELECT … USE INDEX.
    if ((dbType === 'mysql' || dbType === 'mariadb') && head === 'CREATE' && kind === 'TABLE' && last === ')' && depth === 0 && !tableQuery && (word === 'SELECT' || word === 'WITH')) {
      tableQuery = true;
      return true;
    }
    if (first === 'ALTER' && ['ATTACH', 'DETACH', 'REPLACE', 'USE', 'SET', 'ALTER'].includes(word)) return true;
    if (word === 'USE' && first !== 'USE' && /^USE\s+(?:INDEX|KEY)\b/i.test(src.slice(at, at + 12))) return true;
    // CREATE VIEW v AS WITH … / SELECT: the WITH's main query, like a WITH
    // of its own.
    if (first === 'CREATE' && head === 'CREATE' && withOpen && cteBodies.has(word)) {
      head = word;
      return true;
    }
    // A routine's BEGIN or DECLARE starts its body.
    if (head === 'CREATE' && ['BEGIN', 'DECLARE'].includes(word) && kind !== 'TABLE' && !body.closed) return true;
    // CREATE TRIGGER … EXECUTE FUNCTION f(), CREATE RULE … DO ALSO DELETE …:
    // the action is part of the trigger or rule, not a statement after it.
    if (head === 'CREATE' && (kind === 'TRIGGER' || kind === 'RULE') && !body.closed && ['EXECUTE', 'INSERT', 'UPDATE', 'DELETE', 'SELECT'].includes(word)) return true;
    if (head === 'ALTER' && ['ALTER', 'COMMENT', 'DROP', 'UPDATE', 'DELETE'].includes(word)) return true;
    // MERGE's actions come after WHEN … THEN, which the words above keep.
    return false;
  };

  const code = (sign: string, to: number) => {
    if (!started) head = first = /^[A-Z_]/.test(sign) && sign.length > 1 ? sign : '';
    started = true;
    last = sign;
    lastEnd = to;
    blank = false;
  };

  while (i < end) {
    const c = src[i];
    const n = src[i + 1];

    if (c === '\n') {
      if (!lineHasContent && started) blank = true;
      lineHasContent = false;
      lineStart = true;
      i++;
      continue;
    }
    if (c === ' ' || c === '\t' || c === '\r' || c === '\f') {
      i++;
      continue;
    }
    const atLine: boolean = lineStart;
    lineStart = false;
    lineHasContent = true;

    // The quoting and comment rules follow the server's splitter
    // (internal/engine/split.go), so both find the same statements.
    if ((c === 'q' || c === 'Q') && dbType === 'oracle' && n === "'" && (i === start || !/\w/.test(src[i - 1]))) {
      i = skipOracleQuote(src, i, end);
      code('x', i);
    } else if (c === "'" || c === '"' || (c === '`' && backticks(dbType))) {
      // Backslash escapes: MySQL and ClickHouse strings (not names), and
      // PostgreSQL E'…' strings.
      const esc = (bs && c !== '`') || (c === "'" && pgEscape(src, i, dbType));
      i++;
      while (i < end) {
        if (esc && src[i] === '\\') {
          i += 2;
          continue;
        }
        if (src[i] === c) {
          if (src[i + 1] === c) {
            i += 2;
            continue;
          }
          break;
        }
        i++;
      }
      i++;
      code('x', Math.min(i, end));
    } else if (c === '[' && (dbType === 'mssql' || dbType === 'sqlite')) {
      // "]]" is a "]" inside the name.
      i++;
      while (i < end && !(src[i] === ']' && src[i + 1] !== ']')) i += src[i] === ']' ? 2 : 1;
      i = Math.min(i + 1, end);
      code('x', i);
    } else if ((c === '-' && n === '-') || (c === '#' && (dbType === 'mysql' || dbType === 'mariadb'))) {
      // Up to the line break, which the loop then reads as the line's end.
      const j = src.indexOf('\n', i);
      i = j < 0 ? end : j;
      lineStart = atLine;
    } else if (c === '/' && n === '*') {
      i = skipBlock(src, i, end, dbType === 'postgres' || dbType === 'duckdb' || dbType === 'mssql');
      lineStart = atLine;
    } else if (c === '$' && (dbType === 'postgres' || dbType === 'duckdb') && dollarTag.test(src.slice(i, i + 64)) && (i === 0 || !/[\w]/.test(src[i - 1]))) {
      const tag = dollarTag.exec(src.slice(i, i + 64))![0];
      const j = src.indexOf(tag, i + tag.length);
      i = j < 0 ? end : j + tag.length;
      code('x', i);
    } else if (c === ';' && routines && body.open) {
      body.semicolon();
      code(';', i + 1);
      i++;
    } else if (c === ';') {
      push(from, i + 1);
      from = i + 1;
      i++;
      reset();
    } else if (/[A-Za-z_]/.test(c) && (i === start || !/[\w$@.]/.test(src[i - 1]))) {
      const word = /^[A-Za-z_][\w$]*/.exec(src.slice(i, i + 128))![0];
      const upper = word.toUpperCase();
      if (soft && atLine && blank && started && depth === 0 && startsStatement(src, i, upper) && !continues(upper, i)) {
        push(from, lastEnd, true);
        from = i;
        reset();
      } else if (started && depth === 0) {
        // Without a blank line before it the word cannot split, but it still
        // moves the statement on: EXPLAIN's query, WITH's main query.
        follow(upper);
      }
      if (head === 'CREATE' && !kind && !['CREATE', 'OR', 'REPLACE', 'TEMP', 'TEMPORARY', 'UNIQUE', 'CONSTRAINT', 'MATERIALIZED', 'GLOBAL', 'LOCAL', 'UNLOGGED', 'DEFINER', 'IF', 'NOT', 'EXISTS'].includes(upper)) kind = upper;
      if (upper === 'VALUES') values = true;
      if (upper === 'WITH' && started && last === 'AS') withOpen = true;
      // The body sees the word after any split, as the server sees the
      // statement it starts.
      if (routines) body.word(upper);
      code(upper, i + word.length);
      i += word.length;
    } else {
      if (routines && (c === '(' || c === ',' || c === ':')) body.sign(c);
      if (c === '(') depth++;
      else if (c === ')') depth = Math.max(0, depth - 1);
      // A "*" after SELECT, RETURNING, a comma or a dot is all columns, a
      // value; elsewhere it multiplies and the line goes on.
      const star = c === '*' && (/^[A-Z_]{2,}$/.test(last) || last === ',' || last === '.' || last === '(');
      code(/\w/.test(c) || star ? 'x' : c, i + 1);
      i++;
    }
  }

  if (routines && body.open) return split(src, start, end, dbType, blankLines, false);
  push(from, end);
  return out;
}

// A $tag$ opens a dollar-quoted string; the tag may hold digits after its
// first character, as in PostgreSQL.
const dollarTag = /^\$(?:[A-Za-z_][A-Za-z_0-9]*)?\$/;

// backticks quote names in MySQL, MariaDB, ClickHouse and SQLite.
function backticks(dbType: string): boolean {
  return dbType === 'mysql' || dbType === 'mariadb' || dbType === 'clickhouse' || dbType === 'sqlite';
}

// pgEscape tells a PostgreSQL E'…' string, where a backslash escapes.
function pgEscape(src: string, i: number, dbType: string): boolean {
  if (dbType !== 'postgres' && dbType !== 'duckdb') return false;
  const e = src[i - 1];
  return (e === 'E' || e === 'e') && (i < 2 || !/\w/.test(src[i - 2]));
}

// skipOracleQuote skips an Oracle q'<d>…<d>' literal, whose closing
// delimiter pairs with the opening one for brackets.
function skipOracleQuote(src: string, i: number, end: number): number {
  const open = src[i + 2];
  const close = ({ '[': ']', '(': ')', '{': '}', '<': '>' } as Record<string, string>)[open] ?? open;
  const j = src.indexOf(close + "'", i + 3);
  return j < 0 || j >= end ? end : j + 2;
}

// skipBlock skips a /* … */ comment, nested where the database nests them.
function skipBlock(src: string, i: number, end: number, nested: boolean): number {
  let depth = 0;
  while (i < end) {
    if (src[i] === '/' && src[i + 1] === '*') {
      if (depth === 0 || nested) depth++;
      i += 2;
    } else if (src[i] === '*' && src[i + 1] === '/') {
      depth--;
      i += 2;
      if (depth === 0) return i;
    } else i++;
  }
  return end;
}

function onlyComments(s: string): boolean {
  return s.replace(/--[^\n]*/g, '').replace(/\/\*[\s\S]*?\*\//g, '').trim() === '' || s === ';';
}

const plsql = /^\s*(?:begin|declare|create\s+(?:or\s+replace\s+)?(?:editionable\s+|noneditionable\s+)?(?:procedure|function|package|trigger|type))\b/i;

// Oracle scripts: chunks between lines holding only "/"; PL/SQL chunks are
// one statement, the rest split at semicolons.
function oracleStatements(src: string, blankLines: boolean): Statement[] {
  const out: Statement[] = [];
  let start = 0;
  const re = /^[ \t]*\/[ \t]*$/gm;
  const chunks: [number, number][] = [];
  let m: RegExpExecArray | null;
  while ((m = re.exec(src))) {
    chunks.push([start, m.index]);
    start = m.index + m[0].length;
  }
  chunks.push([start, src.length]);

  for (const [a, b] of chunks) {
    const text = src.slice(a, b);
    if (plsql.test(text.replace(/^(\s*(--[^\n]*\n|\/\*[\s\S]*?\*\/))*/, ''))) {
      const lead = text.length - text.trimStart().length;
      const trimmed = text.trim();
      if (trimmed) out.push({ from: a + lead, to: a + lead + trimmed.length, text: trimmed });
    } else {
      out.push(...split(src, a, b, 'oracle', blankLines));
    }
  }
  return out;
}
