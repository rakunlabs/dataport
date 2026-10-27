<script lang="ts">
  import { Plus, Trash2 } from 'lucide-svelte';
  import type { NotebookCell } from '@/lib/api/notebooks';
  import { ws } from '@/lib/stores/workspace.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  let { cell, onchange }: { cell: NotebookCell; onchange: () => void } = $props();
</script>

{#if cell.transfer}
  {@const tr = cell.transfer}
  <div class="space-y-3 border-t border-line-soft bg-panel p-3">
    <p class="text-xs text-muted">{t('notebook.transferHint')}</p>
    <div class="flex flex-wrap items-end gap-3">
      <label class="flex flex-col gap-1 text-xs">{t('notebook.target')}<select class="input w-48" bind:value={tr.connection_id} onchange={onchange}><option value="">{t('notebook.chooseConnection')}</option>{#each ws.connections.filter((c) => !c.read_only && !c.production && c.id !== cell.connection_id) as c}<option value={c.id}>{c.name}</option>{/each}</select></label>
      <label class="flex flex-col gap-1 text-xs">{t('notebook.schema')}<input class="input w-36 font-mono" bind:value={tr.schema} oninput={onchange} /></label>
      <label class="flex flex-col gap-1 text-xs">{t('notebook.table')}<input class="input w-44 font-mono" bind:value={tr.table} oninput={onchange} /></label>
      <label class="flex flex-col gap-1 text-xs">{t('notebook.batch')}<input class="input w-20" type="number" min="1" max="500" bind:value={tr.batch} oninput={onchange} /></label>
      <label class="flex h-7 items-center gap-1.5 text-xs"><input type="checkbox" bind:checked={tr.wipe} onchange={onchange} />{t('notebook.wipe')}</label>
    </div>
    <label class="flex max-w-lg flex-col gap-1 text-xs">{t('notebook.skipError')}<input class="input font-mono" bind:value={tr.skip_error} oninput={onchange} /></label>
    <details><summary class="cursor-pointer text-xs text-muted">{t('notebook.mappings')} ({tr.mappings.length})</summary>
      <div class="mt-2 space-y-3">
        {#each tr.mappings as m, i}
          <div class="flex flex-wrap items-end gap-2 border-b border-line-soft pb-3">
            <label class="flex flex-col gap-1 text-xs">{t('notebook.sourceColumn')}<input class="input w-32 font-mono" bind:value={m.source} oninput={onchange} /></label>
            <label class="flex flex-col gap-1 text-xs">{t('notebook.targetColumn')}<input class="input w-32 font-mono" bind:value={m.target} oninput={onchange} /></label>
            <label class="flex flex-col gap-1 text-xs">{t('notebook.type')}<select class="input" bind:value={m.type} onchange={onchange}><option value="">{t('notebook.keepType')}</option><option>string</option><option>number</option><option>date</option></select></label>
            <label class="flex h-7 items-center gap-1.5 text-xs"><input type="checkbox" bind:checked={m.nullable} onchange={onchange} />{t('notebook.nullable')}</label>
            <label class="flex flex-col gap-1 text-xs">{t('notebook.transform')}<input class="input w-60 font-mono" bind:value={m.template} oninput={onchange} /></label>
            <label class="flex flex-col gap-1 text-xs">{t('notebook.encoding')}<select class="input" bind:value={m.encoding} onchange={onchange}><option value="">UTF-8</option><option>ISO-8859-1</option></select></label>
            <button class="icon-btn" onclick={() => { tr.mappings.splice(i, 1); onchange(); }} title={t('common.delete')} aria-label={t('common.delete')}><Trash2 size={13} /></button>
          </div>
        {/each}
        <button class="btn" onclick={() => { tr.mappings.push({ source: '', target: '', type: '', nullable: true, template: '', encoding: '' }); onchange(); }}><Plus size={13} />{t('common.add')}</button>
      </div>
    </details>
  </div>
{/if}
