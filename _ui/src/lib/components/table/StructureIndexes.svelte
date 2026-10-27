<script lang="ts">
  import { KeyRound, Trash2, Undo2 } from 'lucide-svelte';
  import type { TableStructure } from '@/lib/api/types';
  import { t } from '@/lib/i18n/index.svelte';
  import { supported, type Draft } from '@/lib/helper/structureSql';
  import { toggleList } from '@/lib/components/table/structure';

  let { data, draft = $bindable(), db, writable }: { data: TableStructure; draft: Draft; db: string; writable: boolean } = $props();

  const can = $derived(supported(db));
  const columnNames = $derived(draft.columns.filter((c) => !c.dropped).map((c) => c.name));
  const inputCls = 'input h-7 px-2 font-mono text-xs';
</script>

<table class="w-full border-collapse text-sm">
  <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
    <tr class="[&>th]:h-8 [&>th]:border-b [&>th]:border-line [&>th]:px-2 [&>th]:text-left [&>th]:font-medium">
      <th class="min-w-48">{t('structure.name')}</th>
      <th>{t('structure.columns')}</th>
      <th class="w-24">{t('structure.unique')}</th>
      <th class="w-28">{t('structure.method')}</th>
      <th class="w-12"></th>
    </tr>
  </thead>
  <tbody>
    {#each data.indexes.filter((x) => x.primary) as ix (ix.name)}
      <tr class="border-b border-line-soft text-xs [&>td]:px-2 [&>td]:py-1.5">
        <td class="font-mono">{ix.name}</td>
        <td class="font-mono">{ix.columns.join(', ')}</td>
        <td><span class="pill-rw"><KeyRound size={10} /> {t('structure.primary')}</span></td>
        <td class="font-mono text-muted">{ix.method ?? ''}</td>
        <td></td>
      </tr>
    {/each}
    {#each draft.indexes as ix, n (n)}
      {@const ro = !writable || !can.indexes || ix.dropped}
      {@const method = data.indexes.find((x) => x.name === ix.original)?.method ?? ''}
      <tr class="border-b border-line-soft {ix.dropped ? 'bg-danger-soft opacity-60' : !ix.original ? 'bg-ok/5' : ''} [&>td]:px-2 [&>td]:py-1 align-top">
        <td><input class="{inputCls} w-full {ix.dropped ? 'line-through' : ''}" bind:value={ix.name} oninput={() => (ix.changed = true)} disabled={ro} placeholder={t('structureEdit.autoName')} /></td>
        <td>
          <div class="flex flex-wrap gap-1 py-0.5">
            {#each columnNames as name (name)}
              <button
                class="rounded border px-1.5 py-0.5 font-mono text-xs {ix.columns.includes(name) ? 'border-accent bg-accent/10 text-accent' : 'border-line text-muted hover:bg-hover'}"
                disabled={ro}
                onclick={() => {
                  toggleList(ix.columns, name);
                  ix.changed = true;
                }}>{name}{#if ix.columns.includes(name)}<sup class="ml-0.5">{ix.columns.indexOf(name) + 1}</sup>{/if}</button
              >
            {/each}
          </div>
        </td>
        <td><input type="checkbox" bind:checked={ix.unique} onchange={() => (ix.changed = true)} disabled={ro} /></td>
        <td class="font-mono text-xs text-muted">{method}</td>
        <td class="text-right">
          {#if writable && can.indexes}
            <button
              class="icon-btn size-6"
              onclick={() => (ix.original ? (ix.dropped = !ix.dropped) : draft.indexes.splice(n, 1))}
              title={ix.dropped ? t('structureEdit.restore') : t('structureEdit.drop')}
              aria-label={ix.dropped ? t('structureEdit.restore') : t('structureEdit.drop')}
            >
              {#if ix.dropped}<Undo2 size={13} />{:else}<Trash2 size={13} />{/if}
            </button>
          {/if}
        </td>
      </tr>
    {/each}
  </tbody>
</table>
{#if !data.indexes.length && !draft.indexes.length}<p class="px-3 py-3 text-xs text-faint">{t('structure.noIndexes')}</p>{/if}
{#if draft.indexes.some((x) => x.original && x.changed && !x.dropped)}<p class="px-3 py-2 text-xs text-muted">{t('structureEdit.indexRecreate')}</p>{/if}
