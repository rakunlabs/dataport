<script lang="ts">
  import { KeyRound, Link, Trash2, Undo2 } from 'lucide-svelte';
  import type { TableStructure } from '@/lib/api/types';
  import type { Tab } from '@/lib/stores/workspace.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { supported, type Draft } from '@/lib/helper/structureSql';

  let {
    tab,
    data,
    draft = $bindable(),
    db,
    writable,
    filter,
  }: { tab: Tab; data: TableStructure; draft: Draft; db: string; writable: boolean; filter: string } = $props();

  const can = $derived(supported(db));
  const fkColumns = $derived(new Set(data.foreign_keys.flatMap((f) => f.columns)));

  // Changed fields are marked, so it is clear what a save will touch.
  function changedField(i: number, field: 'name' | 'type' | 'nullable' | 'default' | 'primary' | 'comment'): boolean {
    const c = draft.columns[i];
    if (!c || !c.original) return false;
    const old = data.columns.find((x) => x.name === c.original);
    if (!old) return false;
    switch (field) {
      case 'name':
        return c.name !== old.name;
      case 'type':
        return c.type.trim() !== old.type;
      case 'nullable':
        return c.nullable !== old.nullable;
      case 'default':
        return c.default.trim() !== old.default;
      case 'primary':
        return c.primary !== old.primary_key;
      case 'comment':
        return c.comment !== (data.comments?.[old.name] ?? '');
    }
  }

  const shownColumns = $derived.by(() => {
    const f = filter.trim().toLowerCase();
    return draft.columns.map((c, i) => ({ c, i })).filter(({ c }) => !f || c.name.toLowerCase().includes(f) || c.type.toLowerCase().includes(f));
  });

  function removeColumn(i: number) {
    const c = draft.columns[i];
    if (!c.original) draft.columns.splice(i, 1);
    else c.dropped = !c.dropped;
  }

  const inputCls = 'input h-7 px-2 font-mono text-xs';
</script>

<table class="w-full border-collapse text-sm">
  <thead class="sticky top-0 z-10 bg-panel text-xs text-fg-2">
    <tr class="[&>th]:h-8 [&>th]:border-b [&>th]:border-line [&>th]:px-2 [&>th]:text-left [&>th]:font-medium">
      <th class="w-10">#</th>
      <th class="min-w-44">{t('structure.name')}</th>
      <th class="min-w-52">{t('structure.type')}</th>
      <th class="w-20">{t('structure.nullable')}</th>
      <th class="w-16 text-center">PK</th>
      <th class="min-w-40">{t('structure.default')}</th>
      {#if can.comments}<th class="min-w-48">{t('structureEdit.comment')}</th>{/if}
      <th class="w-12"></th>
    </tr>
  </thead>
  <tbody>
    {#each shownColumns as { c, i } (i)}
      {@const ro = !writable || !can.columns || c.dropped}
      {@const roAlter = ro || (!!c.original && !can.alterColumn)}
      <tr class="border-b border-line-soft {c.dropped ? 'bg-danger-soft opacity-60' : !c.original ? 'bg-ok/5' : ''} [&>td]:px-2 [&>td]:py-1" data-new-col={!c.original ? '' : undefined}>
        <td class="text-xs text-faint tabular-nums">
          <span class="flex items-center gap-1">
            {i + 1}
            {#if c.primary}<KeyRound size={11} class="text-warn" />{/if}
            {#if fkColumns.has(c.original)}<Link size={11} class="text-accent" />{/if}
          </span>
        </td>
        <td><input class="{inputCls} w-full {changedField(i, 'name') ? 'ring-1 ring-warn' : ''} {c.dropped ? 'line-through' : ''}" bind:value={c.name} disabled={ro} /></td>
        <td>
          <input class="{inputCls} w-full text-type {changedField(i, 'type') ? 'ring-1 ring-warn' : ''}" list="structure-types-{tab.id}" bind:value={c.type} disabled={roAlter} />
        </td>
        <td>
          <label class="flex items-center gap-1.5 text-xs {changedField(i, 'nullable') ? 'text-warn' : ''}">
            <input type="checkbox" bind:checked={c.nullable} disabled={roAlter || c.primary} />
            {c.nullable ? t('common.yes') : t('common.no')}
          </label>
        </td>
        <td class="text-center">
          <input
            type="checkbox"
            class={changedField(i, 'primary') ? 'outline outline-warn' : ''}
            bind:checked={c.primary}
            onchange={() => c.primary && (c.nullable = false)}
            disabled={ro || db === 'sqlite' || db === 'clickhouse'}
          />
        </td>
        <td><input class="{inputCls} w-full {changedField(i, 'default') ? 'ring-1 ring-warn' : ''}" bind:value={c.default} disabled={roAlter} placeholder="—" /></td>
        {#if can.comments}
          <td><input class="input h-7 w-full px-2 text-xs {changedField(i, 'comment') ? 'ring-1 ring-warn' : ''}" bind:value={c.comment} disabled={ro && !c.dropped ? true : c.dropped} /></td>
        {/if}
        <td class="text-right">
          {#if writable && can.columns}
            <button class="icon-btn size-6 {c.dropped ? '' : 'hover:text-danger'}" onclick={() => removeColumn(i)} title={c.dropped ? t('structureEdit.restore') : t('structureEdit.dropColumn')} aria-label={c.dropped ? t('structureEdit.restore') : t('structureEdit.dropColumn')}>
              {#if c.dropped}<Undo2 size={13} />{:else}<Trash2 size={13} />{/if}
            </button>
          {/if}
        </td>
      </tr>
    {/each}
  </tbody>
</table>
{#if !can.alterColumn && can.columns}<p class="px-3 py-2 text-xs text-muted">{t('structureEdit.sqliteHint')}</p>{/if}
