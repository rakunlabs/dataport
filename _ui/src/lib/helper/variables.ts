// SQL variables and placeholders, filled in on the client before a run, as
// DBX does. The SQL that reaches the server holds plain literals, so this
// works the same on every database and the query log shows what really ran.
//
//   @set limit = 10;            declares a value for the script
//   SELECT … LIMIT ${limit}     ${name} uses it, as does @limit
//   WHERE id = #{id}            #{name} and :name ask for a value
//
// Strings, quoted identifiers and comments are left alone.

import type { VariableSyntaxes } from '@/lib/stores/prefs.svelte';

export interface Placeholder {
  name: string;
  // The token as written, such as ${id}.
  token: string;
  // A declared value, when @set gives one.
  value?: string;
}

interface Span {
  start: number;
  end: number;
}

// codeSpans returns the parts of the script outside strings, quoted
// identifiers and comments.
function codeSpans(sql: string): Span[] {
  const out: Span[] = [];
  let start = 0;
  let i = 0;
  const n = sql.length;

  const skip = (to: number) => {
    if (i > start) out.push({ start, end: i });
    i = to;
    start = to;
  };

  while (i < n) {
    const c = sql[i];
    const d = sql[i + 1];
    if (c === "'" || c === '"' || c === '`') {
      let j = i + 1;
      while (j < n) {
        if (sql[j] === c) {
          if (sql[j + 1] === c) {
            j += 2;
            continue;
          }
          break;
        }
        if (sql[j] === '\\' && c !== '"') j++;
        j++;
      }
      skip(Math.min(n, j + 1));
    } else if (c === '-' && d === '-') {
      const j = sql.indexOf('\n', i);
      skip(j < 0 ? n : j);
    } else if (c === '/' && d === '*') {
      const j = sql.indexOf('*/', i + 2);
      skip(j < 0 ? n : j + 2);
    } else if (c === '$' && /^\$[A-Za-z_]*\$/.test(sql.slice(i, i + 64)) && !(i > 0 && /\w/.test(sql[i - 1]))) {
      const tag = /^\$[A-Za-z_]*\$/.exec(sql.slice(i, i + 64))![0];
      // ${name} is a variable, not a dollar quote.
      if (sql[i + 1] === '{') {
        i++;
        continue;
      }
      const j = sql.indexOf(tag, i + tag.length);
      skip(j < 0 ? n : j + tag.length);
    } else {
      i++;
    }
  }
  if (start < n) out.push({ start, end: n });
  return out;
}

const NAME = '[\\p{L}_][\\p{L}\\p{N}_]*';
const atSetRe = new RegExp(`@set\\s+(${NAME})\\s*=\\s*([^;]*?)\\s*;`, 'giu');

interface Declaration extends Span {
  name: string;
  value: string;
}

function declarations(sql: string, spans: Span[]): Declaration[] {
  const out: Declaration[] = [];
  for (const s of spans) {
    const part = sql.slice(s.start, s.end);
    for (const m of part.matchAll(atSetRe)) {
      out.push({ name: m[1].toLowerCase(), value: m[2], start: s.start + m.index!, end: s.start + m.index! + m[0].length });
    }
  }
  // @set may also hold a quoted value, which the code spans split; read
  // those from the whole text, outside comments.
  for (const m of sql.matchAll(new RegExp(`@set\\s+(${NAME})\\s*=\\s*('(?:[^']|'')*')\\s*;`, 'giu'))) {
    const start = m.index!;
    if (!spans.some((s) => start >= s.start && start < s.end)) continue;
    if (out.some((d) => d.start === start)) continue;
    out.push({ name: m[1].toLowerCase(), value: m[2], start, end: start + m[0].length });
  }
  return out.sort((a, b) => a.start - b.start);
}

function tokenRes(syntaxes: VariableSyntaxes, declared: Set<string>): { re: RegExp; name: (m: RegExpMatchArray) => string }[] {
  const list: { re: RegExp; name: (m: RegExpMatchArray) => string }[] = [];
  if (syntaxes.shell || syntaxes.atSet) list.push({ re: new RegExp(`\\$\\{\\s*(${NAME})\\s*\\}`, 'gu'), name: (m) => m[1] });
  if (syntaxes.mybatis) list.push({ re: new RegExp(`#\\{\\s*(${NAME})\\s*\\}`, 'gu'), name: (m) => m[1] });
  // :name, never ::type casts or :: inside an expression.
  if (syntaxes.named) list.push({ re: new RegExp(`(?<![:\\p{L}\\p{N}_]):(${NAME})`, 'gu'), name: (m) => m[1] });
  // @name only refers to a value declared with @set, so session variables
  // of SQL Server and MySQL stay untouched.
  if (syntaxes.atSet && declared.size) list.push({ re: new RegExp(`(?<![@\\p{L}\\p{N}_])@(${NAME})`, 'gu'), name: (m) => (declared.has(m[1].toLowerCase()) ? m[1] : '') });
  return list;
}

interface Occurrence extends Span {
  name: string;
  token: string;
}

function occurrences(sql: string, spans: Span[], syntaxes: VariableSyntaxes, declared: Set<string>, skip: Span[]): Occurrence[] {
  const out: Occurrence[] = [];
  for (const { re, name } of tokenRes(syntaxes, declared)) {
    for (const s of spans) {
      const part = sql.slice(s.start, s.end);
      for (const m of part.matchAll(re)) {
        const n = name(m);
        if (!n) continue;
        const start = s.start + m.index!;
        const end = start + m[0].length;
        if (skip.some((k) => start >= k.start && start < k.end)) continue;
        if (n.toLowerCase() === 'set' && m[0].startsWith('@')) continue;
        if (out.some((o) => start < o.end && end > o.start)) continue;
        out.push({ name: n, token: m[0], start, end });
      }
    }
  }
  return out.sort((a, b) => a.start - b.start);
}

// placeholders lists what a run would fill in, in order of first use. Those
// with a declared value need no input.
export function placeholders(sql: string, syntaxes: VariableSyntaxes): Placeholder[] {
  const spans = codeSpans(sql);
  const decls = syntaxes.atSet ? declarations(sql, spans) : [];
  const values = new Map(decls.map((d) => [d.name, d.value]));
  const seen = new Map<string, Placeholder>();
  for (const o of occurrences(sql, spans, syntaxes, new Set(values.keys()), decls)) {
    const key = o.name.toLowerCase();
    if (!seen.has(key)) seen.set(key, { name: o.name, token: o.token, value: values.get(key) });
  }
  return [...seen.values()];
}

// substitute fills in the placeholders and drops @set declarations. Values
// are put in verbatim: write 'text' with its quotes. Unknown names stay.
export function substitute(sql: string, syntaxes: VariableSyntaxes, given: Record<string, string> = {}): string {
  const spans = codeSpans(sql);
  const decls = syntaxes.atSet ? declarations(sql, spans) : [];
  const values = new Map<string, string>(decls.map((d) => [d.name, d.value]));
  for (const [k, v] of Object.entries(given)) values.set(k.toLowerCase(), v);

  const edits: (Span & { text: string })[] = [];
  for (const d of decls) {
    // The declaration goes, with the line break after it.
    let end = d.end;
    if (sql[end] === '\r') end++;
    if (sql[end] === '\n') end++;
    edits.push({ start: d.start, end, text: '' });
  }
  for (const o of occurrences(sql, spans, syntaxes, new Set(decls.map((d) => d.name)), decls)) {
    const v = values.get(o.name.toLowerCase());
    if (v !== undefined) edits.push({ start: o.start, end: o.end, text: v });
  }

  let out = sql;
  for (const e of edits.sort((a, b) => b.start - a.start)) out = out.slice(0, e.start) + e.text + out.slice(e.end);
  return out.trim();
}
