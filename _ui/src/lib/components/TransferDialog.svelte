<script lang="ts">
  import { untrack } from 'svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { ArrowRightLeft, Info, LoaderCircle, Check, TriangleAlert, ArrowRight } from 'lucide-svelte';
  import Modal from '@/lib/components/Modal.svelte';
  import TablePicker from '@/lib/components/TablePicker.svelte';
  import DbIcon from '@/lib/components/DbIcon.svelte';
  import { connection, loadSchemas, refreshConnection, refreshSchema, ws } from '@/lib/stores/workspace.svelte';
  import { closeToolDialog } from '@/lib/stores/dialogs.svelte';
  import { confirmProduction } from '@/lib/stores/confirm.svelte';
  import { askCredentials } from '@/lib/stores/credentials.svelte';
  import { serverMessage } from '@/lib/i18n/index.svelte';
  import type { TransferProgress } from '@/lib/api/types';

  let props: { connectionId: string; schema: string; tables: string[]; schemaFixed: boolean } = $props();

  // The dialog is made anew for each request, so its first values are all
  // it needs from the props.
  const { connectionId, schema: initialSchema, tables, schemaFixed } = untrack(() => ({ ...props }));

  const conn = $derived(connection(connectionId));

  let schema = $state(initialSchema);
  let selected = $state<string[]>([...tables]);
  const targets = $derived(ws.connections.filter((c) => !c.read_only));
  let targetId = $state('');
  let targetSchema = $state('');
  let create = $state(true);
  let data = $state(true);
  let truncate = $state(false);

  let running = $state(false);
  let finished = $state<TransferProgress | null>(null);
  let progress = $state<Record<string, TransferProgress>>({});
  let error = $state('');
  let abort: AbortController | null = null;

  $effect(() => {
    if (!targetId && targets.length) targetId = (targets.find((c) => c.id !== connectionId) ?? targets[0]).id;
  });
  $effect(() => {
    if (targetId) void loadSchemas(targetId);
  });

  const target = $derived(connection(targetId));
  const targetSchemas = $derived(ws.schemas[targetId]?.items ?? []);

  $effect(() => {
    // Copying into the same schema of the same connection would read and
    // write one table; pick another schema by default.
    const list = targetSchemas;
    if (!list.length) return;
    if (!targetSchema || !list.includes(targetSchema)) targetSchema = list.find((s) => !(targetId === connectionId && s === schema)) ?? list[0];
  });

  const same = $derived(targetId === connectionId && targetSchema === schema);

  async function start(confirm = false) {
    running = true;
    error = '';
    finished = null;
    progress = Object.fromEntries(selected.map((tb) => [tb, { table: tb, rows: 0 }]));
    abort = new AbortController();
    try {
      const res = await fetch(`./api/v1/connections/${connectionId}/transfer`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ schema, tables: selected, target_id: targetId, target_schema: targetSchema, create, data, truncate, confirm }),
        signal: abort.signal,
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        running = false;
        if (res.status === 409 && body.confirmation_required) {
          const what = t('transfer.what', { n: selected.length, target: target?.name ?? '', schema: targetSchema });
          if (await confirmProduction({ connection: target?.name ?? '', sql: what, risk: 'write' })) return start(true);
          return;
        }
        if (res.status === 428 && body.credentials_required) {
          if (await askCredentials(connectionId, serverMessage(body.message))) return start(confirm);
          error = serverMessage(body.message);
          return;
        }
        error = serverMessage(body.message || res.statusText);
        return;
      }

      const reader = res.body!.pipeThrough(new TextDecoderStream()).getReader();
      let buf = '';
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buf += value;
        let nl: number;
        while ((nl = buf.indexOf('\n')) >= 0) {
          const line = buf.slice(0, nl).trim();
          buf = buf.slice(nl + 1);
          if (!line) continue;
          const p = JSON.parse(line) as TransferProgress;
          if (p.finished) {
            finished = p;
            if (p.error) error = serverMessage(p.error);
          } else if (p.table) {
            progress[p.table] = p;
          }
        }
      }
    } catch (e) {
      if ((e as Error).name !== 'AbortError') error = e instanceof Error ? e.message : String(e);
    } finally {
      running = false;
      abort = null;
      if (targetId) void (targetSchema ? refreshSchema(targetId, targetSchema) : refreshConnection(targetId));
    }
  }

  function stop() {
    abort?.abort();
  }

  const list = $derived(Object.values(progress));
  const total = $derived(list.reduce((n, p) => n + (p.rows ?? 0), 0));
</script>

<Modal title={t('transfer.title')} onclose={() => (running ? undefined : closeToolDialog())} width="max-w-3xl">
  {#if !list.length}
    <div class="grid grid-cols-[1fr_auto_1fr] gap-4">
      <div class="min-w-0 space-y-2">
        <p class="flex items-center gap-2 text-sm font-semibold"><DbIcon type={conn?.db_type ?? ''} size={14} /> {t('transfer.source')}: {conn?.name}</p>
        <TablePicker {connectionId} bind:schema bind:selected {schemaFixed} views={false} />
      </div>
      <div class="flex items-center pt-6 text-faint"><ArrowRight size={18} /></div>
      <div class="min-w-0 space-y-3">
        <p class="text-sm font-semibold">{t('transfer.target')}</p>
        <div>
          <label class="label" for="tr-conn">{t('transfer.connection')}</label>
          <select id="tr-conn" class="input" bind:value={targetId}>
            {#each targets as c (c.id)}<option value={c.id}>{c.name} ({c.db_type})</option>{/each}
          </select>
        </div>
        {#if targetSchemas.length}
          <div>
            <label class="label" for="tr-schema">{t('tools.schema')}</label>
            <select id="tr-schema" class="input" bind:value={targetSchema}>
              {#each targetSchemas as s (s)}<option value={s}>{s}</option>{/each}
            </select>
          </div>
        {/if}
        <div class="space-y-2 pt-1">
          <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={create} /> {t('transfer.create')}</label>
          <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={data} /> {t('transfer.data')}</label>
          <label class="flex items-center gap-2 text-sm {truncate ? 'text-danger' : ''}"><input type="checkbox" bind:checked={truncate} disabled={!data} /> {t('transfer.truncate')}</label>
        </div>
        {#if target?.production}<p class="text-xs text-prod">{t('transfer.production')}</p>{/if}
        {#if same}<p class="text-xs text-danger">{t('transfer.same')}</p>{/if}
      </div>
    </div>
    <p class="mt-4 flex items-start gap-1.5 text-xs text-muted"><Info size={13} class="mt-0.5 shrink-0" /> {t('transfer.hint')}</p>
  {:else}
    <p class="mb-2 text-sm">
      {conn?.name}{schema ? ` · ${schema}` : ''} <ArrowRight size={12} class="inline text-faint" /> {target?.name}{targetSchema ? ` · ${targetSchema}` : ''}
    </p>
    <div class="max-h-80 overflow-auto rounded-md border border-line">
      {#each list as p (p.table)}
        <div class="flex h-8 items-center gap-2 border-b border-line-soft px-3 text-sm last:border-0">
          {#if p.error}<TriangleAlert size={13} class="shrink-0 text-danger" />
          {:else if p.done}<Check size={13} class="shrink-0 text-ok" />
          {:else if running}<LoaderCircle size={13} class="shrink-0 animate-spin text-muted" />
          {:else}<span class="size-[13px] shrink-0"></span>{/if}
          <span class="w-48 truncate font-mono text-xs">{p.table}</span>
          <span class="text-xs text-muted tabular-nums">{t('transfer.rows', { n: (p.rows ?? 0).toLocaleString() })}</span>
          {#if p.created}<span class="rounded bg-sunken px-1 text-[10px] text-muted">{t('transfer.created')}</span>{/if}
          {#if p.error}<span class="min-w-0 flex-1 truncate text-xs text-danger" title={p.error}>{p.error}</span>{/if}
        </div>
      {/each}
    </div>
    {#if finished}
      <p class="mt-3 text-sm {finished.failed ? 'text-warn' : 'text-ok'}">
        {t('transfer.finished', { tables: finished.tables ?? 0, rows: (finished.rows ?? 0).toLocaleString(), failed: finished.failed ?? 0 })}
      </p>
    {/if}
  {/if}

  {#if error}
    <p class="mt-3 flex items-start gap-1.5 rounded-md border border-danger/30 bg-danger-soft p-2 text-xs text-danger"><TriangleAlert size={13} class="mt-0.5 shrink-0" /> {error}</p>
  {/if}

  {#snippet footer()}
    {#if running}
      <span class="mr-auto flex items-center gap-2 text-xs text-muted"><LoaderCircle size={13} class="animate-spin" /> {t('transfer.running', { n: total.toLocaleString() })}</span>
      <button class="btn-outline" onclick={stop}>{t('transfer.stop')}</button>
    {:else if list.length}
      <button class="btn-outline mr-auto" onclick={() => ((progress = {}), (finished = null), (error = ''))}>{t('transfer.again')}</button>
      <button class="btn-primary" onclick={closeToolDialog}>{t('common.close')}</button>
    {:else}
      <button class="btn-outline" onclick={closeToolDialog}>{t('common.cancel')}</button>
      <button class="btn-primary" disabled={!selected.length || !targetId || same || (!create && !data)} onclick={() => start()}>
        <ArrowRightLeft size={14} />
        {t('transfer.start')}
      </button>
    {/if}
  {/snippet}
</Modal>
