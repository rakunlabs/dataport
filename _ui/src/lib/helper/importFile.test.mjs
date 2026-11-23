import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fromJSON, guessType } from './importFile.ts';

test('JSON import keeps large integers as written', () => {
  const { rows } = fromJSON('[{"id":12345678901234567890,"n":1.5,"small":7}]');
  assert.deepEqual(rows, [['12345678901234567890', 1.5, 7]]);
});

test('guessType: leading zeros are text, plain integers numbers', () => {
  assert.equal(guessType(['00501', '12']), 'text');
  assert.equal(guessType(['0', '12', '-3']), 'bigint');
  assert.equal(guessType([7, 12]), 'bigint');
});

test('guessType: an integer past 18 digits is a decimal, not text', () => {
  assert.equal(guessType(['12345678901234567890']), 'decimal');
});
