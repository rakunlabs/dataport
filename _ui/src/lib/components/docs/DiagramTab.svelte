<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { RefreshCw, LoaderCircle, TriangleAlert, ZoomIn, ZoomOut, Maximize, Key, Link } from 'lucide-svelte';
  import { api, qs } from '@/lib/api/client';
  import type { Diagram, DiagramTable } from '@/lib/api/types';
  import { connection, openTableTab, type Tab } from '@/lib/stores/workspace.svelte';
  import { withCredentials } from '@/lib/stores/credentials.svelte';
  import { cardHeight, edges, extent, HEAD, layout as placeCards, ROW, W, type Positions } from '@/lib/helper/diagramLayout';

  let { tab }: { tab: Tab } = $props();

  const conn = $derived(connection(tab.connectionId));

  let data = $state<Diagram | null>(null);
  let loading = $state(false);
  let error = $state('');

  let pos = $state<Positions>({});
  let view = $state({ x: 20, y: 20, k: 1 });
  let hover = $state('');

  async function load() {
    loading = true;
    error = '';
    try {
      data = await withCredentials(tab.connectionId, () =>
        api.get<Diagram>(`/connections/${tab.connectionId}/diagram${qs({ schema: tab.schema })}`),
      );
      layout();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  void load();

  const height = cardHeight;

  function layout() {
    if (data) pos = placeCards(data.tables);
  }

  const lines = $derived(data ? edges(data.tables, pos) : []);

  // ////////////////////////////////////////////
  // pan, zoom and drag

  let svg: SVGSVGElement | undefined = $state();
  let drag: { kind: 'pan' | 'card'; name?: string; x0: number; y0: number; ox: number; oy: number } | null = null;

  function onpointerdown(e: PointerEvent, name?: string) {
    if (e.button !== 0) return;
    e.stopPropagation();
    (e.currentTarget as Element).setPointerCapture?.(e.pointerId);
    drag = name
      ? { kind: 'card', name, x0: e.clientX, y0: e.clientY, ox: pos[name].x, oy: pos[name].y }
      : { kind: 'pan', x0: e.clientX, y0: e.clientY, ox: view.x, oy: view.y };
  }

  function onpointermove(e: PointerEvent) {
    if (!drag) return;
    const dx = e.clientX - drag.x0;
    const dy = e.clientY - drag.y0;
    if (drag.kind === 'pan') view = { ...view, x: drag.ox + dx, y: drag.oy + dy };
    else if (drag.name) pos[drag.name] = { x: drag.ox + dx / view.k, y: drag.oy + dy / view.k };
  }

  function onpointerup() {
    drag = null;
  }

  function zoomAt(factor: number, cx: number, cy: number) {
    const k = Math.min(2.5, Math.max(0.2, view.k * factor));
    const f = k / view.k;
    view = { k, x: cx - (cx - view.x) * f, y: cy - (cy - view.y) * f };
  }

  function onwheel(e: WheelEvent) {
    e.preventDefault();
    const r = svg!.getBoundingClientRect();
    zoomAt(e.deltaY < 0 ? 1.1 : 1 / 1.1, e.clientX - r.left, e.clientY - r.top);
  }

  function fit() {
    if (!data?.tables.length || !svg) return;
    const r = svg.getBoundingClientRect();
    const { width: maxX, height: maxY } = extent(data.tables, pos);
    const k = Math.min(1.2, Math.max(0.2, Math.min((r.width - 40) / maxX, (r.height - 40) / maxY)));
    view = { k, x: 20, y: 20 };
  }

  $effect(() => {
    if (data && svg) queueMicrotask(fit);
  });

  function related(name: string) {
    if (!hover) return true;
    if (name === hover) return true;
    return lines.some((l) => (l.from === hover && l.to === name) || (l.to === hover && l.from === name));
  }

  const fkCols = (tb: DiagramTable) => new Set(tb.foreign_keys.flatMap((f) => f.columns));
</script>

<div class="flex h-full min-h-0 flex-col">
  <div class="toolbar">
    <span class="text-sm font-medium">{t('diagram.title', { schema: tab.schema })}</span>
    {#if conn}<span class="text-xs text-muted">{conn.name}</span>{/if}
    {#if data}<span class="text-xs text-faint tabular-nums">{t('diagram.count', { count: data.tables.length })}</span>{/if}
    {#if data?.truncated}<span class="text-xs text-warn">{t('diagram.truncated', { n: data.tables.length })}</span>{/if}
    <div class="flex-1"></div>
    <button class="icon-btn" onclick={() => zoomAt(1 / 1.2, 300, 200)} title={t('diagram.zoomOut')} aria-label={t('diagram.zoomOut')}><ZoomOut size={14} /></button>
    <span class="w-10 text-center text-xs text-muted tabular-nums">{Math.round(view.k * 100)}%</span>
    <button class="icon-btn" onclick={() => zoomAt(1.2, 300, 200)} title={t('diagram.zoomIn')} aria-label={t('diagram.zoomIn')}><ZoomIn size={14} /></button>
    <button class="icon-btn" onclick={fit} title={t('diagram.fit')} aria-label={t('diagram.fit')}><Maximize size={14} /></button>
    <button class="btn" onclick={() => { layout(); fit(); }}>{t('diagram.arrange')}</button>
    <button class="btn" onclick={load} disabled={loading}><RefreshCw size={14} class={loading ? 'animate-spin' : ''} /> {t('common.refresh')}</button>
  </div>

  <div class="relative min-h-0 flex-1 overflow-hidden bg-sunken">
    {#if loading && !data}
      <div class="flex h-full items-center justify-center gap-2 text-sm text-muted"><LoaderCircle size={15} class="animate-spin" /> {t('common.loading')}</div>
    {:else if error}
      <div class="p-3"><p class="flex items-start gap-2 rounded-md border border-danger/30 bg-danger-soft p-3 text-sm text-danger"><TriangleAlert size={14} class="mt-0.5 shrink-0" /> {error}</p></div>
    {:else if data && !data.tables.length}
      <div class="flex h-full items-center justify-center text-sm text-muted">{t('diagram.empty')}</div>
    {:else if data}
      <!-- svelte-ignore a11y_no_static_element_interactions -->
      <svg
        bind:this={svg}
        class="h-full w-full cursor-grab touch-none select-none active:cursor-grabbing"
        onpointerdown={(e) => onpointerdown(e)}
        {onpointermove}
        {onpointerup}
        {onwheel}
      >
        <defs>
          <pattern id="dp-grid" width={20 * view.k} height={20 * view.k} patternUnits="userSpaceOnUse" x={view.x} y={view.y}>
            <circle cx="1" cy="1" r="1" fill="var(--color-line)" />
          </pattern>
          <marker id="dp-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M0,0 L10,5 L0,10 z" fill="var(--color-accent)" />
          </marker>
        </defs>
        <rect width="100%" height="100%" fill="url(#dp-grid)" />

        <g transform="translate({view.x},{view.y}) scale({view.k})">

          {#each data.tables as tb (tb.name)}
            {@const p = pos[tb.name]}
            {@const fks = fkCols(tb)}
            {#if p}
              <!-- svelte-ignore a11y_no_static_element_interactions -->
              <g
                transform="translate({p.x},{p.y})"
                opacity={related(tb.name) ? 1 : 0.3}
                class="cursor-move"
                onpointerdown={(e) => onpointerdown(e, tb.name)}
                onpointerenter={() => (hover = tb.name)}
                onpointerleave={() => (hover = '')}
                ondblclick={() => openTableTab(tab.connectionId, tab.schema, tb.name)}
              >
                <rect width={W} height={height(tb)} rx="6" fill="var(--color-raised)" stroke={hover === tb.name ? 'var(--color-accent)' : 'var(--color-line)'} />
                <rect width={W} height={HEAD} rx="6" fill={tb.kind === 'table' ? 'var(--color-accent-soft)' : 'var(--color-panel)'} />
                <text x="10" y="18" font-size="12" font-weight="600" fill="var(--color-fg)">{tb.name.length > 26 ? tb.name.slice(0, 25) + '…' : tb.name}</text>
                {#if tb.kind !== 'table'}<text x={W - 10} y="18" font-size="10" text-anchor="end" fill="var(--color-muted)">{tb.kind === 'view' ? 'view' : 'mview'}</text>{/if}
                {#each tb.columns as c, i (c.name)}
                  {@const y = HEAD + i * ROW}
                  {#if c.primary_key || fks.has(c.name)}
                    <foreignObject x="8" y={y + 4} width="12" height="12">
                      {#if c.primary_key}<Key size={11} class="text-warn" />{:else}<Link size={11} class="text-accent" />{/if}
                    </foreignObject>
                  {/if}
                  <text x="24" y={y + 14} font-size="11" fill={c.primary_key ? 'var(--color-fg)' : 'var(--color-fg-2)'} font-weight={c.primary_key ? 600 : 400}>
                    {c.name.length > 18 ? c.name.slice(0, 17) + '…' : c.name}
                  </text>
                  <text x={W - 10} y={y + 14} font-size="10" text-anchor="end" font-family="var(--font-mono)" fill="var(--color-faint)">
                    {c.type.length > 14 ? c.type.slice(0, 13) + '…' : c.type}{c.nullable ? '' : ' !'}
                  </text>
                {/each}
              </g>
            {/if}
          {/each}
          <!-- Lines go over the cards, so one passing a card is still followed to its end. -->
          {#each lines as l (l.key)}
            {@const lit = !hover || l.from === hover || l.to === hover}
            <path
              d={l.d}
              fill="none"
              stroke="var(--color-accent)"
              stroke-width={lit && hover ? 2 : 1.4}
              opacity={hover ? (lit ? 0.9 : 0.08) : 0.6}
              marker-end="url(#dp-arrow)"
              pointer-events="none"
            />
          {/each}
        </g>
      </svg>
      <p class="pointer-events-none absolute bottom-2 left-3 text-xs text-faint">{t('diagram.help')}</p>
    {/if}
  </div>
</div>
