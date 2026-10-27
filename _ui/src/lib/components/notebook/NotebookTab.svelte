<script lang="ts" module>
  // Tabs whose notebook was read from the server since the page loaded; a
  // restored, unchanged notebook is read again once.
  const fresh = new Set<string>();
</script>

<script lang="ts">
  import { onMount } from 'svelte';
  import { Plus, Play, Save, Trash2, RefreshCw, Square, Download } from 'lucide-svelte';
  import { ApiError } from '@/lib/api/client';
  import * as nb from '@/lib/api/notebooks';
  import { newCell, type NotebookCell, type NotebookResult } from '@/lib/api/notebooks';
  import { connection, persist, closeTab, ws, type Tab } from '@/lib/stores/workspace.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { toast, errorMessage } from '@/lib/stores/toast.svelte';
  import { download, formatDuration } from '@/lib/helper/format';
  import { copyText } from '@/lib/helper/clipboard';
  import ResultPanel from '@/lib/components/result/ResultPanel.svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import NotebookCellEditor from '@/lib/components/notebook/NotebookCellEditor.svelte';
  import NotebookRuns from '@/lib/components/notebook/NotebookRuns.svelte';
  import NotebookSharing from '@/lib/components/notebook/NotebookSharing.svelte';

  let { tab }: { tab: Tab } = $props();
  const note = $derived(tab.notebook);
  const editable = $derived(note?.permission === 'owner' || note?.permission === 'edit');
  const owner = $derived(note?.permission === 'owner');
  const runnable = $derived(note?.permission !== 'view' && !!note);
  let section = $state('cells');
  let loading = $state(false);
  let busy = $state(false);
  let error = $state('');
  const dataText = $derived(tab.notebookInput || '{}');
  let selected = $state('');
  let preview = $state<NotebookResult | null>(null);
  let pending = $state<{ cell: string; background: boolean } | null>(null);
  let resultTab = $state<Tab | null>(null);
  let runsView = $state<NotebookRuns>();
  let runsOffset = $state(0);
  const endpoint = $derived(new URL(`./api/v1/run/${note?.path || ''}`, location.href.split('#')[0]).href);
  const shellQuote = (v: string) => `'${v.replaceAll("'", "'\\''")}'`;
  const curl = $derived(`curl -X POST ${shellQuote(endpoint)} \\\n  -H 'Authorization: Bearer YOUR_TOKEN' \\\n  -H 'Content-Type: application/json' \\\n  --data ${shellQuote(dataText)}`);

  function changed() { tab.dirty = true; if (note) tab.title = note.name || t('notebook.untitled'); persist(); }
  async function load() {
    if (!tab.savedId) {
      if (!tab.notebook) tab.notebook = { id: '', owner_id: '', name: t('notebook.untitled'), path: `notebook-${Date.now()}`, document: { cells: [newCell(tab.connectionId || ws.connections[0]?.id, 1, tab.sql)], parameters: [] }, version: 0, published_version: 0, permission: 'owner', created_at: 0, updated_at: 0 };
      changed(); return;
    }
    loading = true; error = '';
    try { tab.notebook = await nb.getNotebook(tab.savedId); tab.title = tab.notebook.name; tab.dirty = false; fresh.add(tab.id); persist(); }
    catch (e) { error = errorMessage(e); } finally { loading = false; }
  }
  onMount(() => { if (!tab.notebook || (tab.savedId && !tab.dirty && !fresh.has(tab.id))) void load(); selected = tab.notebookResult?.cells.at(-1)?.path || ''; });
  $effect(() => {
    const cell = tab.notebookResult?.cells.find((c) => c.path === selected);
    resultTab = cell?.result ? { ...tab, kind: 'query', connectionId: note?.document.cells.find((c) => c.path === selected)?.connection_id || '', running: false, error: '', result: cell.result, activeResult: 0 } : null;
  });

  async function save() {
    if (!note || !editable || busy || tab.running) return;
    busy = true; error = '';
    try {
      const content = () => JSON.stringify({ name: tab.notebook?.name, path: tab.notebook?.path, document: tab.notebook?.document });
      const submitted = content();
      const body: nb.NotebookContent = JSON.parse(submitted);
      const saved = note.id ? await nb.updateNotebook(note.id, { ...body, version: note.version }) : await nb.createNotebook(body);
      if (content() === submitted) { tab.notebook = saved; tab.dirty = false; }
      else if (tab.notebook) {
        // An editor update queued immediately after the request must not be
        // replaced by the acknowledgement of an older document snapshot.
        Object.assign(tab.notebook, { id: saved.id, version: saved.version, permission: saved.permission, owner_id: saved.owner_id, published_version: saved.published_version, created_at: saved.created_at, updated_at: saved.updated_at });
        tab.dirty = true;
      }
      tab.savedId = saved.id; fresh.add(tab.id); tab.title = tab.notebook?.name || saved.name; persist(); toast(t('notebook.saved'), 'success');
    } catch (e) { error = errorMessage(e); } finally { busy = false; }
  }
  function addCell() {
    if (!note) return;
    let i = note.document.cells.length + 1; while (note.document.cells.some((c) => c.path === `cell${i}`)) i++;
    note.document.cells.push(newCell(ws.connections[0]?.id, i)); changed();
  }
  function move(i: number, by: number) {
    if (!note) return; const cells = note.document.cells; [cells[i], cells[i + by]] = [cells[i + by], cells[i]]; changed();
  }
  function duplicate(c: NotebookCell) {
    if (!note) return; let path = `${c.path}_copy`; while (note.document.cells.some((x) => x.path === path)) path += '_copy';
    note.document.cells.push({ ...JSON.parse(JSON.stringify(c)), id: crypto.randomUUID(), path }); changed();
  }
  function remove(c: NotebookCell) { if (note && confirm(t('notebook.deleteCell'))) { note.document.cells = note.document.cells.filter((x) => x.id !== c.id); changed(); } }
  function inputData() {
    const v = JSON.parse(dataText);
    if (!v || typeof v !== 'object' || Array.isArray(v)) throw new Error(t('notebook.invalidObject'));
    return v;
  }
  async function requestRun(cell: string, background: boolean, confirmations: Record<string, string> = {}, isPreview = false) {
    if (!note) return;
    const body = { version: note.version, cell, background, data: inputData(), preview: isPreview, confirmations };
    tab.notebookAbort = new AbortController();
    try { return await nb.runNotebook(note.id, body, tab.notebookAbort.signal); }
    catch (e) {
      if (e instanceof ApiError && Array.isArray(e.data?.cells)) {
        const out = e.data as unknown as NotebookResult; tab.notebookResult = out; selected = out.cells.at(-1)?.path || ''; throw new Error(out.error || errorMessage(e));
      } throw e;
    }
  }
  async function run(cell = '', background = false, previewOnly = false) {
    if (!note?.id || tab.dirty) { error = t('notebook.saveFirst'); return; }
    if (busy || tab.running) return;
    busy = true; tab.running = true; error = '';
    try {
      // A read-only preview executes dependencies, but no write cells. We
      // never automatically replay a partially committed failed notebook.
      const prodWrite = note.document.cells.some((c) => c.write && c.enabled && connection(c.connection_id)?.production);
      if (previewOnly || prodWrite) {
        const out = await requestRun(cell, false, {}, true) as NotebookResult;
        tab.notebookResult = out; selected = out.cells.at(-1)?.path || '';
        if (prodWrite && !previewOnly) {
          if (out.cells.some((c) => c.confirmation)) { preview = out; pending = { cell, background }; return; }
          await doRun(cell, background);
        }
      } else await doRun(cell, background);
    } catch (e) { fail(e); } finally { busy = false; tab.running = false; }
  }
  function fail(e: unknown) {
    if (tab.notebookAbort?.signal.aborted) toast(t('notebook.cancelled'), 'info');
    else error = errorMessage(e);
  }
  async function doRun(cell: string, background: boolean, confirmations: Record<string, string> = {}) {
    const out = await requestRun(cell, background, confirmations);
    if (!out) return;
    if (background) { toast(t('notebook.backgroundStarted'), 'info', 5000); if (section === 'runs') await runsView?.loadRuns(); else section = 'runs'; }
    else { tab.notebookResult = out as NotebookResult; selected = (out as NotebookResult).cells.at(-1)?.path || ''; }
  }
  async function confirmRun() {
    if (!preview || !pending) return;
    const confirmations: Record<string, string> = {};
    for (const c of preview.cells) if (c.confirmation) confirmations[c.path] = c.confirmation;
    const p = pending; preview = null; pending = null; busy = true; tab.running = true;
    try { await doRun(p.cell, p.background, confirmations); } catch (e) { fail(e); } finally { busy = false; tab.running = false; }
  }
  async function publish(value: boolean) {
    if (!note || tab.dirty) { error = t('notebook.saveFirst'); return; }
    busy = true; error = '';
    try { tab.notebook = await nb.publishNotebook(note.id, note.version, value); persist(); }
    catch (e) { error = errorMessage(e); } finally { busy = false; }
  }
  async function deleteNote() {
    if (!note?.id || !confirm(t('notebook.deleteConfirm'))) return;
    try { await nb.deleteNotebook(note.id); tab.dirty = false; closeTab(tab.id); } catch (e) { error = errorMessage(e); }
  }
  function exportNote() {
    if (!note) return;
    download(`${note.path}.json`, JSON.stringify({ name: note.name, path: note.path, document: note.document }, null, 2), 'application/json');
  }
</script>

<svelte:window onkeydown={(e) => { if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); void save(); } }} />

<div class="flex h-full min-w-0 flex-col">
  {#if loading}<p class="p-4 text-sm text-muted">{t('common.loading')}</p>{/if}
  {#if error}<div class="flex shrink-0 items-center gap-2 border-b border-danger/30 bg-danger-soft px-3 py-2 text-sm text-danger" role="alert"><span class="flex-1">{error}</span><button class="btn" onclick={() => error = ''}>{t('common.close')}</button></div>{/if}
  {#if note}
    <div class="toolbar overflow-x-auto">
      <input class="input min-w-40 flex-1" bind:value={note.name} oninput={changed} disabled={!editable || busy} aria-label={t('notebook.name')} />
      <span class="whitespace-nowrap text-xs text-muted">{tab.dirty ? t('notebook.unsaved') : t('notebook.savedVersion') + ` ${note.version}`}</span>
      <button class="btn" onclick={exportNote} title={t('notebook.export')} aria-label={t('notebook.export')}><Download size={14} /></button>
      <button class="btn" disabled={!note.id || busy} onclick={() => { if (!tab.dirty || confirm(t('notebook.reloadConfirm'))) void load(); }} title={t('notebook.reload')} aria-label={t('notebook.reload')}><RefreshCw size={14} /></button>
      <button class="btn-primary" onclick={save} disabled={!editable || busy || !tab.dirty}><Save size={14} />{t('common.save')}</button>
    </div>
    <div class="flex shrink-0 items-center gap-1 overflow-x-auto border-b border-line bg-panel px-2 py-1">
      {#each ['cells', 'parameters', 'endpoint', 'runs', ...(owner && note.id ? ['sharing'] : [])] as s}
        <button class="btn whitespace-nowrap" class:bg-active={section === s} aria-pressed={section === s} onclick={() => (section = s)}>{t(`notebook.${s}`)}</button>
      {/each}
    </div>
    <div class="min-h-0 flex-1 overflow-auto">
      {#if section === 'cells'}
        <div class="toolbar overflow-x-auto">
          <button class="btn" disabled={!runnable || busy || tab.running || tab.dirty} onclick={() => run()}><Play size={13} />{t('notebook.run')}</button>
          <button class="btn" disabled={!runnable || busy || tab.running || tab.dirty} onclick={() => run('', true)}>{t('notebook.background')}</button>
          <button class="btn" disabled={!runnable || busy || tab.running || tab.dirty} onclick={() => run('', false, true)}>{t('notebook.preview')}</button>
          {#if tab.running}<button class="btn text-danger" onclick={() => tab.notebookAbort?.abort()}><Square size={12} />{t('notebook.cancel')}</button>{/if}
          <div class="flex-1"></div><button class="btn" onclick={addCell} disabled={!editable || busy}><Plus size={14} />{t('notebook.addCell')}</button>
        </div>
        <p class="border-b border-line-soft px-3 py-2 text-xs text-muted">{t('notebook.partial')}</p>
        <div class="min-w-0">
          {#each note.document.cells as cell, i (cell.id)}
            <NotebookCellEditor {tab} bind:cell={note.document.cells[i]} {i} last={i === note.document.cells.length - 1} {editable} {runnable} {busy} {changed} run={(path) => run(path)} {save} {move} {duplicate} {remove} />
          {/each}
        </div>
        {#if busy}<p class="px-3 py-2 text-xs text-muted" role="status">{t('notebook.running')}</p>{/if}
        {#if tab.notebookResult}
          <div class="flex flex-wrap items-center gap-2 border-b border-line bg-panel px-3 py-2 text-xs">
            <span class={tab.notebookResult.error ? 'text-danger' : 'text-muted'}>{t(`notebook.${tab.notebookResult.status === 'preview' ? 'previewStatus' : tab.notebookResult.status}`)}</span>
            <select class="input w-auto max-w-72 text-xs" bind:value={selected} aria-label={t('notebook.results')}>{#each tab.notebookResult.cells as c}<option value={c.path}>{c.path} · {t(`notebook.${c.status === 'preview' ? 'previewStatus' : c.status}`)}</option>{/each}</select>
            <span class="font-mono text-muted">{formatDuration(tab.notebookResult.duration_ms)}</span>
          </div>
          {@const cellResult = tab.notebookResult.cells.find((c) => c.path === selected)}
          {#if cellResult?.error}<p class="p-3 text-sm text-danger">{cellResult.error}</p>{/if}
          {#if cellResult?.sql}<pre class="overflow-auto p-3 font-mono text-xs">{cellResult.sql}{cellResult.args?.length ? '\n' + JSON.stringify(cellResult.args, null, 2) : ''}</pre>{/if}
          {#if resultTab}<div class="h-80 min-h-0 border-b border-line"><ResultPanel tab={resultTab} /></div>{/if}
        {:else}<p class="p-4 text-xs text-muted">{t('notebook.noResults')}</p>{/if}
      {:else if section === 'parameters'}
        <div class="max-w-4xl space-y-4 p-4">
          <p class="text-xs text-muted">{t('notebook.paramHint')}</p>
          <fieldset disabled={!editable || busy} class="space-y-3">
            {#each note.document.parameters as p, i}
              <div class="flex flex-wrap items-end gap-2">
                <label class="flex flex-col gap-1 text-xs">{t('notebook.name')}<input class="input w-36 font-mono" bind:value={p.name} oninput={changed} /></label>
                <label class="flex flex-col gap-1 text-xs">{t('notebook.type')}<select class="input" bind:value={p.type} onchange={changed}>{#each ['string', 'number', 'boolean', 'json'] as type}<option>{type}</option>{/each}</select></label>
                <label class="flex flex-col gap-1 text-xs">{t('notebook.defaultValue')}<input class="input w-64 font-mono" value={p.default === undefined ? '' : JSON.stringify(p.default)} onchange={(e) => { try { p.default = e.currentTarget.value.trim() ? JSON.parse(e.currentTarget.value) : undefined; changed(); error = ''; } catch { error = t('notebook.invalidJSON'); } }} /></label>
                <label class="flex h-7 items-center gap-1.5 text-xs"><input type="checkbox" bind:checked={p.required} onchange={changed} />{t('notebook.required')}</label>
                <button class="icon-btn" onclick={() => { note.document.parameters.splice(i, 1); changed(); }} title={t('common.delete')} aria-label={t('common.delete')}><Trash2 size={13} /></button>
              </div>
            {/each}
            <button class="btn" onclick={() => { note.document.parameters.push({ name: `param${note.document.parameters.length + 1}`, type: 'string', required: false }); changed(); }}><Plus size={13} />{t('notebook.addParameter')}</button>
          </fieldset>
          <label class="flex flex-col gap-2 text-xs">{t('notebook.inputData')}<textarea class="input h-48 w-full p-2 font-mono" value={dataText} oninput={(e) => { tab.notebookInput = e.currentTarget.value; persist(); }} spellcheck="false"></textarea></label>
        </div>
      {:else if section === 'endpoint'}
        <div class="max-w-4xl space-y-4 p-4 text-sm">
          <label class="flex max-w-md flex-col gap-1 text-xs">{t('notebook.path')}<input class="input font-mono" bind:value={note.path} oninput={changed} disabled={!editable || busy || note.published_version > 0} /></label>
          <p class="text-xs text-muted">{t('notebook.publishHint')}</p>
          <div class="flex flex-wrap items-center gap-2"><span class="chip">{t('notebook.draft', { version: note.version })}</span>{#if note.published_version}<span class="chip">{t('notebook.published', { version: note.published_version })}</span>{/if}
            {#if owner}<button class="btn-primary" disabled={!note.id || tab.dirty || busy} onclick={() => publish(true)}>{t('notebook.publish')}</button><button class="btn" disabled={!note.published_version || busy} onclick={() => publish(false)}>{t('notebook.unpublish')}</button>{/if}
          </div>
          <p class="text-xs text-muted">{t('notebook.endpointHint')}</p><p class="text-xs text-muted">{t('notebook.tokenHint')}</p>
          <code class="block overflow-x-auto border border-line bg-panel p-3 font-mono text-xs">POST {endpoint}<br />POST {endpoint}/&lt;cell-path&gt;</code>
          <pre class="overflow-auto border border-line bg-panel p-3 font-mono text-xs">{curl}</pre><button class="btn" onclick={() => copyText(curl, t('notebook.copied'))}>{t('notebook.copy')}</button>
          {#if owner && note.id}<div class="border-t border-line pt-4"><button class="btn text-danger" disabled={busy} onclick={deleteNote}><Trash2 size={13} />{t('common.delete')}</button></div>{/if}
        </div>
      {:else if section === 'runs'}
        <NotebookRuns bind:this={runsView} bind:offset={runsOffset} {note} {runnable} onerror={(msg) => (error = msg)} />
      {:else if section === 'sharing' && owner}
        <NotebookSharing {note} onerror={(msg) => (error = msg)} />
      {/if}
    </div>
  {/if}
</div>

{#if preview}
  <Modal title={t('notebook.confirmWrites')} width="max-w-3xl" onclose={() => { preview = null; pending = null; }}>
    <p class="mb-3 text-xs text-muted">{t('notebook.confirmHint')}</p>
    {#each preview.cells.filter((c) => c.confirmation) as c}<h3 class="mt-3 font-mono text-sm">{c.path}</h3><pre class="mt-1 overflow-auto border border-line bg-panel p-3 font-mono text-xs">{c.sql}{c.args?.length ? '\n' + JSON.stringify(c.args, null, 2) : ''}</pre>{/each}
    {#snippet footer()}<button class="btn" onclick={() => { preview = null; pending = null; }}>{t('common.cancel')}</button><button class="btn-danger" onclick={confirmRun}>{t('notebook.confirmRun')}</button>{/snippet}
  </Modal>
{/if}
