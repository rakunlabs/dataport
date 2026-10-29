<script lang="ts">
  import { t, fmtNumber } from '@/lib/i18n/index.svelte';
  import { Copy, RefreshCw, ListTree, FileText, Braces, ChevronRight } from 'lucide-svelte';
  import type { ResultSet } from '@/lib/api/types';
  import JsonTree from '@/lib/components/result/json/JsonTree.svelte';
  import { LosslessNumber, parseJson } from '@/lib/components/result/json/jsonTree';
  import { copyText } from '@/lib/helper/clipboard';

  // An execution plan: the plan text with costs and times picked out, a
  // tree of PostgreSQL's JSON plan with the costliest steps marked, or the
  // raw JSON.
  let { result, onreexplain }: { result: ResultSet; onreexplain?: () => void } = $props();

  const plan = $derived(result.plan!);
  const rows = $derived(result.rows ?? []);

  // Text plans come one line per row; SQLite and SQL Server return columns
  // of their own, shown as a table.
  const single = $derived((result.columns?.length ?? 0) === 1);
  const text = $derived(single ? rows.map((r) => String(r[0] ?? '')).join('\n') : '');

  const json = $derived.by(() => {
    if (plan.format !== 'json' || !text.trim()) return undefined;
    const p = parseJson(text);
    return p.ok ? p.value : undefined;
  });

  // PostgreSQL: [{ Plan: {...}, Planning Time, Execution Time }].
  type Node = { label: string; detail: string; cost: number; self: number; rows?: number; actualRows?: number; time?: number; loops?: number; children: Node[] };

  function num(v: unknown): number | undefined {
    if (typeof v === 'number') return v;
    if (v instanceof LosslessNumber) return Number(v.raw);
    return typeof v === 'string' && v.trim() !== '' && !Number.isNaN(Number(v)) ? Number(v) : undefined;
  }

  function toNode(p: Record<string, unknown>): Node {
    const kids = ((p['Plans'] as Record<string, unknown>[]) ?? []).map(toNode);
    const total = num(p['Actual Total Time']) !== undefined ? (num(p['Actual Total Time'])! * (num(p['Actual Loops']) ?? 1)) : (num(p['Total Cost']) ?? 0);
    const childTotal = kids.reduce((s, k) => s + k.cost, 0);
    const on = [p['Relation Name'] ?? p['Function Name'] ?? p['CTE Name'], p['Alias'] && p['Alias'] !== p['Relation Name'] ? p['Alias'] : undefined].filter(Boolean).join(' ');
    const detail = [p['Index Name'] ? `${t('explain.index')} ${p['Index Name']}` : '', p['Join Type'] ? `${p['Join Type']} join` : '', p['Filter'] ?? p['Index Cond'] ?? p['Hash Cond'] ?? p['Merge Cond'] ?? p['Join Filter'] ?? '']
      .filter(Boolean)
      .join(' · ');
    return {
      label: `${p['Node Type'] ?? '?'}${on ? ` on ${on}` : ''}`,
      detail: String(detail),
      cost: total,
      self: Math.max(0, total - childTotal),
      rows: num(p['Plan Rows']),
      actualRows: num(p['Actual Rows']),
      time: num(p['Actual Total Time']),
      loops: num(p['Actual Loops']),
      children: kids,
    };
  }

  const tree = $derived.by(() => {
    const top = Array.isArray(json) ? (json[0] as Record<string, unknown>) : undefined;
    const p = top?.['Plan'] as Record<string, unknown> | undefined;
    if (!p) return undefined;
    return { root: toNode(p), planning: num(top!['Planning Time']), execution: num(top!['Execution Time']) };
  });

  // The steps that take most of the plan get marked, as DBeaver's "heavy
  // route".
  const totalSelf = $derived.by(() => {
    let s = 0;
    const walk = (n: Node) => {
      s += n.self;
      n.children.forEach(walk);
    };
    if (tree) walk(tree.root);
    return s || 1;
  });

  let view = $state<'tree' | 'text' | 'json'>('tree');
  const views = $derived(
    [tree ? 'tree' : '', single ? 'text' : '', json !== undefined ? 'json' : ''].filter(Boolean) as ('tree' | 'text' | 'json')[],
  );
  const current = $derived(views.includes(view) ? view : (views[0] ?? 'table'));

  let open = $state<Record<string, boolean>>({});

  // Text lines: costs, actual times and rows stand out.
  const tokens = /(\(cost=[^)]*\)|\(actual[^)]*\)|actual time=[\d.]+\.\.[\d.]+|rows=\d+|loops=\d+|cost=[\d.]+(?:\.\.[\d.]+)?|Seq Scan|Table scan|Full scan|TABLE ACCESS FULL)/g;

  function parts(line: string): { s: string; kind: string }[] {
    const out: { s: string; kind: string }[] = [];
    let last = 0;
    for (const m of line.matchAll(tokens)) {
      if (m.index! > last) out.push({ s: line.slice(last, m.index), kind: '' });
      const s = m[0];
      out.push({ s, kind: /scan|full/i.test(s) ? 'scan' : s.includes('actual') ? 'actual' : 'cost' });
      last = m.index! + s.length;
    }
    if (last < line.length) out.push({ s: line.slice(last), kind: '' });
    return out;
  }

  function copy() {
    void copyText(single ? text : rows.map((r) => r.join('\t')).join('\n'));
  }

  function ms(v?: number) {
    return v === undefined ? '' : v < 1 ? `${v.toFixed(3)} ms` : `${fmtNumber(Math.round(v * 100) / 100)} ms`;
  }
</script>

<div class="flex h-full min-h-0 flex-col">
  <div class="flex h-8 shrink-0 items-center gap-2 border-b border-line-soft bg-panel px-2 text-xs">
    <span class="font-semibold">{plan.analyzed ? t('explain.analyzed') : t('explain.estimated')}</span>
    {#if tree?.planning !== undefined}<span class="text-muted">{t('explain.planning')} {ms(tree.planning)}</span>{/if}
    {#if tree?.execution !== undefined}<span class="text-muted">{t('explain.execution')} {ms(tree.execution)}</span>{/if}
    <span class="flex-1"></span>
    {#if views.length > 1}
      <div class="flex rounded-md border border-line p-px" role="tablist">
        {#each views as v (v)}
          <button role="tab" aria-selected={current === v} class="flex h-[20px] items-center gap-1 rounded px-1.5 {current === v ? 'bg-row-active text-fg' : 'text-muted hover:text-fg'}" onclick={() => (view = v)}>
            {#if v === 'tree'}<ListTree size={12} />{:else if v === 'text'}<FileText size={12} />{:else}<Braces size={12} />{/if}
            {t(`explain.view.${v}`)}
          </button>
        {/each}
      </div>
    {/if}
    <button class="icon-btn size-[22px]" onclick={copy} title={t('explain.copy')} aria-label={t('explain.copy')}><Copy size={12} /></button>
    {#if onreexplain}<button class="icon-btn size-[22px]" onclick={onreexplain} title={t('explain.again')} aria-label={t('explain.again')}><RefreshCw size={12} /></button>{/if}
  </div>

  <div class="min-h-0 flex-1 overflow-auto">
    {#if current === 'tree' && tree}
      {@render node(tree.root, '0', 0)}
    {:else if current === 'json'}
      <JsonTree value={json} depth={6} />
    {:else if current === 'text'}
      <pre class="px-3 py-2 font-mono text-xs leading-[1.7] whitespace-pre">{#each text.split('\n') as line, i (i)}{#each parts(line) as p, j (j)}{#if p.kind === 'cost'}<span class="text-accent">{p.s}</span>{:else if p.kind === 'actual'}<span class="text-ok">{p.s}</span>{:else if p.kind === 'scan'}<span class="font-semibold text-warn">{p.s}</span>{:else}{p.s}{/if}{/each}{'\n'}{/each}</pre>
    {:else}
      <table class="table">
        <thead><tr>{#each result.columns ?? [] as c (c.name)}<th>{c.name}</th>{/each}</tr></thead>
        <tbody>
          {#each rows as r, i (i)}
            <tr>{#each r as v, j (j)}<td class="font-mono text-xs whitespace-pre">{v ?? ''}</td>{/each}</tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
</div>

{#snippet node(n: Node, key: string, depth: number)}
  {@const share = n.self / totalSelf}
  {@const closed = open[key] === false}
  <div class="flex items-start gap-1.5 border-b border-line-soft py-1 pr-3 text-xs hover:bg-hover/60" style="padding-left:{8 + depth * 18}px">
    {#if n.children.length}
      <button class="icon-btn mt-px size-4" onclick={() => (open[key] = closed)} aria-label={closed ? t('explain.expand') : t('explain.collapse')} aria-expanded={!closed}>
        <ChevronRight size={12} class={closed ? '' : 'rotate-90'} />
      </button>
    {:else}<span class="size-4 shrink-0"></span>{/if}
    <div class="min-w-0 flex-1">
      <div class="flex items-center gap-2">
        <span class="font-medium {share > 0.3 ? 'text-danger' : share > 0.1 ? 'text-warn' : ''}">{n.label}</span>
        {#if share > 0.3}<span class="pill bg-danger-soft text-danger">{t('explain.heavy')}</span>{/if}
      </div>
      {#if n.detail}<div class="truncate font-mono text-[11px] text-muted" title={n.detail}>{n.detail}</div>{/if}
    </div>
    <div class="flex shrink-0 items-center gap-3 tabular-nums text-fg-2">
      {#if n.time !== undefined}<span title={t('explain.time')}>{ms(n.time)}{#if n.loops && n.loops > 1}<span class="text-muted"> ×{n.loops}</span>{/if}</span>{/if}
      {#if n.actualRows !== undefined}<span title={t('explain.actualRows')}>{fmtNumber(n.actualRows)} {t('explain.rows')}</span>{/if}
      {#if n.rows !== undefined}<span class="text-muted" title={t('explain.estimatedRows')}>~{fmtNumber(n.rows)}</span>{/if}
      <span class="w-24 text-right text-muted" title={t('explain.share')}>
        <span class="mr-1 inline-block h-1.5 w-12 overflow-hidden rounded-full bg-sunken align-middle"><span class="block h-full {share > 0.3 ? 'bg-danger' : share > 0.1 ? 'bg-warn' : 'bg-accent'}" style="width:{Math.max(2, share * 100)}%"></span></span>
        {Math.round(share * 100)}%
      </span>
    </div>
  </div>
  {#if !closed}
    {#each n.children as c, i (i)}{@render node(c, `${key}.${i}`, depth + 1)}{/each}
  {/if}
{/snippet}
