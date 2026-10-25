<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { Play, Square, Save, Lock, LoaderCircle } from 'lucide-svelte';
  import type { SQLNamespace } from '@codemirror/lang-sql';
  import SqlEditor from '@/lib/components/SqlEditor.svelte';
  import ResultPanel from '@/lib/components/ResultPanel.svelte';
  import DbIcon from '@/lib/components/DbIcon.svelte';
  import { cancel, connection, persist, run, saveTab, ws, type Tab } from '@/lib/stores/workspace.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let { tab }: { tab: Tab } = $props();

  let editor: SqlEditor | undefined = $state();
  let split = $state(45);
  let container: HTMLDivElement;

  const conn = $derived(connection(tab.connectionId));

  // Table names of the loaded tree feed the editor's autocompletion.
  const schema = $derived.by(() => {
    const ns: Record<string, Record<string, string[]>> = {};
    const prefix = tab.connectionId + '|';
    for (const [key, node] of Object.entries(ws.objects)) {
      if (!key.startsWith(prefix)) continue;
      const s = key.slice(prefix.length);
      ns[s] = {};
      for (const o of node.items) {
        const cols = ws.columns[`${prefix}${s}|${o.name}`]?.items.map((c) => c.name) ?? [];
        ns[s][o.name] = cols;
      }
    }
    return ns as SQLNamespace;
  });

  function onchange(v: string) {
    tab.sql = v;
    tab.dirty = true;
    persist();
  }

  async function save() {
    let name = tab.title;
    if (!tab.savedId) {
      const input = prompt(t('query.saveName'), tab.title);
      if (!input) return;
      name = input;
    }
    try {
      await saveTab(tab.id, name);
      toast(t('query.saved'), 'success', 1500);
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  function startDrag(e: PointerEvent) {
    e.preventDefault();
    const rect = container.getBoundingClientRect();
    const move = (ev: PointerEvent) => {
      split = Math.min(85, Math.max(15, ((ev.clientY - rect.top) / rect.height) * 100));
    };
    const up = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }

  function changeConnection(e: Event) {
    tab.connectionId = (e.target as HTMLSelectElement).value;
    tab.result = null;
    persist();
  }
</script>

<div class="flex h-full min-h-0 flex-col" bind:this={container}>
  <div class="toolbar">
    {#if tab.running}
      <button class="btn-danger" onclick={() => cancel(tab.id)}><Square size={12} fill="currentColor" /> {t('query.stop')}</button>
    {:else}
      <button class="btn-primary" onclick={() => run(tab.id, editor?.currentSQL())} disabled={!tab.sql.trim()} title="{t('query.run')} (Ctrl+Enter)">
        <Play size={12} fill="currentColor" /> {t('query.run')}
      </button>
    {/if}
    <button class="btn" onclick={save} title="Ctrl+S"><Save size={14} /> {tab.savedId ? t('query.save') : t('query.saveAs')}</button>

    <div class="toolbar-sep"></div>

    <label class="relative flex items-center">
      <span class="pointer-events-none absolute left-2 flex">{#if conn}<DbIcon type={conn.db_type} size={12} />{/if}</span>
      <select class="input h-[26px] w-auto max-w-64 pl-7 text-sm" value={tab.connectionId} onchange={changeConnection} aria-label={t('query.connection')}>
        {#each ws.connections as c (c.id)}
          <option value={c.id}>{c.name}</option>
        {/each}
      </select>
    </label>
    {#if conn?.production}<span class="pill-prod" title={t('conn.productionHint')}>{t('conn.prodShort')}</span>{/if}
    {#if conn?.read_only}
      <span class="pill-ro" title={t('conn.readOnlyHint')}><Lock size={10} /> {t('conn.readOnlyShort')}</span>
    {:else if conn}
      <span class="pill-rw" title={t('conn.writeHint')}>{t('conn.rwShort')}</span>
    {/if}

    <div class="flex-1"></div>
    {#if tab.running}<span class="flex items-center gap-1.5 text-xs text-muted"><LoaderCircle size={13} class="animate-spin" /> {t('result.running')}</span>{/if}
    {#if conn}
      <span class="text-xs text-muted tabular-nums" title={t('conn.limitsHint')}>{t('conn.limits', { rows: conn.max_rows, seconds: Math.round(conn.timeout_ms / 1000) })}</span>
    {/if}
  </div>

  <div class="min-h-0" style="height:{split}%">
    <SqlEditor
      bind:this={editor}
      value={tab.sql}
      dbType={conn?.db_type ?? 'postgres'}
      {schema}
      {onchange}
      onrun={(sql) => run(tab.id, sql)}
      onsave={save}
    />
  </div>

  <div
    class="group relative h-px shrink-0 cursor-row-resize bg-line"
    onpointerdown={startDrag}
    role="separator"
    aria-orientation="horizontal"
  >
    <span class="absolute inset-x-0 -top-[3px] h-[7px] transition-colors group-hover:bg-accent/40"></span>
  </div>

  <div class="min-h-0 flex-1">
    <ResultPanel {tab} />
  </div>
</div>
