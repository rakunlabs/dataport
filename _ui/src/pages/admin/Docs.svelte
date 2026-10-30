<script lang="ts">
  import { t, i18n } from '@/lib/i18n/index.svelte';
  import { Play, LoaderCircle, CircleCheck, TriangleAlert, CircleSlash, Circle } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import DocsExport from '@/pages/admin/DocsExport.svelte';
  import DocsConnections from '@/pages/admin/DocsConnections.svelte';
  import NotebookAccess from '@/pages/admin/NotebookAccess.svelte';
  import { api } from '@/lib/api/client';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { openDocs } from '@/lib/stores/workspace.svelte';
  import { session } from '@/lib/stores/session.svelte';
  import type { DocInfo, DocRun } from '@/lib/api/types';

  const sessionUser = $derived(session.user?.id ?? '');

  interface DocSchedule {
    enabled: boolean;
    cron_spec: string;
    next_run: number;
    run: DocRun;
    history: DocRun[];
    snapshots: DocInfo[];
  }

  let saved = $state<DocSchedule | null>(null);
  let enabled = $state(true);
  let draft = $state('');
  let error = $state('');
  let saving = $state(false);
  let starting = $state(false);
  let now = $state(Date.now());

  const run = $derived(saved?.run);
  const dirty = $derived(!!saved && (enabled !== saved.enabled || draft.trim() !== saved.cron_spec));

  function apply(v: DocSchedule, form = true) {
    saved = v;
    if (form) {
      enabled = v.enabled;
      draft = v.cron_spec;
    }
  }

  async function load(form = true) {
    try {
      apply(await api.get<DocSchedule>('/admin/docs/schedule'), form);
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  void load();

  // Follow a running refresh, and notice one started by the schedule.
  $effect(() => {
    const id = setInterval(() => {
      now = Date.now();
      if (!document.hidden) void load(false);
    }, run?.running ? 2000 : 30000);
    return () => clearInterval(id);
  });

  async function save(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    error = '';
    try {
      apply(await api.put<DocSchedule>('/admin/docs/schedule', { enabled, cron_spec: draft.trim() }));
      toast(t('admin.docs.saved'), 'success');
    } catch (e) {
      error = errorMessage(e);
    } finally {
      saving = false;
    }
  }

  async function runNow() {
    starting = true;
    try {
      apply(await api.post<DocSchedule>('/admin/docs/run'), false);
    } catch (e) {
      toast(errorMessage(e), 'error');
    } finally {
      starting = false;
    }
  }

  // A rough duration such as "2 min" or "45 s", for estimates.
  function span(ms: number): string {
    const s = Math.max(1, Math.round(ms / 1000));
    if (s < 60) return t('admin.docs.seconds', { n: s });
    const m = Math.round(s / 60);
    if (m < 120) return t('admin.docs.minutes', { n: m });
    return t('admin.docs.hours', { n: Math.round(m / 60) });
  }

  const fullTime = (at: number) => new Date(at).toLocaleString(i18n.locale, { dateStyle: 'medium', timeStyle: 'short' });
  const percent = $derived(run && run.total ? Math.round((run.done / run.total) * 100) : 0);

  const statusIcon = { running: LoaderCircle, done: CircleCheck, failed: TriangleAlert, stopped: CircleSlash } as const;
  const statusClass = { running: 'animate-spin text-accent', done: 'text-ok', failed: 'text-warn', stopped: 'text-muted' } as const;
  const runStatus = (r: DocRun) => (r.status || (r.running ? 'running' : 'done')) as keyof typeof statusIcon;
</script>

<Page title={t('admin.docs.title')} description={t('admin.docs.description')}>
  {#snippet actions()}
    <button class="btn-outline" onclick={runNow} disabled={!saved || starting || run?.running}>
      {#if run?.running}<LoaderCircle size={14} class="animate-spin" />{:else}<Play size={14} />{/if}
      {t(run?.running ? 'admin.docs.running' : 'admin.docs.runNow')}
    </button>
  {/snippet}

  <div class="max-w-3xl space-y-6 px-6 py-5">
    {#if run && (run.running || run.finished_at)}
      <section class="space-y-2 rounded-md border border-line p-3" role="status" aria-live="polite">
        {#if run.running}
          <div class="flex items-center justify-between gap-3 text-sm">
            <span class="flex items-center gap-2 font-medium"><LoaderCircle size={14} class="animate-spin text-accent" /> {t(run.manual ? 'admin.docs.runManual' : 'admin.docs.runScheduled')}</span>
            <span class="text-xs text-muted tabular-nums">{t('admin.docs.progress', { done: run.done, total: run.total })}</span>
          </div>
          <div class="h-1.5 overflow-hidden rounded-full bg-line" role="progressbar" aria-valuemin={0} aria-valuemax={run.total} aria-valuenow={run.done}>
            <div class="h-full bg-accent transition-all" style="width: {percent}%"></div>
          </div>
          <p class="text-xs text-muted">
            {t('admin.docs.startedAt', { time: fullTime(run.started_at) })}
            ·
            {run.estimated_end
              ? t('admin.docs.eta', { time: fullTime(run.estimated_end), left: span(Math.max(0, run.estimated_end - now)) })
              : t('admin.docs.etaUnknown')}
          </p>
          <p class="text-xs text-muted">{t('admin.docs.keepsOld')}</p>
        {:else}
          {@const st = runStatus(run)}
          {@const Icon = statusIcon[st]}
          <p class="flex flex-wrap items-center gap-1.5 text-sm">
            <Icon size={14} class={statusClass[st]} />
            {t('admin.docs.lastRun', { time: fullTime(run.finished_at), done: run.done, duration: span(run.finished_at - run.started_at) })}
            {#if run.failed}<span class="text-warn"> · {t('admin.docs.failedCount', { count: run.failed })}</span>{/if}
            {#if st === 'stopped'}<span class="text-muted"> · {t('admin.docs.status.stopped')}</span>{/if}
          </p>
        {/if}
      </section>
    {/if}

    <form class="space-y-3" onsubmit={save}>
      <label class="flex items-center gap-2 text-sm font-medium">
        <input type="checkbox" bind:checked={enabled} disabled={!saved || saving} />
        {t('admin.docs.enabled')}
      </label>
      <div>
        <label class="label" for="docs-cron">{t('admin.docs.cron')}</label>
        <div class="flex flex-wrap items-center gap-2">
          <input id="docs-cron" class="input min-w-64 flex-1 font-mono" maxlength="256" placeholder="0 0 * * *" bind:value={draft} aria-describedby="docs-cron-hint" aria-invalid={!!error} disabled={!saved || saving || !enabled} />
          <button type="button" class="btn" onclick={() => (draft = '0 0 * * *')} disabled={saving || !enabled}>{t('admin.docs.midnight')}</button>
          <button type="button" class="btn" onclick={() => (draft = '@hourly')} disabled={saving || !enabled}>{t('admin.docs.hourly')}</button>
        </div>
      </div>
      <p id="docs-cron-hint" class="text-xs text-muted">{t('admin.docs.hint')}</p>
      {#if saved}
        <p class="text-xs text-muted">{saved.next_run ? t('admin.docs.next', { time: fullTime(saved.next_run) }) : t('admin.docs.disabled')}</p>
      {/if}
      {#if error}<p role="alert" class="text-xs text-danger">{error}</p>{/if}
      <button type="submit" class="btn-primary" disabled={!dirty || saving}>{saving ? t('common.loading') : t('common.save')}</button>
    </form>

    {#if saved}
      <DocsConnections running={!!run?.running} onchange={() => load(false)} />

      <section class="space-y-2">
        <h3 class="text-sm font-semibold">{t('admin.docs.history')}</h3>
        {#if !saved.history.length}
          <p class="text-xs text-muted">{t('admin.docs.historyEmpty')}</p>
        {:else}
          <div class="overflow-hidden rounded-md border border-line">
            <table class="w-full text-left text-xs">
              <thead class="bg-panel text-muted">
                <tr>
                  <th class="px-3 py-1.5 font-medium">{t('admin.docs.started')}</th>
                  <th class="px-3 py-1.5 font-medium">{t('admin.docs.trigger')}</th>
                  <th class="px-3 py-1.5 font-medium">{t('admin.docs.result')}</th>
                  <th class="px-3 py-1.5 text-right font-medium">{t('admin.docs.duration')}</th>
                </tr>
              </thead>
              <tbody>
                {#each saved.history as h (h.id)}
                  {@const st = runStatus(h)}
                  {@const Icon = statusIcon[st]}
                  <tr class="border-t border-line-soft">
                    <td class="px-3 py-1.5 whitespace-nowrap tabular-nums">{fullTime(h.started_at)}</td>
                    <td class="px-3 py-1.5 text-muted">{h.manual ? (h.username ? t('admin.docs.manualBy', { user: h.username }) : t('admin.docs.manual')) : t('admin.docs.scheduled')}</td>
                    <td class="px-3 py-1.5">
                      <span class="inline-flex items-center gap-1.5">
                        <Icon size={12} class={statusClass[st]} />
                        {t(`admin.docs.status.${st}`)}
                        <span class="text-muted tabular-nums">
                          · {h.total ? t('admin.docs.counts', { done: h.done, total: h.total }) : t('admin.docs.nothingDue')}
                        </span>
                        {#if h.failed}<span class="text-warn">· {t('admin.docs.failedCount', { count: h.failed })}</span>{/if}
                      </span>
                    </td>
                    <td class="px-3 py-1.5 text-right text-muted tabular-nums">{h.finished_at ? span(h.finished_at - h.started_at) : span(now - h.started_at)}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>

      <section class="space-y-2">
        <h3 class="text-sm font-semibold">{t('admin.docs.snapshots')}</h3>
        <p class="text-xs text-muted">{t('admin.docs.snapshotsHint')}</p>
        {#if !saved.snapshots.length}
          <p class="text-xs text-muted">{t('admin.docs.snapshotsEmpty')}</p>
        {:else}
          <div class="max-h-96 overflow-auto rounded-md border border-line">
            <table class="w-full text-left text-xs">
              <thead class="sticky top-0 bg-panel text-muted">
                <tr>
                  <th class="px-3 py-1.5 font-medium">{t('admin.log.connection')}</th>
                  <th class="px-3 py-1.5 font-medium">{t('admin.docs.schema')}</th>
                  <th class="px-3 py-1.5 font-medium">{t('admin.docs.owner')}</th>
                  <th class="px-3 py-1.5 font-medium">{t('admin.docs.generated')}</th>
                </tr>
              </thead>
              <tbody>
                {#each saved.snapshots as d (`${d.connection_id}|${d.user_id}|${d.schema}`)}
                  <tr class="border-t border-line-soft">
                    <td class="px-3 py-1.5">{d.connection_name}</td>
                    <td class="px-3 py-1.5 font-mono">
                      {#if d.user_id === sessionUser || d.system}
                        <button class="text-accent hover:underline" onclick={() => openDocs(d.connection_id, d.schema)}>{d.schema}</button>
                      {:else}{d.schema}{/if}
                    </td>
                    <td class="px-3 py-1.5 text-muted">{d.system ? t('admin.docConn.everyone') : d.username}</td>
                    <td class="px-3 py-1.5">
                      <span class="inline-flex items-center gap-1.5">
                        {#if d.refreshing}<LoaderCircle size={12} class="animate-spin text-accent" />
                        {:else if d.last_error}<TriangleAlert size={12} class="text-warn" />
                        {:else if d.ready}<CircleCheck size={12} class="text-ok" />
                        {:else}<Circle size={12} class="text-faint" />{/if}
                        {d.ready ? fullTime(d.generated_at) : t('admin.docs.notYet')}
                        {#if d.last_error}<span class="text-warn">· {t('admin.docs.refreshFailed')}</span>{/if}
                      </span>
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>
    {/if}

    <div class="-mx-6"><NotebookAccess feature="docs" /></div>

    <DocsExport />
  </div>
</Page>
