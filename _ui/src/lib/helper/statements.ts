// Finds the statements of an editor script, with their positions, so the
// editor can put a run button next to each. It follows the server's rules
// closely enough for that: quotes, comments and PostgreSQL dollar quotes do
// not end a statement, and Oracle PL/SQL blocks end at a line with "/".

export interface Statement {
  from: number;
  to: number;
  text: string;
}

function backslashes(dbType: string): boolean {
  return dbType === 'mysql' || dbType === 'mariadb' || dbType === 'clickhouse';
}

export function statements(src: string, dbType: string): Statement[] {
  if (dbType === 'oracle') return oracleStatements(src);
  return split(src, 0, src.length, dbType);
}

function split(src: string, start: number, end: number, dbType: string): Statement[] {
  const out: Statement[] = [];
  const bs = backslashes(dbType);
  let from = start;
  let i = start;

  const push = (to: number) => {
    const text = src.slice(from, to);
    const lead = text.length - text.trimStart().length;
    const trimmed = text.trim();
    if (trimmed && !onlyComments(trimmed)) out.push({ from: from + lead, to: from + lead + trimmed.length, text: trimmed });
  };

  while (i < end) {
    const c = src[i];
    const n = src[i + 1];

    if (c === "'" || c === '"' || (c === '`' && dbType !== 'postgres')) {
      i++;
      while (i < end) {
        if (bs && src[i] === '\\') {
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
    } else if (c === '[' && dbType === 'mssql') {
      const j = src.indexOf(']', i);
      i = j < 0 ? end : j + 1;
    } else if (c === '-' && n === '-') {
      const j = src.indexOf('\n', i);
      i = j < 0 ? end : j + 1;
    } else if (c === '#' && (dbType === 'mysql' || dbType === 'mariadb')) {
      const j = src.indexOf('\n', i);
      i = j < 0 ? end : j + 1;
    } else if (c === '/' && n === '*') {
      const j = src.indexOf('*/', i + 2);
      i = j < 0 ? end : j + 2;
    } else if (c === '$' && (dbType === 'postgres' || dbType === 'duckdb')) {
      const m = /^\$[A-Za-z_]*\$/.exec(src.slice(i, i + 64));
      if (m && (i === 0 || !/[\w]/.test(src[i - 1]))) {
        const j = src.indexOf(m[0], i + m[0].length);
        i = j < 0 ? end : j + m[0].length;
      } else {
        i++;
      }
    } else if (c === ';') {
      push(i + 1);
      from = i + 1;
      i++;
    } else {
      i++;
    }
  }

  push(end);
  return out;
}

function onlyComments(s: string): boolean {
  return s.replace(/--[^\n]*/g, '').replace(/\/\*[\s\S]*?\*\//g, '').trim() === '' || s === ';';
}

const plsql = /^\s*(?:begin|declare|create\s+(?:or\s+replace\s+)?(?:editionable\s+|noneditionable\s+)?(?:procedure|function|package|trigger|type))\b/i;

// Oracle scripts: chunks between lines holding only "/"; PL/SQL chunks are
// one statement, the rest split at semicolons.
function oracleStatements(src: string): Statement[] {
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
      out.push(...split(src, a, b, 'oracle'));
    }
  }
  return out;
}
