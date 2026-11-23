import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readNdjson } from './ndjson.ts';

function stream(chunks) {
  const enc = new TextEncoder();
  return new ReadableStream({
    start(c) {
      for (const ch of chunks) c.enqueue(enc.encode(ch));
      c.close();
    },
  });
}

test('ndjson lines split across chunks, blank lines and a last line without newline', async () => {
  const got = [];
  await readNdjson(stream(['{"a":1}\n{"b"', ':"ş"}\n\n', '{"c":3}']), (v) => got.push(v));
  assert.deepEqual(got, [{ a: 1 }, { b: 'ş' }, { c: 3 }]);
});

test('a multi-byte character split between chunks', async () => {
  const bytes = new TextEncoder().encode('{"x":"€"}\n');
  const got = [];
  const body = new ReadableStream({
    start(c) {
      c.enqueue(bytes.slice(0, 7));
      c.enqueue(bytes.slice(7));
      c.close();
    },
  });
  await readNdjson(body, (v) => got.push(v));
  assert.deepEqual(got, [{ x: '€' }]);
});
