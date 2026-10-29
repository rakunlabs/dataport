// sourceTable finds the table a query reads when every row of its result is
// one row of that table, so the result can be edited like the table: a plain
// SELECT of columns or * from one table. Joins, grouping, DISTINCT, set
// operations and computed or renamed columns give no table.

export interface SourceTable {
  // Empty when the query does not name the schema.
  schema: string;
  table: string;
}

interface Token {
  kind: 'word' | 'quoted' | 'string' | 'number' | 'punct';
  text: string;
}

const rejectWords = new Set(['JOIN', 'UNION', 'INTERSECT', 'EXCEPT', 'MINUS', 'GROUP', 'HAVING', 'DISTINCT', 'INTO', 'PIVOT', 'UNPIVOT', 'LATERAL', 'CONNECT']);
const stopWords = new Set(['WHERE', 'ORDER', 'LIMIT', 'OFFSET', 'FETCH', 'FOR', 'WINDOW']);
// Words that read as a column but are values of the session or the row,
// such as Postgres's user or Oracle's rownum.
const valueWords = new Set([
  'USER', 'CURRENT_USER', 'SESSION_USER', 'SYSTEM_USER', 'CURRENT_ROLE', 'CURRENT_SCHEMA', 'CURRENT_CATALOG',
  'CURRENT_DATE', 'CURRENT_TIME', 'CURRENT_TIMESTAMP', 'LOCALTIME', 'LOCALTIMESTAMP',
  'SYSDATE', 'SYSTIMESTAMP', 'ROWNUM', 'ROWID', 'LEVEL', 'NULL', 'TRUE', 'FALSE', 'DEFAULT',
]);

export function sourceTable(sql: string, dbType: string): SourceTable | null {
  const tokens = tokenize(sql, dbType);
  if (!tokens) return null;
  while (tokens.length && tokens[tokens.length - 1].text === ';') tokens.pop();

  const word = (i: number) => (tokens[i]?.kind === 'word' ? tokens[i].text.toUpperCase() : '');
  if (word(0) !== 'SELECT') return null;
  if (tokens.some((t, i) => t.kind === 'word' && rejectWords.has(word(i)))) return null;

  let i = 1;
  // SQL Server's TOP n or TOP (n).
  if (word(i) === 'TOP') {
    i++;
    if (tokens[i]?.text === '(') i += 3;
    else i++;
  }

  // The select list: *, or columns as they are, without expressions or names.
  for (;;) {
    if (tokens[i]?.text === '*') {
      i++;
    } else {
      if (!ident(tokens[i])) return null;
      if (tokens[i].kind === 'word' && tokens[i + 1]?.text !== '.' && valueWords.has(word(i))) return null;
      i++;
      while (tokens[i]?.text === '.') {
        if (tokens[i + 1]?.text === '*') {
          i += 2;
          break;
        }
        if (!ident(tokens[i + 1])) return null;
        i += 2;
      }
    }
    if (tokens[i]?.text !== ',') break;
    i++;
  }

  if (word(i) !== 'FROM') return null;
  i++;

  const parts: Token[] = [];
  // ONLY reads a parent table without its children; edits by key would not.
  if (word(i) === 'ONLY') return null;
  if (!ident(tokens[i])) return null;
  parts.push(tokens[i++]);
  while (tokens[i]?.text === '.' && ident(tokens[i + 1])) {
    parts.push(tokens[i + 1]);
    i += 2;
  }
  if (parts.length > 2) return null;

  // An alias, with or without AS.
  if (word(i) === 'AS') i++;
  if (ident(tokens[i]) && !stopWords.has(word(i))) i++;

  if (i < tokens.length && !stopWords.has(word(i))) return null;
  // FOR is a lock, or else changes what is read, such as SQL Server's
  // FOR SYSTEM_TIME history or FOR JSON.
  for (let j = i; j < tokens.length; j++) {
    if (word(j) !== 'FOR') continue;
    const next = word(j + 1);
    const ok = next === 'UPDATE' || next === 'SHARE' || (next === 'READ' && word(j + 2) === 'ONLY') || (next === 'NO' && word(j + 2) === 'KEY') || (next === 'KEY' && word(j + 2) === 'SHARE');
    if (!ok) return null;
  }

  const name = (t: Token) => (t.kind === 'quoted' ? t.text : fold(t.text, dbType));
  return parts.length === 2 ? { schema: name(parts[0]), table: name(parts[1]) } : { schema: '', table: name(parts[0]) };
}

// The default schemas a table without one is looked for in, best first.
export function defaultSchemas(dbType: string): string[] {
  switch (dbType) {
    case 'postgres':
      return ['public'];
    case 'mssql':
      return ['dbo'];
    case 'sqlite':
      return ['main'];
  }
  return [];
}

function ident(t: Token | undefined): boolean {
  return !!t && (t.kind === 'quoted' || (t.kind === 'word' && !stopWords.has(t.text.toUpperCase()) && t.text.toUpperCase() !== 'FROM'));
}

// Unquoted names are folded as the database does.
function fold(name: string, dbType: string): string {
  if (dbType === 'postgres') return name.toLowerCase();
  if (dbType === 'oracle') return name.toUpperCase();
  return name;
}

// tokenize reads the statement's words, names and strings. It gives up,
// returning null, on what it cannot read for sure, so a text it misreads
// can never hide a second table.
function tokenize(sql: string, dbType: string): Token[] | null {
  const out: Token[] = [];
  const mysql = dbType === 'mysql' || dbType === 'mariadb';
  const backslash = mysql || dbType === 'clickhouse';
  // Only SQL Server and SQLite quote names in brackets; elsewhere [ is an
  // array subscript.
  const brackets = dbType === 'mssql' || dbType === 'sqlite';
  let i = 0;

  while (i < sql.length) {
    const c = sql[i];
    const n = sql[i + 1];

    if (/\s/.test(c)) {
      i++;
    } else if (c === '#' && (mysql || dbType === 'clickhouse')) {
      // A comment there; given up rather than read.
      return null;
    } else if (c === '-' && n === '-') {
      // MySQL needs a space after --; without it, it is two minus signs.
      if (mysql && !/\s/.test(sql[i + 2] ?? ' ')) return null;
      const j = sql.indexOf('\n', i);
      i = j < 0 ? sql.length : j + 1;
    } else if (c === '/' && n === '*') {
      // MySQL runs /*! … */ as code.
      if (sql[i + 2] === '!' || sql[i + 2] === '+') return null;
      const j = sql.indexOf('*/', i + 2);
      if (j < 0) return null;
      i = j + 2;
    } else if ((c === "'" || c === '"') && /[A-Za-z0-9_]/.test(sql[i - 1] ?? ' ')) {
      // A prefixed string, such as E'…', N'…', q'[…]' or x'…', may quote in
      // ways not read here.
      return null;
    } else if (c === "'" || c === '"' || c === '`' || (c === '[' && brackets)) {
      const close = c === '[' ? ']' : c;
      let j = i + 1;
      let text = '';
      for (;;) {
        if (j >= sql.length) return null;
        if (backslash && (c === "'" || c === '"') && sql[j] === '\\') {
          text += sql.slice(j, j + 2);
          j += 2;
          continue;
        }
        if (sql[j] === close) {
          if (sql[j + 1] === close && c !== '[') {
            text += close;
            j += 2;
            continue;
          }
          break;
        }
        text += sql[j++];
      }
      // MySQL's "…" is a string, not a name.
      out.push({ kind: c === "'" || (c === '"' && mysql) ? 'string' : 'quoted', text });
      i = j + 1;
    } else if (c === '$' && dbType === 'postgres' && /^\$[A-Za-z_]*\$/.test(sql.slice(i))) {
      // A dollar-quoted string.
      const tag = sql.slice(i).match(/^\$[A-Za-z_]*\$/)![0];
      const j = sql.indexOf(tag, i + tag.length);
      if (j < 0) return null;
      out.push({ kind: 'string', text: sql.slice(i + tag.length, j) });
      i = j + tag.length;
    } else if (/[A-Za-z_]/.test(c) || c.charCodeAt(0) > 127) {
      let j = i + 1;
      while (j < sql.length && (/[\w$#]/.test(sql[j]) || sql.charCodeAt(j) > 127)) j++;
      out.push({ kind: 'word', text: sql.slice(i, j) });
      i = j;
    } else if (/[0-9]/.test(c)) {
      let j = i + 1;
      while (j < sql.length && /[\w.]/.test(sql[j])) j++;
      out.push({ kind: 'number', text: sql.slice(i, j) });
      i = j;
    } else {
      out.push({ kind: 'punct', text: c });
      i++;
    }
  }

  return out;
}
