import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sourceTable } from './sourceTable.ts';

const pg = (sql) => sourceTable(sql, 'postgres');

test('plain reads of one table name it', () => {
  assert.deepEqual(pg('SELECT * FROM users'), { schema: '', table: 'users' });
  assert.deepEqual(pg('select id, name from Public.Users u where id > 3 order by id limit 10;'), { schema: 'public', table: 'users' });
  assert.deepEqual(pg('SELECT u.* FROM "Sales"."Order Items" AS u'), { schema: 'Sales', table: 'Order Items' });
  assert.deepEqual(pg('-- comment\nSELECT t.id, t.name FROM t /* x */ WHERE name = \'a; JOIN b\''), { schema: '', table: 't' });
  assert.deepEqual(sourceTable('SELECT TOP (50) * FROM dbo.[Orders]', 'mssql'), { schema: 'dbo', table: 'Orders' });
  assert.deepEqual(sourceTable('SELECT * FROM `shop`.`orders`', 'mysql'), { schema: 'shop', table: 'orders' });
  assert.deepEqual(sourceTable('SELECT * FROM emp', 'oracle'), { schema: '', table: 'EMP' });
});

test('reads that are not one table\'s rows give none', () => {
  for (const sql of [
    'SELECT * FROM a JOIN b ON a.id = b.id',
    'SELECT * FROM a, b',
    'SELECT count(*) FROM a',
    'SELECT id AS key FROM a',
    'SELECT id + 1 FROM a',
    'SELECT DISTINCT id FROM a',
    'SELECT id FROM a GROUP BY id',
    'SELECT * FROM a UNION SELECT * FROM b',
    'SELECT * FROM (SELECT * FROM a) x',
    'WITH x AS (SELECT 1) SELECT * FROM x',
    'SELECT * FROM generate_series(1, 3)',
    'SELECT 1',
    'UPDATE a SET x = 1',
    'SELECT * INTO b FROM a',
    'SELECT * FROM a; SELECT * FROM b',
  ]) assert.equal(pg(sql), null, sql);
});

test('text that could hide a second table gives none', () => {
  const my = (sql) => sourceTable(sql, 'mysql');
  assert.equal(pg("SELECT * FROM t WHERE j[']'] IS NULL UNION SELECT * FROM u WHERE j['['] IS NULL"), null);
  assert.equal(my("SELECT * FROM t WHERE a=1 # '\nUNION SELECT * FROM u -- '"), null);
  assert.equal(my('SELECT * FROM t WHERE id = 1--1 UNION SELECT * FROM u'), null);
  assert.equal(my('SELECT * FROM t /*!, u */'), null);
  assert.equal(my('SELECT * FROM t WHERE a = "\\"" UNION SELECT * FROM u WHERE b = "x" -- "'), null);
  assert.equal(my('SELECT "a" FROM t'), null);
  assert.equal(pg("SELECT * FROM t WHERE a = E'\\'' UNION SELECT * FROM u WHERE b='x' --'"), null);
  assert.equal(sourceTable("SELECT * FROM t WHERE a = q'[it's]'", 'oracle'), null);
  assert.equal(sourceTable("SELECT * FROM t FOR SYSTEM_TIME ALL", 'mssql'), null);
  assert.equal(pg('SELECT user FROM t'), null);
  assert.equal(pg('SELECT id, current_date FROM t'), null);
  assert.equal(pg('SELECT * FROM ONLY parent'), null);
  assert.equal(pg('SELECT * FROM t TABLESAMPLE SYSTEM (10)'), null);
  assert.equal(pg('SELECT * FROM t AS b(x, y)'), null);
  // Still found:
  assert.deepEqual(pg('SELECT * FROM t WHERE id = 1 FOR UPDATE'), { schema: '', table: 't' });
  assert.deepEqual(pg('SELECT t.user FROM t'), { schema: '', table: 't' });
  assert.deepEqual(sourceTable('SELECT [id] FROM [dbo].[t]', 'mssql'), { schema: 'dbo', table: 't' });
  assert.deepEqual(my("SELECT * FROM t WHERE a = 'it\\'s' -- note"), { schema: '', table: 't' });
});
