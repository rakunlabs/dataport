import { test } from 'node:test';
import assert from 'node:assert/strict';
import { defaultListOptions, listItems, morphList, trimSpans } from './textMorph.ts';

test('a pasted column becomes an IN list', () => {
  assert.equal(morphList('ali\nveli\r\n  ayse  \n\n', defaultListOptions), "('ali', 'veli', 'ayse')");
});

test('items split at tabs and commas too, empty ones dropped', () => {
  assert.deepEqual(listItems('a\tb,,c\n', '\n\t,'), ['a', 'b', 'c']);
  assert.deepEqual(listItems('a b;c', ';'), ['a b', 'c']);
});

test('quotes inside items are doubled, or left', () => {
  assert.equal(morphList("o'neil\nx", defaultListOptions), "('o''neil', 'x')");
  assert.equal(morphList("o'neil", { ...defaultListOptions, escape: false }), "('o'neil')");
  assert.equal(morphList('a\nb', { ...defaultListOptions, quote: '"', join: ',', prefix: '', suffix: '' }), '"a","b"');
  assert.equal(morphList('1\n2', { ...defaultListOptions, quote: '' }), '(1, 2)');
});

test('unique keeps the first of each', () => {
  assert.equal(morphList('b\na\nb', { ...defaultListOptions, unique: true }), "('b', 'a')");
});

test('wrapping starts a line after the delimiter', () => {
  const out = morphList('aaaa\nbbbb\ncccc', { ...defaultListOptions, quote: '', prefix: '', suffix: '', wrap: 10 });
  assert.equal(out, 'aaaa, bbbb,\ncccc');
});

test('trimming a line', () => {
  assert.deepEqual(trimSpans('  a b\t', 'both'), [{ from: 0, to: 2 }, { from: 5, to: 6 }]);
  assert.deepEqual(trimSpans('  a  ', 'leading'), [{ from: 0, to: 2 }]);
  assert.deepEqual(trimSpans('  a  ', 'trailing'), [{ from: 3, to: 5 }]);
  assert.deepEqual(trimSpans(' \t ', 'leading'), [{ from: 0, to: 3 }]);
  assert.deepEqual(trimSpans('', 'both'), []);
  assert.deepEqual(trimSpans('a', 'both'), []);
});
