import { untrack } from 'svelte';
import { api, qs } from '@/lib/api/client';
import type { ColumnInfo, ConnectionView, DbObject, Routine, TableStructure } from '@/lib/api/types';
import { withCredentials } from '@/lib/stores/credentials.svelte';
import { errorMessage } from '@/lib/stores/toast.svelte';
import { ws, type TreeNode } from '@/lib/stores/workspace/state.svelte';

export async function loadConnections() {
  try {
    ws.connections = await api.get<ConnectionView[]>('/connections');
    ws.connectionsError = '';
  } catch (e) {
    ws.connectionsError = errorMessage(e);
  } finally {
    ws.connectionsLoaded = true;
  }
}

// Reload forces a load; 'failed' retries a failed node but keeps a loaded one.
type Reload = boolean | 'failed';

// loadNode is called from effects: it reads the node untracked, so writing
// it does not rerun the caller. A failed node loads again only when forced,
// or with 'failed', which callers pass on an explicit open.
async function loadNode<T>(map: Record<string, TreeNode<T>>, key: string, fetcher: () => Promise<T[]>, force: Reload = false) {
  const node = untrack(() => map[key]);
  if (node && (node.loading || (force !== true && (node.loaded || (node.error && force !== 'failed'))))) return;
  map[key] = { loading: true, loaded: false, error: '', items: node?.items ?? [] };
  try {
    const items = await fetcher();
    map[key] = { loading: false, loaded: true, error: '', items };
  } catch (e) {
    map[key] = { loading: false, loaded: false, error: errorMessage(e), items: [] };
  }
}

export function loadSchemas(connId: string, force: Reload = false) {
  return loadNode(ws.schemas, connId, () => withCredentials(connId, () => api.get<string[]>(`/connections/${connId}/schemas`)), force);
}

export function loadObjects(connId: string, schema: string, force: Reload = false) {
  return loadNode(ws.objects, `${connId}|${schema}`, () =>
    withCredentials(connId, () => api.get<DbObject[]>(`/connections/${connId}/objects${qs({ schema })}`)), force);
}

export function loadRoutines(connId: string, schema: string, force: Reload = false) {
  return loadNode(ws.routines, `${connId}|${schema}`, () =>
    withCredentials(connId, () => api.get<Routine[]>(`/connections/${connId}/routines${qs({ schema })}`)), force);
}

export function loadDefinition(connId: string, schema: string, r: Routine): Promise<string> {
  return withCredentials(connId, () =>
    api.get<{ definition: string }>(`/connections/${connId}/definition${qs({ schema, name: r.name, kind: r.kind, signature: r.signature })}`),
  ).then((d) => d.definition);
}

export function loadColumns(connId: string, schema: string, table: string, force: Reload = false) {
  return loadNode(ws.columns, `${connId}|${schema}|${table}`, () =>
    withCredentials(connId, () => api.get<ColumnInfo[]>(`/connections/${connId}/columns${qs({ schema, table })}`)), force);
}

// Structures are cached per table until the tab asks for a refresh.
const structures = new Map<string, Promise<TableStructure>>();

export function loadStructure(connId: string, schema: string, table: string, force = false): Promise<TableStructure> {
  const key = `${connId}|${schema}|${table}`;
  let p = force ? undefined : structures.get(key);
  if (!p) {
    p = withCredentials(connId, () => api.get<TableStructure>(`/connections/${connId}/structure${qs({ schema, table })}`));
    p.catch(() => structures.delete(key));
    structures.set(key, p);
  }
  return p;
}

function forgetStructures(prefix: string) {
  for (const key of structures.keys()) if (key.startsWith(prefix)) structures.delete(key);
}

// refreshSchema reloads the objects of one schema.
export function refreshSchema(connId: string, schema: string) {
  const prefix = `${connId}|${schema}|`;
  forgetStructures(prefix);
  for (const key of Object.keys(ws.columns)) if (key.startsWith(prefix)) delete ws.columns[key];
  return Promise.all([loadObjects(connId, schema, true), loadRoutines(connId, schema, true)]);
}

export function refreshConnection(connId: string) {
  forgetStructures(connId + '|');
  for (const key of Object.keys(ws.objects)) if (key.startsWith(connId + '|')) delete ws.objects[key];
  for (const key of Object.keys(ws.columns)) if (key.startsWith(connId + '|')) delete ws.columns[key];
  for (const key of Object.keys(ws.routines)) if (key.startsWith(connId + '|')) delete ws.routines[key];
  return loadSchemas(connId, true);
}

export function toggle(key: string, value?: boolean) {
  ws.expanded[key] = value ?? !ws.expanded[key];
}

// setSelfReadOnly lets the user use a writable connection read-only, in
// the workspace, notebooks and MCP alike, or allow writes again.
export async function setSelfReadOnly(connId: string, readOnly: boolean) {
  await api.put(`/connections/${connId}/read-only`, { read_only: readOnly });
  await loadConnections();
}

// disconnect lets the server close the sessions it holds for the
// connection and folds its tree, so nothing stays connected.
export async function disconnect(connId: string) {
  await api.post(`/connections/${connId}/disconnect`);
  ws.expanded[connId] = false;
  for (const key of Object.keys(ws.expanded)) if (key.startsWith(connId + '|')) delete ws.expanded[key];
  forgetStructures(connId + '|');
  for (const m of [ws.objects, ws.columns, ws.routines] as Record<string, unknown>[]) {
    for (const key of Object.keys(m)) if (key.startsWith(connId + '|')) delete m[key];
  }
  delete ws.schemas[connId];
}
