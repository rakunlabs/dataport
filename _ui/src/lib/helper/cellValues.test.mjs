import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fillOf, firstToFill, kind, quickValues, shortDefault } from './cellValues.ts';

test('types are sorted into kinds', () => {
  assert.equal(kind('timestamp with time zone'), 'timestamptz');
  assert.equal(kind('timestamp(6) with time zone'), 'timestamptz');
  assert.equal(kind('timestamp without time zone'), 'timestamp');
  assert.equal(kind('datetime2'), 'timestamp');
  assert.equal(kind('Nullable(DateTime64(3))'), 'timestamp');
  assert.equal(kind('date'), 'date');
  assert.equal(kind('time without time zone'), 'time');
  assert.equal(kind('uniqueidentifier'), 'uuid');
  assert.equal(kind('jsonb'), 'json');
  assert.equal(kind('character varying(20)'), 'text');
  assert.equal(kind('integer'), 'other');
  assert.equal(kind('timestamp[]'), 'other');
});

test('now is written for the type, in local time', () => {
  const at = new Date(2026, 10, 19, 9, 5, 7);
  assert.deepEqual(quickValues('date', at), [{ key: 'today', value: '2026-11-19' }]);
  assert.equal(quickValues('timestamp', at)[0].value, '2026-11-19 09:05:07');
  assert.match(String(quickValues('timestamptz', at)[0].value), /^2026-11-19 09:05:07[+-]\d\d:\d\d$/);
  assert.match(String(quickValues('uuid')[0].value), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  assert.deepEqual(quickValues('integer'), []);
});

test('defaults are shortened', () => {
  assert.equal(shortDefault("'draft'::character varying"), "'draft'");
  assert.equal(shortDefault('((0))'), '0');
  assert.equal(shortDefault('(getdate())'), 'getdate()');
  assert.equal(shortDefault('(a) + (b)'), '(a) + (b)');
  assert.equal(shortDefault('now()'), 'now()');
});

test('what a new row gets', () => {
  const col = (o) => ({ name: 'c', type: 'int', nullable: true, default: '', primary_key: false, ...o });
  assert.deepEqual(fillOf(col({ auto: 'identity' })), { kind: 'auto' });
  assert.deepEqual(fillOf(col({ default: 'now()' })), { kind: 'default', expr: 'now()' });
  assert.deepEqual(fillOf(col({})), { kind: 'null' });
  assert.deepEqual(fillOf(col({ nullable: false })), { kind: 'required' });
  const meta = [col({ name: 'id', auto: 'identity' }), col({ name: 'at', default: 'now()' }), col({ name: 'name', nullable: false })];
  assert.equal(firstToFill(['id', 'at', 'name'], meta), 2);
  assert.equal(firstToFill(['id', 'at'], meta), 1);
  assert.equal(firstToFill(['x'], meta), 0);
});
