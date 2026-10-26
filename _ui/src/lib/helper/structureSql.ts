// The structure editor turns a draft of a table's columns, indexes and
// foreign keys into ALTER statements for its database, as DBX does. The
// SQL is shown before anything runs, so each database's spelling matters
// less than that the user sees exactly what will happen.

import { qualified, quoteIdent } from '@/lib/stores/workspace.svelte';
import type { ColumnInfo, ForeignKey, IndexInfo } from '@/lib/api/types';
import { stringLiteral } from '@/lib/helper/copy';

export interface ColumnDraft {
  // The column as loaded; empty for a new one.
  original: string;
  name: string;
  type: string;
  nullable: boolean;
  default: string;
  primary: boolean;
  comment: string;
  dropped: boolean;
  // Kept as loaded, so restating a MySQL column keeps AUTO_INCREMENT.
  extra?: string;
}

export interface IndexDraft {
  original: string;
  name: string;
  columns: string[];
  unique: boolean;
  dropped: boolean;
  // Loaded indexes are not edited in place: a change drops and recreates.
  changed: boolean;
}

export interface FkDraft {
  original: string;
  name: string;
  columns: string[];
  refSchema: string;
  refTable: string;
  refColumns: string[];
  onDelete: string;
  onUpdate: string;
  dropped: boolean;
  changed: boolean;
}

export interface Draft {
  comment: string;
  columns: ColumnDraft[];
  indexes: IndexDraft[];
  fks: FkDraft[];
}

export interface Loaded {
  comment: string;
  comments: Record<string, string>;
  columns: ColumnInfo[];
  indexes: IndexInfo[];
  fks: ForeignKey[];
}

export function draftOf(l: Loaded): Draft {
  return {
    comment: l.comment,
    columns: l.columns.map((c) => ({
      original: c.name,
      name: c.name,
      type: c.type,
      nullable: c.nullable,
      default: c.default,
      primary: c.primary_key,
      extra: c.extra ?? '',
      comment: l.comments[c.name] ?? '',
      dropped: false,
    })),
    indexes: l.indexes
      .filter((i) => !i.primary)
      .map((i) => ({ original: i.name, name: i.name, columns: [...i.columns], unique: i.unique, dropped: false, changed: false })),
    fks: l.fks.map((f) => ({
      original: f.name,
      name: f.name,
      columns: [...f.columns],
      refSchema: f.ref_schema,
      refTable: f.ref_table,
      refColumns: [...f.ref_columns],
      onDelete: f.on_delete ?? '',
      onUpdate: f.on_update ?? '',
      dropped: false,
      changed: false,
    })),
  };
}

export function newColumn(n: number): ColumnDraft {
  return { original: '', name: `column_${n}`, type: 'text', nullable: true, default: '', primary: false, comment: '', dropped: false };
}

// Type names offered in the editor, per database; any other may be typed.
export function typeSuggestions(db: string): string[] {
  switch (db) {
    case 'mysql':
    case 'mariadb':
      return ['int', 'bigint', 'smallint', 'tinyint(1)', 'decimal(10,2)', 'double', 'varchar(255)', 'char(36)', 'text', 'longtext', 'json', 'date', 'datetime', 'timestamp', 'time', 'blob', 'boolean'];
    case 'mssql':
      return ['int', 'bigint', 'smallint', 'bit', 'decimal(10,2)', 'float', 'nvarchar(255)', 'nvarchar(max)', 'varchar(255)', 'uniqueidentifier', 'date', 'datetime2', 'datetimeoffset', 'time', 'varbinary(max)'];
    case 'oracle':
      return ['NUMBER(10)', 'NUMBER(19)', 'NUMBER(10,2)', 'VARCHAR2(255 CHAR)', 'CLOB', 'DATE', 'TIMESTAMP', 'TIMESTAMP WITH TIME ZONE', 'BLOB', 'BINARY_DOUBLE'];
    case 'sqlite':
      return ['INTEGER', 'REAL', 'TEXT', 'NUMERIC', 'BLOB'];
    case 'clickhouse':
      return ['Int32', 'Int64', 'UInt64', 'Float64', 'Decimal(18,2)', 'String', 'Bool', 'Date', 'DateTime', 'DateTime64(3)', 'UUID', 'Nullable(String)'];
    case 'duckdb':
      return ['INTEGER', 'BIGINT', 'DOUBLE', 'DECIMAL(10,2)', 'VARCHAR', 'BOOLEAN', 'DATE', 'TIMESTAMP', 'TIMESTAMPTZ', 'UUID', 'JSON', 'BLOB'];
  }
  return ['integer', 'bigint', 'smallint', 'numeric(10,2)', 'double precision', 'text', 'varchar(255)', 'boolean', 'uuid', 'jsonb', 'json', 'date', 'timestamp', 'timestamp with time zone', 'time', 'bytea', 'serial', 'bigserial'];
}

export const fkActions = ['', 'CASCADE', 'SET NULL', 'SET DEFAULT', 'RESTRICT', 'NO ACTION'];


const mysqlish = (db: string) => db === 'mysql' || db === 'mariadb';

// columnDef is a column's definition as CREATE and ADD COLUMN take it.
function columnDef(db: string, c: ColumnDraft): string {
  const q = (s: string) => quoteIdent(db, s);
  let out = `${q(c.name)} ${c.type.trim()}`;
  if (!c.nullable) out += ' NOT NULL';
  else if (db === 'mssql') out += ' NULL';
  if (c.default.trim()) out += ` DEFAULT ${c.default.trim()}`;
  if (mysqlish(db) && c.extra) out += ` ${c.extra}`;
  if (mysqlish(db) && c.comment) out += ` COMMENT ${stringLiteral(db, c.comment)}`;
  if (db === 'clickhouse' && c.comment) out += ` COMMENT ${stringLiteral(db, c.comment)}`;
  return out;
}

// Supported tells what the editor may change for a database; the rest is
// read only and points to the SQL editor.
export function supported(db: string) {
  return {
    columns: !['trino', 'odbc'].includes(db),
    // SQLite can only add, rename and drop columns.
    alterColumn: !['sqlite', 'trino', 'odbc'].includes(db),
    indexes: !['trino', 'odbc', 'clickhouse'].includes(db),
    fks: !['trino', 'odbc', 'clickhouse', 'sqlite', 'duckdb'].includes(db),
    comments: ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'clickhouse'].includes(db),
  };
}

// alterSQL lists the statements that turn the loaded structure into the
// draft. Problems the database would refuse anyway are reported as notes.
export function alterSQL(db: string, schema: string, table: string, loaded: Loaded, d: Draft): { statements: string[]; notes: string[] } {
  const T = qualified(db, schema, table);
  const q = (s: string) => quoteIdent(db, s);
  const out: string[] = [];
  const notes: string[] = [];
  const byName = new Map(loaded.columns.map((c) => [c.name, c]));
  const can = supported(db);

  // Foreign keys and indexes that go are dropped first, so columns they use
  // can change; new ones come last.
  for (const f of d.fks) {
    if (!f.original || !(f.dropped || f.changed)) continue;
    out.push(mysqlish(db) ? `ALTER TABLE ${T} DROP FOREIGN KEY ${q(f.original)};` : `ALTER TABLE ${T} DROP CONSTRAINT ${q(f.original)};`);
  }
  for (const i of d.indexes) {
    if (!i.original || !(i.dropped || i.changed)) continue;
    if (mysqlish(db) || db === 'mssql') out.push(`DROP INDEX ${q(i.original)} ON ${T};`);
    else out.push(`DROP INDEX ${schema && db !== 'sqlite' ? qualified(db, schema, i.original) : q(i.original)};`);
  }

  // Primary key changes rebuild the key.
  const oldPk = loaded.columns.filter((c) => c.primary_key).map((c) => c.name);
  const newPk = d.columns.filter((c) => !c.dropped && c.primary).map((c) => c.name);
  const pkChanged = oldPk.join('\u0000') !== d.columns.filter((c) => !c.dropped && c.primary && c.original).map((c) => c.original).join('\u0000') || newPk.length !== oldPk.length;
  if (pkChanged && oldPk.length && db !== 'sqlite') {
    const pkName = loaded.indexes.find((i) => i.primary)?.name;
    if (mysqlish(db)) out.push(`ALTER TABLE ${T} DROP PRIMARY KEY;`);
    else if (pkName) out.push(`ALTER TABLE ${T} DROP CONSTRAINT ${q(pkName)};`);
    else notes.push('pkName');
  }

  for (const c of d.columns) {
    const old = c.original ? byName.get(c.original) : undefined;
    if (c.dropped) {
      if (old) out.push(`ALTER TABLE ${T} DROP COLUMN ${q(old.name)};`);
      continue;
    }
    if (!old) {
      const add = db === 'mssql' || db === 'oracle' ? 'ADD' : 'ADD COLUMN';
      out.push(`ALTER TABLE ${T} ${add} ${db === 'oracle' ? '(' + columnDef(db, c) + ')' : columnDef(db, c)};`);
      if (!mysqlish(db) && db !== 'clickhouse' && c.comment) out.push(commentSQL(db, schema, table, c.name, c.comment));
      continue;
    }

    let name = old.name;
    if (c.name !== old.name) {
      if (db === 'mssql') out.push(`EXEC sp_rename ${stringLiteral(db, `${schema ? quoteIdent(db, schema) + '.' : ''}${quoteIdent(db, table)}.${quoteIdent(db, old.name)}`)}, ${stringLiteral(db, c.name)}, 'COLUMN';`);
      else out.push(`ALTER TABLE ${T} RENAME COLUMN ${q(old.name)} TO ${q(c.name)};`);
      name = c.name;
    }

    const typeChanged = c.type.trim() !== old.type;
    const nullChanged = c.nullable !== old.nullable;
    const defChanged = c.default.trim() !== old.default;
    const commentChanged = c.comment !== (loaded.comments[old.name] ?? '');

    if ((typeChanged || nullChanged || defChanged) && !can.alterColumn) {
      notes.push('sqliteAlter');
      continue;
    }

    if (mysqlish(db)) {
      // MySQL restates the whole column.
      if (typeChanged || nullChanged || defChanged || commentChanged) out.push(`ALTER TABLE ${T} MODIFY COLUMN ${columnDef(db, { ...c, name })};`);
      continue;
    }

    if (db === 'mssql') {
      if (typeChanged || nullChanged) out.push(`ALTER TABLE ${T} ALTER COLUMN ${q(name)} ${c.type.trim()}${c.nullable ? ' NULL' : ' NOT NULL'};`);
      if (defChanged) {
        if (old.default) notes.push('mssqlDefault');
        if (c.default.trim()) out.push(`ALTER TABLE ${T} ADD DEFAULT ${c.default.trim()} FOR ${q(name)};`);
      }
    } else if (db === 'oracle') {
      const parts: string[] = [];
      if (typeChanged) parts.push(c.type.trim());
      if (defChanged) parts.push(`DEFAULT ${c.default.trim() || 'NULL'}`);
      if (nullChanged) parts.push(c.nullable ? 'NULL' : 'NOT NULL');
      if (parts.length) out.push(`ALTER TABLE ${T} MODIFY (${q(name)} ${parts.join(' ')});`);
    } else if (db === 'clickhouse') {
      if (typeChanged || nullChanged || defChanged || commentChanged) {
        let type = c.type.trim();
        if (nullChanged && c.nullable && !/^Nullable\(/i.test(type)) type = `Nullable(${type})`;
        if (nullChanged && !c.nullable) type = type.replace(/^Nullable\((.*)\)$/i, '$1');
        out.push(`ALTER TABLE ${T} MODIFY COLUMN ${q(name)} ${type}${c.default.trim() ? ` DEFAULT ${c.default.trim()}` : ''}${c.comment ? ` COMMENT ${stringLiteral(db, c.comment)}` : ''};`);
      }
      continue;
    } else {
      if (typeChanged) out.push(`ALTER TABLE ${T} ALTER COLUMN ${q(name)} TYPE ${c.type.trim()}${db === 'postgres' ? ` USING ${q(name)}::${c.type.trim()}` : ''};`);
      if (nullChanged) out.push(`ALTER TABLE ${T} ALTER COLUMN ${q(name)} ${c.nullable ? 'DROP NOT NULL' : 'SET NOT NULL'};`);
      if (defChanged) out.push(c.default.trim() ? `ALTER TABLE ${T} ALTER COLUMN ${q(name)} SET DEFAULT ${c.default.trim()};` : `ALTER TABLE ${T} ALTER COLUMN ${q(name)} DROP DEFAULT;`);
    }
    if (commentChanged) out.push(commentSQL(db, schema, table, name, c.comment));
  }

  if (pkChanged && newPk.length && db !== 'sqlite' && db !== 'clickhouse') {
    out.push(`ALTER TABLE ${T} ADD PRIMARY KEY (${newPk.map(q).join(', ')});`);
  }
  if (pkChanged && (db === 'sqlite' || db === 'clickhouse')) notes.push('pkRebuild');

  if (d.comment !== loaded.comment && can.comments) out.push(commentSQL(db, schema, table, '', d.comment));

  for (const i of d.indexes) {
    if (i.dropped || (i.original && !i.changed) || !i.columns.length) continue;
    const name = i.name.trim() || `${table}_${i.columns.join('_')}_idx`;
    out.push(`CREATE ${i.unique ? 'UNIQUE ' : ''}INDEX ${q(name)} ON ${T} (${i.columns.map(q).join(', ')});`);
  }

  for (const f of d.fks) {
    if (f.dropped || (f.original && !f.changed) || !f.columns.length || !f.refTable) continue;
    const name = f.name.trim() || `${table}_${f.columns.join('_')}_fkey`;
    const ref = qualified(db, f.refSchema || schema, f.refTable);
    let s = `ALTER TABLE ${T} ADD CONSTRAINT ${q(name)} FOREIGN KEY (${f.columns.map(q).join(', ')}) REFERENCES ${ref} (${f.refColumns.map(q).join(', ')})`;
    if (f.onDelete) s += ` ON DELETE ${f.onDelete}`;
    if (f.onUpdate && db !== 'oracle') s += ` ON UPDATE ${f.onUpdate}`;
    out.push(s + ';');
  }

  return { statements: out, notes: [...new Set(notes)] };
}

function commentSQL(db: string, schema: string, table: string, column: string, text: string): string {
  const T = qualified(db, schema, table);
  const q = (s: string) => quoteIdent(db, s);
  if (mysqlish(db)) return `ALTER TABLE ${T} COMMENT = ${stringLiteral(db, text)};`;
  if (db === 'clickhouse') return column ? `ALTER TABLE ${T} COMMENT COLUMN ${q(column)} ${stringLiteral(db, text)};` : `ALTER TABLE ${T} MODIFY COMMENT ${stringLiteral(db, text)};`;
  if (db === 'mssql') {
    const s = stringLiteral(db, schema || 'dbo');
    const lvl2 = column ? `, 'COLUMN', ${stringLiteral(db, column)}` : '';
    return `EXEC sys.sp_addextendedproperty 'MS_Description', ${stringLiteral(db, text)}, 'SCHEMA', ${s}, 'TABLE', ${stringLiteral(db, table)}${lvl2};`;
  }
  const target = column ? `COLUMN ${T}.${q(column)}` : `TABLE ${T}`;
  return `COMMENT ON ${target} IS ${text ? stringLiteral(db, text) : 'NULL'};`;
}
