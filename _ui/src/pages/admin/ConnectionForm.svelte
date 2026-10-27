<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { PlugZap, LoaderCircle, CircleCheck, CircleX, ChevronRight, ChevronLeft } from 'lucide-svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import DbPicker from '@/lib/components/ui/DbPicker.svelte';
  import DbIcon from '@/lib/components/ui/DbIcon.svelte';
  import { dbLabel } from '@/lib/helper/databases';
  import { api } from '@/lib/api/client';
  import type { Access, Connection, Limits } from '@/lib/api/types';
  import LimitsFields from '@/lib/components/ui/LimitsFields.svelte';
  import ConnectionAccess from '@/pages/admin/ConnectionAccess.svelte';
  import ConnectionSecurity from '@/pages/admin/ConnectionSecurity.svelte';
  import { admin } from '@/pages/admin/admin.svelte';
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

  // A new connection starts by choosing the database.
  let step = $state<'pick' | 'details'>(c ? 'details' : 'pick');
  let changePassword = $state(!c);
  let busy = $state(false);
  let testing = $state(false);
  let test = $state<{ ok: boolean; message?: string; duration_ms?: number } | null>(null);
  let error = $state('');

  const driver = $derived(admin.drivers.find((d) => d.type === form.db_type));
  const target = $derived(driver?.target ?? 'network');
  // Trino, Oracle and the file databases call the database field differently.
  const databaseLabel = $derived(
    target === 'file'
      ? t('admin.conn.filePath')
      : form.db_type === 'trino'
        ? t('admin.conn.catalog')
        : form.db_type === 'oracle'
          ? t('admin.conn.serviceName')
          : t('admin.conn.database'),
  );
  const canTest = $derived(target === 'file' ? !!form.database : target === 'dsn' ? !!form.options.dsn : !!form.host);

  function choose() {
    // Options of another database type do not apply.
    if (form.db_type !== (c?.db_type ?? '')) form.options = {};
    if (target === 'file') form.user_credentials = false;
    step = 'details';
  }
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
</script>

<Modal
  title={c ? t('admin.conn.edit', { name: c.name }) : step === 'pick' ? t('picker.title') : t('admin.conn.newOf', { type: dbLabel(form.db_type) })}
  {onclose}
  width={step === 'pick' ? 'max-w-3xl' : 'max-w-2xl'}
>
  {#if step === 'pick'}
    <DbPicker drivers={admin.drivers} filesEnabled={admin.filesEnabled} bind:selected={form.db_type} onnext={choose} />
  {:else}
  <form id="conn-form" onsubmit={submit} class="space-y-5">
    <section class="grid grid-cols-6 gap-3">
      <div class="col-span-4">
        <label class="label" for="c-name">{t('admin.conn.name')}</label>
        <input id="c-name" class="input" bind:value={form.name} required maxlength="100" placeholder={t('admin.conn.namePlaceholder')} />
      </div>
      <div class="col-span-2">
        <span class="label">{t('admin.conn.type')}</span>
        {#if c}
          <span class="input flex items-center gap-2 bg-sunken"><DbIcon type={form.db_type} /> {dbLabel(form.db_type)}</span>
        {:else}
          <button type="button" class="input flex items-center gap-2 text-left hover:bg-hover" onclick={() => (step = 'pick')} title={t('picker.change')}>
            <DbIcon type={form.db_type} /> <span class="flex-1">{dbLabel(form.db_type)}</span> <ChevronLeft size={13} class="text-faint" />
          </button>
        {/if}
      </div>
      <div class="col-span-6">
        <label class="label" for="c-desc">{t('admin.conn.descriptionLabel')} <span class="font-normal text-faint">{t('admin.conn.visibleToUsers')}</span></label>
        <input id="c-desc" class="input" bind:value={form.description} />
      </div>
      {#if target === 'network'}
        <div class="col-span-4">
          <label class="label" for="c-host">{t('admin.conn.host')}</label>
          <input id="c-host" class="input font-mono" bind:value={form.host} required placeholder="db.internal" />
        </div>
        <div class="col-span-2">
          <label class="label" for="c-port">{t('admin.conn.port')}</label>
          <input id="c-port" class="input font-mono" type="number" min="0" max="65535" bind:value={form.port} placeholder={String(driver?.default_port ?? '')} />
        </div>
        <div class="col-span-2">
          <label class="label" for="c-db">{databaseLabel}</label>
          <input id="c-db" class="input font-mono" bind:value={form.database} required={form.db_type === 'trino'} />
        </div>
        {#if form.db_type === 'oracle'}
          <div class="col-span-2">
            <label class="label" for="c-sid">SID <span class="font-normal text-faint">{t('admin.conn.sidHint')}</span></label>
            <input id="c-sid" class="input font-mono" bind:value={form.options.sid} />
          </div>
        {/if}
      {:else if target === 'file'}
        <div class="col-span-6">
          <label class="label" for="c-file">{databaseLabel}</label>
          <input id="c-file" class="input font-mono" bind:value={form.database} required placeholder={form.db_type === 'duckdb' ? 'analytics.duckdb' : 'app.db'} />
          <p class="hint mt-1">{t('admin.conn.filePathHint')}</p>
        </div>
      {:else}
        <div class="col-span-6">
          <label class="label" for="c-dsn">{t('admin.conn.connString')}</label>
          <input id="c-dsn" class="input font-mono" bind:value={form.options.dsn} required placeholder="DSN=warehouse  ·  Driver={'{'}Ingres{'}'};Server=@db,tcp_ip,II;Database=app" />
          <p class="hint mt-1">{t('admin.conn.connStringHint')}</p>
        </div>
        <div class="col-span-3">
          <label class="label" for="c-dialect">{t('admin.conn.dialect')}</label>
          <select id="c-dialect" class="input" bind:value={form.options.dialect}>
            {#each admin.odbcDialects as d (d)}<option value={d}>{d || t('admin.conn.dialectStandard')}</option>{/each}
          </select>
        </div>
      {/if}
      {#if target !== 'file'}
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
      {/if}
      {#if target !== 'file'}
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
      {/if}
      <div class="col-span-3">
        <ConnectionSecurity bind:options={form.options} dbType={form.db_type} {target} />
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
      <button type="button" class="btn-outline" onclick={runTest} disabled={testing || !canTest}>
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

    <ConnectionAccess bind:access={form.access} readOnly={form.read_only} />

    {#if error}<p class="rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">{error}</p>{/if}
  </form>
  {/if}

  {#snippet footer()}
    {#if step === 'pick'}
      <span class="mr-auto flex items-center gap-2 text-xs text-muted"><DbIcon type={form.db_type} size={12} /> {dbLabel(form.db_type)}</span>
      <button class="btn-outline" onclick={onclose}>{t('common.cancel')}</button>
      <button class="btn-primary" onclick={choose}>{t('picker.next')} <ChevronRight size={14} /></button>
    {:else}
      {#if !c}<button class="btn-outline mr-auto" onclick={() => (step = 'pick')}><ChevronLeft size={14} /> {t('picker.back')}</button>{/if}
      <button class="btn-outline" onclick={onclose}>{t('common.cancel')}</button>
      <button class="btn-primary" form="conn-form" disabled={busy}>{c ? t('admin.conn.saveChanges') : t('admin.conn.create')}</button>
    {/if}
  {/snippet}
</Modal>
