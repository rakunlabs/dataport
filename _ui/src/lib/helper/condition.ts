// Suggestions for the WHERE and ORDER BY boxes of a table tab, after DBX's
// condition editor: the text before the cursor decides whether a column,
// an operator, a value or a connector comes next.

import type { ColumnInfo } from '@/lib/api/types';

export interface Suggestion {
  label: string;
  insert: string;
  kind: 'column' | 'operator' | 'keyword' | 'value';
  detail?: string;
}

export interface Completion {
  from: number;
  to: number;
  items: Suggestion[];
  // The first item is preselected so Enter or Tab takes it.
  preselect: boolean;
}

const operators: { label: string; insert: string }[] = [
  { label: '=', insert: '= ' },
  { label: '<>', insert: '<> ' },
  { label: '>', insert: '> ' },
  { label: '>=', insert: '>= ' },
  { label: '<', insert: '< ' },
  { label: '<=', insert: '<= ' },
  { label: 'LIKE', insert: 'LIKE ' },
  { label: 'NOT LIKE', insert: 'NOT LIKE ' },
  { label: 'ILIKE', insert: 'ILIKE ' },
  { label: 'IN', insert: 'IN (' },
  { label: 'NOT IN', insert: 'NOT IN (' },
  { label: 'BETWEEN', insert: 'BETWEEN ' },
  { label: 'IS NULL', insert: 'IS NULL ' },
  { label: 'IS NOT NULL', insert: 'IS NOT NULL ' },
];

const identifier = /("(?:[^"]|"")*"|`(?:[^`]|``)*`|\[(?:\]\]|[^\]])*\]|[A-Za-z_][\w$]*)$/;
const valueOperator = /(?:<>|!=|<=|>=|=|<|>|\b(?:NOT\s+)?(?:LIKE|ILIKE|BETWEEN))\s*$/i;

function unquote(s: string): string {
  const open = s[0];
  if (open === '"' || open === '`') return s.slice(1, -1).replaceAll(open + open, open);
  if (open === '[') return s.slice(1, -1).replaceAll(']]', ']');
  return s;
}

// inQuote reports whether the cursor is inside a string literal or a
// quoted identifier, where nothing should be suggested.
function inQuote(text: string): 'string' | 'ident' | '' {
  let quote = '';
  for (let i = 0; i < text.length; i++) {
    const ch = text[i];
    if (quote) {
      const close = quote === '[' ? ']' : quote;
      if (ch === close) {
        if (text[i + 1] === close) i++;
        else quote = '';
      }
    } else if (ch === "'" || ch === '"' || ch === '`' || ch === '[') {
      quote = ch;
    }
  }
  if (!quote) return '';
  return quote === "'" ? 'string' : 'ident';
}

// insideInList reports whether the innermost open parenthesis belongs to
// IN, where values are typed instead of columns.
function insideInList(text: string): boolean {
  const opens: number[] = [];
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '(') opens.push(i);
    else if (text[i] === ')') opens.pop();
  }
  const open = opens[opens.length - 1];
  if (open === undefined) return false;
  return /\b(?:NOT\s+)?IN\s*$/i.test(text.slice(0, open));
}

function matchColumns(columns: ColumnInfo[], token: string, quote: (n: string) => string): Suggestion[] {
  const q = token.toLowerCase();
  const scored = columns
    .map((c, i) => {
      const n = c.name.toLowerCase();
      const score = !q ? 1 : n.startsWith(q) ? 3 : n.includes(q) ? 2 : 0;
      return { c, i, score };
    })
    .filter((x) => x.score > 0 && x.c.name.toLowerCase() !== q)
    .sort((a, b) => b.score - a.score || a.i - b.i);
  return scored.map(({ c }) => ({
    label: c.name,
    // Plain lower-case names stay bare; others are quoted for the dialect.
    // The trailing space moves on to the operator, as in DBX.
    insert: (/^[a-z_][a-z0-9_]*$/.test(c.name) ? c.name : quote(c.name)) + ' ',
    kind: 'column',
    detail: c.type,
  }));
}

function keywords(list: string[], token: string, kind: Suggestion['kind'] = 'keyword'): Suggestion[] {
  const q = token.toLowerCase();
  return list.filter((k) => !q || (k.toLowerCase().startsWith(q) && k.toLowerCase() !== q)).map((k) => ({ label: k, insert: k + ' ', kind }));
}

function valueSnippets(type: string, dbType: string): string[] {
  const ty = type.toLowerCase();
  if (/bool|bit/.test(ty)) return dbType === 'mssql' ? ['1', '0'] : ['TRUE', 'FALSE'];
  if (/date|time/.test(ty)) {
    if (dbType === 'mssql') return ['GETDATE()', 'CAST(GETDATE() AS date)'];
    if (dbType === 'clickhouse') return ['now()', 'today()', 'today() - 7'];
    if (dbType === 'postgres' || dbType === 'duckdb') return ['now()', 'CURRENT_DATE', "now() - interval '7 days'"];
    if (dbType === 'oracle') return ['SYSDATE', 'TRUNC(SYSDATE)', 'SYSDATE - 7'];
    if (dbType === 'sqlite') return ["datetime('now')", "date('now')", "datetime('now', '-7 days')"];
    if (dbType === 'trino' || dbType === 'odbc') return ['CURRENT_TIMESTAMP', 'CURRENT_DATE', "CURRENT_DATE - INTERVAL '7' DAY"];
    return ['NOW()', 'CURDATE()', 'NOW() - INTERVAL 7 DAY'];
  }
  return [];
}

export function complete(
  kind: 'where' | 'order',
  value: string,
  cursor: number,
  columns: ColumnInfo[],
  dbType: string,
  quote: (n: string) => string,
): Completion | null {
  const before = value.slice(0, cursor);
  if (inQuote(before) === 'string') return null;

  const tokenMatch = before.match(/[A-Za-z_][\w$]*$/);
  const token = tokenMatch?.[0] ?? '';
  const from = cursor - token.length;
  const after = value.slice(cursor).match(/^[\w$]* ?/)?.[0] ?? '';
  const to = cursor + after.length;
  const prefix = value.slice(0, from).trimEnd();
  const done = (items: Suggestion[], preselect = false): Completion | null =>
    items.length ? { from, to, items: items.slice(0, items[0].kind === 'operator' ? items.length : 12), preselect } : null;

  if (kind === 'order') {
    if (!prefix || prefix.endsWith(',')) return done(matchColumns(columns, token, quote), !!token);
    return done(keywords(['ASC', 'DESC', 'NULLS FIRST', 'NULLS LAST'].filter((k) => ['postgres', 'oracle', 'duckdb', 'trino', 'sqlite'].includes(dbType) || !k.startsWith('NULLS')), token));
  }

  if (insideInList(before)) return null;

  if (/\bIS\s+NOT$/i.test(prefix)) return done(keywords(['NULL', 'TRUE', 'FALSE'], token));
  if (/\bIS$/i.test(prefix)) return done(keywords(['NULL', 'NOT NULL', 'TRUE', 'FALSE'], token));

  // Value position: after a comparison, suggest values that fit the type.
  const op = valueOperator.exec(prefix);
  if (op) {
    const id = identifier.exec(prefix.slice(0, op.index).trimEnd());
    const col = id ? columns.find((c) => c.name.toLowerCase() === unquote(id[1]).toLowerCase()) : undefined;
    return done(keywords(valueSnippets(col?.type ?? '', dbType), token, 'value'));
  }

  // Start of a predicate: a column.
  if (!prefix || /(?:^|\s)(?:AND|OR|NOT)$/i.test(prefix) || /[(,]$/.test(prefix)) {
    const items = matchColumns(columns, token, quote);
    if (/(?:^|\s)NOT$/i.test(prefix)) items.push(...keywords(['EXISTS'], token));
    return done(items, !!token);
  }

  // Right after a column: an operator.
  const id = identifier.exec(prefix);
  if (id && !token.length && columns.some((c) => c.name.toLowerCase() === unquote(id[1]).toLowerCase())) {
    const left = prefix.slice(0, id.index).trimEnd();
    if (!left || /(?:^|\s)(?:AND|OR|NOT)$/i.test(left) || /[(,]$/.test(left)) {
      return done(
        operators.filter((o) => o.label !== 'ILIKE' || dbType === 'postgres' || dbType === 'clickhouse' || dbType === 'duckdb').map((o) => ({ ...o, kind: 'operator' })),
      );
    }
  }
  if (id && token && columns.some((c) => c.name.toLowerCase() === unquote(id[1]).toLowerCase()) && prefix.endsWith(id[1])) {
    const ops = operators.filter((o) => o.label.toLowerCase().startsWith(token.toLowerCase()) && /^[A-Z]/.test(o.label));
    return done(ops.map((o) => ({ ...o, kind: 'operator' })));
  }

  // After a complete predicate: a connector.
  return done(keywords(['AND', 'OR'], token));
}
