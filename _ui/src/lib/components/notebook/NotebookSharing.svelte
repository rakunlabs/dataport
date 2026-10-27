<script lang="ts">
  import { onMount } from 'svelte';
  import { Trash2 } from 'lucide-svelte';
  import * as nb from '@/lib/api/notebooks';
  import type { Notebook, NotebookAccess, NotebookSubject } from '@/lib/api/notebooks';
  import { t } from '@/lib/i18n/index.svelte';
  import { errorMessage } from '@/lib/stores/toast.svelte';

  let { note, onerror }: { note: Notebook; onerror: (msg: string) => void } = $props();

  let access = $state<NotebookAccess[]>([]);
  let subjects = $state<NotebookSubject[]>([]);
  let shareSubject = $state('');
  let sharePermission = $state('run');

  onMount(() => {
    void loadSharing();
  });

  async function loadSharing() {
    if (!note.id) return;
    try { [access, subjects] = await Promise.all([nb.getNotebookAccess(note.id), nb.getNotebookSubjects(note.id)]); }
    catch (e) { onerror(errorMessage(e)); }
  }
  async function saveAccess(next: NotebookAccess[]) {
    try { access = await nb.setNotebookAccess(note.id, next); } catch (e) { onerror(errorMessage(e)); }
  }
</script>

<div class="max-w-3xl space-y-4 p-4"><p class="text-xs text-muted">{t('notebook.accessHint')}</p>
  {#each access as a}<div class="flex items-center gap-3 border-b border-line-soft py-2 text-sm"><span class="flex-1">{subjects.find((s) => s.id === a.subject_id && s.type === a.subject_type)?.name || a.subject_id}</span><span class="chip">{a.permission}</span><button class="icon-btn" onclick={() => saveAccess(access.filter((x) => x !== a))} title={t('common.delete')} aria-label={t('common.delete')}><Trash2 size={13} /></button></div>{/each}
  <div class="flex flex-wrap gap-2"><select class="input" bind:value={shareSubject} aria-label={t('notebook.subject')}><option value="">{t('notebook.subject')}</option>{#each subjects as s}<option value={`${s.type}:${s.id}`}>{s.name} ({s.type})</option>{/each}</select><select class="input" bind:value={sharePermission} aria-label={t('notebook.permission')}><option value="view">{t('notebook.view')}</option><option value="run">{t('notebook.execute')}</option><option value="edit">{t('notebook.edit')}</option></select><button class="btn" disabled={!shareSubject} onclick={() => { const [type, id] = shareSubject.split(':'); void saveAccess([...access.filter((a) => !(a.subject_type === type && a.subject_id === id)), { subject_type: type, subject_id: id, permission: sharePermission }]); }}>{t('notebook.addAccess')}</button></div>
</div>
