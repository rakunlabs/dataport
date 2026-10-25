<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { PlugZap, LoaderCircle, CircleCheck, CircleX, X, Users, UsersRound, ChevronRight } from 'lucide-svelte';
  import Modal from '@/lib/components/Modal.svelte';
  import { api } from '@/lib/api/client';
  import type { Access, Connection, Limits } from '@/lib/api/types';
  import LimitsFields from '@/lib/components/LimitsFields.svelte';
  import { admin, groupName, userName } from '@/pages/admin/admin.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let {
    connection,
    onclose,
    onsaved,
  }: { connection: Connection | null; onclose: () => void; onsaved: () => void } = $props();

  // svelte-ignore state_referenced_locally
  const c = connection;

  let form = $state({
    name: c?.name ?? '',
    description: c?.description ?? '',
    db_type: c?.db_type ?? 'postgres',
    host: c?.host ?? '',
    port: c?.port || ('' as number | ''),
    database: c?.database ?? '',
    username: c?.username ?? '',
    password: '',
    options: { ...(c?.options ?? {}) } as Record<string, string>,
    color: c?.color ?? '',
    read_only: c?.read_only ?? true,
    production: c?.production ?? false,
    user_credentials: c?.user_credentials ?? false,
    limits: { ...(c?.limits ?? {}) } as Limits,
    max_rows: c?.max_rows || ('' as number | ''),
    timeout_s: (c ? Math.round(c.timeout_ms / 1000) : 0) || ('' as number | ''),
    access: (c?.access ?? []).map((a) => ({ ...a })) as Access[],
  });

  let changePassword = $state(!c);
  let busy = $state(false);
  let testing = $state(false);
  let test = $state<{ ok: boolean; message?: string; duration_ms?: number } | null>(null);
  let error = $state('');
  let pick = $state('');

  const driver = $derived(admin.drivers.find((d) => d.type === form.db_type));
  const colors = ['', '#ef4444', '#f59e0b', '#22c55e', '#3b82f6', '#a855f7', '#ec4899'];

  function body() {
    return {
      name: form.name,
      description: form.description,
      db_type: form.db_type,
      host: form.host,
      port: Number(form.port) || 0,
      database: form.database,
      username: form.username,
      password: changePassword ? form.password : null,
      options: Object.fromEntries(Object.entries(form.options).filter(([, v]) => v !== '')),
      color: form.color,
      read_only: form.read_only,
      production: form.production,
      user_credentials: form.user_credentials,
      limits: form.limits,
      max_rows: Number(form.max_rows) || 0,
      timeout_ms: (Number(form.timeout_s) || 0) * 1000,
      access: form.access,
    };
  }

  async function runTest() {
    testing = true;
    test = null;
    try {
      test = await api.post('/admin/connections/test', { ...body(), id: c?.id ?? '' });
    } catch (e) {
      test = { ok: false, message: errorMessage(e) };
    } finally {
      testing = false;
    }
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      if (c) await api.put(`/admin/connections/${c.id}`, body());
      else await api.post('/admin/connections', body());
      toast(c ? t('admin.conn.updated') : t('admin.conn.created'), 'success');
      onsaved();
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }

  function addAccess() {
    if (!pick) return;
    const [subject_type, subject_id] = pick.split(':') as ['user' | 'group', string];
    if (!form.access.some((a) => a.subject_type === subject_type && a.subject_id === subject_id)) {
      form.access.push({ subject_type, subject_id, read_only: true });
    }
    pick = '';
  }

  const available = $derived({
    groups: admin.groups.filter((g) => !form.access.some((a) => a.subject_type === 'group' && a.subject_id === g.id)),
    users: admin.users.filter((u) => u.role !== 'admin' && !form.access.some((a) => a.subject_type === 'user' && a.subject_id === u.id)),
  });
</script>

<Modal title={c ? t('admin.conn.edit', { name: c.name }) : t('admin.conn.new')} {onclose} width="max-w-2xl">
  <form id="conn-form" onsubmit={submit} class="space-y-5">
    <section class="grid grid-cols-6 gap-3">
      <div class="col-span-4">
        <label class="label" for="c-name">{t('admin.conn.name')}</label>
        <input id="c-name" class="input" bind:value={form.name} required maxlength="100" placeholder={t('admin.conn.namePlaceholder')} />
      </div>
      <div class="col-span-2">
        <label class="label" for="c-type">{t('admin.conn.type')}</label>
        <select id="c-type" class="input" bind:value={form.db_type}>
          {#each admin.drivers as d (d.type)}<option value={d.type}>{d.type}</option>{/each}
        </select>
      </div>
      <div class="col-span-6">
        <label class="label" for="c-desc">{t('admin.conn.descriptionLabel')} <span class="font-normal text-faint">{t('admin.conn.visibleToUsers')}</span></label>
        <input id="c-desc" class="input" bind:value={form.description} />
      </div>
      <div class="col-span-4">
        <label class="label" for="c-host">{t('admin.conn.host')}</label>
        <input id="c-host" class="input font-mono" bind:value={form.host} required placeholder="db.internal" />
      </div>
      <div class="col-span-2">
        <label class="label" for="c-port">{t('admin.conn.port')}</label>
        <input id="c-port" class="input font-mono" type="number" min="0" max="65535" bind:value={form.port} placeholder={String(driver?.default_port ?? '')} />
      </div>
      <div class="col-span-2">
        <label class="label" for="c-db">{t('admin.conn.database')}</label>
        <input id="c-db" class="input font-mono" bind:value={form.database} />
      </div>
      <div class="col-span-6">
        <span class="label">{t('admin.conn.auth')}</span>
        <div class="grid grid-cols-2 gap-2">
          {#each [false, true] as perUser (perUser)}
            <label
              class="flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm {form.user_credentials === perUser
                ? 'border-accent bg-accent-soft'
                : 'border-line hover:bg-hover'}"
            >
              <input type="radio" name="c-auth" class="mt-0.5" checked={form.user_credentials === perUser} onchange={() => (form.user_credentials = perUser)} />
              <span>
                <span class="block font-medium">{perUser ? t('admin.conn.authPerUser') : t('admin.conn.authShared')}</span>
                <span class="block text-xs text-muted">{perUser ? t('admin.conn.authPerUserHint') : t('admin.conn.authSharedHint')}</span>
              </span>
            </label>
          {/each}
        </div>
        {#if form.user_credentials}
          <p class="mt-1.5 text-xs text-muted">{t('admin.conn.testAccountHint')}</p>
        {/if}
      </div>
      <div class="col-span-2">
        <label class="label" for="c-user">{form.user_credentials ? t('admin.conn.testUsername') : t('admin.conn.username')}</label>
        <input id="c-user" class="input font-mono" bind:value={form.username} autocomplete="off" />
      </div>
      <div class="col-span-2">
        <label class="label" for="c-pass">{form.user_credentials ? t('admin.conn.testPassword') : t('admin.conn.password')}</label>
        {#if changePassword || form.user_credentials}
          <input id="c-pass" class="input font-mono" type="password" bind:value={form.password} autocomplete="new-password" />
        {:else}
          <button type="button" class="input text-left text-muted" onclick={() => (changePassword = true)}>
            {c?.has_password ? t('admin.unchanged') : t('admin.notSet')}
          </button>
        {/if}
      </div>
      <div class="col-span-3">
        {#if form.db_type === 'postgres'}
          <label class="label" for="c-ssl">{t('admin.conn.sslMode')}</label>
          <select id="c-ssl" class="input" bind:value={form.options.sslmode}>
            <option value="">{t('admin.conn.default', { value: 'prefer' })}</option>
            {#each ['disable', 'allow', 'require', 'verify-ca', 'verify-full'] as m (m)}<option value={m}>{m}</option>{/each}
          </select>
        {:else if form.db_type === 'mssql'}
          <label class="label" for="c-enc">{t('admin.conn.encryption')}</label>
          <div class="flex gap-2">
            <select id="c-enc" class="input" bind:value={form.options.encrypt}>
              <option value="">{t('admin.conn.encRequired')}</option>
              <option value="disable">{t('admin.conn.encDisabled')}</option>
              <option value="false">{t('admin.conn.encLogin')}</option>
              <option value="strict">{t('admin.conn.encStrict')}</option>
            </select>
            <label class="flex shrink-0 items-center gap-1.5 text-xs" title={t('admin.conn.trustCertHint')}>
              <input type="checkbox" checked={form.options.trust_server_certificate === 'true'}
                onchange={(e) => (form.options.trust_server_certificate = (e.target as HTMLInputElement).checked ? 'true' : '')} />
              {t('admin.conn.trustCert')}
            </label>
          </div>
        {:else if form.db_type === 'clickhouse'}
          <label class="label" for="c-sec">{t('admin.conn.tlsNative')}</label>
          <select id="c-sec" class="input" bind:value={form.options.secure}>
            <option value="">{t('admin.conn.chOff')}</option>
            <option value="true">{t('admin.conn.chOn')}</option>
            <option value="skip-verify">{t('admin.conn.chSkip')}</option>
          </select>
        {:else}
          <label class="label" for="c-tls">{t('admin.conn.tls')}</label>
          <select id="c-tls" class="input" bind:value={form.options.tls}>
            <option value="">{t('admin.conn.default', { value: 'preferred' })}</option>
            {#each ['false', 'true', 'skip-verify'] as m (m)}<option value={m}>{m}</option>{/each}
          </select>
        {/if}
      </div>
      <div class="col-span-3">
        <span class="label">{t('admin.conn.color')}</span>
        <div class="flex h-7 items-center gap-2">
          {#each colors as col (col)}
            <button
              type="button"
              class="size-5 rounded-full border {form.color === col ? 'ring-2 ring-focus ring-offset-2 ring-offset-raised' : 'border-line'}"
              style="background:{col || 'transparent'}"
              onclick={() => (form.color = col)}
              aria-label={col || t('admin.conn.colorNone')}
            ></button>
          {/each}
        </div>
      </div>
    </section>

    <section class="flex items-center gap-3">
      <button type="button" class="btn-outline" onclick={runTest} disabled={testing || !form.host}>
        {#if testing}<LoaderCircle size={14} class="animate-spin" />{:else}<PlugZap size={14} />{/if}
        {t('admin.conn.test')}
      </button>
      {#if test}
        {#if test.ok}
          <span class="flex items-center gap-1.5 text-xs text-ok"><CircleCheck size={14} /> {t('admin.conn.connectedIn', { ms: test.duration_ms ?? 0 })}</span>
        {:else}
          <span class="flex min-w-0 items-center gap-1.5 text-xs text-danger"><CircleX size={14} class="shrink-0" /> <span class="truncate" title={test.message}>{test.message}</span></span>
        {/if}
      {/if}
    </section>

    <section class="grid grid-cols-3 gap-3">
      <div>
        <span class="label">{t('admin.conn.mode')}</span>
        <label class="flex h-6 items-center gap-2 text-sm">
          <input type="checkbox" bind:checked={form.read_only} />
          {t('admin.conn.readOnlyAll')}
        </label>
        <label class="flex h-6 items-center gap-2 text-sm" title={t('admin.conn.productionHint')}>
          <input type="checkbox" bind:checked={form.production} disabled={form.read_only} />
          {t('admin.conn.production')}
        </label>
      </div>
      <div>
        <label class="label" for="c-rows">{t('admin.conn.maxRows')} <span class="font-normal text-faint">{t('admin.defaultHint')}</span></label>
        <input id="c-rows" class="input" type="number" min="0" bind:value={form.max_rows} />
      </div>
      <div>
        <label class="label" for="c-timeout">{t('admin.conn.timeout')} <span class="font-normal text-faint">{t('admin.defaultHint')}</span></label>
        <input id="c-timeout" class="input" type="number" min="0" bind:value={form.timeout_s} />
      </div>
    </section>

    <details class="group rounded-md border border-line" open={Object.values(form.limits).some(Boolean)}>
      <summary class="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-sm select-none hover:bg-hover">
        <ChevronRight size={14} class="text-faint disclosure-icon" />
        <span class="font-medium">{t('limits.title')}</span>
        <span class="text-xs text-muted">{t('limits.summary', { open: form.limits.max_open || admin.poolDefaults.max_open || 0 })}</span>
      </summary>
      <div class="space-y-2 border-t border-line-soft px-3 pt-2.5 pb-3">
        <p class="hint">{form.user_credentials ? t('limits.adminPerUserHint') : t('limits.adminHint')}</p>
        <LimitsFields id="c-limits" bind:value={form.limits} inherited={admin.poolDefaults} />
      </div>
    </details>

    <section>
      <span class="label">{t('admin.conn.whoCanUse')}</span>
      <p class="mb-2 text-xs text-muted">
        {t('admin.conn.whoCanUseHint')}
      </p>
      <div class="mb-2 flex gap-2">
        <select class="input" bind:value={pick}>
          <option value="">{t('admin.conn.addSubject')}</option>
          {#if available.groups.length}
            <optgroup label={t('admin.groups')}>
              {#each available.groups as g (g.id)}<option value={'group:' + g.id}>{g.name}</option>{/each}
            </optgroup>
          {/if}
          {#if available.users.length}
            <optgroup label={t('admin.users')}>
              {#each available.users as u (u.id)}<option value={'user:' + u.id}>{u.display_name || u.username}</option>{/each}
            </optgroup>
          {/if}
        </select>
        <button type="button" class="btn-outline" onclick={addAccess} disabled={!pick}>{t('common.add')}</button>
      </div>
      <div class="divide-y divide-line-soft rounded-md border border-line">
        {#each form.access as a, i (a.subject_type + a.subject_id)}
          <div class="flex items-center gap-2 px-3 py-1.5 text-sm">
            {#if a.subject_type === 'group'}<UsersRound size={14} class="text-muted" />{:else}<Users size={14} class="text-muted" />{/if}
            <span class="flex-1">{a.subject_type === 'group' ? groupName(a.subject_id) : userName(a.subject_id)}</span>
            <select class="h-7 rounded border border-line bg-bg px-1.5 text-xs" bind:value={a.read_only} disabled={form.read_only}>
              <option value={true}>{t('admin.readOnly')}</option>
              <option value={false}>{t('admin.readWrite')}</option>
            </select>
            <button type="button" class="icon-btn size-6" onclick={() => form.access.splice(i, 1)} aria-label={t('admin.remove')}><X size={13} /></button>
          </div>
        {:else}
          <p class="px-3 py-3 text-center text-xs text-faint">{t('admin.conn.nobody')}</p>
        {/each}
      </div>
    </section>

    {#if error}<p class="rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">{error}</p>{/if}
  </form>

  {#snippet footer()}
    <button class="btn-outline" onclick={onclose}>{t('common.cancel')}</button>
    <button class="btn-primary" form="conn-form" disabled={busy}>{c ? t('admin.conn.saveChanges') : t('admin.conn.create')}</button>
  {/snippet}
</Modal>
