import { api } from '@/lib/api/client';
import type { SavedQuery } from '@/lib/api/types';
import { findTab, ws } from '@/lib/stores/workspace/state.svelte';
import { persist } from '@/lib/stores/workspace/persist';

export async function loadSaved() {
  ws.saved = await api.get<SavedQuery[]>('/saved');
}

export async function saveTab(id: string, name?: string) {
  const t = findTab(id);
  if (!t) return;
  const body = { connection_id: t.connectionId, name: name ?? t.title, sql: t.sql };
  if (t.savedId) {
    await api.put(`/saved/${t.savedId}`, body);
  } else {
    const created = await api.post<SavedQuery>('/saved', body);
    t.savedId = created.id;
  }
  t.title = body.name;
  t.dirty = false;
  persist();
  await loadSaved();
}

// renameTab gives a query tab a new title. A saved query takes the new name
// too, with its saved SQL, so unsaved edits in the tab stay unsaved.
export async function renameTab(id: string, name: string) {
  const t = findTab(id);
  name = name.trim();
  if (!t || !name || name === t.title) return;
  const saved = t.savedId ? ws.saved.find((q) => q.id === t.savedId) : undefined;
  if (saved) {
    await api.put(`/saved/${saved.id}`, { connection_id: saved.connection_id, name, sql: saved.sql });
  }
  t.title = name;
  persist();
  if (saved) await loadSaved();
}

export async function deleteSaved(id: string) {
  await api.del(`/saved/${id}`);
  for (const t of ws.tabs) if (t.savedId === id) t.savedId = '';
  await loadSaved();
}
