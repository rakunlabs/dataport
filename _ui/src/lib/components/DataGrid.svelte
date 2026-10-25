<script lang="ts">
  import { ArrowDown, ArrowUp, ArrowUpDown } from 'lucide-svelte';
  import type { Column } from '@/lib/api/types';
  import type { EditState } from '@/lib/stores/edits.svelte';
  import { toast } from '@/lib/stores/toast.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { formatRows } from '@/lib/helper/copy';

  let {
    columns,
    rows,
    rowOffset = 0,
    sortColumn = '',
    sortDesc = false,
    onsort,
    oncell,
    edit,
    editable = false,
    oncontext,
  }: {
    columns: Column[];
    rows: unknown[][];
    rowOffset?: number;
    sortColumn?: string;
    sortDesc?: boolean;
    onsort?: (column: string) => void;
    oncell?: (row: number, col: number) => void;
    // Pending edits of a table tab; rows past rows.length are new rows.
    edit?: EditState;
    editable?: boolean;
    oncontext?: (e: MouseEvent, row: number, col: number) => void;
  } = $props();

  const rowHeight = 26;
  const overscan = 12;
  const numberWidth = 52;

  let scroller: HTMLDivElement;
  let scrollTop = $state(0);
  let viewport = $state(400);
  let widths = $state<number[]>([]);
  let selected = $state<{ row: number; col: number } | null>(null);
  // Selected rows for delete and clone, by display index.
  let selectedRows = $state(new Set<number>());
  let anchor = -1;
  // typed is true when editing started by typing a character, so the
  // editor keeps it instead of selecting it.
  let editing = $state<{ row: number; col: number; value: string; isNull: boolean; typed: boolean } | null>(null);

  // Measure initial column widths from the header and the first rows.
  $effect(() => {
    const cols = columns;
    const sample = rows.slice(0, 100);
    widths = cols.map((c, i) => {
      let w = c.name.length * 7.4 + Math.min(c.type.length, 14) * 6.2 + 44;
      for (const r of sample) {
        const len = display(r[i]).length;
        w = Math.max(w, Math.min(len, 60) * 7.2 + 24);
      }
      return Math.round(Math.min(Math.max(w, 72), 420));
    });
    selected = null;
    selectedRows = new Set();
    editing = null;
  });

  const newRows = $derived(edit?.inserted ?? []);
  const total = $derived(rows.length + newRows.length);
  const start = $derived(Math.max(0, Math.floor(scrollTop / rowHeight) - overscan));
  const end = $derived(Math.min(total, Math.ceil((scrollTop + viewport) / rowHeight) + overscan));
  const visibleIdx = $derived(Array.from({ length: Math.max(0, end - start) }, (_, i) => start + i));
  const template = $derived(`${numberWidth}px ${widths.map((w) => w + 'px').join(' ')}`);
  const fullWidth = $derived(numberWidth + widths.reduce((a, b) => a + b, 0));

  function isNew(r: number) {
    return r >= rows.length;
  }

  function cellValue(r: number, c: number): unknown {
    if (isNew(r)) return newRows[r - rows.length]?.[c];
    const original = rows[r][c];
    return edit ? edit.value(r, c, original) : original;
  }

  function display(v: unknown): string {
    if (v === null || v === undefined) return 'NULL';
    if (typeof v === 'object') return JSON.stringify(v);
    return String(v);
  }

  function isNumeric(type: string) {
    return /^(u?int|integer|bigint|smallint|tinyint|mediumint|numeric|decimal|real|double|float|serial|bigserial|money|int2|int4|int8|float4|float8|nullable\((u?int|decimal|float))/i.test(type);
  }

  function typeColor(type: string): string {
    const ty = type.toLowerCase();
    if (/(int|serial)/.test(ty)) return 'var(--color-type-int)';
    if (/(numeric|decimal|real|double|float|money)/.test(ty)) return 'var(--color-type-num)';
    if (/(bool|bit)/.test(ty)) return 'var(--color-type-bool)';
    if (/(date|time)/.test(ty)) return 'var(--color-type-time)';
    if (/(json|xml|array|map|tuple)/.test(ty)) return 'var(--color-type-struct)';
    if (/(uuid|uniqueidentifier)/.test(ty)) return 'var(--color-type-id)';
    if (/(bytea|blob|binary)/.test(ty)) return 'var(--color-type-bin)';
    return 'var(--color-type-text)';
  }

  function startResize(e: PointerEvent, i: number) {
    e.preventDefault();
    e.stopPropagation();
    const x0 = e.clientX;
    const w0 = widths[i];
    const move = (ev: PointerEvent) => {
      widths[i] = Math.max(48, w0 + ev.clientX - x0);
    };
    const up = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast(t('grid.copied'), 'success', 1200);
    } catch {
      toast(t('grid.clipboardUnavailable'), 'error');
    }
  }

  // ////////////////////////////////////////////
  // selection

  function selectCell(e: MouseEvent, r: number, c: number) {
    selected = { row: r, col: c };
    if (e.shiftKey && anchor >= 0) {
      const next = new Set<number>();
      for (let i = Math.min(anchor, r); i <= Math.max(anchor, r); i++) next.add(i);
      selectedRows = next;
    } else if (e.metaKey || e.ctrlKey) {
      const next = new Set(selectedRows);
      if (next.has(r)) next.delete(r);
      else next.add(r);
      selectedRows = next;
      anchor = r;
    } else {
      selectedRows = new Set([r]);
      anchor = r;
    }
  }

  export function selection(): { existing: number[]; added: number[] } {
    const list = selectedRows.size ? [...selectedRows] : selected ? [selected.row] : [];
    return {
      existing: list.filter((r) => !isNew(r)).sort((a, b) => a - b),
      added: list.filter(isNew).map((r) => r - rows.length).sort((a, b) => b - a),
    };
  }

  // selectedValues returns the selected rows as shown, edits included, in
  // display order, and the column of the selected cell.
  export function selectedValues(): { rows: unknown[][]; col: number } {
    const list = (selectedRows.size ? [...selectedRows] : selected ? [selected.row] : []).sort((a, b) => a - b);
    return { rows: list.map((r) => columns.map((_, c) => cellValue(r, c))), col: selected?.col ?? 0 };
  }

  export function focusCell(row: number, col = 0) {
    selected = { row, col };
    selectedRows = new Set([row]);
    anchor = row;
    const top = row * rowHeight;
    if (top < scroller.scrollTop || top > scroller.scrollTop + viewport - rowHeight * 2) scroller.scrollTop = Math.max(0, top - viewport / 2);
    scroller.focus();
  }

  // ////////////////////////////////////////////
  // inline editing

  function canEdit(r: number) {
    return editable && edit && !(edit.deleted.has(r) && !isNew(r));
  }

  export function beginEdit(r: number, c: number, initial?: string) {
    if (!canEdit(r)) {
      oncell?.(r, c);
      return;
    }
    const v = cellValue(r, c);
    editing = {
      row: r,
      col: c,
      value: initial ?? (v === null || v === undefined ? '' : display(v)),
      isNull: initial === undefined && (v === null || v === undefined),
      typed: initial !== undefined,
    };
  }

  function commitEdit() {
    if (!editing || !edit) return;
    const { row, col, value, isNull } = editing;
    const v = isNull && value === '' ? null : value;
    if (isNew(row)) edit.setNew(row - rows.length, col, v);
    else edit.set(row, col, v, rows[row][col]);
    editing = null;
    scroller.focus();
  }

  function cancelEdit() {
    editing = null;
    scroller.focus();
  }

  export function setNull() {
    if (!edit || !selected || !canEdit(selected.row)) return;
    const { row, col } = selected;
    if (isNew(row)) edit.setNew(row - rows.length, col, null);
    else edit.set(row, col, null, rows[row][col]);
  }

  function editorKey(e: KeyboardEvent) {
    if (e.isComposing) return;
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      commitEdit();
      if (selected) move(selected.row + 1, selected.col);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      cancelEdit();
    } else if (e.key === 'Tab') {
      e.preventDefault();
      commitEdit();
      if (selected) move(selected.row, selected.col + (e.shiftKey ? -1 : 1));
    }
    e.stopPropagation();
  }

  function autofocus(node: HTMLInputElement | HTMLTextAreaElement) {
    node.focus();
    if (editing?.typed) node.setSelectionRange(node.value.length, node.value.length);
    else node.select();
  }

  function move(r: number, c: number) {
    const nr = Math.min(Math.max(r, 0), total - 1);
    const nc = Math.min(Math.max(c, 0), columns.length - 1);
    selected = { row: nr, col: nc };
    selectedRows = new Set([nr]);
    anchor = nr;
    const top = nr * rowHeight;
    if (top < scroller.scrollTop) scroller.scrollTop = top;
    else if (top + rowHeight > scroller.scrollTop + viewport - rowHeight) scroller.scrollTop = top - viewport + rowHeight * 2;
  }

  function onkeydown(e: KeyboardEvent) {
    if (!selected || editing) return;
    const { row, col } = selected;
    const handled = () => e.preventDefault();

    if ((e.metaKey || e.ctrlKey) && e.key === 'c') {
      // Several rows copy as TSV, which pastes into spreadsheets; one row
      // copies the selected cell.
      if (selectedRows.size > 1) void copy(formatRows('tsv', { columns, rows: selectedValues().rows, dbType: '' }));
      else void copy(display(cellValue(row, col)));
      handled();
    } else if (e.key === 'ArrowDown') {
      move(row + 1, col);
      handled();
    } else if (e.key === 'ArrowUp') {
      move(row - 1, col);
      handled();
    } else if (e.key === 'ArrowLeft') {
      move(row, col - 1);
      handled();
    } else if (e.key === 'ArrowRight' || e.key === 'Tab') {
      move(row, col + (e.shiftKey && e.key === 'Tab' ? -1 : 1));
      handled();
    } else if (e.key === 'Enter' || e.key === 'F2') {
      beginEdit(row, col);
      handled();
    } else if (editable && e.key.length === 1 && !e.metaKey && !e.ctrlKey && !e.altKey) {
      // Typing on a selected cell starts editing with that character.
      beginEdit(row, col, e.key);
      handled();
    }
  }
</script>

<div
  class="relative h-full min-h-0 overflow-auto outline-none"
  bind:this={scroller}
  bind:clientHeight={viewport}
  onscroll={() => (scrollTop = scroller.scrollTop)}
  tabindex="0"
  role="grid"
  aria-rowcount={total}
  {onkeydown}
>
  <div style="width:{fullWidth}px; min-width:100%">
    <!-- header -->
    <div class="sticky top-0 z-10 grid border-b border-line bg-grid-head" style="grid-template-columns:{template}; height:28px">
      <div class="sticky left-0 z-10 flex items-center justify-end border-r border-line bg-grid-head pr-2 text-2xs text-faint"></div>
      {#each columns as col, i (i)}
        {@const sorted = sortColumn === col.name}
        <div
          class="group relative flex min-w-0 items-center border-r border-line-soft {onsort ? 'hover:bg-hover' : ''}"
          role="columnheader"
          aria-sort={sorted ? (sortDesc ? 'descending' : 'ascending') : undefined}
        >
          <button
            class="flex h-full min-w-0 flex-1 items-center gap-1.5 px-2 text-left"
            onclick={() => onsort?.(col.name)}
            disabled={!onsort}
            title="{col.name} · {col.type || '?'}"
          >
            <span class="truncate text-xs font-semibold text-fg">{col.name}</span>
            <span class="shrink truncate font-mono text-2xs" style="color:{typeColor(col.type)}">{col.type}</span>
            {#if onsort}
              <span class="ml-auto shrink-0">
                {#if sorted}
                  {#if sortDesc}<ArrowDown size={12} class="text-accent" />{:else}<ArrowUp size={12} class="text-accent" />{/if}
                {:else}
                  <ArrowUpDown size={12} class="text-faint opacity-0 group-hover:opacity-100" />
                {/if}
              </span>
            {/if}
          </button>
          <div
            class="absolute top-0 right-[-3px] z-10 h-full w-[6px] cursor-col-resize hover:bg-accent/50"
            onpointerdown={(e) => startResize(e, i)}
            role="separator"
            aria-orientation="vertical"
          ></div>
        </div>
      {/each}
    </div>

    <!-- body -->
    <div class="relative" style="height:{total * rowHeight}px">
      {#each visibleIdx as r (r)}
        {@const added = isNew(r)}
        {@const removed = !added && !!edit?.deleted.has(r)}
        {@const rowSel = selectedRows.has(r)}
        <div
          class="absolute right-0 left-0 grid border-b border-line-soft
            {added ? 'bg-row-new' : removed ? 'bg-row-deleted text-faint line-through' : rowSel ? 'bg-row-active' : r % 2 ? 'bg-row-stripe' : 'bg-bg'}
            {added || removed || rowSel ? '' : 'hover:bg-hover/70'}"
          style="grid-template-columns:{template}; height:{rowHeight}px; transform:translateY({r * rowHeight}px)"
          oncontextmenu={(e) => {
            if (!oncontext) return;
            e.preventDefault();
            const cell = (e.target as HTMLElement).closest('[data-col]') as HTMLElement | null;
            const col = cell ? Number(cell.dataset.col) : (selected?.col ?? 0);
            if (!selectedRows.has(r)) {
              selectedRows = new Set([r]);
              anchor = r;
            }
            selected = { row: r, col };
            oncontext(e, r, col);
          }}
          role="row"
          tabindex="-1"
        >
          <div
            class="sticky left-0 flex items-center justify-end border-r border-line pr-2 text-2xs tabular-nums no-underline
              {added ? 'bg-row-new font-semibold text-row-new-fg' : removed ? 'bg-row-deleted text-danger' : edit?.dirty.has(r) ? 'bg-grid-head font-semibold text-row-dirty-fg' : rowSel ? 'bg-row-active text-fg' : 'bg-grid-head text-faint'}"
          >
            {added ? '+' : rowOffset + r + 1}
          </div>
          {#each columns as column, c (c)}
            {@const value = cellValue(r, c)}
            {@const isSel = selected?.row === r && selected?.col === c}
            {@const dirty = !added && !!edit?.isDirty(r, c)}
            {@const text = display(value)}
            <div
              class="relative flex min-w-0 items-center border-r border-line-soft px-2 font-mono text-xs
                {dirty ? 'bg-cell-dirty' : ''}
                {isSel ? 'outline-2 -outline-offset-2 outline-focus' : ''}
                {isNumeric(column.type) ? 'justify-end tabular-nums' : ''}"
              onclick={(e) => selectCell(e, r, c)}
              onmousedown={(e) => e.shiftKey && e.preventDefault()}
              ondblclick={() => beginEdit(r, c)}
              role="gridcell"
              data-col={c}
              tabindex="-1"
              onkeydown={() => {}}
              title={text.length > 60 ? text.slice(0, 2000) : undefined}
            >
              {#if editing && editing.row === r && editing.col === c}
                {#if editing.value.includes('\n') || editing.value.length > 80}
                  <textarea
                    class="absolute top-0 left-0 z-20 h-40 w-[max(100%,28rem)] resize rounded-md border border-focus bg-bg p-2 font-mono text-xs text-fg shadow-dialog outline-none"
                    bind:value={editing.value}
                    onkeydown={(e) => {
                      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
                        e.preventDefault();
                        commitEdit();
                      } else if (e.key === 'Escape') {
                        e.preventDefault();
                        cancelEdit();
                      }
                      e.stopPropagation();
                    }}
                    onblur={commitEdit}
                    use:autofocus
                  ></textarea>
                {:else}
                  <input
                    class="absolute inset-0 z-20 border-2 border-focus bg-bg px-1.5 font-mono text-xs text-fg outline-none"
                    placeholder={editing.isNull ? 'NULL' : ''}
                    bind:value={editing.value}
                    oninput={() => editing && (editing.isNull = false)}
                    onkeydown={editorKey}
                    onblur={commitEdit}
                    use:autofocus
                  />
                {/if}
              {:else if value === null || value === undefined}
                <span class="text-faint italic">{added ? t('grid.default') : '<null>'}</span>
              {:else if typeof value === 'boolean'}
                <span style="color:var(--color-type-bool)">{text}</span>
              {:else}
                <span class="truncate">{text.length > 300 ? text.slice(0, 300) + '…' : text}</span>
              {/if}
            </div>
          {/each}
        </div>
      {/each}
    </div>
  </div>
</div>
