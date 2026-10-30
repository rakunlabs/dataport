<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { KeyRound, TriangleAlert } from 'lucide-svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import { connection, loadStructure } from '@/lib/stores/workspace.svelte';
  import { comparable } from '@/lib/stores/virtualKeys.svelte';
  import type { ColumnInfo, IndexInfo } from '@/lib/api/types';

  // Chooses a virtual key for a table without a primary key, as DBeaver
  // does: the user picks the columns that find each row, starting from a
  // unique index or all columns.
  let {
    connectionId,
    schema,
    table,
    columns,
    initial,
    onsave,
    onclose,
  }: {
    connectionId: string;
    schema: string;
    table: string;
    columns: ColumnInfo[];
    initial: string[];
    onsave: (columns: string[]) => void;
    onclose: () => void;
  } = $props();

  // Columns whose values cannot be compared with = cannot find a row.
  const dbType = $derived(connection(connectionId)?.db_type ?? '');
  const usable = $derived(columns.filter((c) => comparable(c.type, dbType)));
  const skipped = $derived(columns.filter((c) => !comparable(c.type, dbType)));

  // The dialog starts from the key it was opened with.
  const start = () => new Set<string>(initial);
  let chosen = $state(start());
  let uniques = $state<IndexInfo[]>([]);

  $effect(() => {
    loadStructure(connectionId, schema, table)
      .then((s) => (uniques = (s.indexes ?? []).filter((ix) => ix.unique && ix.columns.length && ix.columns.every((c) => usable.some((m) => m.name === c)))))
      .catch(() => (uniques = []));
  });

  // Without a choice yet, the first unique index is suggested, once, so
  // clearing the columns by hand does not bring it back.
  let suggested = false;
  $effect(() => {
    if (suggested || !uniques.length) return;
    suggested = true;
    if (!initial.length && !chosen.size) chosen = new Set(uniques[0].columns);
  });

  const all = $derived(usable.length > 0 && usable.every((c) => chosen.has(c.name)));
  const nullable = $derived(columns.filter((c) => chosen.has(c.name) && c.nullable).map((c) => c.name));
  const matchesUnique = $derived(uniques.some((ix) => ix.columns.length === chosen.size && ix.columns.every((c) => chosen.has(c))));

  function toggle(name: string) {
    const next = new Set(chosen);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    chosen = next;
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!chosen.size) return;
    // Kept in the table's column order.
    onsave(usable.filter((c) => chosen.has(c.name)).map((c) => c.name));
  }
</script>

<Modal title={t('edit.virtualKeyTitle', { table })} {onclose} width="max-w-lg">
  <form id="virtual-key-form" class="space-y-4" onsubmit={submit}>
    <p class="text-sm text-fg-2">{t('edit.virtualKeyHint')}</p>

    <div class="flex flex-wrap gap-2">
      {#each uniques as ix (ix.name)}
        <button type="button" class="btn h-7 border border-line text-xs" onclick={() => (chosen = new Set(ix.columns))} title={ix.name}>
          <KeyRound size={12} class="text-warn" /> {t('edit.virtualKeyUnique')}: <span class="font-mono">{ix.columns.join(', ')}</span>
        </button>
      {/each}
      <button type="button" class="btn h-7 border text-xs {all ? 'border-accent bg-accent-soft' : 'border-line'}" aria-pressed={all} onclick={() => (chosen = new Set(all ? [] : usable.map((c) => c.name)))}>
        {t('edit.virtualKeyUseAll')}
      </button>
    </div>

    <fieldset class="max-h-72 overflow-y-auto rounded-md border border-line">
      <legend class="sr-only">{t('edit.virtualKey')}</legend>
      {#each usable as c (c.name)}
        <label class="flex cursor-pointer items-center gap-2 border-b border-line-soft px-3 py-1.5 text-sm last:border-b-0 hover:bg-hover">
          <input type="checkbox" checked={chosen.has(c.name)} onchange={() => toggle(c.name)} />
          <span class="min-w-0 flex-1 truncate font-mono text-xs">{c.name}</span>
          <span class="shrink-0 font-mono text-2xs text-faint">{c.type}{c.nullable ? '' : ' NOT NULL'}</span>
        </label>
      {/each}
    </fieldset>
    {#if skipped.length}
      <p class="hint">{t('edit.virtualKeySkipped', { columns: skipped.map((c) => c.name).join(', ') })}</p>
    {/if}

    {#if chosen.size && !matchesUnique}
      <p class="flex items-start gap-2 rounded-md bg-warn-soft px-3 py-2 text-xs text-warn">
        <TriangleAlert size={14} class="mt-px shrink-0" />
        <span>{t('edit.virtualKeyWarn')}{#if nullable.length}{' ' + t('edit.virtualKeyNullable', { columns: nullable.join(', ') })}{/if}</span>
      </p>
    {/if}
  </form>

  {#snippet footer()}
    {#if initial.length}
      <button class="btn mr-auto" type="button" onclick={() => onsave([])}>{t('edit.virtualKeyClear')}</button>
    {/if}
    <button class="btn" type="button" onclick={onclose}>{t('common.cancel')}</button>
    <button class="btn-primary" type="submit" form="virtual-key-form" disabled={!chosen.size}>{t('common.save')}</button>
  {/snippet}
</Modal>
