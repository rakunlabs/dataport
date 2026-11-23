<script lang="ts">
  import { pendingEdits } from '@/lib/stores/edits.svelte';
  import { t as tr } from '@/lib/i18n/index.svelte';
  import { X, Table2, FileCode2, Settings, Network, Activity, UsersRound, BookOpen, NotebookPen } from '@lucide/svelte';
  import { closeOthers, closeTab, connection, moveTab, renameTab, ws } from '@/lib/stores/workspace.svelte';
  import { overlayScroll } from '@/lib/helper/overlayScroll';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let menu = $state<{ id: string; x: number; y: number } | null>(null);

  // The query tab whose title is being edited in place, and the draft.
  let renamingId = $state('');
  let draft = $state('');

  function startRename(id: string) {
    const tab = ws.tabs.find((t) => t.id === id);
    if (!tab || tab.kind !== 'query') return;
    ws.activeId = id;
    draft = tab.title;
    renamingId = id;
  }

  async function commitRename() {
    const id = renamingId;
    if (!id) return;
    renamingId = '';
    try {
      await renameTab(id, draft);
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  function focusSelect(el: HTMLInputElement) {
    el.focus();
    el.select();
  }

  // A tab being dragged to another place, and where it would go: the index
  // of the tab it goes before, or the number of tabs for the end.
  let dragId = $state('');
  let dropAt = $state(-1);

  function onauxclick(e: MouseEvent, id: string) {
    if (e.button === 1) {
      e.preventDefault();
      closeTab(id);
    }
  }

  function ondragstart(e: DragEvent, id: string) {
    dragId = id;
    ws.activeId = id;
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData('text/plain', id);
    }
  }

  function ondragover(e: DragEvent, i: number) {
    if (!dragId) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    dropAt = e.clientX < r.left + r.width / 2 ? i : i + 1;
  }

  function ondrop(e: DragEvent) {
    if (!dragId) return;
    e.preventDefault();
    if (dropAt >= 0) moveTab(dragId, dropAt);
    ondragend();
  }

  function ondragend() {
    dragId = '';
    dropAt = -1;
  }

  // The drop marker shows only where the tab would actually move.
  const from = $derived(dragId ? ws.tabs.findIndex((t) => t.id === dragId) : -1);
  const marker = $derived(from >= 0 && dropAt >= 0 && dropAt !== from && dropAt !== from + 1 ? dropAt : -1);
</script>

<svelte:window onclick={() => (menu = null)} onkeydown={(e) => e.key === 'Escape' && (menu = null)} />

<div class="pane-head relative">
  <div
    class="flex min-w-0 flex-1 items-stretch overflow-x-auto"
    role="tablist"
    tabindex="-1"
    use:overlayScroll
    ondragover={(e) => {
      // Past the last tab drops at the end.
      if (!dragId || e.target !== e.currentTarget) return;
      e.preventDefault();
      dropAt = ws.tabs.length;
    }}
    ondrop={ondrop}
    ondragleave={(e) => {
      if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node)) dropAt = -1;
    }}
  >
    {#each ws.tabs as t, i (t.id)}
      {@const active = t.id === ws.activeId}
      {@const conn = connection(t.connectionId)}
      {@const unsaved = (['query', 'notebook'].includes(t.kind) && t.dirty) || pendingEdits(t) > 0}
      <div
        class="group relative flex max-w-72 min-w-32 shrink-0 cursor-pointer items-center gap-1.5 border-r border-line-soft pr-1.5 pl-3
          {active ? 'bg-bg text-fg' : 'text-muted hover:bg-hover hover:text-fg-2'} {dragId === t.id ? 'opacity-50' : ''}"
        draggable={renamingId !== t.id}
        ondragstart={(e) => ondragstart(e, t.id)}
        ondragover={(e) => ondragover(e, i)}
        ondrop={ondrop}
        ondragend={ondragend}
        onclick={() => (ws.activeId = t.id)}
        ondblclick={() => startRename(t.id)}
        onauxclick={(e) => onauxclick(e, t.id)}
        oncontextmenu={(e) => {
          e.preventDefault();
          menu = { id: t.id, x: e.clientX, y: e.clientY };
        }}
        onkeydown={(e) => {
          if (e.key === 'Enter') ws.activeId = t.id;
          else if (e.key === 'F2') {
            e.preventDefault();
            startRename(t.id);
          }
          // Ctrl+Shift+Left/Right moves the tab without a mouse.
          else if ((e.ctrlKey || e.metaKey) && e.shiftKey && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
            e.preventDefault();
            moveTab(t.id, e.key === 'ArrowLeft' ? Math.max(0, i - 1) : Math.min(ws.tabs.length, i + 2));
            const el = e.currentTarget as HTMLElement;
            requestAnimationFrame(() => el.focus());
          }
        }}
        role="tab"
        tabindex="0"
        aria-selected={active}
        title={conn ? `${t.title} — ${conn.name}` : t.title}
      >
        {#if marker === i}<span class="absolute top-1 bottom-1 -left-px z-[2] w-0.5 rounded-full bg-accent"></span>{/if}
        {#if marker === ws.tabs.length && i === ws.tabs.length - 1}<span class="absolute top-1 -right-px bottom-1 z-[2] w-0.5 rounded-full bg-accent"></span>{/if}
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
        {#if renamingId === t.id}
          <input
            class="min-w-0 flex-1 rounded-sm border border-accent bg-bg px-1 text-sm text-fg outline-none"
            bind:value={draft}
            use:focusSelect
            aria-label={tr('tabs.rename')}
            onclick={(e) => e.stopPropagation()}
            ondblclick={(e) => e.stopPropagation()}
            onkeydown={(e) => {
              e.stopPropagation();
              if (e.key === 'Enter') void commitRename();
              else if (e.key === 'Escape') renamingId = '';
            }}
            onblur={() => void commitRename()}
          />
        {:else}
        <span class="min-w-0 flex-1 truncate text-sm">
          {t.kind === 'settings' ? tr('settings.title') : t.kind === 'notebooks' ? tr('notebook.title') : t.kind === 'docsHub' ? tr('app.docs') : t.title}{#if conn}<span class="ml-1 text-faint">· {conn.name}</span>{/if}
        </span>
        {/if}
        <span class="relative flex size-[18px] shrink-0 items-center justify-center">
          {#if unsaved}
            <span class="size-2 rounded-full {pendingEdits(t) ? 'bg-warn' : 'bg-fg-2'} group-hover:hidden"></span>
          {/if}
          <button
            class="absolute inset-0 items-center justify-center rounded-sm text-faint hover:bg-active hover:text-fg
              {unsaved ? 'hidden group-hover:flex' : active ? 'flex' : 'hidden group-hover:flex'}"
            draggable="false"
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
</div>

{#if menu}
  <div class="popover fixed z-50 w-48" style="left:{menu.x}px; top:{menu.y}px" role="menu">
    {#if ws.tabs.find((x) => x.id === menu?.id)?.kind === 'query'}
      <button class="menu-item" role="menuitem" onclick={() => menu && startRename(menu.id)}>{tr('tabs.rename')}</button>
    {/if}
    <button class="menu-item" role="menuitem" onclick={() => menu && closeTab(menu.id)}>{tr('tabs.close')}</button>
    <button class="menu-item" role="menuitem" onclick={() => menu && closeOthers(menu.id)}>{tr('tabs.closeOthers')}</button>
  </div>
{/if}
