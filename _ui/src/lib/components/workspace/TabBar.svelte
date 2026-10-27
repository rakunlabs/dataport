<script lang="ts">
  import { t as tr } from '@/lib/i18n/index.svelte';
  import { X, Table2, FileCode2, Settings, Network, Activity, UsersRound, BookOpen, NotebookPen } from 'lucide-svelte';
  import { closeOthers, closeTab, connection, ws } from '@/lib/stores/workspace.svelte';

  let menu = $state<{ id: string; x: number; y: number } | null>(null);

  function onauxclick(e: MouseEvent, id: string) {
    if (e.button === 1) {
      e.preventDefault();
      closeTab(id);
    }
  }
</script>

<svelte:window onclick={() => (menu = null)} />

<div class="pane-head items-stretch overflow-x-auto" role="tablist">
  {#each ws.tabs as t (t.id)}
    {@const active = t.id === ws.activeId}
    {@const conn = connection(t.connectionId)}
    {@const unsaved = (['query', 'notebook'].includes(t.kind) && t.dirty) || t.edit.count > 0}
    <div
      class="group relative flex max-w-72 min-w-32 shrink-0 cursor-pointer items-center gap-1.5 border-r border-line-soft pr-1.5 pl-3
        {active ? 'bg-bg text-fg' : 'text-muted hover:bg-hover hover:text-fg-2'}"
      onclick={() => (ws.activeId = t.id)}
      onauxclick={(e) => onauxclick(e, t.id)}
      oncontextmenu={(e) => {
        e.preventDefault();
        menu = { id: t.id, x: e.clientX, y: e.clientY };
      }}
      onkeydown={(e) => e.key === 'Enter' && (ws.activeId = t.id)}
      role="tab"
      tabindex="0"
      aria-selected={active}
      title={conn ? `${t.title} — ${conn.name}` : t.title}
    >
      {#if active}<span class="absolute right-0 bottom-0 left-0 h-0.5 {conn?.production ? 'bg-prod' : 'bg-accent'}"></span>{/if}
      {#if t.kind === 'notebook' || t.kind === 'notebooks'}
        <NotebookPen size={14} class="shrink-0 {active ? 'text-type' : ''}" />
      {:else if t.kind === 'settings'}
        <Settings size={14} class="shrink-0 {active ? 'text-accent' : ''}" />
      {:else if t.kind === 'diagram'}
        <Network size={14} class="shrink-0 {active ? 'text-accent' : ''}" />
      {:else if t.kind === 'docs' || t.kind === 'docsHub'}
        <BookOpen size={14} class="shrink-0 {active ? 'text-accent' : ''}" />
      {:else if t.kind === 'processes'}
        <Activity size={14} class="shrink-0 {active ? 'text-accent' : ''}" />
      {:else if t.kind === 'users'}
        <UsersRound size={14} class="shrink-0 {active ? 'text-accent' : ''}" />
      {:else if t.kind === 'table'}
        <Table2 size={14} class="shrink-0 {active ? 'text-obj-table' : ''}" />
      {:else}
        <FileCode2 size={14} class="shrink-0 {active ? 'text-type' : ''}" />
      {/if}
      <span class="min-w-0 flex-1 truncate text-sm">
        {t.kind === 'settings' ? tr('settings.title') : t.kind === 'notebooks' ? tr('notebook.title') : t.kind === 'docsHub' ? tr('app.docs') : t.title}{#if conn}<span class="ml-1 text-faint">· {conn.name}</span>{/if}
      </span>
      <span class="relative flex size-[18px] shrink-0 items-center justify-center">
        {#if unsaved}
          <span class="size-2 rounded-full {t.edit.count ? 'bg-warn' : 'bg-fg-2'} group-hover:hidden"></span>
        {/if}
        <button
          class="absolute inset-0 items-center justify-center rounded-sm text-faint hover:bg-active hover:text-fg
            {unsaved ? 'hidden group-hover:flex' : active ? 'flex' : 'hidden group-hover:flex'}"
          onclick={(e) => {
            e.stopPropagation();
            closeTab(t.id);
          }}
          aria-label={tr('tabs.closeTab')}
        >
          <X size={12} />
        </button>
      </span>
    </div>
  {/each}
</div>

{#if menu}
  <div class="popover fixed z-50 w-48" style="left:{menu.x}px; top:{menu.y}px" role="menu">
    <button class="menu-item" role="menuitem" onclick={() => menu && closeTab(menu.id)}>{tr('tabs.close')}</button>
    <button class="menu-item" role="menuitem" onclick={() => menu && closeOthers(menu.id)}>{tr('tabs.closeOthers')}</button>
  </div>
{/if}
