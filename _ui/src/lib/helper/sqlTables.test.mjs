import { test } from 'node:test';
import assert from 'node:assert/strict';
import { qualifiers, tableRefs } from './sqlTables.ts';

test('tables with schemas and aliases', () => {
  assert.deepEqual(tableRefs('select * from public.users u join orders as o on o.uid = u.id where x'), [
    { schema: 'public', table: 'users', alias: 'u' },
    { schema: '', table: 'orders', alias: 'o' },
  ]);
});

test('from lists, quotes and stop words', () => {
  assert.deepEqual(tableRefs('SELECT 1 FROM a, "My Table" t, b WHERE 1'), [
    { schema: '', table: 'a', alias: '' },
    { schema: '', table: 'My Table', alias: 't' },
    { schema: '', table: 'b', alias: '' },
  ]);
  assert.deepEqual(tableRefs('update items set x = 1'), [{ schema: '', table: 'items', alias: '' }]);
  assert.deepEqual(tableRefs('insert into logs (a) values (1)'), [{ schema: '', table: 'logs', alias: '' }]);
});

test('strings and comments are skipped', () => {
  assert.deepEqual(tableRefs("select 'from x' -- from y\nfrom z"), [{ schema: '', table: 'z', alias: '' }]);
});

test('qualifiers', () => {
  assert.deepEqual(qualifiers('select u. from sales.orders u'), ['u', 'sales']);
});
