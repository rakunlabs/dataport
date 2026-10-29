<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import Page from '@/pages/admin/Page.svelte';
  import ExternalUrl from '@/pages/admin/ExternalUrl.svelte';
  import { api } from '@/lib/api/client';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  // Durations travel as whole minutes.
  interface Lifetime {
    session: number;
    remember_idle: number;
    remember_max: number;
  }

  const hour = 60;
  const day = 24 * hour;

  let saved = $state<Lifetime | null>(null);
  let sessionHours = $state(24);
  let remember = $state(true);
  let idleDays = $state(7);
  let maxDays = $state(30);
  let error = $state('');
  let saving = $state(false);

  function apply(v: Lifetime) {
    saved = v;
    sessionHours = Math.round(v.session / hour);
    remember = v.remember_max > 0;
    idleDays = Math.round(v.remember_idle / day) || 7;
    maxDays = Math.round(v.remember_max / day) || 30;
  }

  const draft = $derived<Lifetime>({
    session: sessionHours * hour,
    remember_idle: remember ? idleDays * day : 0,
    remember_max: remember ? maxDays * day : 0,
  });
  const dirty = $derived(!!saved && JSON.stringify(draft) !== JSON.stringify(saved));

  async function load() {
    try {
      apply(await api.get<Lifetime>('/admin/sessions/lifetime'));
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  void load();

  // The sign-in page: a name for this instance and the folded local form.
  interface LoginPage {
    subtitle: string;
    collapse_local_login: boolean;
  }

  let pageSaved = $state<LoginPage | null>(null);
  let page = $state<LoginPage>({ subtitle: '', collapse_local_login: false });
  let pageError = $state('');
  let pageSaving = $state(false);
  const pageDirty = $derived(!!pageSaved && JSON.stringify(page) !== JSON.stringify(pageSaved));

  function applyPage(v: LoginPage) {
    pageSaved = { ...v };
    page = { ...v };
  }

  api
    .get<LoginPage>('/admin/login-page')
    .then(applyPage)
    .catch((e) => toast(errorMessage(e), 'error'));

  async function savePage(e: SubmitEvent) {
    e.preventDefault();
    pageSaving = true;
    pageError = '';
    try {
      applyPage(await api.put<LoginPage>('/admin/login-page', page));
      toast(t('admin.sessions.pageSaved'), 'success');
    } catch (e) {
      pageError = errorMessage(e);
    } finally {
      pageSaving = false;
    }
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    error = '';
    try {
      apply(await api.put<Lifetime>('/admin/sessions/lifetime', draft));
      toast(t('admin.sessions.saved'), 'success');
    } catch (e) {
      error = errorMessage(e);
    } finally {
      saving = false;
    }
  }
</script>

<Page title={t('admin.sessions.title')} description={t('admin.sessions.description')}>
  <form class="max-w-2xl space-y-5 px-6 py-5" onsubmit={save}>
    <section class="space-y-1">
      <label class="label" for="session-hours">{t('admin.sessions.session')}</label>
      <div class="flex items-center gap-2">
        <input id="session-hours" class="input w-28" type="number" min="1" max="8760" required bind:value={sessionHours} disabled={!saved || saving} />
        <span class="text-sm text-muted">{t('admin.sessions.hours')}</span>
      </div>
      <p class="hint">{t('admin.sessions.sessionHint')}</p>
    </section>

    <section class="space-y-3">
      <label class="flex items-center gap-2 text-sm font-medium">
        <input type="checkbox" bind:checked={remember} disabled={!saved || saving} />
        {t('admin.sessions.remember')}
      </label>
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="label" for="remember-idle">{t('admin.sessions.idle')}</label>
          <div class="flex items-center gap-2">
            <input id="remember-idle" class="input w-24" type="number" min="1" max="3650" required bind:value={idleDays} disabled={!saved || saving || !remember} />
            <span class="text-sm text-muted">{t('admin.sessions.days')}</span>
          </div>
        </div>
        <div>
          <label class="label" for="remember-max">{t('admin.sessions.max')}</label>
          <div class="flex items-center gap-2">
            <input id="remember-max" class="input w-24" type="number" min="1" max="3650" required bind:value={maxDays} disabled={!saved || saving || !remember} />
            <span class="text-sm text-muted">{t('admin.sessions.days')}</span>
          </div>
        </div>
      </div>
      <p class="hint">{remember ? t('admin.sessions.rememberHint', { idle: idleDays, max: maxDays }) : t('admin.sessions.rememberOff')}</p>
    </section>

    <p class="hint">{t('admin.sessions.applies')}</p>
    {#if error}<p role="alert" class="text-xs text-danger">{error}</p>{/if}
    <button type="submit" class="btn-primary" disabled={!dirty || saving}>{saving ? t('common.loading') : t('common.save')}</button>
  </form>

  <form class="max-w-2xl space-y-5 border-t border-line px-6 py-5" onsubmit={savePage}>
    <h2 class="text-sm font-semibold">{t('admin.sessions.page')}</h2>

    <section class="space-y-1">
      <label class="label" for="login-subtitle">{t('admin.sessions.subtitle')}</label>
      <input id="login-subtitle" class="input max-w-sm" maxlength="64" placeholder="sandbox" bind:value={page.subtitle} disabled={!pageSaved || pageSaving} />
      <p class="hint">{t('admin.sessions.subtitleHint')}</p>
    </section>

    <section class="space-y-1">
      <label class="flex items-center gap-2 text-sm font-medium">
        <input type="checkbox" bind:checked={page.collapse_local_login} disabled={!pageSaved || pageSaving} />
        {t('admin.sessions.collapse')}
      </label>
      <p class="hint">{t('admin.sessions.collapseHint')}</p>
    </section>

    {#if pageError}<p role="alert" class="text-xs text-danger">{pageError}</p>{/if}
    <button type="submit" class="btn-primary" disabled={!pageDirty || pageSaving}>{pageSaving ? t('common.loading') : t('common.save')}</button>
  </form>

  <ExternalUrl />
</Page>
