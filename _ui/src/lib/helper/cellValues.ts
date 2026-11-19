// What a cell of a row being edited can be set to in one click, from the
// column's type, and what a new row's column gets when left alone.

import type { ColumnInfo } from '@/lib/api/types';

export type QuickKey = 'now' | 'today' | 'time' | 'uuid' | 'true' | 'false' | 'emptyJson' | 'emptyArray' | 'empty';

export interface Quick {
  key: QuickKey;
  value: unknown;
}

const pad = (n: number, w = 2) => String(n).padStart(w, '0');

function date(d: Date) {
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

function time(d: Date) {
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function offset(d: Date) {
  const m = -d.getTimezoneOffset();
  return `${m < 0 ? '-' : '+'}${pad(Math.floor(Math.abs(m) / 60))}:${pad(Math.abs(m) % 60)}`;
}

// kind sorts a column type into what the quick values know.
export function kind(type: string): 'date' | 'time' | 'timestamp' | 'timestamptz' | 'uuid' | 'bool' | 'json' | 'text' | 'other' {
  const t = type.toLowerCase().replace(/^nullable\((.*)\)$/, '$1').trim();
  if (t.endsWith('[]') || t.startsWith('array')) return 'other';
  if (/^(timestamptz|timestamp\s*(\(\d+\))?\s*with time zone|datetimeoffset|timestamp\(\d+\) with (local )?time zone)/.test(t)) return 'timestamptz';
  if (/^(timestamp|datetime|smalldatetime)/.test(t)) return 'timestamp';
  if (/^date$|^date32$/.test(t)) return 'date';
  if (/^(time|timetz)\b/.test(t)) return 'time';
  if (/^(uuid|uniqueidentifier)$/.test(t)) return 'uuid';
  if (/^(bool|boolean)$/.test(t)) return 'bool';
  if (/^jsonb?$/.test(t)) return 'json';
  if (/(char|text|string|clob)/.test(t)) return 'text';
  return 'other';
}

// quickValues lists the values offered for a column of the given type, at
// the time given. Times are the browser's local time; with a time zone
// the offset is written too.
export function quickValues(type: string, now: Date = new Date()): Quick[] {
  switch (kind(type)) {
    case 'timestamptz':
      return [{ key: 'now', value: `${date(now)} ${time(now)}${offset(now)}` }];
    case 'timestamp':
      return [
        { key: 'now', value: `${date(now)} ${time(now)}` },
        { key: 'today', value: `${date(now)} 00:00:00` },
      ];
    case 'date':
      return [{ key: 'today', value: date(now) }];
    case 'time':
      return [{ key: 'time', value: time(now) }];
    case 'uuid':
      return [{ key: 'uuid', value: uuid() }];
    case 'bool':
      return [
        { key: 'true', value: true },
        { key: 'false', value: false },
      ];
    case 'json':
      return [
        { key: 'emptyJson', value: '{}' },
        { key: 'emptyArray', value: '[]' },
      ];
    case 'text':
      return [{ key: 'empty', value: '' }];
  }
  return [];
}

function uuid(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID();
  const b = Array.from({ length: 16 }, () => Math.floor(Math.random() * 256));
  b[6] = (b[6] & 0x0f) | 0x40;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = b.map((x) => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

export type Fill = { kind: 'auto' | 'generated' | 'default' | 'null' | 'required'; expr?: string };

// fillOf tells what the database puts in a column a new row leaves out.
export function fillOf(c: ColumnInfo | undefined): Fill | undefined {
  if (!c) return undefined;
  if (c.auto === 'identity') return { kind: 'auto' };
  if (c.auto === 'generated') return { kind: 'generated' };
  const expr = shortDefault(c.default);
  if (expr) return { kind: 'default', expr };
  return c.nullable ? { kind: 'null' } : { kind: 'required' };
}

// shortDefault drops what makes a default hard to read in a cell: the
// casts PostgreSQL adds ('a'::text, now()::date) and a wrapping pair of
// parentheses, as SQL Server stores them.
export function shortDefault(def: string): string {
  let s = def.trim();
  while (/^\(.*\)$/.test(s) && balanced(s.slice(1, -1))) s = s.slice(1, -1).trim();
  s = s.replace(/^('(?:[^']|'')*')::[\w ."]+(\[\])?$/, '$1');
  return s;
}

function balanced(s: string) {
  let depth = 0;
  for (const ch of s) {
    if (ch === '(') depth++;
    else if (ch === ')' && --depth < 0) return false;
  }
  return depth === 0;
}

// firstToFill is the first column a new row should be typed into: one the
// database does not fill, else one it does not compute, else the first.
export function firstToFill(columns: string[], meta: ColumnInfo[]): number {
  const fills = columns.map((n) => fillOf(meta.find((m) => m.name === n)));
  const need = fills.findIndex((f) => f?.kind === 'required');
  if (need >= 0) return need;
  const free = fills.findIndex((f) => !f || f.kind === 'null' || f.kind === 'default');
  return free >= 0 ? free : 0;
}
