// The databases dataport can connect to, as the connection picker shows them.
// A server reports which of them its build supports; the rest show as
// unavailable with the reason.

export type DbCategory = 'relational' | 'analytics' | 'embedded' | 'generic';

export interface DbKind {
  type: string;
  label: string;
  category: DbCategory;
  // Shown under the name in the picker.
  hint: string;
  // Extra words the picker search matches, such as compatible products.
  aliases: string[];
  // Products that speak this protocol, listed under the name.
  compatible?: string;
}

export const categories: { key: DbCategory; labelKey: string }[] = [
  { key: 'relational', labelKey: 'picker.relational' },
  { key: 'analytics', labelKey: 'picker.analytics' },
  { key: 'embedded', labelKey: 'picker.embedded' },
  { key: 'generic', labelKey: 'picker.generic' },
];

export const kinds: DbKind[] = [
  {
    type: 'postgres',
    label: 'PostgreSQL',
    category: 'relational',
    hint: 'picker.hint.postgres',
    aliases: ['pg', 'postgresql', 'cockroachdb', 'yugabyte', 'timescale', 'neon', 'supabase', 'redshift', 'aurora'],
    compatible: 'CockroachDB, YugabyteDB, TimescaleDB, Neon, Supabase',
  },
  {
    type: 'mysql',
    label: 'MySQL',
    category: 'relational',
    hint: 'picker.hint.mysql',
    aliases: ['tidb', 'planetscale', 'vitess', 'singlestore', 'doris', 'starrocks', 'aurora'],
    compatible: 'TiDB, PlanetScale, Vitess, Doris, StarRocks',
  },
  { type: 'mariadb', label: 'MariaDB', category: 'relational', hint: 'picker.hint.mariadb', aliases: ['maria'] },
  { type: 'mssql', label: 'SQL Server', category: 'relational', hint: 'picker.hint.mssql', aliases: ['sqlserver', 'azure sql', 'microsoft', 'tsql'] },
  { type: 'oracle', label: 'Oracle', category: 'relational', hint: 'picker.hint.oracle', aliases: ['plsql', 'ora', 'autonomous'] },
  { type: 'clickhouse', label: 'ClickHouse', category: 'analytics', hint: 'picker.hint.clickhouse', aliases: ['ch', 'olap'] },
  { type: 'trino', label: 'Trino', category: 'analytics', hint: 'picker.hint.trino', aliases: ['presto', 'starburst', 'athena', 'hive', 'iceberg'], compatible: 'Presto, Starburst' },
  { type: 'duckdb', label: 'DuckDB', category: 'embedded', hint: 'picker.hint.duckdb', aliases: ['duck', 'parquet', 'olap'] },
  { type: 'sqlite', label: 'SQLite', category: 'embedded', hint: 'picker.hint.sqlite', aliases: ['sqlite3', 'lite'] },
  {
    type: 'odbc',
    label: 'ODBC',
    category: 'generic',
    hint: 'picker.hint.odbc',
    aliases: ['ingres', 'db2', 'informix', 'teradata', 'hana', 'sybase', 'progress', 'vertica', 'netezza', 'dsn'],
    compatible: 'Ingres, DB2, Informix, Teradata, SAP HANA',
  },
];

export function kind(type: string): DbKind | undefined {
  return kinds.find((k) => k.type === type);
}

export function dbLabel(type: string): string {
  return kind(type)?.label ?? type;
}

export function matches(k: DbKind, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return [k.type, k.label, ...k.aliases].some((v) => v.toLowerCase().includes(q));
}
