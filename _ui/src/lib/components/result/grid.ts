export function display(v: unknown): string {
  if (v === null || v === undefined) return 'NULL';
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

export function isJsonType(type: string) {
  return /json/i.test(type);
}

export function isNumeric(type: string) {
  if (type.endsWith('[]')) return false;
  return /^(u?int|integer|bigint|smallint|tinyint|mediumint|numeric|decimal|real|double|float|serial|bigserial|money|int2|int4|int8|float4|float8|nullable\((u?int|decimal|float))/i.test(type);
}

// numericColumns tells which columns hold numbers. A column without a type
// (an expression in SQLite, for one) counts when its first values are all
// JSON numbers.
export function numericColumns(columns: { type: string }[], rows: unknown[][]): boolean[] {
  return columns.map((col, c) => {
    if (col.type) return isNumeric(col.type);
    let seen = false;
    for (let r = 0; r < rows.length && r < 200; r++) {
      const v = rows[r][c];
      if (v === null || v === undefined) continue;
      if (typeof v !== 'number') return false;
      seen = true;
    }
    return seen;
  });
}

export function typeColor(type: string): string {
  const ty = type.toLowerCase();
  if (ty.endsWith('[]')) return 'var(--color-type-struct)';
  if (/(int|serial)/.test(ty)) return 'var(--color-type-int)';
  if (/(numeric|decimal|real|double|float|money)/.test(ty)) return 'var(--color-type-num)';
  if (/(bool|bit)/.test(ty)) return 'var(--color-type-bool)';
  if (/(date|time)/.test(ty)) return 'var(--color-type-time)';
  if (/(json|xml|array|map|tuple)/.test(ty)) return 'var(--color-type-struct)';
  if (/(uuid|uniqueidentifier)/.test(ty)) return 'var(--color-type-id)';
  if (/(bytea|blob|binary)/.test(ty)) return 'var(--color-type-bin)';
  return 'var(--color-type-text)';
}
