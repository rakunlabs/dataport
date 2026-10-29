// Row colours, as DBeaver's: rules that paint the rows, or one column's
// cells, whose value matches. The first matching rule paints a row; a range
// rule paints its column with a gradient between two colours.

export type ColorOp = '=' | '<>' | '>' | '>=' | '<' | '<=' | 'contains' | 'regex' | 'null' | 'notnull' | 'range';

export interface ColorRule {
  id: string;
  column: string;
  op: ColorOp;
  value: string;
  // The upper end of a range.
  value2?: string;
  color: string;
  // The colour at the upper end of a range.
  color2?: string;
  // Paints only the column's cell instead of the whole row.
  cellOnly?: boolean;
  enabled: boolean;
}

export interface Paint {
  row?: string;
  cells?: Record<number, string>;
}

export const palette = ['#ef4444', '#f97316', '#eab308', '#22c55e', '#14b8a6', '#3b82f6', '#8b5cf6', '#ec4899', '#64748b'];

export const ops: ColorOp[] = ['=', '<>', '>', '>=', '<', '<=', 'contains', 'regex', 'null', 'notnull', 'range'];

// What a rule compares a value as: its text, as the grid shows it.
export function valueText(v: unknown): string | null {
  if (v === null || v === undefined) return null;
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

function num(s: string | null): number | null {
  if (s === null || s.trim() === '') return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}

function compare(a: string, b: string): number {
  const x = num(a);
  const y = num(b);
  if (x !== null && y !== null) return x - y;
  return a < b ? -1 : a > b ? 1 : 0;
}

const regexCache = new Map<string, RegExp | null>();

function regex(src: string): RegExp | null {
  if (!regexCache.has(src)) {
    try {
      regexCache.set(src, new RegExp(src, 'i'));
    } catch {
      regexCache.set(src, null);
    }
  }
  return regexCache.get(src) ?? null;
}

export function matches(rule: ColorRule, v: unknown): boolean {
  const s = valueText(v);
  switch (rule.op) {
    case 'null':
      return s === null;
    case 'notnull':
      return s !== null;
  }
  if (s === null) return false;
  switch (rule.op) {
    case '=':
      return compare(s, rule.value) === 0;
    case '<>':
      return compare(s, rule.value) !== 0;
    case '>':
      return compare(s, rule.value) > 0;
    case '>=':
      return compare(s, rule.value) >= 0;
    case '<':
      return compare(s, rule.value) < 0;
    case '<=':
      return compare(s, rule.value) <= 0;
    case 'contains':
      return s.toLowerCase().includes(rule.value.toLowerCase());
    case 'regex':
      return !!regex(rule.value)?.test(s);
    case 'range': {
      const n = num(s);
      const lo = num(rule.value);
      const hi = num(rule.value2 ?? '');
      return n !== null && lo !== null && hi !== null && n >= Math.min(lo, hi) && n <= Math.max(lo, hi);
    }
  }
  return false;
}

// tint is the background a colour paints: see-through, so text stays
// readable in light and dark themes.
export function tint(color: string): string {
  return `color-mix(in srgb, ${color} 30%, transparent)`;
}

function rgb(hex: string): [number, number, number] {
  const h = hex.replace('#', '');
  const full = h.length === 3 ? [...h].map((c) => c + c).join('') : h;
  const n = parseInt(full, 16) || 0;
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

export function mix(a: string, b: string, f: number): string {
  const x = rgb(a);
  const y = rgb(b);
  const c = x.map((v, i) => Math.round(v + (y[i] - v) * f));
  return '#' + c.map((v) => v.toString(16).padStart(2, '0')).join('');
}

function gradient(rule: ColorRule, v: unknown): string {
  const n = num(valueText(v)) ?? 0;
  const lo = num(rule.value) ?? 0;
  const hi = num(rule.value2 ?? '') ?? 0;
  const f = hi === lo ? 0 : (n - lo) / (hi - lo);
  return mix(rule.color, rule.color2 || rule.color, Math.min(1, Math.max(0, f)));
}

// paint finds the colours of one row from its values.
export function paint(rules: ColorRule[], columns: string[], get: (col: number) => unknown): Paint | null {
  let out: Paint | null = null;
  for (const rule of rules) {
    if (!rule.enabled) continue;
    const col = columns.indexOf(rule.column);
    if (col < 0) continue;
    const v = get(col);
    if (!matches(rule, v)) continue;
    out ??= {};
    if (rule.op === 'range' || rule.cellOnly) {
      out.cells ??= {};
      out.cells[col] ??= tint(rule.op === 'range' ? gradient(rule, v) : rule.color);
    } else {
      out.row ??= tint(rule.color);
    }
  }
  return out;
}
