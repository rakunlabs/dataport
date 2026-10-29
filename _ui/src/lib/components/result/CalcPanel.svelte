<script lang="ts">
  // Statistics of the selected cells, beside the grid, as DBeaver's Calc
  // panel: functions by column, or the rows grouped by some columns.
  import { Copy, X, Plus, Group, Hash } from 'lucide-svelte';
  import type { Column } from '@/lib/api/types';
  import { t, fmtNumber } from '@/lib/i18n/index.svelte';
  import { copyText } from '@/lib/helper/clipboard';
  import { calc, calcFns, groupRows, toNumber, type CalcFn } from '@/lib/helper/calc';
  import { isNumeric } from '@/lib/components/result/grid';
  import { saveValuePanel, valuePanel } from '@/lib/stores/valuePanel.svelte';

  let {
    columns,
    rows,
    cols,
    onclose,
  }: {
    columns: Column[];
    // The selected rows, as shown, and the selected columns.
    rows: unknown[][];
    cols: number[];
    onclose: () => void;
  } = $props();

  let grouped = $state(false);
  let duplicates = $state(false);
  let adding = $state(false);
  // Columns the user turned from group keys into aggregated ones, or back.
  let flipped = $state(new Set<string>());

  const fns = $derived(valuePanel.calcFns);
  const missing = $derived(calcFns.filter((f) => !fns.includes(f)));

  function addFn(f: CalcFn) {
    valuePanel.calcFns = [...fns, f];
    saveValuePanel();
    adding = false;
  }

  function removeFn(f: CalcFn) {
    valuePanel.calcFns = fns.filter((x) => x !== f);
    saveValuePanel();
  }

  function toggleNumeric() {
    valuePanel.calcNumeric = !valuePanel.calcNumeric;
    saveValuePanel();
  }

  const summary = $derived(
    fns.map((f) => ({
      fn: f,
      values: cols.map((c) => {
        const vals = rows.map((r) => r[c]);
        // Numbers only: text columns show no sum, average or extremes.
        const numericCol = isNumeric(columns[c]?.type ?? '') || vals.every((v) => v === null || v === undefined || toNumber(v) !== null);
        if (valuePanel.calcNumeric && !numericCol && !['count', 'distinct', 'nulls', 'mode'].includes(f)) return null;
        return calc(f, vals, valuePanel.calcNumeric);
      }),
    })),
  );

  // Text columns group the rows and numeric ones are aggregated; a click
  // swaps a column. With only numeric columns, all of them group.
  const numericCols = $derived(new Set(cols.filter((c) => isNumeric(columns[c]?.type ?? ''))));
  const keyCols = $derived.by(() => {
    const allNumeric = cols.every((c) => numericCols.has(c));
    return cols.filter((c) => (allNumeric || !numericCols.has(c)) !== flipped.has(columns[c]?.name));
  });
  const aggCols = $derived(cols.filter((c) => !keyCols.includes(c)));
  const aggFns = $derived(fns.filter((f) => f !== 'count'));
  const groups = $derived.by(() => {
    if (!grouped) return [];
    const aggs = aggCols.flatMap((col) => aggFns.map((fn) => ({ fn, col })));
    let list = groupRows(rows, keyCols, aggs);
    if (duplicates) list = list.filter((g) => g.count > 1);
    return list.sort((a, b) => b.count - a.count);
  });

  function flip(c: number) {
    const name = columns[c]?.name;
    const next = new Set(flipped);
    if (next.has(name)) next.delete(name);
    else next.add(name);
    flipped = next;
  }

  function show(v: unknown): string {
    if (v === null || v === undefined) return '';
    if (typeof v === 'number') return fmtNumber(v);
    if (typeof v === 'object') return JSON.stringify(v);
    return String(v);
  }

  function raw(v: unknown): string {
    if (v === null || v === undefined) return '';
    return typeof v === 'object' ? JSON.stringify(v) : String(v);
  }

  // Copies the table shown, as TSV.
  function copyAll() {
    let lines: string[][];
    if (grouped) {
      const head = [...keyCols.map((c) => columns[c].name), 'count', '%', ...aggCols.flatMap((c) => aggFns.map((f) => `${f}(${columns[c].name})`))];
      lines = [head, ...groups.map((g) => [...g.keys.map(raw), String(g.count), pct(g.count), ...g.values.map(raw)])];
    } else {
      lines = [['', ...cols.map((c) => columns[c].name)], ...summary.map((s) => [s.fn, ...s.values.map(raw)])];
    }
    void copyText(lines.map((l) => l.join('\t')).join('\n'));
  }

  function pct(n: number) {
    return rows.length ? ((n / rows.length) * 100).toFixed(1) : '0';
  }
</script>

<svelte:window onclick={() => (adding = false)} />

<aside class="flex h-full min-w-0 flex-col border-l border-line bg-bg">
  <div class="flex h-[30px] shrink-0 items-center gap-1 border-b border-line bg-panel pr-1 pl-3">
    <span class="min-w-0 truncate text-xs text-muted">
      {t('calc.selection', { rows: fmtNumber(rows.length), cols: fmtNumber(cols.length) })}
    </span>
    <div class="flex-1"></div>
    <button class="icon-btn size-6" aria-pressed={grouped} onclick={() => (grouped = !grouped)} title={t('calc.group')} aria-label={t('calc.group')}><Group size={13} /></button>
    <button class="icon-btn size-6" aria-pressed={valuePanel.calcNumeric} onclick={toggleNumeric} title={t('calc.numericOnly')} aria-label={t('calc.numericOnly')}><Hash size={13} /></button>
    <div class="relative">
      <button class="icon-btn size-6" onclick={(e) => {
          e.stopPropagation();
          adding = !adding;
        }} disabled={!missing.length} title={t('calc.add')} aria-label={t('calc.add')}><Plus size={13} /></button>
      {#if adding}
        <div class="popover absolute top-full right-0 z-30 mt-1 w-44">
          {#each missing as f (f)}
            <button class="menu-item" onclick={() => addFn(f)}>{t(`calc.fn.${f}`)}</button>
          {/each}
        </div>
      {/if}
    </div>
    <button class="icon-btn size-6" onclick={copyAll} title={t('calc.copyAll')} aria-label={t('calc.copyAll')}><Copy size={13} /></button>
    <button class="icon-btn size-6" onclick={onclose} title={t('valuePanel.close')} aria-label={t('valuePanel.close')}><X size={14} /></button>
  </div>

  <div class="min-h-0 flex-1 overflow-auto">
    {#if !rows.length || !cols.length}
      <p class="p-3 text-xs text-faint">{t('calc.empty')}</p>
    {:else if !grouped}
      <table class="w-full border-collapse text-xs">
        <thead class="sticky top-0 bg-grid-head">
          <tr>
            <th class="w-24 border-r border-b border-line px-2 py-1 text-left font-medium text-faint">{t('calc.function')}</th>
            {#each cols as c (c)}
              <th class="border-r border-b border-line px-2 py-1 text-right font-semibold whitespace-nowrap">{columns[c]?.name}</th>
            {/each}
          </tr>
        </thead>
        <tbody>
          {#each summary as s (s.fn)}
            <tr class="group hover:bg-hover/70">
              <td class="border-r border-b border-line-soft px-2 py-1 whitespace-nowrap text-muted">
                <span class="flex items-center gap-1">
                  {t(`calc.fn.${s.fn}`)}
                  <button class="ml-auto hidden size-4 items-center justify-center rounded-sm text-faint group-hover:flex hover:bg-active hover:text-fg" onclick={() => removeFn(s.fn)} title={t('calc.remove')} aria-label={t('calc.remove')}><X size={10} /></button>
                </span>
              </td>
              {#each s.values as v, i (i)}
                <td class="cursor-copy border-r border-b border-line-soft px-2 py-1 text-right font-mono tabular-nums" title={t('calc.copyValue')} onclick={() => copyText(raw(v))}>
                  {#if v === null || v === undefined}<span class="text-faint">–</span>{:else}{show(v)}{/if}
                </td>
              {/each}
            </tr>
          {/each}
        </tbody>
      </table>
    {:else}
      <div class="flex flex-wrap items-center gap-1 border-b border-line-soft px-2 py-1.5 text-2xs">
        <span class="text-faint">{t('calc.groupBy')}</span>
        {#each cols as c (c)}
          {@const isKey = keyCols.includes(c)}
          <button class="rounded-sm border px-1.5 py-0.5 font-mono {isKey ? 'border-accent/50 bg-accent/15 text-fg' : 'border-line text-muted'}" onclick={() => flip(c)} title={isKey ? t('calc.asKey') : t('calc.asAgg')}>
            {columns[c]?.name}
          </button>
        {/each}
        <label class="ml-auto flex items-center gap-1 text-muted">
          <input type="checkbox" bind:checked={duplicates} /> {t('calc.duplicates')}
        </label>
      </div>
      <table class="w-full border-collapse text-xs">
        <thead class="sticky top-0 bg-grid-head">
          <tr>
            {#each keyCols as c (c)}<th class="border-r border-b border-line px-2 py-1 text-left font-semibold whitespace-nowrap">{columns[c]?.name}</th>{/each}
            <th class="border-r border-b border-line px-2 py-1 text-right font-medium text-faint">{t('calc.fn.count')}</th>
            <th class="border-r border-b border-line px-2 py-1 text-right font-medium text-faint">%</th>
            {#each aggCols as c (c)}
              {#each aggFns as f (f)}<th class="border-r border-b border-line px-2 py-1 text-right font-medium whitespace-nowrap text-faint">{t(`calc.fn.${f}`)}({columns[c]?.name})</th>{/each}
            {/each}
          </tr>
        </thead>
        <tbody>
          {#each groups as g, i (i)}
            <tr class="hover:bg-hover/70">
              {#each g.keys as k, j (j)}
                <td class="border-r border-b border-line-soft px-2 py-1 font-mono whitespace-nowrap">{#if k === null || k === undefined}<span class="text-faint italic">NULL</span>{:else}{raw(k)}{/if}</td>
              {/each}
              <td class="border-r border-b border-line-soft px-2 py-1 text-right font-mono tabular-nums">{fmtNumber(g.count)}</td>
              <td class="border-r border-b border-line-soft px-2 py-1 text-right font-mono text-muted tabular-nums">{pct(g.count)}</td>
              {#each g.values as v, j (j)}
                <td class="border-r border-b border-line-soft px-2 py-1 text-right font-mono tabular-nums">{show(v)}</td>
              {/each}
            </tr>
          {/each}
        </tbody>
      </table>
      {#if !groups.length}<p class="p-3 text-xs text-faint">{t('calc.noGroups')}</p>{/if}
    {/if}
  </div>
</aside>
