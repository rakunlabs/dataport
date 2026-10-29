// Reads which tables a statement names, with their aliases, for the
// editor's completion. It is a quick look, not a parser: names after FROM,
// JOIN, UPDATE and INTO, and the lists after FROM.

export interface TableRef {
  schema: string;
  table: string;
  alias: string;
}

const name = String.raw`(?:"(?:[^"]|"")+"|\x60[^\x60]+\x60|\[[^\]]+\]|[\p{L}_][\p{L}\p{N}_$#]*)`;
const qualified = new RegExp(String.raw`^(${name})(?:\s*\.\s*(${name}))?(?:\s+(?:as\s+)?(${name}))?`, 'iu');
const start = /\b(from|join|update|into)\s+/giu;
// Words that end a FROM list or cannot be an alias.
const stop = new Set(
  'where on using set group order limit having values union intersect except inner left right full outer cross join natural window offset fetch returning for lateral select default partition'.split(' '),
);

function unquote(s: string): string {
  if (/^["`[]/.test(s)) return s.slice(1, -1).replace(/""/g, '"');
  return s;
}

// Without comments and strings, so their text is not read as names.
function strip(sql: string): string {
  return sql
    .replace(/--[^\n]*/g, ' ')
    .replace(/\/\*[\s\S]*?(\*\/|$)/g, ' ')
    .replace(/'(?:[^']|'')*('|$)/g, "''");
}

export function tableRefs(sql: string): TableRef[] {
  const text = strip(sql);
  const out: TableRef[] = [];
  for (const m of text.matchAll(start)) {
    let rest = text.slice((m.index ?? 0) + m[0].length);
    // FROM a, b x, c AS y: a list; JOIN, UPDATE and INTO take one name.
    const list = m[1].toLowerCase() === 'from';
    for (;;) {
      const q = qualified.exec(rest);
      if (!q) break;
      const [first, second] = [q[1], q[2]];
      if (stop.has(first.toLowerCase())) break;
      const alias = q[3] && !stop.has(q[3].toLowerCase()) ? unquote(q[3]) : '';
      out.push(second ? { schema: unquote(first), table: unquote(second), alias } : { schema: '', table: unquote(first), alias });
      if (!list) break;
      // The alias may have been a stop word; only what was read is skipped.
      const used = alias ? q[0].length : q[0].length - (q[3] ? q[0].length - q[0].lastIndexOf(q[3]) : 0);
      rest = rest.slice(used);
      const comma = /^\s*,\s*/.exec(rest);
      if (!comma) break;
      rest = rest.slice(comma[0].length);
    }
  }
  return out;
}

// Names written before a dot, such as a schema in "sales.", so its
// tables can be loaded for completion.
export function qualifiers(sql: string): string[] {
  const out = new Set<string>();
  for (const m of strip(sql).matchAll(new RegExp(String.raw`(${name})\s*\.`, 'gu'))) out.add(unquote(m[1]));
  return [...out];
}
