<script lang="ts">
  import { t, i18n } from '@/lib/i18n/index.svelte';
  import { Send, LoaderCircle, CircleCheck, TriangleAlert, Plus, Trash2 } from 'lucide-svelte';
  import { api } from '@/lib/api/client';
  import { admin, loadConnections, loadUsers } from '@/pages/admin/admin.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  // Pushes saved documentation to a search service's push collection
  // after each refresh of all documentation, for search by agents.
  interface Target {
    connection_id: string;
    user_id: string;
    schemas: string[];
  }

  interface Status {
    running: boolean;
    manual: boolean;
    started_at: number;
    finished_at: number;
    tables: number;
    sent: number;
    changed: number;
    removed: number;
    unchanged?: boolean;
    error?: string;
    failed?: string[];
  }

  interface Export {
    enabled: boolean;
    url: string;
    collection: string;
    has_token: boolean;
    targets: Target[];
    status: Status;
  }

  interface FormTarget {
    connection_id: string;
    user_id: string;
    schemas: string;
  }

  let saved = $state<Export | null>(null);
  let enabled = $state(false);
  let url = $state('');
  let collection = $state('');
  let token = $state('');
  let clearToken = $state(false);
  let targets = $state<FormTarget[]>([]);
  let error = $state('');
  let saving = $state(false);
  let starting = $state(false);

  const status = $derived(saved?.status);
  // Personal connections belong to their owner and are never exported.
  const connections = $derived(admin.connections.filter((c) => !c.owner_id));
  const users = $derived(admin.users.filter((u) => !u.disabled));

  const splitSchemas = (v: string) => v.split(',').map((s) => s.trim()).filter(Boolean);
  const norm = (v: { enabled: boolean; url: string; collection: string; targets: Target[] }) =>
    JSON.stringify([v.enabled, v.url.trim().replace(/\/+$/, ''), v.collection.trim() || 'dataport', v.targets.map((x) => [x.connection_id, x.user_id, x.schemas])]);
  const formTargets = $derived(targets.map((x) => ({ connection_id: x.connection_id, user_id: x.user_id, schemas: splitSchemas(x.schemas) })));
  const dirty = $derived(!!saved && (norm({ enabled, url, collection, targets: formTargets }) !== norm(saved) || token.trim() !== '' || clearToken));
  const complete = $derived(targets.every((x) => x.connection_id && x.user_id));

  function apply(v: Export, form = true) {
    saved = v;
    if (!form) return;
    enabled = v.enabled;
    url = v.url;
    collection = v.collection;
    token = '';
    clearToken = false;
    targets = v.targets.map((x) => ({ connection_id: x.connection_id, user_id: x.user_id, schemas: x.schemas.join(', ') }));
  }

  async function load(form = true) {
    try {
      apply(await api.get<Export>('/admin/docs/export'), form);
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  void Promise.all([load(), loadConnections(), loadUsers()]).catch((e) => toast(errorMessage(e), 'error'));

  $effect(() => {
    if (!status?.running) return;
    const id = setInterval(() => {
      if (!document.hidden) void load(false);
    }, 2000);
    return () => clearInterval(id);
  });

  async function save(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    error = '';
    try {
      const body: Record<string, unknown> = { enabled, url: url.trim(), collection: collection.trim(), targets: formTargets };
      if (token.trim()) body.token = token.trim();
      else if (clearToken) body.token = '';
      apply(await api.put<Export>('/admin/docs/export', body));
      toast(t('admin.docsExport.saved'), 'success');
    } catch (err) {
      error = errorMessage(err);
    } finally {
      saving = false;
    }
  }

  async function sendNow() {
    starting = true;
    try {
      apply(await api.post<Export>('/admin/docs/export/run'), false);
    } catch (e) {
      toast(errorMessage(e), 'error');
    } finally {
      starting = false;
    }
  }

  const pushApi = [
    'GET    {url}/api/v1/sources/{collection}',
    '       → {"source":{"type":"push","page_count":n}}, 404 when missing',
    'POST   {url}/api/v1/sources',
    '       {"name":"{collection}","type":"push","description":"…"}',
    'POST   {url}/api/v1/sources/{collection}/items',
    '       {"items":[{"id","title","content_type":"text/markdown",',
    '                  "content","teams","updated_at"}]}   (≤ 20 per request)',
    'POST   {url}/api/v1/sources/{collection}/items/prune',
    '       {"keep":["id", …]} → {"removed":n}',
    'DELETE {url}/api/v1/sources/{collection}/items?id={id}',
    '',
    'Authorization: Bearer {token}   (when a token is saved)',
  ].join('\n');

  const fullTime = (at: number) => new Date(at).toLocaleString(i18n.locale, { dateStyle: 'medium', timeStyle: 'short' });
</script>

<form class="space-y-3 border-t border-line pt-5" onsubmit={save}>
  <div class="flex items-start justify-between gap-3">
    <div>
      <h3 class="text-sm font-semibold">{t('admin.docsExport.title')}</h3>
      <p class="hint mt-0.5">{t('admin.docsExport.description')}</p>
    </div>
    <button type="button" class="btn-outline shrink-0" onclick={sendNow} disabled={!saved?.enabled || !saved.url || dirty || starting || status?.running}>
      {#if status?.running}<LoaderCircle size={14} class="animate-spin" />{:else}<Send size={14} />{/if}
      {t(status?.running ? 'admin.docsExport.sending' : 'admin.docsExport.sendNow')}
    </button>
  </div>

  {#if status && (status.running || status.finished_at)}
    <div class="space-y-1 rounded-md border border-line p-3 text-xs" role="status" aria-live="polite">
      {#if status.running}
        <p class="flex items-center gap-2 text-sm"><LoaderCircle size={14} class="animate-spin text-accent" /> {t('admin.docsExport.running')}</p>
      {:else}
        <p class="flex flex-wrap items-center gap-1.5 text-sm">
          {#if status.error || status.failed?.length}<TriangleAlert size={14} class="text-warn" />{:else}<CircleCheck size={14} class="text-ok" />{/if}
          {status.unchanged
            ? t('admin.docsExport.lastUnchanged', { time: fullTime(status.finished_at), tables: status.tables })
            : t('admin.docsExport.last', { time: fullTime(status.finished_at), tables: status.tables, sent: status.sent, removed: status.removed })}
        </p>
        {#if status.error}<p class="text-warn">{status.error}</p>{/if}
        {#each status.failed ?? [] as f (f)}<p class="text-warn">{f}</p>{/each}
      {/if}
    </div>
  {/if}

  <details class="rounded-md border border-line px-3 py-2 text-xs">
    <summary class="cursor-pointer text-sm font-medium">{t('admin.docsExport.howTitle')}</summary>
    <div class="mt-2 space-y-1.5 text-muted">
      <p>{t('admin.docsExport.howWhen')}</p>
      <p>{t('admin.docsExport.howWhat')}</p>
      <p>{t('admin.docsExport.howIds')}</p>
      <p>{t('admin.docsExport.howSync')}</p>
      <p>{t('admin.docsExport.howApi')}</p>
      <pre class="overflow-auto rounded bg-panel p-2 font-mono text-[11px] leading-relaxed text-fg">{pushApi}</pre>
    </div>
  </details>

  <label class="flex items-center gap-2 text-sm font-medium">
    <input type="checkbox" bind:checked={enabled} disabled={!saved || saving} />
    {t('admin.docsExport.enabled')}
  </label>

  <div class="grid gap-3 sm:grid-cols-2">
    <div>
      <label class="label" for="push-url">{t('admin.docsExport.url')}</label>
      <input id="push-url" class="input w-full font-mono" type="url" placeholder="https://search.example.com" bind:value={url} disabled={!saved || saving} />
    </div>
    <div>
      <label class="label" for="push-collection">{t('admin.docsExport.collection')}</label>
      <input id="push-collection" class="input w-full font-mono" placeholder="dataport" maxlength="128" bind:value={collection} disabled={!saved || saving} />
    </div>
  </div>

  <div>
    <label class="label" for="push-token">{t('admin.docsExport.token')}</label>
    <input id="push-token" class="input w-full font-mono" type="password" autocomplete="off" placeholder={saved?.has_token ? t('admin.docsExport.tokenKept') : t('admin.docsExport.tokenNone')} bind:value={token} disabled={!saved || saving || clearToken} />
    {#if saved?.has_token}
      <label class="mt-1 flex items-center gap-2 text-xs text-muted">
        <input type="checkbox" bind:checked={clearToken} disabled={saving} />
        {t('admin.docsExport.tokenClear')}
      </label>
    {/if}
    <p class="hint mt-1">{t('admin.docsExport.tokenHint')}</p>
  </div>

  <div class="space-y-1.5">
    <p class="label">{t('admin.docsExport.targets')}</p>
    <p class="hint">{t('admin.docsExport.targetsHint')}</p>
    {#each targets as target, i (i)}
      <div class="flex flex-wrap items-center gap-2">
        <select class="input w-48" bind:value={target.connection_id} aria-label={t('admin.log.connection')} disabled={saving}>
          <option value="">{t('admin.docsExport.pickConnection')}</option>
          {#each connections as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
        </select>
        <select class="input w-40" bind:value={target.user_id} aria-label={t('admin.docsExport.asUser')} disabled={saving}>
          <option value="">{t('admin.docsExport.pickUser')}</option>
          {#each users as u (u.id)}<option value={u.id}>{u.display_name || u.username}</option>{/each}
        </select>
        <input class="input min-w-40 flex-1 font-mono" placeholder={t('admin.docsExport.schemasPlaceholder')} aria-label={t('admin.docs.schema')} bind:value={target.schemas} disabled={saving} />
        <button type="button" class="btn" onclick={() => targets.splice(i, 1)} aria-label={t('admin.docsExport.remove')} disabled={saving}><Trash2 size={14} /></button>
      </div>
    {:else}
      <p class="text-xs text-muted">{t('admin.docsExport.noTargets')}</p>
    {/each}
    <button type="button" class="btn" onclick={() => targets.push({ connection_id: '', user_id: '', schemas: '' })} disabled={saving}>
      <Plus size={14} />
      {t('admin.docsExport.add')}
    </button>
  </div>

  <p class="hint text-warn">{t('admin.docsExport.warning')}</p>
  {#if error}<p role="alert" class="text-xs text-danger">{error}</p>{/if}
  <button type="submit" class="btn-primary" disabled={!dirty || !complete || saving}>{saving ? t('common.loading') : t('common.save')}</button>
</form>
