<script lang="ts">
  // Picks a schema of a connection and some of its tables, for the export
  // and transfer dialogs.
  import { t } from '@/lib/i18n/index.svelte';
  import { LoaderCircle, Search, Table2, Eye, Layers } from 'lucide-svelte';
  import { loadObjects, loadSchemas, ws } from '@/lib/stores/workspace.svelte';

  let {
    connectionId,
    schema = $bindable(),
    selected = $bindable(),
    schemaFixed = false,
    views = true,
  }: { connectionId: string; schema: string; selected: string[]; schemaFixed?: boolean; views?: boolean } = $props();

  let filter = $state('');

  $effect(() => {
    void loadSchemas(connectionId);
  });

  const schemas = $derived(ws.schemas[connectionId]?.items ?? []);

  // A connection without a schema list, or a schema not chosen yet, starts
  // on the first one.
  $effect(() => {
    if (!schemaFixed && !schema && schemas.length) schema = schemas[0];
  });

  $effect(() => {
    void loadObjects(connectionId, schema);
  });

  const node = $derived(ws.objects[`${connectionId}|${schema}`]);
  const objects = $derived((node?.items ?? []).filter((o) => views || o.kind === 'table' || o.kind === 'foreign_table'));
  const shown = $derived.by(() => {
    const f = filter.trim().toLowerCase();
    return f ? objects.filter((o) => o.name.toLowerCase().includes(f)) : objects;
  });

  const icon = { table: Table2, view: Eye, materialized_view: Layers, foreign_table: Table2 } as const;

  function toggle(name: string) {
    selected = selected.includes(name) ? selected.filter((x) => x !== name) : [...selected, name];
  }

  function all(on: boolean) {
    const names = new Set(shown.map((o) => o.name));
    selected = on ? [...new Set([...selected, ...names])] : selected.filter((x) => !names.has(x));
  }
</script>

<div class="space-y-2">
  {#if !schemaFixed && schemas.length}
    <div>
      <label class="label" for="pick-schema">{t('tools.schema')}</label>
      <select id="pick-schema" class="input" bind:value={schema} onchange={() => (selected = [])}>
        {#each schemas as s (s)}<option value={s}>{s}</option>{/each}
      </select>
    </div>
  {/if}
  <div class="flex items-center gap-2">
    <span class="label mb-0">{t('tools.tables')}</span>
    <span class="text-xs text-faint tabular-nums">{selected.length}/{objects.length}</span>
    <div class="flex-1"></div>
    <button type="button" class="text-xs text-accent hover:underline" onclick={() => all(true)}>{t('tools.selectAll')}</button>
    <button type="button" class="text-xs text-accent hover:underline" onclick={() => all(false)}>{t('tools.selectNone')}</button>
  </div>
  <div class="relative">
    <Search size={13} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
    <input class="input h-[26px] pl-7 text-xs" placeholder={t('sidebar.filter')} bind:value={filter} />
  </div>
  <div class="h-52 overflow-auto rounded-md border border-line py-1">
    {#if node?.loading && !objects.length}
      <p class="flex items-center gap-2 px-3 py-1 text-xs text-muted"><LoaderCircle size={12} class="animate-spin" /> {t('common.loading')}</p>
    {:else if node?.error}
      <p class="px-3 py-1 text-xs text-danger">{node.error}</p>
    {/if}
    {#each shown as o (o.name)}
      {@const Icon = icon[o.kind as keyof typeof icon] ?? Table2}
      <label class="flex h-6 items-center gap-2 px-3 text-sm hover:bg-hover">
        <input type="checkbox" checked={selected.includes(o.name)} onchange={() => toggle(o.name)} />
        <Icon size={12} class="shrink-0 {o.kind === 'table' ? 'text-accent' : 'text-type'}" />
        <span class="truncate">{o.name}</span>
      </label>
    {:else}
      {#if node?.loaded}<p class="px-3 py-1 text-xs text-muted">{t('tools.noTables')}</p>{/if}
    {/each}
  </div>
</div>
