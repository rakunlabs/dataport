<script lang="ts">
  // One row read from top to bottom, as DBeaver's record view: each column
  // with its value, a cell to edit as in the grid, and arrows to the next
  // and previous rows.
  import { untrack } from 'svelte';
  import { ChevronLeft, ChevronRight, ArrowDownAZ, Search, X } from 'lucide-svelte';
  import type { Column } from '@/lib/api/types';
  import type { EditState } from '@/lib/stores/edits.svelte';
  import type { Paint } from '@/lib/helper/rowColors';
  import { t, fmtNumber } from '@/lib/i18n/index.svelte';
  import { display, typeColor } from '@/lib/components/result/grid';

  let {
    columns,
    rows,
    row = $bindable(0),
    rowOffset = 0,
    edit,
    editable = false,
    paint,
    oncontext,
    onselect,
  }: {
    columns: Column[];
    rows: unknown[][];
    row?: number;
    rowOffset?: number;
    edit?: EditState;
    editable?: boolean;
    paint?: (row: number) => Paint | null;
    oncontext?: (e: MouseEvent, row: number, col: number) => void;
    onselect?: (row: number, col: number) => void;
  } = $props();

  let sorted = $state(false);
  let filter = $state('');
  let selectedCol = $state(0);
  let editing = $state<{ col: number; value: string; isNull: boolean } | null>(null);

  const total = $derived(rows.length + (edit?.inserted.length ?? 0));
  const isNew = $derived(row >= rows.length);
  const removed = $derived(!isNew && !!edit?.deleted.has(row));
  const colors = $derived(paint?.(row));

  // Columns in their order, or by name; the filter keeps those whose name
  // or value has the text.
  const order = $derived.by(() => {
    let idx = columns.map((_, i) => i);
    if (sorted) idx = idx.sort((a, b) => columns[a].name.localeCompare(columns[b].name, undefined, { sensitivity: 'base' }));
    const f = filter.trim().toLowerCase();
    if (f) idx = idx.filter((i) => columns[i].name.toLowerCase().includes(f) || display(value(i)).toLowerCase().includes(f));
    return idx;
  });

  $effect(() => {
    if (row >= total && total > 0) row = total - 1;
  });

  $effect(() => {
    const r = row;
    const c = selectedCol;
    untrack(() => onselect?.(r, c));
  });

  function value(c: number): unknown {
    if (isNew) return edit?.inserted[row - rows.length]?.[c];
    const original = rows[row]?.[c];
    return edit ? edit.value(row, c, original) : original;
  }

  function go(r: number) {
    editing = null;
    row = Math.max(0, Math.min(total - 1, r));
  }

  function canEdit() {
    return editable && !!edit && !removed;
  }

  function begin(c: number) {
    selectedCol = c;
    if (!canEdit()) return;
    const v = value(c);
    editing = { col: c, value: v === null || v === undefined ? '' : display(v), isNull: v === null || v === undefined };
  }

  function commit() {
    if (!editing || !edit) return;
    const v = editing.isNull && editing.value === '' ? null : editing.value;
    if (isNew) edit.setNew(row - rows.length, editing.col, v);
    else edit.set(row, editing.col, v, rows[row][editing.col]);
    editing = null;
  }

  function onkeydown(e: KeyboardEvent) {
    if (editing || (e.target as HTMLElement).tagName === 'INPUT') return;
    if (e.key === 'ArrowLeft' || e.key === 'PageUp') {
      go(row - 1);
      e.preventDefault();
    } else if (e.key === 'ArrowRight' || e.key === 'PageDown') {
      go(row + 1);
      e.preventDefault();
    } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      const at = order.indexOf(selectedCol);
      const next = order[Math.max(0, Math.min(order.length - 1, at + (e.key === 'ArrowDown' ? 1 : -1)))];
      if (next !== undefined) selectedCol = next;
      e.preventDefault();
    } else if (e.key === 'Enter' || e.key === 'F2') {
      begin(selectedCol);
      e.preventDefault();
    }
  }

  function autofocus(node: HTMLInputElement) {
    node.focus();
    node.select();
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div class="flex h-full min-h-0 flex-col outline-none" tabindex="0" role="grid" aria-label={t('record.title')} {onkeydown}>
  <div class="flex h-[30px] shrink-0 items-center gap-1 border-b border-line bg-panel px-2 text-xs">
    <button class="icon-btn size-6" onclick={() => go(row - 1)} disabled={row <= 0} title={t('record.prev')} aria-label={t('record.prev')}><ChevronLeft size={14} /></button>
    <span class="min-w-24 text-center font-medium tabular-nums {isNew ? 'text-row-new-fg' : removed ? 'text-danger line-through' : ''}">
      {isNew ? t('record.newRow') : t('record.row', { n: fmtNumber(rowOffset + row + 1) })}
      <span class="font-normal text-faint">/ {fmtNumber(rowOffset + total)}</span>
    </span>
    <button class="icon-btn size-6" onclick={() => go(row + 1)} disabled={row >= total - 1} title={t('record.next')} aria-label={t('record.next')}><ChevronRight size={14} /></button>
    <div class="toolbar-sep"></div>
    <label class="flex h-6 min-w-0 flex-1 items-center gap-1.5 rounded-md border border-line bg-bg px-2 sm:max-w-64">
      <Search size={12} class="shrink-0 text-faint" />
      <input class="min-w-0 flex-1 bg-transparent text-xs outline-none" placeholder={t('record.filter')} bind:value={filter} />
      {#if filter}<button class="text-faint hover:text-fg" onclick={() => (filter = '')} aria-label={t('common.clear')}><X size={11} /></button>{/if}
    </label>
    <div class="flex-1"></div>
    <button class="icon-btn size-6" aria-pressed={sorted} onclick={() => (sorted = !sorted)} title={t('record.sortByName')} aria-label={t('record.sortByName')}><ArrowDownAZ size={14} /></button>
  </div>

  <div class="min-h-0 flex-1 overflow-auto" style={colors?.row ? `background-image:linear-gradient(${colors.row}, ${colors.row})` : undefined}>
    {#if !total}
      <p class="p-3 text-sm text-muted">{t('result.noRows')}</p>
    {:else}
    <table class="w-full border-collapse text-xs">
      <thead class="sticky top-0 z-10 bg-grid-head">
        <tr>
          <th class="w-[30%] max-w-80 border-r border-b border-line px-3 py-1 text-left font-medium text-faint">{t('record.column')}</th>
          <th class="border-b border-line px-3 py-1 text-left font-medium text-faint">{t('record.value')}</th>
        </tr>
      </thead>
      <tbody>
        {#each order as c (c)}
          {@const v = value(c)}
          {@const dirty = !isNew && !!edit?.isDirty(row, c)}
          {@const cellColor = colors?.cells?.[c]}
          <tr
            class="h-[26px] {selectedCol === c ? 'bg-row-active' : 'hover:bg-hover/70'}"
            onclick={() => (selectedCol = c)}
            ondblclick={() => begin(c)}
            oncontextmenu={(e) => {
              if (!oncontext) return;
              e.preventDefault();
              selectedCol = c;
              oncontext(e, row, c);
            }}
          >
            <td class="border-r border-b border-line-soft px-3 align-middle whitespace-nowrap">
              <span class="font-semibold">{columns[c].name}</span>
              <span class="ml-1.5 font-mono text-2xs" style="color:{typeColor(columns[c].type)}">{columns[c].type}</span>
            </td>
            <td class="relative border-b border-line-soft px-3 font-mono {dirty ? 'bg-cell-dirty' : ''}" style={cellColor && !dirty ? `background:${cellColor}` : undefined}>
              {#if editing?.col === c}
                <input
                  class="absolute inset-0 z-20 border-2 border-focus bg-bg px-2.5 font-mono text-xs text-fg outline-none"
                  placeholder={editing.isNull ? 'NULL' : ''}
                  bind:value={editing.value}
                  oninput={() => editing && (editing.isNull = false)}
                  onkeydown={(e) => {
                    if (e.key === 'Enter') {
                      e.preventDefault();
                      commit();
                    } else if (e.key === 'Escape') {
                      e.preventDefault();
                      editing = null;
                    }
                    e.stopPropagation();
                  }}
                  onblur={commit}
                  use:autofocus
                />
              {:else if v === null || v === undefined}
                <span class="text-faint italic">{isNew ? t('grid.default') : '<null>'}</span>
              {:else}
                <span class="break-all whitespace-pre-wrap">{display(v).length > 2000 ? display(v).slice(0, 2000) + '…' : display(v)}</span>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
    {/if}
  </div>
</div>
