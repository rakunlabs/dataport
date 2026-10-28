<script lang="ts">
  import { Play, ArrowUp, ArrowDown, CopyPlus, Trash2, ChevronDown, ChevronRight } from 'lucide-svelte';
  import type { NotebookCell } from '@/lib/api/notebooks';
  import { connection, notebookConnections, type Tab } from '@/lib/stores/workspace.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { editorOptions } from '@/lib/helper/editorOptions';
  import { prefs } from '@/lib/stores/prefs.svelte';
  import SqlEditor from '@/lib/components/editor/SqlEditor.svelte';
  import NotebookTransfer from '@/lib/components/notebook/NotebookTransfer.svelte';

  let {
    tab,
    cell = $bindable(),
    i,
    last,
    editable,
    runnable,
    busy,
    changed,
    run,
    save,
    move,
    duplicate,
    remove,
  }: {
    tab: Tab;
    cell: NotebookCell;
    i: number;
    last: boolean;
    editable: boolean;
    runnable: boolean;
    busy: boolean;
    changed: () => void;
    run: (cell: string) => void;
    save: () => void;
    move: (i: number, by: number) => void;
    duplicate: (c: NotebookCell) => void;
    remove: (c: NotebookCell) => void;
  } = $props();

  const conn = $derived(connection(cell.connection_id));
</script>

<section class="border-b border-line">
  <div class="flex min-h-9 items-center gap-1 overflow-x-auto bg-panel px-2">
    <button class="icon-btn shrink-0" onclick={() => { cell.collapsed = !cell.collapsed; if (editable) changed(); }} title={t(cell.collapsed ? 'notebook.expand' : 'notebook.collapse')} aria-label={t(cell.collapsed ? 'notebook.expand' : 'notebook.collapse')} aria-expanded={!cell.collapsed}>{#if cell.collapsed}<ChevronRight size={14} />{:else}<ChevronDown size={14} />{/if}</button>
    <span class="w-5 shrink-0 text-center font-mono text-xs text-faint">{i + 1}</span>
    <input class="input w-32 shrink-0 font-mono text-xs" bind:value={cell.path} oninput={changed} readonly={!editable || busy || tab.running} aria-label={t('notebook.path')} />
    <select class="input w-44 shrink-0 text-xs" bind:value={cell.connection_id} disabled={!editable || busy || tab.running} onchange={changed} aria-label={t('notebook.connection')}><option value="">{t('notebook.chooseConnection')}</option>{#each notebookConnections() as c}<option value={c.id}>{c.name}</option>{/each}</select>
    {#if conn?.production}<span class="pill-prod">{t('conn.prodShort')}</span>{/if}
    <span class={cell.write ? 'pill-rw' : 'pill-ro'}>{t(cell.write ? 'notebook.write' : 'notebook.readOnly')}</span>
    <div class="flex-1"></div>
    <button class="btn shrink-0" disabled={!runnable || busy || tab.running || tab.dirty || !cell.enabled} onclick={() => run(cell.path)} title={t('notebook.runCell')}><Play size={12} />{t('notebook.runCell')}</button>
    <button class="icon-btn shrink-0" disabled={!editable || busy || tab.running || i === 0} onclick={() => move(i, -1)} title={t('notebook.up')} aria-label={t('notebook.up')}><ArrowUp size={13} /></button>
    <button class="icon-btn shrink-0" disabled={!editable || busy || tab.running || last} onclick={() => move(i, 1)} title={t('notebook.down')} aria-label={t('notebook.down')}><ArrowDown size={13} /></button>
    <button class="icon-btn shrink-0" disabled={!editable || busy || tab.running} onclick={() => duplicate(cell)} title={t('notebook.duplicate')} aria-label={t('notebook.duplicate')}><CopyPlus size={13} /></button>
    <button class="icon-btn shrink-0" disabled={!editable || busy || tab.running} onclick={() => remove(cell)} title={t('common.delete')} aria-label={t('common.delete')}><Trash2 size={13} /></button>
  </div>
  {#if !cell.collapsed}
    <input class="input m-2 w-[calc(100%-16px)] text-xs" bind:value={cell.description} readonly={!editable || busy || tab.running} oninput={changed} placeholder={t('notebook.description')} aria-label={t('notebook.description')} />
    <div class="h-44 border-y border-line-soft"><SqlEditor value={cell.sql} dbType={conn?.db_type || 'postgres'} onchange={(v) => { cell.sql = v; changed(); }} onrun={() => run(cell.path)} onsave={save} readonly={!editable || busy || tab.running} options={editorOptions(prefs)} /></div>
    <fieldset disabled={!editable || busy || tab.running} class="flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-2 text-xs">
      {#each ['enabled', 'output', 'template', 'write'] as key}
        <label class="flex items-center gap-1.5"><input type="checkbox" disabled={key === 'write' && !!cell.transfer} checked={cell[key as 'enabled']} onchange={(e) => { cell[key as 'enabled'] = e.currentTarget.checked; changed(); }} />{t(`notebook.${key}`)}</label>
      {/each}
      <label class="flex items-center gap-1.5"><input type="checkbox" checked={!!cell.transfer} onchange={(e) => { cell.transfer = e.currentTarget.checked ? { connection_id: '', schema: '', table: '', wipe: false, batch: 100, skip_error: '', mappings: [] } : undefined; if (cell.transfer) cell.write = true; changed(); }} />{t('notebook.transfer')}</label>
      <details class="ml-auto"><summary class="cursor-pointer text-muted">{t('notebook.details')}</summary><div class="flex flex-wrap gap-3 py-2">
        <label class="flex flex-col gap-1">{t('notebook.dependencies')}<input class="input w-64 font-mono text-xs" value={cell.dependencies.join(', ')} onchange={(e) => { cell.dependencies = e.currentTarget.value.split(',').map((x) => x.trim()).filter(Boolean); changed(); }} /></label>
        <label class="flex flex-col gap-1">{t('notebook.rows')}<input class="input w-40" type="number" min="-1" bind:value={cell.max_rows} oninput={changed} /></label>
        <label class="flex flex-col gap-1">{t('notebook.timeout')}<input class="input w-40" type="number" min="0" max="86400" bind:value={cell.timeout_sec} oninput={changed} /></label>
      </div></details>
    </fieldset>
    <fieldset disabled={!editable || busy || tab.running}><NotebookTransfer {cell} onchange={changed} /></fieldset>
  {/if}
</section>
