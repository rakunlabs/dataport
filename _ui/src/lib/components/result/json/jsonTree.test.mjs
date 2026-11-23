import { test } from 'node:test';
import assert from 'node:assert/strict';
import { formatJson } from './jsonTree.ts';

test('formatJson keeps big numbers and strings as written', () => {
  const src = '{"id":12345678901234567890,"s":"a, b: {c}","esc":"q\\"x","n":[1.50, -2e5],"e":{},"l":[ ]}';
  assert.equal(
    formatJson(src),
    '{\n  "id": 12345678901234567890,\n  "s": "a, b: {c}",\n  "esc": "q\\"x",\n  "n": [\n    1.50,\n    -2e5\n  ],\n  "e": {},\n  "l": []\n}',
  );
  assert.equal(formatJson(formatJson(src), 0), '{"id":12345678901234567890,"s":"a, b: {c}","esc":"q\\"x","n":[1.50,-2e5],"e":{},"l":[]}');
  assert.equal(formatJson('not json'), undefined);
  assert.equal(formatJson('"text"'), '"text"');
});
