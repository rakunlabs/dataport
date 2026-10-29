// Statistics of selected cells and groups of rows, as DBeaver's Calc and
// Grouping panels compute them, over the rows the result holds.

export type CalcFn = 'count' | 'distinct' | 'nulls' | 'sum' | 'avg' | 'min' | 'max' | 'median' | 'mode';

export const calcFns: CalcFn[] = ['count', 'distinct', 'nulls', 'sum', 'avg', 'min', 'max', 'median', 'mode'];

function isNull(v: unknown) {
  return v === null || v === undefined;
}

function key(v: unknown): string {
  return typeof v === 'object' && v !== null ? JSON.stringify(v) : String(v);
}

export function toNumber(v: unknown): number | null {
  if (typeof v === 'number') return Number.isFinite(v) ? v : null;
  if (typeof v === 'bigint') return Number(v);
  if (typeof v === 'string' && v.trim() !== '') {
    const n = Number(v);
    return Number.isFinite(n) ? n : null;
  }
  return null;
}

// calc applies one function to values. With numeric, only numbers count
// for sum, average, median, minimum and maximum; otherwise minimum and
// maximum compare text when the values are not all numbers.
export function calc(fn: CalcFn, values: unknown[], numeric = true): unknown {
  const present = values.filter((v) => !isNull(v));
  const nums = present.map(toNumber).filter((n): n is number => n !== null);
  switch (fn) {
    case 'count':
      return values.length;
    case 'distinct':
      return new Set(present.map(key)).size;
    case 'nulls':
      return values.length - present.length;
    case 'sum':
      return nums.length ? round(nums.reduce((a, b) => a + b, 0)) : null;
    case 'avg':
      return nums.length ? round(nums.reduce((a, b) => a + b, 0) / nums.length) : null;
    case 'median': {
      if (!nums.length) return null;
      const s = [...nums].sort((a, b) => a - b);
      const m = s.length >> 1;
      return round(s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2);
    }
    case 'min':
    case 'max': {
      const sign = fn === 'min' ? -1 : 1;
      if (nums.length && (numeric || nums.length === present.length)) return nums.reduce((a, b) => (sign * (b - a) > 0 ? b : a));
      if (numeric || !present.length) return null;
      return present.map(key).reduce((a, b) => (sign * (b < a ? -1 : b > a ? 1 : 0) > 0 ? b : a));
    }
    case 'mode': {
      const counts = new Map<string, { v: unknown; n: number }>();
      for (const v of present) {
        const k = key(v);
        const e = counts.get(k);
        if (e) e.n++;
        else counts.set(k, { v, n: 1 });
      }
      let best: { v: unknown; n: number } | null = null;
      for (const e of counts.values()) if (!best || e.n > best.n) best = e;
      return best?.v ?? null;
    }
  }
}

// Float sums such as 0.1 + 0.2 are shown as written.
function round(n: number): number {
  return Math.round(n * 1e10) / 1e10;
}

export interface GroupAgg {
  fn: CalcFn;
  col: number;
}

export interface Group {
  keys: unknown[];
  count: number;
  values: unknown[];
}

// groupRows groups rows by the values of columns, as GROUP BY would, and
// counts each group; aggs add more functions of other columns.
export function groupRows(rows: unknown[][], by: number[], aggs: GroupAgg[]): Group[] {
  const groups = new Map<string, { keys: unknown[]; rows: unknown[][] }>();
  for (const r of rows) {
    const keys = by.map((c) => r[c]);
    const k = JSON.stringify(keys.map((v) => (isNull(v) ? null : key(v))));
    const g = groups.get(k);
    if (g) g.rows.push(r);
    else groups.set(k, { keys, rows: [r] });
  }
  return [...groups.values()].map((g) => ({
    keys: g.keys,
    count: g.rows.length,
    values: aggs.map((a) => calc(a.fn, g.rows.map((r) => r[a.col]))),
  }));
}
