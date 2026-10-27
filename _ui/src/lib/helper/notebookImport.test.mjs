import { test } from 'node:test';
import assert from 'node:assert/strict';
import { importNotebook } from './notebookImport.ts';

const connections = [{ id: 'source', name: 'legacy-source' }, { id: 'target', name: 'legacy-target' }];
const saz = (mode) => ({ name: 'Saz migration', path: 'migration', content: { cells: [{ id: 'old', path: 'transfer', db_type: 'legacy-source', content: 'SELECT id FROM source', enabled: true, mode }] } });
const transfer = (map_type) => ({ enabled: true, name: 'transfer', db_type: 'legacy-target', table: 'public.target', batch: 100, map_type });

test('native JSON preserves timeout and normalizes optional collections', () => {
  const note = importNotebook({ document: { cells: [{ connection_id: 'source', sql: 'SELECT 1', timeout_sec: 30, dependencies: null }] } }, connections);
  assert.equal(note.document.cells[0].timeout_sec, 30);
  assert.deepEqual(note.document.cells[0].dependencies, []);
  assert.deepEqual(note.document.parameters, []);
});
test('Saz wrapper, connection names, destination mappings and unmapped columns', () => {
  const raw = saz(transfer({ enabled: true, destination: { id: { type: 'string', nullable: true } } }));
  const cell = importNotebook({ data: raw }, connections).document.cells[0];
  assert.equal(cell.connection_id, 'source'); assert.equal(cell.transfer.connection_id, 'target');
  assert.equal(cell.transfer.keep_unmapped, true); assert.equal(cell.transfer.schema, 'public'); assert.equal(cell.transfer.table, 'target');
  assert.deepEqual(cell.transfer.mappings[0], { source: 'id', target: 'id', type: 'string', nullable: true, template: '', encoding: '' });
});
test('never silently collapse source-scan and destination stages', () => {
  for (const destination of [{}, { id: { type: 'string' } }]) {
    assert.throws(() => importNotebook(saz(transfer({ enabled: true, column: { id: { type: 'number' } }, destination })), connections), /source scan conversions require manual migration/);
  }
});
test('unsupported encoding and skip-all-errors are rejected, not changed', () => {
  assert.throws(() => importNotebook(saz(transfer({ enabled: true, destination: { id: { encoding: { enabled: true, coding: 'UTF-16' } } } })), connections), /unsupported source encoding/);
  const raw = saz(transfer({ enabled: false })); raw.content.cells[0].mode.skip_error = { enabled: true, message: '' };
  assert.throws(() => importNotebook(raw, connections), /skip-all-errors/);
});
