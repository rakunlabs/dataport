<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, RefreshCw, Upload, NotebookPen } from 'lucide-svelte';
  import { listNotebooks, type Notebook } from '@/lib/api/notebooks';
  import { importNotebook } from '@/lib/helper/notebookImport';
  import { openNotebook, persist, ws } from '@/lib/stores/workspace.svelte';
  import { fmtDateTime, t } from '@/lib/i18n/index.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let notes = $state<Notebook[]>([]);
  let loading = $state(true);
  let error = $state('');
  let filter = $state('');
  let input: HTMLInputElement;
  const filtered = $derived(notes.filter((n) => `${n.name} ${n.path}`.toLowerCase().includes(filter.toLowerCase())));

  async function load() {
    loading = true; error = '';
    try { notes = await listNotebooks(); } catch (e) { error = errorMessage(e); }
    finally { loading = false; }
  }
  onMount(load);

  async function importFile(e: Event) {
    const file = (e.currentTarget as HTMLInputElement).files?.[0];
    if (!file) return;
    try {
      if (file.size > 4 * 1024 * 1024) throw new Error('Notebook JSON must be smaller than 4 MiB.');
      const raw = JSON.parse(await file.text());
      const imported = importNotebook(raw, ws.connections);
      const saz = !!(raw.content || raw.data?.content);
      const tab = openNotebook();
      tab.notebook = imported;
      tab.title = tab.notebook.name; tab.dirty = true; persist();
      if (saz) toast(t('notebook.importHint'), 'info', 8000);
    } catch (e) { toast(errorMessage(e), 'error'); }
    finally { input.value = ''; }
  }
</script>

<div class="flex h-full flex-col">
  <div class="toolbar">
    <NotebookPen size={15} class="text-muted" /><h1 class="text-sm font-semibold">{t('notebook.title')}</h1>
    <input class="input ml-3 w-56 text-xs" bind:value={filter} placeholder={t('common.search')} aria-label={t('common.search')} />
    <div class="flex-1"></div>
    <input class="hidden" type="file" accept="application/json,.json" bind:this={input} onchange={importFile} aria-label={t('notebook.import')} />
    <button class="btn" onclick={() => input.click()}><Upload size={14} />{t('notebook.import')}</button>
    <button class="icon-btn" onclick={load} disabled={loading} title={t('common.refresh')} aria-label={t('common.refresh')}><RefreshCw size={14} /></button>
    <button class="btn-primary" onclick={() => openNotebook()}><Plus size={14} />{t('notebook.new')}</button>
  </div>
  {#if error}
    <div class="m-4 text-sm text-danger" role="alert">{error}<button class="btn ml-2" onclick={load}>{t('notebook.retry')}</button></div>
  {:else if loading}
    <p class="p-4 text-sm text-muted">{t('common.loading')}</p>
  {:else if !notes.length}
    <div class="flex flex-1 items-center justify-center p-6"><div class="max-w-md"><h2 class="text-sm font-semibold">{t('notebook.empty')}</h2><p class="mt-2 text-sm text-muted">{t('notebook.emptyHint')}</p><button class="btn-primary mt-4" onclick={() => openNotebook()}><Plus size={14} />{t('notebook.new')}</button></div></div>
  {:else}
    <div class="min-h-0 flex-1 overflow-auto">
      <table class="w-full text-left text-sm">
        <thead class="sticky top-0 bg-panel text-xs text-muted"><tr><th class="px-4 py-2">{t('notebook.name')}</th><th class="px-4 py-2">{t('notebook.path')}</th><th class="px-4 py-2">{t('notebook.version')}</th><th class="px-4 py-2">{t('notebook.updated')}</th></tr></thead>
        <tbody>{#each filtered as n (n.id)}<tr class="border-b border-line-soft hover:bg-hover"><td class="px-4 py-2"><button class="text-left font-medium hover:underline" onclick={() => openNotebook(n.id)}>{n.name}</button></td><td class="px-4 py-2 font-mono text-xs text-muted">{n.path}</td><td class="px-4 py-2 text-xs">{n.published_version ? t('notebook.published', { version: n.published_version }) : t('notebook.draft', { version: n.version })}</td><td class="px-4 py-2 text-xs text-muted">{fmtDateTime(n.updated_at)}</td></tr>{/each}</tbody>
      </table>
    </div>
  {/if}
</div>
