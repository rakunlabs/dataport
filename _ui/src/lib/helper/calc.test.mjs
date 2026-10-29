import { test } from 'node:test';
import assert from 'node:assert/strict';
import { calc, groupRows } from './calc.ts';
import { matches, paint } from './rowColors.ts';

test('calculator functions', () => {
  const v = [10, '20', null, 30, 30, 'x'];
  assert.equal(calc('count', v), 6);
  assert.equal(calc('distinct', v), 4);
  assert.equal(calc('nulls', v), 1);
  assert.equal(calc('sum', v), 90);
  assert.equal(calc('avg', v), 22.5);
  assert.equal(calc('median', v), 25);
  assert.equal(calc('min', v), 10);
  assert.equal(calc('max', v), 30);
  assert.equal(calc('mode', v), 30);
  assert.equal(calc('sum', [0.1, 0.2]), 0.3);
  assert.equal(calc('max', ['b', 'a', 'c'], false), 'c');
  assert.equal(calc('max', ['b', 'a'], true), null);
});

test('grouping counts and aggregates per group', () => {
  const rows = [['a', 1], ['b', 2], ['a', 3], [null, 4]];
  const g = groupRows(rows, [0], [{ fn: 'sum', col: 1 }]);
  assert.deepEqual(g.map((x) => [x.keys[0], x.count, x.values[0]]), [['a', 2, 4], ['b', 1, 2], [null, 1, 4]]);
});

test('row colour rules', () => {
  const r = (op, value, extra = {}) => ({ id: '1', column: 'amount', op, value, color: '#ff0000', enabled: true, ...extra });
  assert.ok(matches(r('=', '50'), 50));
  assert.ok(matches(r('=', '50'), '50.0'));
  assert.ok(!matches(r('=', '50'), null));
  assert.ok(matches(r('>', '9'), 10));
  assert.ok(matches(r('contains', 'AB'), 'xabx'));
  assert.ok(matches(r('regex', '^a\\d$'), 'A1'));
  assert.ok(matches(r('null', ''), null));
  assert.ok(matches(r('range', '1', { value2: '10' }), 5));
  const p = paint([r('=', '50'), r('range', '0', { value2: '100', color2: '#0000ff' })], ['id', 'amount'], (c) => (c === 1 ? 50 : 1));
  assert.ok(p.row.includes('#ff0000'));
  assert.ok(p.cells[1].includes('#800080'));
});
