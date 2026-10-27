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

export async function deleteSaved(id: string) {
  await api.del(`/saved/${id}`);
  for (const t of ws.tabs) if (t.savedId === id) t.savedId = '';
  await loadSaved();
}
