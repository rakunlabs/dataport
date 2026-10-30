<script lang="ts">
  import { t, i18n } from '@/lib/i18n/index.svelte';
  import { Play, LoaderCircle, CircleCheck, TriangleAlert, ChevronRight, ChevronDown, Eye, Ban } from 'lucide-svelte';
  import { api } from '@/lib/api/client';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import DbIcon from '@/lib/components/ui/DbIcon.svelte';
  import type { DocConnection, DocSchemaPreview } from '@/lib/api/types';
  import { matchesAny, schemaChosen } from '@/lib/helper/docSchemas';

  // Documentation the server builds for every user of a connection, with
  // the schemas an admin chooses and an account of its own.
  let { running = false, onchange }: { running?: boolean; onchange?: () => void } = $props();

  interface Form {
    enabled: boolean;
    mode: 'all' | 'only';
    include: string;
    exclude: string;
    block: boolean;
    username: string;
    password: string;
    clearPassword: boolean;
  }

  let list = $state<DocConnection[] | null>(null);
  let open = $state<string | null>(null);
  let form = $state<Form | null>(null);
  let saving = $state(false);
  let error = $state('');
  // The schemas the account sees; which are chosen follows the form.
  let preview = $state<DocSchemaPreview | null>(null);
  let schemaFilter = $state('');
  let previewing = $state(false);
  let previewError = $state('');

  async function load() {
    try {
      list = await api.get<DocConnection[]>('/admin/docs/connections');
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  void load();

  // Follow builds started here or by the schedule.
  $effect(() => {
    const id = setInterval(() => !document.hidden && void load(), running ? 2000 : 30000);
    return () => clearInterval(id);
  });

  const split = (v: string) => v.split(/[,\n]/).map((s) => s.trim()).filter(Boolean);

  function edit(c: DocConnection) {
    if (open === c.connection_id) {
      open = null;
      return;
    }
    open = c.connection_id;
    error = '';
    preview = null;
    previewError = '';
    schemaFilter = '';
    form = {
      enabled: c.enabled,
      mode: c.include.length ? 'only' : 'all',
      include: c.include.join(', '),
      exclude: c.exclude.join(', '),
      block: c.block_excluded,
      username: c.username,
      password: '',
      clearPassword: false,
    };
  }

  const include = $derived(form && form.mode === 'only' ? split(form.include) : []);
  const exclude = $derived(form ? split(form.exclude) : []);
  const current = $derived(list?.find((c) => c.connection_id === open));
  const needsAccount = $derived(!!current?.user_credentials && !form?.username.trim());
  const needsInclude = $derived(form?.mode === 'only' && !include.length);
  // The patterns apply while documentation is on or other schemas are closed.
  const usesPatterns = $derived(!!form && (form.enabled || form.block));
  const chosen = (name: string) => schemaChosen(include, exclude, name);
  const visible = $derived(
    (preview?.schemas ?? []).filter((s) => !schemaFilter.trim() || s.name.toLowerCase().includes(schemaFilter.trim().toLowerCase())),
  );
  const chosenCount = $derived((preview?.schemas ?? []).filter((s) => chosen(s.name)).length);

  async function showSchemas() {
    if (!current) return;
    previewing = true;
    previewError = '';
    try {
      preview = await api.get<DocSchemaPreview>(`/admin/docs/connections/${current.connection_id}/schemas`);
    } catch (e) {
      preview = null;
      previewError = errorMessage(e);
    } finally {
      previewing = false;
    }
  }

  const names = (v: string[]) => v.join(', ');
  const without = (v: string[], name: string) => v.filter((x) => x.toLowerCase() !== name.toLowerCase());

  // A schema left out by a pattern of its own kind cannot be ticked back
  // without editing the pattern.
  function lockedBy(name: string): string | null {
    const byExclude = exclude.filter((p) => p.toLowerCase() !== name.toLowerCase() && matchesAny([p], name));
    if (byExclude.length) return byExclude.join(', ');
    return null;
  }

  // Ticking writes plain names into the fields, keeping the patterns there.
  function tick(name: string, on: boolean) {
    if (!form) return;
    let inc = split(form.include);
    let exc = without(split(form.exclude), name);
    if (on) {
      if (form.mode === 'only' && !matchesAny(inc, name)) inc = [...inc, name];
    } else if (form.mode === 'only' && inc.some((p) => p.toLowerCase() === name.toLowerCase()) && !matchesAny(without(inc, name), name)) {
      inc = without(inc, name);
    } else {
      exc = [...exc, name];
    }
    form.include = names(inc);
    form.exclude = names(exc);
  }

  // Every shown schema at once, e.g. after filtering by a prefix.
  function tickAll(on: boolean) {
    for (const s of visible) if (chosen(s.name) !== on && !(on && lockedBy(s.name))) tick(s.name, on);
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    if (!form || !current) return;
    saving = true;
    error = '';
    try {
      const body: Record<string, unknown> = { enabled: form.enabled, include, exclude, block_excluded: form.block, username: form.username.trim() };
      if (form.password) body.password = form.password;
      else if (form.clearPassword) body.password = '';
      const v = await api.put<DocConnection>(`/admin/docs/connections/${current.connection_id}`, body);
      if (list) list = list.map((c) => (c.connection_id === v.connection_id ? v : c));
      form.password = '';
      form.clearPassword = false;
      toast(t(v.enabled ? 'admin.docConn.savedBuilding' : 'admin.docConn.saved'), 'success');
      onchange?.();
    } catch (err) {
      error = errorMessage(err);
    } finally {
      saving = false;
    }
  }

  async function build(c: DocConnection) {
    try {
      await api.post(`/admin/docs/connections/${c.connection_id}/run`);
      onchange?.();
      void load();
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  const fullTime = (at: number) => new Date(at).toLocaleString(i18n.locale, { dateStyle: 'medium', timeStyle: 'short' });
</script>

<section class="space-y-2">
  <div>
    <h3 class="text-sm font-semibold">{t('admin.docConn.title')}</h3>
    <p class="hint mt-0.5">{t('admin.docConn.description')}</p>
  </div>

  {#if !list}
    <p class="flex items-center gap-1.5 text-xs text-muted"><LoaderCircle size={12} class="animate-spin" /> {t('common.loading')}</p>
  {:else if !list.length}
    <p class="text-xs text-muted">{t('admin.docConn.none')}</p>
  {:else}
    <div class="overflow-hidden rounded-md border border-line">
      {#each list as c (c.connection_id)}
        {@const expanded = open === c.connection_id}
        <div class="border-t border-line-soft first:border-t-0">
          <div class="flex items-center gap-2 px-3 py-1.5 text-sm">
            <button class="flex min-w-0 flex-1 items-center gap-2 text-left" onclick={() => edit(c)} aria-expanded={expanded}>
              {#if expanded}<ChevronDown size={14} class="text-faint" />{:else}<ChevronRight size={14} class="text-faint" />{/if}
              <DbIcon type={c.db_type} size={14} />
              <span class="truncate font-medium">{c.name}</span>
            </button>
            <span class="flex items-center gap-1.5 text-xs text-muted">
              {#if c.block_excluded}<span class="inline-flex items-center gap-1 text-warn" title={t('admin.docConn.blockOn')}><Ban size={12} /></span>{/if}
              {#if !c.enabled}
                {t('admin.docConn.off')}
              {:else if c.last_error && c.last_error !== 'limit_reached'}
                <TriangleAlert size={12} class="text-warn" /> {t(c.last_error === 'account_needed' ? 'admin.docConn.accountNeeded' : 'admin.docConn.discoveryFailed')}
              {:else}
                {#if c.ready < c.schemas}<LoaderCircle size={12} class={running ? 'animate-spin text-accent' : 'text-faint'} />{:else}<CircleCheck size={12} class="text-ok" />{/if}
                {t('admin.docConn.counts', { ready: c.ready, total: c.schemas })}
                {#if c.failed}<span class="text-warn">· {t('admin.docs.failedCount', { count: c.failed })}</span>{/if}
                {#if c.last_error === 'limit_reached'}<span class="text-warn">· {t('admin.docConn.limitReached')}</span>{/if}
              {/if}
            </span>
            {#if c.enabled}
              <button class="icon-btn" onclick={() => build(c)} disabled={running} title={t('admin.docConn.buildNow')} aria-label={t('admin.docConn.buildNow')}><Play size={13} /></button>
            {/if}
          </div>

          {#if expanded && form}
            <form class="space-y-3 border-t border-line-soft bg-panel/40 px-3 py-3" onsubmit={save}>
              {#if c.notebook_only}
                <p class="text-xs text-warn">{t('admin.docConn.notebookOnly')}</p>
              {/if}
              <label class="flex items-center gap-2 text-sm font-medium">
                <input type="checkbox" bind:checked={form.enabled} disabled={saving || c.notebook_only} />
                {t('admin.docConn.enabled')}
              </label>
              <label class="flex items-start gap-2 text-sm">
                <input type="checkbox" class="mt-0.5" bind:checked={form.block} disabled={saving} />
                <span>
                  {t('admin.docConn.block')}
                  <span class="hint block">{t(form.block ? 'admin.docConn.blockHintOn' : 'admin.docConn.blockHintOff')}</span>
                </span>
              </label>

              <fieldset class="space-y-2" disabled={saving || !usesPatterns}>
                <legend class="label">{t('admin.docConn.schemas')}</legend>
                <label class="flex items-center gap-2 text-sm"><input type="radio" value="all" bind:group={form.mode} /> {t('admin.docConn.modeAll')}</label>
                <label class="flex items-center gap-2 text-sm"><input type="radio" value="only" bind:group={form.mode} /> {t('admin.docConn.modeOnly')}</label>
                {#if form.mode === 'only'}
                  <input class="input w-full font-mono" placeholder="sales, crm_*" aria-label={t('admin.docConn.include')} bind:value={form.include} aria-invalid={needsInclude} />
                  {#if needsInclude}<p class="text-xs text-warn">{t('admin.docConn.includeNeeded')}</p>{/if}
                {/if}
                <div>
                  <label class="label" for="doc-exclude-{c.connection_id}">{t('admin.docConn.exclude')}</label>
                  <input id="doc-exclude-{c.connection_id}" class="input w-full font-mono" placeholder="tmp_*, audit" bind:value={form.exclude} />
                </div>
                <p class="hint">{t('admin.docConn.patternsHint')}</p>

                <div class="space-y-1.5 rounded-md border border-line p-2">
                  <div class="flex flex-wrap items-center gap-2">
                    <button type="button" class="btn" onclick={showSchemas} disabled={previewing || form.username.trim() !== c.username || !!form.password}>
                      {#if previewing}<LoaderCircle size={14} class="animate-spin" />{:else}<Eye size={14} />{/if}
                      {t(preview ? 'admin.docConn.previewReload' : 'admin.docConn.preview')}
                    </button>
                    {#if preview}
                      <input class="input h-7 w-44 text-xs" placeholder={t('admin.docConn.filter')} aria-label={t('admin.docConn.filter')} bind:value={schemaFilter} />
                      <button type="button" class="btn h-7 text-xs" onclick={() => tickAll(true)}>{t('admin.docConn.tickAll')}</button>
                      <button type="button" class="btn h-7 text-xs" onclick={() => tickAll(false)}>{t('admin.docConn.tickNone')}</button>
                      <span class="text-xs text-muted tabular-nums">
                        {t('admin.docConn.previewCount', { chosen: chosenCount, total: preview.schemas.length })}{#if chosenCount > preview.limit} · <span class="text-warn">{t('admin.docConn.previewLimit', { limit: preview.limit })}</span>{/if}
                      </span>
                    {/if}
                  </div>
                  {#if form.username.trim() !== c.username || form.password}<p class="hint">{t('admin.docConn.previewSaveFirst')}</p>{/if}
                  {#if previewError}<p class="text-xs text-danger">{previewError}</p>{/if}
                  {#if preview}
                    <div class="grid max-h-64 grid-cols-[repeat(auto-fill,minmax(12rem,1fr))] gap-x-3 overflow-auto py-1">
                      {#each visible as s (s.name)}
                        {@const on = chosen(s.name)}
                        {@const locked = on ? null : lockedBy(s.name)}
                        <label class="flex min-w-0 items-center gap-2 py-0.5 text-xs {on ? '' : 'text-faint'}" title={locked ? t('admin.docConn.lockedBy', { pattern: locked }) : ''}>
                          <input type="checkbox" checked={on} disabled={!!locked} onchange={(e) => tick(s.name, e.currentTarget.checked)} />
                          <span class="truncate font-mono">{s.name}</span>
                          {#if !on && form.block}<Ban size={10} class="shrink-0 text-warn" />{/if}
                        </label>
                      {:else}
                        <p class="text-xs text-muted">{t('admin.docConn.noMatch')}</p>
                      {/each}
                    </div>
                  {/if}
                </div>
              </fieldset>

              <fieldset class="space-y-2" disabled={saving || !usesPatterns}>
                <legend class="label">{t('admin.docConn.account')}</legend>
                <p class="hint">{t(c.user_credentials ? 'admin.docConn.accountHintOwn' : 'admin.docConn.accountHint')}</p>
                <div class="grid gap-2 sm:grid-cols-2">
                  <input class="input font-mono" autocomplete="off" placeholder={t(c.user_credentials ? 'admin.docConn.usernameRequired' : 'admin.docConn.usernameShared')} aria-label={t('admin.docConn.username')} bind:value={form.username} />
                  <input
                    class="input font-mono"
                    type="password"
                    autocomplete="new-password"
                    placeholder={c.has_password ? t('admin.docsExport.tokenKept') : t('admin.docConn.password')}
                    aria-label={t('admin.docConn.password')}
                    bind:value={form.password}
                    disabled={saving || !usesPatterns || form.clearPassword || !form.username.trim()}
                  />
                </div>
                {#if c.has_password && form.username.trim()}
                  <label class="flex items-center gap-2 text-xs text-muted"><input type="checkbox" bind:checked={form.clearPassword} /> {t('admin.docConn.clearPassword')}</label>
                {/if}
                {#if needsAccount && form.enabled}<p class="text-xs text-warn">{t('admin.docConn.accountNeededHint')}</p>{/if}
              </fieldset>

              {#if c.discovered_at}<p class="hint">{t('admin.docConn.discovered', { time: fullTime(c.discovered_at) })}</p>{/if}
              {#if error}<p role="alert" class="text-xs text-danger">{error}</p>{/if}
              <button type="submit" class="btn-primary" disabled={saving || (form.enabled && needsAccount) || (usesPatterns && needsInclude)}>{saving ? t('common.loading') : t('common.save')}</button>
            </form>
          {/if}
        </div>
      {/each}
    </div>
  {/if}
</section>
