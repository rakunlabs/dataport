<script lang="ts">
  import { Trash2, Undo2, ArrowUpRight } from 'lucide-svelte';
  import { loadColumns, loadObjects, openTableTab, ws, type Tab } from '@/lib/stores/workspace.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { fkActions, supported, type Draft } from '@/lib/helper/structureSql';
  import { toggleList } from '@/lib/components/table/structure';

  let { tab, draft = $bindable(), db, writable }: { tab: Tab; draft: Draft; db: string; writable: boolean } = $props();

  const can = $derived(supported(db));
  const columnNames = $derived(draft.columns.filter((c) => !c.dropped).map((c) => c.name));
  const inputCls = 'input h-7 px-2 font-mono text-xs';

  // Tables and their columns of the schema, for foreign keys.
  $effect(() => {
    void loadObjects(tab.connectionId, tab.schema, 'failed');
  });
  const tables = $derived((ws.objects[`${tab.connectionId}|${tab.schema}`]?.items ?? []).filter((o) => o.kind === 'table').map((o) => o.name));
  function refColumnsOf(schema: string, table: string) {
    if (!table) return [];
    return (ws.columns[`${tab.connectionId}|${schema || tab.schema}|${table}`]?.items ?? []).map((c) => c.name);
  }

  // The columns of the referenced tables load as they are chosen.
  $effect(() => {
    for (const f of draft.fks) if (f.refTable) void loadColumns(tab.connectionId, f.refSchema || tab.schema, f.refTable);
  });
</script>

<table class="w-full border-collapse text-sm">
  <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
    <tr class="[&>th]:h-8 [&>th]:border-b [&>th]:border-line [&>th]:px-2 [&>th]:text-left [&>th]:font-medium">
      <th class="min-w-40">{t('structure.name')}</th>
      <th>{t('structure.columns')}</th>
      <th class="min-w-40">{t('structure.references')}</th>
      <th>{t('structureEdit.refColumns')}</th>
      <th class="w-32">ON DELETE</th>
      <th class="w-32">ON UPDATE</th>
      <th class="w-16"></th>
    </tr>
  </thead>
  <tbody>
    {#each draft.fks as fk, n (n)}
      {@const ro = !writable || !can.fks || fk.dropped}
      {@const refCols = refColumnsOf(fk.refSchema, fk.refTable)}
      <tr class="border-b border-line-soft {fk.dropped ? 'bg-danger-soft opacity-60' : !fk.original ? 'bg-ok/5' : ''} [&>td]:px-2 [&>td]:py-1 align-top">
        <td><input class="{inputCls} w-full {fk.dropped ? 'line-through' : ''}" bind:value={fk.name} oninput={() => (fk.changed = true)} disabled={ro} placeholder={t('structureEdit.autoName')} /></td>
        <td>
          <div class="flex flex-wrap gap-1 py-0.5">
            {#each columnNames as name (name)}
              <button
                class="rounded border px-1.5 py-0.5 font-mono text-xs {fk.columns.includes(name) ? 'border-accent bg-accent/10 text-accent' : 'border-line text-muted hover:bg-hover'}"
                disabled={ro}
                onclick={() => {
                  toggleList(fk.columns, name);
                  fk.changed = true;
                }}>{name}</button
              >
            {/each}
          </div>
        </td>
        <td>
          <select class="input h-7 w-full font-mono text-xs" bind:value={fk.refTable} onchange={() => ((fk.changed = true), (fk.refColumns = []))} disabled={ro}>
            <option value="" disabled>{t('structureEdit.pickTable')}</option>
            {#if fk.refTable && !tables.includes(fk.refTable)}<option value={fk.refTable}>{fk.refSchema ? fk.refSchema + '.' : ''}{fk.refTable}</option>{/if}
            {#each tables as tb (tb)}<option value={tb}>{tb}</option>{/each}
          </select>
        </td>
        <td>
          <div class="flex flex-wrap gap-1 py-0.5">
            {#each refCols as name (name)}
              <button
                class="rounded border px-1.5 py-0.5 font-mono text-xs {fk.refColumns.includes(name) ? 'border-accent bg-accent/10 text-accent' : 'border-line text-muted hover:bg-hover'}"
                disabled={ro}
                onclick={() => {
                  toggleList(fk.refColumns, name);
                  fk.changed = true;
                }}>{name}</button
              >
            {:else}
              <span class="font-mono text-xs text-muted">{fk.refColumns.join(', ')}</span>
            {/each}
          </div>
        </td>
        <td>
          <select class="input h-7 w-full text-xs" bind:value={fk.onDelete} onchange={() => (fk.changed = true)} disabled={ro}>
            {#each fkActions as a (a)}<option value={a}>{a || '—'}</option>{/each}
          </select>
        </td>
        <td>
          <select class="input h-7 w-full text-xs" bind:value={fk.onUpdate} onchange={() => (fk.changed = true)} disabled={ro || db === 'oracle'}>
            {#each fkActions as a (a)}<option value={a}>{a || '—'}</option>{/each}
          </select>
        </td>
        <td class="text-right whitespace-nowrap">
          {#if fk.refTable}
            <button class="icon-btn size-6" onclick={() => openTableTab(tab.connectionId, fk.refSchema || tab.schema, fk.refTable)} title={t('structure.openTable')} aria-label={t('structure.openTable')}><ArrowUpRight size={13} /></button>
          {/if}
          {#if writable && can.fks}
            <button
              class="icon-btn size-6"
              onclick={() => (fk.original ? (fk.dropped = !fk.dropped) : draft.fks.splice(n, 1))}
              title={fk.dropped ? t('structureEdit.restore') : t('structureEdit.drop')}
              aria-label={fk.dropped ? t('structureEdit.restore') : t('structureEdit.drop')}
            >
              {#if fk.dropped}<Undo2 size={13} />{:else}<Trash2 size={13} />{/if}
            </button>
          {/if}
        </td>
      </tr>
    {/each}
  </tbody>
</table>
{#if !draft.fks.length}<p class="px-3 py-3 text-xs text-faint">{t('structure.noForeignKeys')}</p>{/if}
