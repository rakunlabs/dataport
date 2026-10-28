<script lang="ts">
  import { push, router } from 'svelte-spa-router';
  import { LogIn, ExternalLink, Languages, TriangleAlert, LoaderCircle, Sun, Moon, Monitor, KeyRound, ChevronDown, ChevronRight } from 'lucide-svelte';
  import Logo from '@/lib/components/ui/Logo.svelte';
  import { login, session, takeReturn } from '@/lib/stores/session.svelte';
  import { setTheme, theme, type ThemeMode } from '@/lib/stores/theme.svelte';
  import { api } from '@/lib/api/client';
  import type { AuthInfo } from '@/lib/api/types';
  import { i18n, locales, serverMessage, setLocale, t, type Locale } from '@/lib/i18n/index.svelte';

  let username = $state('');
  let password = $state('');
  let busy = $state(false);
  // The provider the browser is being sent to, until the page unloads.
  let leaving = $state('');
  // The last choice is kept in this browser, as sign-in pages usually do.
  let remember = $state(localStorage.getItem('dataport.remember') === '1');
  $effect(() => {
    localStorage.setItem('dataport.remember', remember ? '1' : '0');
  });
  let info = $state<AuthInfo | null>(null);

  // SSO failures come back as #/login?error=...
  let error = $state(serverMessage(new URLSearchParams(router.querystring ?? '').get('error') ?? ''));

  if (session.user) void push('/');

  api
    .get<AuthInfo>('/auth/info')
    .then((v) => (info = v))
    .catch(() => (info = { password: true, providers: [] }));

  const themes: { mode: ThemeMode; icon: typeof Sun; key: string }[] = [
    { mode: 'system', icon: Monitor, key: 'app.themeSystem' },
    { mode: 'light', icon: Sun, key: 'app.themeLight' },
    { mode: 'dark', icon: Moon, key: 'app.themeDark' },
  ];
  // As pika's "Collapse local login form": the username and password form
  // starts folded, and the "Local login" button reveals it.
  let localOpen = $state(false);
  const passwordCollapsible = $derived(!!info?.password && !!info.collapse_local_login);
  const showPassword = $derived(!!info?.password && (!passwordCollapsible || localOpen));

  function toggleLocal() {
    localOpen = !localOpen;
    error = '';
    if (localOpen) setTimeout(() => document.getElementById('username')?.focus());
  }

  const currentTheme = $derived(themes.find((th) => th.mode === theme.mode) ?? themes[0]);

  function nextTheme() {
    const i = themes.findIndex((th) => th.mode === theme.mode);
    setTheme(themes[(i + 1) % themes.length].mode);
  }

  function ssoHref(name: string) {
    return `./api/v1/auth/sso/${encodeURIComponent(name)}?remember=${remember && info?.remember_days ? 1 : 0}`;
  }

  // Coming back with the browser's back button shows the page from cache.
  function onPageShow(e: PageTransitionEvent) {
    if (e.persisted) leaving = '';
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (busy) return;
    busy = true;
    error = '';
    try {
      await login(username, password, remember && !!info?.remember_days);
      await push(takeReturn() || '/');
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      busy = false;
    }
  }
</script>

<svelte:window onpageshow={onPageShow} />

<div class="flex h-full flex-col items-center overflow-auto bg-panel px-4 pt-[12vh]">
  <div class="w-full max-w-sm">
    <div class="relative rounded-lg border border-line bg-raised p-8 shadow-dialog">
      {#if info?.version}
        <span class="absolute top-3 left-3 font-mono text-[10px] text-faint">{info.version}</span>
      {/if}

      <button
        type="button"
        class="absolute top-3 right-3 flex size-7 cursor-pointer items-center justify-center rounded-md border border-line bg-bg text-muted transition-colors hover:bg-hover hover:text-fg"
        onclick={nextTheme}
        title={t(currentTheme.key)}
        aria-label={t(currentTheme.key)}
      >
        <currentTheme.icon size={14} />
      </button>

      {#if passwordCollapsible}
        <button
          type="button"
          class="absolute top-3 right-12 inline-flex cursor-pointer items-center gap-1 rounded-md px-2 py-1 text-[11px] font-medium text-muted transition-colors hover:bg-hover hover:text-fg"
          aria-expanded={localOpen}
          aria-controls="local-login"
          onclick={toggleLocal}
        >
          <KeyRound size={11} />
          {t('login.localLogin')}
          {#if localOpen}<ChevronDown size={11} />{:else}<ChevronRight size={11} />{/if}
        </button>
      {/if}

      <div class="mb-6 flex items-center justify-center gap-3">
        <Logo size={40} />
        <div class="flex min-w-0 flex-col leading-tight">
          <span class="text-2xl font-semibold tracking-tight text-fg">dataport</span>
          <!-- Always takes its line, so the card does not jump when it loads. -->
          <span class="min-h-5 truncate text-sm text-muted" title={info?.subtitle}>{info?.subtitle || t('login.title')}</span>
        </div>
      </div>

      {#if error}
        <p class="mb-4 flex items-start gap-2 rounded-md border border-danger/30 bg-danger-soft p-3 text-sm text-danger" role="alert">
          <TriangleAlert size={14} class="mt-0.5 shrink-0" />{error}
        </p>
      {/if}

      {#snippet rememberBox()}
        {#if info?.remember_days}
          <label class="flex items-center gap-2 text-sm text-fg-2" title={t('login.rememberHint', { days: info.remember_days })}>
            <input type="checkbox" bind:checked={remember} />
            {t('login.remember')}
          </label>
        {/if}
      {/snippet}

      {#if !info}
        <div class="py-4 text-center text-sm text-faint">{t('common.loading')}</div>
      {:else}
        {#if showPassword}
          <form id="local-login" onsubmit={submit} class="space-y-4">
            <div>
              <label class="label" for="username">{t('login.username')}</label>
              <!-- svelte-ignore a11y_autofocus -->
              <input id="username" class="input h-9" autocomplete="username" autofocus bind:value={username} required />
            </div>
            <div>
              <label class="label" for="password">{t('login.password')}</label>
              <input id="password" class="input h-9" type="password" autocomplete="current-password" bind:value={password} required />
            </div>
            {@render rememberBox()}
            <button class="btn-primary h-9 w-full justify-center" disabled={busy}>
              {#if busy}<LoaderCircle size={14} class="animate-spin" />{:else}<LogIn size={14} />{/if}
              {busy ? t('login.signingIn') : t('login.signIn')}
            </button>
          </form>
        {/if}

        {#if info.providers.length}
          {#if showPassword}
            <div class="my-5 flex items-center gap-3 text-xs text-faint">
              <span class="h-px flex-1 bg-line"></span>{t('login.or')}<span class="h-px flex-1 bg-line"></span>
            </div>
          {:else}
            <div class="mb-4">{@render rememberBox()}</div>
          {/if}
          <div class="space-y-2">
            {#each info.providers as p (p.name)}
              <a
                href={ssoHref(p.name)}
                class="btn-outline h-9 w-full justify-center bg-panel font-medium {leaving ? 'pointer-events-none opacity-50' : ''}"
                aria-disabled={!!leaving}
                onclick={() => (leaving = p.name)}
              >
                {#if leaving === p.name}<LoaderCircle size={14} class="animate-spin" />{:else}<ExternalLink size={14} />{/if}
                {t('login.continueWith', { name: p.label })}
              </a>
            {/each}
          </div>
        {/if}

        {#if !info.password}
          <p class="mt-4 text-center text-xs text-muted">{t('login.passwordDisabled')}</p>
        {/if}
      {/if}
    </div>

    <div class="mt-4 flex items-center justify-center gap-1 text-xs text-muted">
      <Languages size={12} />
      <select class="cursor-pointer rounded-sm border border-transparent bg-transparent px-1 py-0.5 text-xs text-fg-2 hover:border-line hover:bg-raised hover:text-fg focus:border-focus focus:outline-none" value={i18n.locale} onchange={(e) => setLocale(e.currentTarget.value as Locale)} aria-label="Language">
        {#each locales as l (l.value)}
          <option class="bg-raised text-fg" value={l.value}>{l.label}</option>
        {/each}
      </select>
    </div>
  </div>
</div>
