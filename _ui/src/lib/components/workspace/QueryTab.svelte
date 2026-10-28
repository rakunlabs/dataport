<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { Play, Square, Save, Lock, SquareArrowOutUpRight, CopyPlus, Check, Undo2 } from 'lucide-svelte';
  import type { SQLNamespace } from '@codemirror/lang-sql';
  import SqlEditor from '@/lib/components/editor/SqlEditor.svelte';
  import ResultPanel from '@/lib/components/result/ResultPanel.svelte';
  import RunTimer from '@/lib/components/result/RunTimer.svelte';
  import DbIcon from '@/lib/components/ui/DbIcon.svelte';
  import { cancel, connection, endTx, persist, run, runInNewTab, saveTab, setManualTx, openNotebook, ws, workspaceConnections, type Tab } from '@/lib/stores/workspace.svelte';
  import { prefs } from '@/lib/stores/prefs.svelte';
  import { editorOptions } from '@/lib/helper/editorOptions';
  import TreeMenu, { type MenuItem } from '@/lib/components/workspace/TreeMenu.svelte';
  import { fmtNumber } from '@/lib/i18n/index.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let { tab }: { tab: Tab } = $props();

  let editor: SqlEditor | undefined = $state();
  let split = $state(45);
  // Results show once something ran; closing them gives the editor the room.
  const showResults = $derived(!!tab.result || !!tab.error || tab.running);
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

  // A statement's run button runs it here or in a new tab, as the settings
  // say; a right click (or Alt-click) offers both.
  let stmtMenu = $state<{ x: number; y: number; items: MenuItem[] } | null>(null);

  function runStatement(sql: string, e: MouseEvent) {
    const here = () => run(tab.id, sql);
    const beside = () => run(tab.id, sql, 'append');
    const elsewhere = () => runInNewTab(tab.connectionId, sql, '', tab.maxRows);

    if (e.button === 2 || e.altKey) {
      stmtMenu = {
        x: e.clientX,
        y: e.clientY,
        items: [
          { label: t('editor.runHere'), icon: Play, action: here },
          { label: t('editor.runNewResult'), icon: CopyPlus, hidden: !tab.result, action: beside },
          { label: t('editor.runNewTab'), icon: SquareArrowOutUpRight, action: elsewhere },
        ],
      };
      return;
    }

    if (prefs.resultInNewTab) elsewhere();
    else void here();
  }

  // Row choices for a run: the connection default, fixed sizes, or as
  // many as the server allows.
  const rowChoices = $derived.by(() => {
    const limit = conn?.max_rows_limit ?? 0;
    return [100, 1000, 10000, 100000].filter((n) => !limit || n <= limit);
  });

  // Time limits a run may have; with a server cap, longer ones are dropped.
  const timeoutChoices = $derived.by(() => {
    const cap = (conn?.max_timeout_ms ?? 0) / 1000;
    return [30, 60, 300, 900, 3600].filter((n) => !cap || n <= cap);
  });

  function seconds(n: number) {
    return n >= 60 ? t('editor.minutes', { n: n / 60 }) : t('editor.seconds', { n });
  }

  function changeTimeout(e: Event) {
    tab.timeoutSec = Number((e.target as HTMLSelectElement).value);
    persist();
  }

  function changeRows(e: Event) {
    tab.maxRows = Number((e.target as HTMLSelectElement).value);
    persist();
  }

  function changeConnection(e: Event) {
    tab.connectionId = (e.target as HTMLSelectElement).value;
    tab.result = null;
    persist();
  }

  // The transaction belongs to the connection; the tab cannot move while
  // one is open.
  const txAvailable = $derived(!!conn?.manual_tx);

  // An open transaction is ended by Commit or Rollback, never by the toggle.
  function toggleTx() {
    if (!tab.txOpen) setManualTx(tab, !tab.manualTx);
  }

  async function rollback() {
    if (!confirm(t('tx.rollbackConfirm', { n: tab.txStatements }))) return;
    await endTx(tab, false);
  }
</script>

<div class="flex h-full min-h-0 flex-col" bind:this={container}>
  <div class="toolbar">
    {#if tab.running}
      <button class="btn-danger" onclick={() => cancel(tab.id)} disabled={tab.cancelling}><Square size={12} fill="currentColor" /> {t('query.stop')}</button>
      <button class="btn" onclick={() => run(tab.id, editor?.currentSQL())} disabled={!tab.sql.trim()} title={t('query.runParallel')}>
        <Play size={12} fill="currentColor" /> {t('query.run')}
      </button>
    {:else}
      <div class="flex">
        <button class="btn-primary rounded-r-none" onclick={() => run(tab.id, editor?.currentSQL())} disabled={!tab.sql.trim()} title="{t('query.run')} (Ctrl+Enter)">
          <Play size={12} fill="currentColor" /> {t('query.run')}
        </button>
        <button
          class="btn-primary rounded-l-none border-l border-white/25 px-1.5"
          onclick={() => run(tab.id, editor?.currentSQL(), 'append')}
          disabled={!tab.sql.trim() || !tab.result}
          title="{t('query.runNewResult')} (Ctrl+\)"
          aria-label={t('query.runNewResult')}
        >
          <CopyPlus size={13} />
        </button>
      </div>
    {/if}
    <button class="btn" onclick={save} title="Ctrl+S"><Save size={14} /> {tab.savedId ? t('query.save') : t('query.saveAs')}</button>
    <button class="btn" onclick={() => openNotebook('', tab.sql, tab.connectionId)}>{t('notebook.fromQuery')}</button>

    <div class="toolbar-sep"></div>

    <label class="relative flex items-center">
      <span class="pointer-events-none absolute left-2 flex">{#if conn}<DbIcon type={conn.db_type} size={12} />{/if}</span>
      <select class="input h-[26px] w-auto max-w-64 pl-7 text-sm" value={tab.connectionId} onchange={changeConnection} disabled={tab.running || tab.txOpen} title={tab.txOpen ? t('tx.connectionLocked') : undefined} aria-label={t('query.connection')}>
        {#each workspaceConnections() as c (c.id)}
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

    {#if txAvailable}
      <div class="toolbar-sep"></div>
      <button
        class="btn {tab.manualTx ? 'border-accent bg-accent-soft text-fg' : ''}"
        aria-pressed={tab.manualTx}
        onclick={toggleTx}
        disabled={tab.running || tab.txEnding || tab.txOpen}
        title={tab.txOpen ? t('tx.toggleLocked') : tab.manualTx ? t('tx.onHint') : t('tx.offHint')}
      >
        <span class="font-mono text-xs font-semibold">TX</span>
        {tab.manualTx ? t('tx.manual') : t('tx.auto')}
      </button>
      {#if tab.manualTx}
        {#if tab.txOpen}
          <span class="pill border border-warn/50 bg-warn-soft text-warn" title={t('tx.openHint')}>
            <span class="size-1.5 rounded-full bg-warn"></span>
            {t('tx.open', { count: tab.txStatements })}
          </span>
        {/if}
        <button class="btn" onclick={() => endTx(tab, true)} disabled={!tab.txOpen || tab.running || tab.txEnding} title={t('tx.commitHint')}>
          <Check size={13} class="text-ok" /> {t('tx.commit')}
        </button>
        <button class="btn" onclick={rollback} disabled={!tab.txOpen || tab.running || tab.txEnding} title={t('tx.rollbackHint')}>
          <Undo2 size={13} class="text-danger" /> {t('tx.rollback')}
        </button>
      {/if}
    {/if}

    <div class="flex-1"></div>
    {#if tab.running}<RunTimer {tab} />{/if}
    {#if conn}
      <label class="flex items-center gap-1.5 text-xs text-muted" title={t('conn.limitsHint')}>
        {t('editor.rows')}
        <select class="input h-[24px] w-auto py-0 pr-6 text-xs tabular-nums" value={tab.maxRows} onchange={changeRows}>
          <option value={0}>{t('editor.rowsDefault', { n: fmtNumber(conn.max_rows) })}</option>
          {#each rowChoices.filter((n) => n !== conn.max_rows) as n (n)}<option value={n}>{fmtNumber(n)}</option>{/each}
          <option value={-1}>{conn.max_rows_limit ? t('editor.rowsMax', { n: fmtNumber(conn.max_rows_limit) }) : t('editor.rowsAll')}</option>
        </select>
      </label>
      <label class="flex items-center gap-1.5 text-xs text-muted" title={t('editor.timeoutHint')}>
        {t('editor.timeout')}
        <select class="input h-[24px] w-auto py-0 pr-6 text-xs tabular-nums" value={tab.timeoutSec} onchange={changeTimeout}>
          <option value={-1}>{conn.timeout_ms ? t('editor.rowsDefault', { n: seconds(conn.timeout_ms / 1000) }) : t('editor.noLimitDefault')}</option>
          {#if !conn.max_timeout_ms}<option value={0}>{t('editor.noLimit')}</option>{/if}
          {#each timeoutChoices.filter((n) => n * 1000 !== conn.timeout_ms) as n (n)}<option value={n}>{seconds(n)}</option>{/each}
        </select>
      </label>
    {/if}
  </div>

  <div class="min-h-0 {showResults ? '' : 'flex-1'}" style={showResults ? `height:${split}%` : ''}>
    <SqlEditor
      bind:this={editor}
      value={tab.sql}
      dbType={conn?.db_type ?? 'postgres'}
      {schema}
      {onchange}
      onrun={(sql, append) => run(tab.id, sql, append ? 'append' : 'replace')}
      onrunstatement={prefs.statementRun ? runStatement : undefined}
      onsave={save}
      options={editorOptions(prefs)}
    />
  </div>

  {#if showResults}
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
  {:else}
    <div class="flex h-7 shrink-0 items-center gap-1.5 border-t border-line bg-panel px-3 text-xs text-faint">
      {t('result.empty')} <span class="kbd">Ctrl</span><span class="kbd">Enter</span>
    </div>
  {/if}
</div>

{#if stmtMenu}
  <TreeMenu x={stmtMenu.x} y={stmtMenu.y} items={stmtMenu.items} onclose={() => (stmtMenu = null)} />
{/if}
