// Copy formats for result grids, after DBX's data grid extractors. All of
// them work on the selected rows only.

import type { Column } from '@/lib/api/types';

export type CopyFormat = 'tsv' | 'tsv-headers' | 'csv' | 'json' | 'markdown' | 'insert' | 'update' | 'in-list' | 'where';

export interface CopyContext {
  columns: Column[];
  rows: unknown[][];
  dbType: string;
  // Qualified, quoted table name; formats that write SQL need it.
  table?: string;
  // Key column names; UPDATE and WHERE use them when set.
  keys?: string[];
  // Column used by the IN list; defaults to the first column.
  column?: number;
}

export const sqlFormats: CopyFormat[] = ['insert', 'update', 'where', 'in-list'];

export function quoteIdent(dbType: string, name: string): string {
  if (dbType === 'mysql' || dbType === 'mariadb' || dbType === 'clickhouse') return '`' + name.replaceAll('`', '``') + '`';
  if (dbType === 'mssql') return '[' + name.replaceAll(']', ']]') + ']';
  return '"' + name.replaceAll('"', '""') + '"';
}

function text(v: unknown): string {
  if (v === null || v === undefined) return '';
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

function isNumericType(type: string) {
  return /^(nullable\()?(u?int|integer|bigint|smallint|tinyint|mediumint|numeric|decimal|real|double|float|money|int[248]|float[48])/i.test(type);
}

// sqlLiteral writes a value for SQL text. Numbers stay bare only when the
// column is numeric and the value looks like a number.
export function sqlLiteral(v: unknown, type: string, dbType: string): string {
  if (v === null || v === undefined) return 'NULL';
  if (typeof v === 'boolean') {
    if (dbType === 'mssql') return v ? '1' : '0';
    return v ? 'TRUE' : 'FALSE';
  }
  const s = text(v);
  if ((typeof v === 'number' || isNumericType(type)) && /^-?\d+(\.\d+)?(e[+-]?\d+)?$/i.test(s)) return s;
  let escaped = s.replaceAll("'", "''");
  // MySQL and ClickHouse treat backslash as an escape inside strings.
  if (dbType === 'mysql' || dbType === 'mariadb' || dbType === 'clickhouse') escaped = escaped.replaceAll('\\', '\\\\');
  return (dbType === 'mssql' && /[^\x00-\x7f]/.test(s) ? 'N' : '') + `'${escaped}'`;
}

function delimited(ctx: CopyContext, sep: string, headers: boolean): string {
  const cell = (v: unknown) => {
    const s = v === null || v === undefined ? '' : text(v);
    if (sep === '\t') return s.replace(/[\t\r\n]+/g, ' ');
    return /[",\r\n]/.test(s) ? `"${s.replaceAll('"', '""')}"` : s;
  };
  const lines = ctx.rows.map((r) => r.map(cell).join(sep));
  if (headers) lines.unshift(ctx.columns.map((c) => cell(c.name)).join(sep));
  return lines.join(sep === ',' ? '\r\n' : '\n');
}

function markdown(ctx: CopyContext): string {
  const cell = (v: unknown) => (v === null || v === undefined ? 'NULL' : text(v)).replaceAll('|', '\\|').replace(/\r?\n/g, '<br>');
  const head = `| ${ctx.columns.map((c) => cell(c.name)).join(' | ')} |`;
  const rule = `| ${ctx.columns.map((c) => (isNumericType(c.type) ? '--:' : '---')).join(' | ')} |`;
  return [head, rule, ...ctx.rows.map((r) => `| ${r.map(cell).join(' | ')} |`)].join('\n');
}

function json(ctx: CopyContext): string {
  return JSON.stringify(
    ctx.rows.map((r) => Object.fromEntries(ctx.columns.map((c, i) => [c.name, r[i] ?? null]))),
    null,
    2,
  );
}

function condition(ctx: CopyContext, row: unknown[]): string {
  const idx = ctx.keys?.length ? ctx.keys.map((k) => ctx.columns.findIndex((c) => c.name === k)).filter((i) => i >= 0) : ctx.columns.map((_, i) => i);
  return idx
    .map((i) => {
      const col = quoteIdent(ctx.dbType, ctx.columns[i].name);
      const v = row[i];
      return v === null || v === undefined ? `${col} IS NULL` : `${col} = ${sqlLiteral(v, ctx.columns[i].type, ctx.dbType)}`;
    })
    .join(' AND ');
}

export function formatRows(format: CopyFormat, ctx: CopyContext): string {
  const table = ctx.table || 'table_name';
  const cols = ctx.columns.map((c) => quoteIdent(ctx.dbType, c.name));
  const lit = (r: unknown[], i: number) => sqlLiteral(r[i], ctx.columns[i].type, ctx.dbType);

  switch (format) {
    case 'tsv':
      return delimited(ctx, '\t', false);
    case 'tsv-headers':
      return delimited(ctx, '\t', true);
    case 'csv':
      return delimited(ctx, ',', true);
    case 'json':
      return json(ctx);
    case 'markdown':
      return markdown(ctx);
    case 'insert':
      return ctx.rows.map((r) => `INSERT INTO ${table} (${cols.join(', ')}) VALUES (${r.map((_, i) => lit(r, i)).join(', ')});`).join('\n');
    case 'update': {
      const keys = new Set(ctx.keys ?? []);
      return ctx.rows
        .map((r) => {
          const set = ctx.columns
            .map((c, i) => (keys.has(c.name) ? '' : `${cols[i]} = ${lit(r, i)}`))
            .filter(Boolean)
            .join(', ');
          return `UPDATE ${table} SET ${set} WHERE ${condition(ctx, r)};`;
        })
        .join('\n');
    }
    case 'where':
      return ctx.rows.length === 1 ? condition(ctx, ctx.rows[0]) : ctx.rows.map((r) => `(${condition(ctx, r)})`).join('\nOR ');
    case 'in-list': {
      const i = ctx.column ?? 0;
      const values = [...new Set(ctx.rows.map((r) => lit(r, i)))];
      return `${cols[i]} IN (${values.join(', ')})`;
    }
  }
}
