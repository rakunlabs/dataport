// Virtual keys: columns the user chose to find the rows of a table that has
// no primary key, as DBeaver offers. Kept per table in this browser.

const key = 'dataport.virtualKeys';

function load(): Record<string, string[]> {
  try {
    const v = JSON.parse(localStorage.getItem(key) ?? '{}');
    return v && typeof v === 'object' ? v : {};
  } catch {
    return {};
  }
}

export const virtualKeys = $state<{ keys: Record<string, string[]> }>({ keys: load() });

function save() {
  try {
    localStorage.setItem(key, JSON.stringify(virtualKeys.keys));
  } catch {
    // The key then lasts for this page only.
  }
}

function scope(connId: string, schema: string, table: string) {
  return `${connId}|${schema}|${table}`;
}

// virtualKey returns the chosen columns. When the table lost one of them,
// the rest may no longer tell rows apart, so there is no key until the user
// chooses again.
export function virtualKey(connId: string, schema: string, table: string, columns?: string[]): string[] {
  const cols = virtualKeys.keys[scope(connId, schema, table)] ?? [];
  return columns && !cols.every((c) => columns.includes(c)) ? [] : cols;
}

export function setVirtualKey(connId: string, schema: string, table: string, columns: string[]) {
  const s = scope(connId, schema, table);
  if (columns.length) virtualKeys.keys[s] = [...columns];
  else delete virtualKeys.keys[s];
  save();
}

// Values of these types cannot be compared with = (JSON in PostgreSQL, LOBs
// in Oracle, text and image in SQL Server, spatial types) or do not come
// back as written (floating point), so they cannot find a row.
const incomparable = /json|lob\b|xml|geometry|geography|hierarchyid|sql_variant|\[\]|^array|^map\(|^tuple\(|^struct|^(float|real|double|binary_float|binary_double)|^long(\s+raw)?$/i;
const mssqlIncomparable = /^(n?text|image)$/i;

export function comparable(type: string, dbType: string): boolean {
  // ClickHouse wraps types: Nullable(Float64), LowCardinality(String).
  const ty = type.trim().replace(/^(?:Nullable|LowCardinality)\((.*)\)$/i, '$1').replace(/^(?:Nullable|LowCardinality)\((.*)\)$/i, '$1');
  if (dbType === 'mssql' && mssqlIncomparable.test(ty)) return false;
  return !incomparable.test(ty);
}
