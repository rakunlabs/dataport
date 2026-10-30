import { test } from 'node:test';
import assert from 'node:assert/strict';
import { statements } from './statements.ts';

// Builds many random but valid single statements, one clause per line, and
// checks that none is cut, even with a blank line between every two lines.
// A cut statement would run as two broken ones, so this is the guarantee
// that blank lines only ever separate whole statements.

let seed = 12345;
const rnd = (n) => {
  seed = (seed * 1103515245 + 12345) & 0x7fffffff;
  return seed % n;
};
const pick = (a) => a[rnd(a.length)];

// Column names that are also keywords that may start a statement.
const cols = ['id', 'name', 'type', 'value', 'set', 'update', 'delete', 'comment', 'select', 'with', 'replace', 'truncate', 'grant', 'show', 'begin', 'commit', 'declare', 'call', 'use', 'drop'];
const col = () => (rnd(3) === 0 ? `"${pick(cols)}"` : pick(cols.slice(0, 4)));
const table = () => pick(['orders', 'items', 'users', 'public.events', '"select"']);
const value = () => pick(['1', "'a'", 'now()', 'null', `${col()} + 1`, `replace(${col()}, 'a', 'b')`, `coalesce(${col()}, 0)`]);

function cond(depth) {
  const parts = [`${col()} = ${value()}`, `${col()} in (1, 2)`, `${col()} is not null`];
  if (depth < 2) parts.push(`exists (\n${select(depth + 1)}\n)`, `${col()} in (\n${select(depth + 1)}\n)`);
  return pick(parts);
}

function where(depth) {
  const lines = [`where ${cond(depth)}`];
  for (let i = rnd(3); i > 0; i--) lines.push(`  ${pick(['and', 'or'])} ${cond(depth)}`);
  return lines.join('\n');
}

function select(depth = 0) {
  const lines = ['select'];
  const n = 1 + rnd(4);
  for (let i = 0; i < n; i++) lines.push(`  ${pick([col(), value(), `case\n    when ${cond(2)} then 1\n    else 0\n  end`])}${i < n - 1 ? ',' : ''}`);
  lines.push(`from ${table()}`);
  if (rnd(2)) lines.push(`join ${table()} x\n  on x.id = ${col()}`);
  if (rnd(2)) lines.push(where(depth));
  if (rnd(3) === 0) lines.push(`group by ${col()}\nhaving count(*) > 1`);
  if (depth === 0 && rnd(3) === 0) lines.push(`union${rnd(2) ? ' all' : ''}\n${select(1)}`);
  if (depth === 0 && rnd(2)) lines.push(`order by ${col()}${rnd(2) ? ' desc' : ''}`);
  return lines.join('\n');
}

function statement() {
  switch (rnd(9)) {
    case 0:
      return select();
    case 1:
      return `with a as (\n${select(1)}\n),\nb as (\n${select(1)}\n)\n${select()}`;
    case 2:
      return `update ${table()}\nset ${col()} = ${value()},\n  ${col()} = ${value()}\n${where(0)}${rnd(2) ? '\nreturning *' : ''}`;
    case 3:
      return `delete from ${table()}\n${where(0)}`;
    case 4:
      return `insert into ${table()} (${col()}, ${col()})\n${rnd(2) ? `values\n  (1, 2),\n  (3, 4)` : select()}${rnd(2) ? `\non conflict (id) do update\nset ${col()} = excluded.${col()}` : ''}`;
    case 5:
      return `create ${pick(['', 'or replace '])}view v as\n${rnd(2) ? `with a as (\n${select(1)}\n)\n` : ''}${select()}`;
    case 6:
      return `create table t as\n${select()}`;
    case 7:
      return `explain ${pick(['', 'analyze ', '(format json)\n'])}\n${select()}`;
    default:
      return `merge into ${table()} t\nusing ${table()} s\non t.id = s.id\nwhen matched then\n  update set ${col()} = 1\nwhen not matched then\n  insert (id) values (1)`;
  }
}

test('random single statements are never cut, with or without blank lines', () => {
  for (let i = 0; i < 3000; i++) {
    const one = statement();
    for (const sql of [one, one.replace(/\n/g, '\n\n'), one.replace(/\n/g, '\n  \n')]) {
      const got = statements(sql, pick(['postgres', 'mysql', 'sqlite', 'duckdb', 'trino', 'clickhouse'])).map((s) => s.text);
      assert.equal(got.length, 1, `cut into ${got.length}:\n${got.join('\n----\n')}`);
    }
  }
});

test('random statements after one another are found one by one', () => {
  for (let i = 0; i < 500; i++) {
    const list = Array.from({ length: 2 + rnd(3) }, statement);
    const got = statements(list.join('\n\n'), 'postgres').map((s) => s.text);
    // Each statement may be one or more, never fewer than written: blank
    // lines only ever add splits between whole statements here.
    assert.equal(got.length, list.length, `got ${got.length} for ${list.length}:\n${got.join('\n----\n')}`);
  }
});
