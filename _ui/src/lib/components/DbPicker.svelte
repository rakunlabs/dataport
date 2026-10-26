<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { Search, LayoutGrid, List } from 'lucide-svelte';
  import DbIcon from '@/lib/components/DbIcon.svelte';
  import type { DriverInfo } from '@/lib/api/types';
  import { categories, kinds, matches, type DbCategory, type DbKind } from '@/lib/helper/databases';

  let {
    drivers,
    filesEnabled,
    selected = $bindable(),
    onnext,
  }: { drivers: DriverInfo[]; filesEnabled: boolean; selected: string; onnext: () => void } = $props();

  const viewKey = 'dataport.picker.view';
  let view = $state<'grid' | 'list'>(localStorage.getItem(viewKey) === 'list' ? 'list' : 'grid');
  let query = $state('');
  // svelte-ignore state_referenced_locally
  let category = $state<DbCategory | 'all'>('all');

  function setView(v: 'grid' | 'list') {
    view = v;
    try {
      localStorage.setItem(viewKey, v);
    } catch {
      // The choice then lasts for this dialog only.
    }
  }

  // Kinds the server knows, plus any driver the list here does not name yet.
  const all = $derived<DbKind[]>([
    ...kinds.filter((k) => drivers.some((d) => d.type === k.type)),
    ...drivers
      .filter((d) => !kinds.some((k) => k.type === d.type))
      .map((d) => ({ type: d.type, label: d.type, category: 'generic' as DbCategory, hint: '', aliases: [] })),
  ]);

  const visible = $derived(all.filter((k) => (query.trim() ? matches(k, query) : category === 'all' || k.category === category)));

  function counts(c: DbCategory | 'all') {
    return c === 'all' ? all.length : all.filter((k) => k.category === c).length;
  }

  // Why a database cannot be picked on this server, or '' when it can.
  function blocked(k: DbKind): string {
    const d = drivers.find((d) => d.type === k.type);
    if (!d) return t('picker.notInBuild');
    if (!d.available) return t('picker.needsCgo');
    if (d.target === 'file' && !filesEnabled) return t('picker.filesDisabled');
    return '';
  }

  function pick(k: DbKind) {
    if (!blocked(k)) selected = k.type;
  }

  function open(k: DbKind) {
    if (blocked(k)) return;
    selected = k.type;
    onnext();
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && selected && !blocked(all.find((k) => k.type === selected)!)) {
      e.preventDefault();
      onnext();
    }
  }
</script>

<div class="flex h-[26rem] min-h-0 flex-col gap-3">
  <div class="flex items-center gap-2">
    <div class="relative flex-1">
      <Search size={14} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
      <!-- svelte-ignore a11y_autofocus -->
      <input class="input pl-7" placeholder={t('picker.search')} bind:value={query} {onkeydown} autofocus aria-label={t('picker.search')} />
    </div>
    <div class="segmented" role="group" aria-label={t('picker.view')}>
      <button type="button" aria-pressed={view === 'grid'} title={t('picker.grid')} onclick={() => setView('grid')}><LayoutGrid size={13} /></button>
      <button type="button" aria-pressed={view === 'list'} title={t('picker.list')} onclick={() => setView('list')}><List size={13} /></button>
    </div>
  </div>

  <div class="flex min-h-0 flex-1 gap-3">
    <nav class="w-36 shrink-0 space-y-px border-r border-line-soft pr-3" aria-label={t('picker.categories')}>
      {#each [{ key: 'all' as const, labelKey: 'picker.all' }, ...categories] as c (c.key)}
        {@const active = !query.trim() && category === c.key}
        <button
          type="button"
          class="flex h-7 w-full items-center justify-between rounded-md px-2 text-left text-sm {active ? 'bg-row-active font-medium text-fg' : 'text-fg-2 hover:bg-hover'}"
          aria-current={active ? 'page' : undefined}
          onclick={() => {
            category = c.key;
            query = '';
          }}
        >
          {t(c.labelKey)}
          <span class="text-xs text-faint tabular-nums">{counts(c.key)}</span>
        </button>
      {/each}
    </nav>

    <div class="min-w-0 flex-1 overflow-y-auto pr-1" role="listbox" aria-label={t('picker.databases')}>
      {#if query.trim()}<p class="mb-2 text-xs text-muted">{t('picker.results', { count: visible.length })}</p>{/if}

      {#if visible.length === 0}
        <p class="rounded-md border border-dashed border-line py-10 text-center text-sm text-muted">{t('picker.noMatch')}</p>
      {:else if view === 'grid'}
        <div class="grid grid-cols-3 gap-2">
          {#each visible as k (k.type)}
            {@const reason = blocked(k)}
            <button
              type="button"
              role="option"
              aria-selected={selected === k.type}
              aria-disabled={!!reason}
              title={reason || (k.compatible ? t('picker.alsoWorks', { list: k.compatible }) : '')}
              class="flex min-h-24 flex-col items-center justify-center gap-1.5 rounded-md border p-2 text-center {selected === k.type
                ? 'border-accent bg-accent-soft'
                : 'border-line hover:border-fg-2/30 hover:bg-hover'} {reason ? 'cursor-not-allowed opacity-50' : ''}"
              onclick={() => pick(k)}
              ondblclick={() => open(k)}
            >
              <DbIcon type={k.type} size={24} />
              <span class="text-sm font-medium">{k.label}</span>
              <span class="line-clamp-2 text-xs leading-tight text-muted">{reason || (k.hint ? t(k.hint) : '')}</span>
            </button>
          {/each}
        </div>
      {:else}
        <div class="divide-y divide-line-soft rounded-md border border-line">
          {#each visible as k (k.type)}
            {@const reason = blocked(k)}
            <button
              type="button"
              role="option"
              aria-selected={selected === k.type}
              aria-disabled={!!reason}
              class="flex w-full items-center gap-3 px-3 py-2 text-left {selected === k.type ? 'bg-accent-soft' : 'hover:bg-hover'} {reason
                ? 'cursor-not-allowed opacity-50'
                : ''}"
              onclick={() => pick(k)}
              ondblclick={() => open(k)}
            >
              <DbIcon type={k.type} size={16} />
              <span class="min-w-0 flex-1">
                <span class="block text-sm font-medium">{k.label}</span>
                <span class="block truncate text-xs text-muted">{reason || (k.hint ? t(k.hint) : '')}</span>
              </span>
              {#if k.compatible && !reason}<span class="hidden max-w-56 truncate text-xs text-faint lg:block">{k.compatible}</span>{/if}
              {#if query.trim()}<span class="chip">{t(categories.find((c) => c.key === k.category)?.labelKey ?? '')}</span>{/if}
            </button>
          {/each}
        </div>
      {/if}
    </div>
  </div>
</div>
