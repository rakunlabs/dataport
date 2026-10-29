<script lang="ts">
  import { untrack } from 'svelte';
  import type { Column } from '@/lib/api/types';
  import type { EditState } from '@/lib/stores/edits.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { formatRows } from '@/lib/helper/copy';
  import { copyText } from '@/lib/helper/clipboard';
  import DataGridHeader from '@/lib/components/result/DataGridHeader.svelte';
  import { display, isJsonType, isNumeric } from '@/lib/components/result/grid';
  import JsonEditor from '@/lib/components/editor/JsonEditor.svelte';
  import type { Paint } from '@/lib/helper/rowColors';

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
    onselect,
    onrange,
    paint,
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
    // Called when the selected cell changes, for the value panel.
    onselect?: (row: number, col: number) => void;
    // Called when the selected rows or columns change, for the calculator.
    onrange?: (rows: number[], cols: number[]) => void;
    // The colours of a row, from row colour rules.
    paint?: (row: number) => Paint | null;
  } = $props();

  const rowHeight = 26;
  const overscan = 12;
  const numberWidth = 52;

  let scroller: HTMLDivElement;
  let scrollTop = $state(0);
  let viewport = $state(400);
  let widths = $state<number[]>([]);
  let selected = $state<{ row: number; col: number } | null>(null);

  $effect(() => {
    const s = selected;
    if (s) untrack(() => onselect?.(s.row, s.col));
  });

  // cellAt is what a cell shows, pending edits included.
  export function cellAt(r: number, c: number): unknown {
    return cellValue(r, c);
  }

  // setCell changes a cell as typing in it would.
  export function setCell(r: number, c: number, v: unknown) {
    if (!edit || !canEdit(r)) return;
    if (isNew(r)) edit.setNew(r - rows.length, c, v);
    else edit.set(r, c, v, rows[r][c]);
  }

  export function canEditCell(r: number): boolean {
    return !!canEdit(r);
  }
  // Selected rows for delete and clone, by display index; with the
  // selected columns they make the cells the calculator reads.
  let selectedRows = $state(new Set<number>());
  let selectedCols = $state(new Set<number>());
  let anchor = -1;
  let anchorCol = -1;
  // Set while the mouse drags a selection from a cell.
  let dragging = false;
  // Set while the mouse drags over row numbers.
  let rowDragging = false;

  $effect(() => {
    const rs = [...selectedRows].sort((a, b) => a - b);
    const cs = [...selectedCols].sort((a, b) => a - b);
    untrack(() => onrange?.(rs, cs));
  });

  const range = (a: number, b: number) => {
    const out = new Set<number>();
    for (let i = Math.min(a, b); i <= Math.max(a, b); i++) out.add(i);
    return out;
  };
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
    selectedCols = new Set();
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

  // ////////////////////////////////////////////
  // selection

  // Shift selects the rectangle from the anchor; Ctrl adds or removes a row
  // and adds the column.
  function selectCell(e: MouseEvent, r: number, c: number) {
    selected = { row: r, col: c };
    if (e.shiftKey && anchor >= 0) {
      selectedRows = range(anchor, r);
      selectedCols = range(anchorCol < 0 ? c : anchorCol, c);
    } else if (e.metaKey || e.ctrlKey) {
      const next = new Set(selectedRows);
      if (next.has(r)) next.delete(r);
      else next.add(r);
      selectedRows = next;
      selectedCols = new Set([...selectedCols, c]);
      anchor = r;
      anchorCol = c;
    } else {
      selectedRows = new Set([r]);
      selectedCols = new Set([c]);
      anchor = r;
      anchorCol = c;
    }
  }

  function dragStart(e: MouseEvent, r: number, c: number) {
    if (e.button !== 0 || e.shiftKey || e.metaKey || e.ctrlKey) return;
    if ((e.target as HTMLElement).closest('input, textarea, .cm-editor')) return;
    // No text selection while dragging over cells; the grid keeps the keys.
    e.preventDefault();
    if (editing) commitEdit();
    scroller.focus();
    dragging = true;
    const up = () => {
      dragging = false;
      window.removeEventListener('mouseup', up);
    };
    window.addEventListener('mouseup', up);
    selectCell(e, r, c);
  }

  function dragOver(r: number, c: number) {
    if (!dragging || anchor < 0) return;
    selected = { row: r, col: c };
    selectedRows = range(anchor, r);
    selectedCols = range(anchorCol, c);
  }

  export function selectAll() {
    if (!total || !columns.length) return;
    selectedRows = range(0, total - 1);
    selectedCols = range(0, columns.length - 1);
    if (!selected) selected = { row: 0, col: 0 };
  }

  // The selected columns are whole while every row is selected.
  const wholeCols = $derived(total > 0 && selectedRows.size === total);
  // Columns picked in the header, not every column: the rows are not
  // highlighted and row actions such as delete keep to the selected cell's.
  const columnPick = $derived(wholeCols && selectedCols.size < columns.length);

  // selectColumn selects every row of a column. Shift selects the columns
  // from the last one picked; Ctrl or Cmd adds a column or takes it away,
  // and taking away the last one clears the selection.
  function selectColumn(e: MouseEvent, c: number) {
    if (!total) return;
    if (editing) commitEdit();
    let cols: Set<number>;
    if (e.shiftKey && wholeCols && anchorCol >= 0) {
      cols = range(anchorCol, c);
    } else if ((e.metaKey || e.ctrlKey) && wholeCols) {
      cols = new Set(selectedCols);
      if (cols.has(c)) cols.delete(c);
      else cols.add(c);
      anchorCol = c;
    } else {
      cols = new Set([c]);
      anchorCol = c;
    }
    if (!cols.size) {
      selectedRows = new Set();
      selectedCols = new Set();
      anchor = -1;
      anchorCol = -1;
      scroller.focus();
      return;
    }
    selectedRows = range(0, total - 1);
    selectedCols = cols;
    anchor = 0;
    const col = cols.has(c) ? c : selected && cols.has(selected.col) ? selected.col : Math.min(...cols);
    selected = { row: selected && cols.has(selected.col) ? selected.row : 0, col };
    scroller.focus();
  }

  // selectRow selects whole rows from the row number: Shift selects from
  // the last row picked, Ctrl or Cmd adds a row or takes it away, and a
  // drag over the numbers selects the rows passed.
  function selectRow(e: MouseEvent, r: number) {
    if (e.button !== 0 || !columns.length) return;
    e.preventDefault();
    if (editing) commitEdit();
    const all = range(0, columns.length - 1);
    const col = selected?.col ?? 0;
    if (e.shiftKey && anchor >= 0) {
      selectedRows = range(anchor, r);
    } else if (e.metaKey || e.ctrlKey) {
      const next = new Set(selectedRows);
      if (next.has(r) && selectedCols.size === columns.length) next.delete(r);
      else next.add(r);
      selectedRows = next;
      anchor = r;
    } else {
      selectedRows = new Set([r]);
      anchor = r;
      rowDragging = true;
      const up = () => {
        rowDragging = false;
        window.removeEventListener('mouseup', up);
      };
      window.addEventListener('mouseup', up);
    }
    selectedCols = all;
    anchorCol = 0;
    selected = { row: r, col };
    scroller.focus();
  }

  function rowDragOver(r: number) {
    if (!rowDragging || anchor < 0) return;
    selectedRows = range(anchor, r);
    selected = { row: r, col: selected?.col ?? 0 };
  }

  // selectedColumns lists the selected columns, at least the selected cell's.
  export function selectedColumns(): number[] {
    const cs = [...selectedCols].sort((a, b) => a - b);
    return cs.length ? cs : selected ? [selected.col] : [];
  }

  // rowValues is a row as shown, edits included.
  export function rowValues(r: number): unknown[] {
    return columns.map((_, c) => cellValue(r, c));
  }

  export function rowCount(): number {
    return total;
  }

  export function selection(): { existing: number[]; added: number[] } {
    const list = selectedRows.size && !columnPick ? [...selectedRows] : selected ? [selected.row] : [];
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

  // focusCell selects a cell and scrolls to it; with focus, the grid also
  // takes the keyboard.
  export function focusCell(row: number, col = 0, focus = true) {
    selected = { row, col };
    selectedRows = new Set([row]);
    selectedCols = new Set([col]);
    anchor = row;
    anchorCol = col;
    const top = row * rowHeight;
    if (top < scroller.scrollTop || top > scroller.scrollTop + viewport - rowHeight * 2) scroller.scrollTop = Math.max(0, top - viewport / 2);
    if (focus) scroller.focus();
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
    selectedCols = new Set([nc]);
    anchor = nr;
    anchorCol = nc;
    const top = nr * rowHeight;
    if (top < scroller.scrollTop) scroller.scrollTop = top;
    else if (top + rowHeight > scroller.scrollTop + viewport - rowHeight) scroller.scrollTop = top - viewport + rowHeight * 2;
  }

  function onkeydown(e: KeyboardEvent) {
    // Keys typed after the first, before its editor took the focus, go on
    // into it instead of being lost.
    if (editing?.typed && e.target === scroller && e.key.length === 1 && !e.metaKey && !e.ctrlKey && !e.altKey) {
      editing.value += e.key;
      e.preventDefault();
      return;
    }
    if (!selected || editing) return;
    const { row, col } = selected;
    const handled = () => e.preventDefault();

    if ((e.metaKey || e.ctrlKey) && e.key === 'c') {
      // Several cells copy as TSV, the selected columns of the selected
      // rows, which pastes into spreadsheets; one copies its value.
      if (selectedRows.size > 1 || selectedCols.size > 1) {
        const cs = selectedColumns();
        const picked = selectedValues().rows.map((r) => cs.map((c) => r[c]));
        void copyText(formatRows('tsv', { columns: cs.map((c) => columns[c]), rows: picked, dbType: '' }));
      } else void copyText(display(cellValue(row, col)));
      handled();
    } else if ((e.metaKey || e.ctrlKey) && e.key === 'a') {
      selectAll();
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
    <DataGridHeader {columns} {template} {sortColumn} {sortDesc} {onsort} selectedCols={wholeCols ? selectedCols : undefined} oncolumn={selectColumn} onresize={startResize} />

    <!-- body -->
    <div class="relative" style="height:{total * rowHeight}px">
      {#each visibleIdx as r (r)}
        {@const added = isNew(r)}
        {@const removed = !added && !!edit?.deleted.has(r)}
        {@const rowSel = selectedRows.has(r) && !columnPick}
        {@const colors = paint?.(r)}
        {@const multi = selectedRows.size > 1 || selectedCols.size > 1}
        <div
          class="absolute right-0 left-0 grid border-b border-line-soft
            {added ? 'bg-row-new' : removed ? 'bg-row-deleted text-faint line-through' : rowSel ? 'bg-row-active' : r % 2 ? 'bg-row-stripe' : 'bg-bg'}
            {added || removed || rowSel ? '' : 'hover:bg-hover/70'}"
          style="grid-template-columns:{template}; height:{rowHeight}px; transform:translateY({r * rowHeight}px){colors?.row && !added && !removed && !rowSel ? `; background-image:linear-gradient(${colors.row}, ${colors.row})` : ''}"
          oncontextmenu={(e) => {
            if (!oncontext) return;
            e.preventDefault();
            const cell = (e.target as HTMLElement).closest('[data-col]') as HTMLElement | null;
            const col = cell ? Number(cell.dataset.col) : (selected?.col ?? 0);
            if (!selectedRows.has(r)) {
              selectedRows = new Set([r]);
              selectedCols = new Set([col]);
              anchor = r;
              anchorCol = col;
            } else if (!selectedCols.has(col)) {
              selectedCols = new Set([...selectedCols, col]);
            }
            selected = { row: r, col };
            oncontext(e, r, col);
          }}
          role="row"
          tabindex="-1"
        >
          <div
            class="sticky left-0 z-[1] flex cursor-pointer items-center justify-end border-r border-line pr-2 text-2xs tabular-nums no-underline select-none
              {added ? 'bg-row-new font-semibold text-row-new-fg' : removed ? 'bg-row-deleted text-danger' : edit?.dirty.has(r) ? 'bg-grid-head font-semibold text-row-dirty-fg' : rowSel ? 'bg-row-active text-fg' : 'bg-grid-head text-faint hover:text-fg'}"
            onmousedown={(e) => selectRow(e, r)}
            onmouseenter={() => rowDragOver(r)}
            role="rowheader"
            tabindex="-1"
          >
            {added ? '+' : rowOffset + r + 1}
          </div>
          {#each columns as column, c (c)}
            {@const value = cellValue(r, c)}
            {@const isSel = selected?.row === r && selected?.col === c}
            {@const dirty = !added && !!edit?.isDirty(r, c)}
            {@const text = display(value)}
            {@const inRange = multi && selectedRows.has(r) && selectedCols.has(c)}
            {@const cellColor = colors?.cells?.[c]}
            <div
              class="relative flex min-w-0 items-center border-r border-line-soft px-2 font-mono text-xs
                {dirty ? 'bg-cell-dirty' : inRange ? 'bg-accent/15' : ''}
                {isSel ? 'outline-2 -outline-offset-2 outline-focus' : ''}
                {isNumeric(column.type) ? 'justify-end tabular-nums' : ''}"
              style={cellColor && !dirty && !inRange ? `background:${cellColor}` : undefined}
              onclick={(e) => (e.shiftKey || e.metaKey || e.ctrlKey) && selectCell(e, r, c)}
              onmousedown={(e) => {
                if (e.shiftKey) e.preventDefault();
                dragStart(e, r, c);
              }}
              onmouseenter={() => dragOver(r, c)}
              ondblclick={(e) => !(e.target as HTMLElement).closest('input, textarea, .cm-editor') && beginEdit(r, c)}
              role="gridcell"
              data-col={c}
              tabindex="-1"
              onkeydown={() => {}}
              title={text.length > 60 ? text.slice(0, 2000) : undefined}
            >
              {#if editing && editing.row === r && editing.col === c}
                {#if isJsonType(column.type)}
                  <div class="absolute top-0 left-0 z-20 h-48 w-[max(100%,32rem)] resize overflow-hidden rounded-md border border-focus bg-bg shadow-dialog">
                    <JsonEditor bind:value={editing.value} cursor={editing.typed ? 'end' : 'all'} onapply={commitEdit} oncancel={cancelEdit} onblur={commitEdit} />
                  </div>
                {:else if editing.value.includes('\n') || editing.value.length > 80}
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
