// SQL for managing database users, shown for review before it runs, as DBX
// does. Passwords never reach the query log: the server masks them.

import { quoteIdent } from '@/lib/stores/workspace.svelte';
import type { DbUser } from '@/lib/api/types';

const str = (s: string) => `'${s.replaceAll("'", "''")}'`;
const mysqlish = (db: string) => db === 'mysql' || db === 'mariadb';

export function account(db: string, u: Pick<DbUser, 'name' | 'host'>): string {
  if (mysqlish(db)) return `${str(u.name)}@${str(u.host || '%')}`;
  return quoteIdent(db, u.name);
}

// The privileges offered in the grant dialog, per database.
export function privileges(db: string): string[] {
  if (mysqlish(db)) return ['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'CREATE', 'DROP', 'ALTER', 'INDEX', 'REFERENCES', 'EXECUTE', 'CREATE VIEW', 'SHOW VIEW', 'TRIGGER', 'ALL PRIVILEGES'];
  if (db === 'mssql') return ['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'EXECUTE', 'REFERENCES', 'ALTER', 'CONTROL'];
  if (db === 'oracle') return ['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'REFERENCES', 'ALTER', 'INDEX', 'EXECUTE'];
  if (db === 'clickhouse') return ['SELECT', 'INSERT', 'ALTER', 'CREATE', 'DROP', 'TRUNCATE', 'SHOW', 'ALL'];
  return ['SELECT', 'INSERT', 'UPDATE', 'DELETE', 'TRUNCATE', 'REFERENCES', 'TRIGGER', 'ALL PRIVILEGES'];
}

export function supportsUsers(db: string): boolean {
  return ['postgres', 'mysql', 'mariadb', 'mssql', 'oracle', 'clickhouse'].includes(db);
}

export function createUserSQL(db: string, name: string, password: string, host = '%'): string {
  const pw = str(password || 'change-me');
  if (mysqlish(db)) return `CREATE USER ${account(db, { name, host })} IDENTIFIED BY ${pw};`;
  if (db === 'mssql') return `CREATE LOGIN ${quoteIdent(db, name)} WITH PASSWORD = ${pw};\nCREATE USER ${quoteIdent(db, name)} FOR LOGIN ${quoteIdent(db, name)};`;
  if (db === 'oracle') return `CREATE USER ${quoteIdent(db, name)} IDENTIFIED BY "${(password || 'change-me').replaceAll('"', '')}";\nGRANT CREATE SESSION TO ${quoteIdent(db, name)};`;
  if (db === 'clickhouse') return `CREATE USER ${quoteIdent(db, name)} IDENTIFIED WITH sha256_password BY ${pw};`;
  return `CREATE ROLE ${quoteIdent(db, name)} WITH LOGIN PASSWORD ${pw};`;
}

export function passwordSQL(db: string, u: DbUser, password: string): string {
  const pw = str(password || 'change-me');
  if (mysqlish(db)) return `ALTER USER ${account(db, u)} IDENTIFIED BY ${pw};`;
  if (db === 'mssql') return `ALTER LOGIN ${quoteIdent(db, u.name)} WITH PASSWORD = ${pw};`;
  if (db === 'oracle') return `ALTER USER ${quoteIdent(db, u.name)} IDENTIFIED BY "${(password || 'change-me').replaceAll('"', '')}";`;
  if (db === 'clickhouse') return `ALTER USER ${quoteIdent(db, u.name)} IDENTIFIED WITH sha256_password BY ${pw};`;
  return `ALTER ROLE ${quoteIdent(db, u.name)} WITH PASSWORD ${pw};`;
}

export function lockSQL(db: string, u: DbUser, lock: boolean): string {
  if (mysqlish(db)) return `ALTER USER ${account(db, u)} ACCOUNT ${lock ? 'LOCK' : 'UNLOCK'};`;
  if (db === 'mssql') return `ALTER LOGIN ${quoteIdent(db, u.name)} ${lock ? 'DISABLE' : 'ENABLE'};`;
  if (db === 'oracle') return `ALTER USER ${quoteIdent(db, u.name)} ACCOUNT ${lock ? 'LOCK' : 'UNLOCK'};`;
  if (db === 'postgres') return `ALTER ROLE ${quoteIdent(db, u.name)} WITH ${lock ? 'NOLOGIN' : 'LOGIN'};`;
  return '';
}

export function dropUserSQL(db: string, u: DbUser): string {
  if (mysqlish(db)) return `DROP USER ${account(db, u)};`;
  if (db === 'mssql') return `DROP USER IF EXISTS ${quoteIdent(db, u.name)};\nDROP LOGIN ${quoteIdent(db, u.name)};`;
  if (db === 'oracle') return `DROP USER ${quoteIdent(db, u.name)} CASCADE;`;
  if (db === 'clickhouse') return `DROP USER ${quoteIdent(db, u.name)};`;
  return `DROP ROLE ${quoteIdent(db, u.name)};`;
}

// target is a schema or database name and an optional table; empty table
// means every table in it.
export function grantSQL(db: string, u: DbUser, privs: string[], schema: string, table: string, revoke: boolean): string {
  const list = privs.length ? privs.join(', ') : 'SELECT';
  const who = account(db, u);
  const verb = revoke ? 'REVOKE' : 'GRANT';
  const dir = revoke ? 'FROM' : 'TO';
  if (mysqlish(db) || db === 'clickhouse') {
    const on = `${schema ? quoteIdent(db, schema) : '*'}.${table ? quoteIdent(db, table) : '*'}`;
    return `${verb} ${list} ON ${on} ${dir} ${who};`;
  }
  if (db === 'mssql') {
    if (!table) return `${verb} ${list} ON SCHEMA::${quoteIdent(db, schema || 'dbo')} ${dir} ${who};`;
    return `${verb} ${list} ON ${quoteIdent(db, schema || 'dbo')}.${quoteIdent(db, table)} ${dir} ${who};`;
  }
  if (db === 'oracle') {
    if (!table) return `-- Oracle grants are per object:\n${verb} ${list} ON ${quoteIdent(db, schema)}.<table> ${dir} ${who};`;
    return `${verb} ${list} ON ${quoteIdent(db, schema)}.${quoteIdent(db, table)} ${dir} ${who};`;
  }
  const s = quoteIdent(db, schema || 'public');
  if (!table) {
    const usage = revoke ? '' : `GRANT USAGE ON SCHEMA ${s} TO ${who};\n`;
    return `${usage}${verb} ${list} ON ALL TABLES IN SCHEMA ${s} ${dir} ${who};`;
  }
  return `${verb} ${list} ON ${s}.${quoteIdent(db, table)} ${dir} ${who};`;
}
