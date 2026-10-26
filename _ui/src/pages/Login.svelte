<script lang="ts">
  import { push, router } from 'svelte-spa-router';
  import { LogIn, KeyRound, Languages, TriangleAlert, LoaderCircle } from 'lucide-svelte';
  import Logo from '@/lib/components/Logo.svelte';
  import { login, session, takeReturn } from '@/lib/stores/session.svelte';
  import { api } from '@/lib/api/client';
  import type { AuthInfo } from '@/lib/api/types';
  import { i18n, locales, serverMessage, setLocale, t, type Locale } from '@/lib/i18n/index.svelte';

  let username = $state('');
  let password = $state('');
  let busy = $state(false);
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

<div class="flex h-full flex-col bg-panel">
  <div class="flex flex-1 items-center justify-center px-4">
    <div class="w-full max-w-[340px]">
      <div class="mb-6 flex items-center gap-2.5">
        <Logo size={28} />
        <span class="text-lg font-semibold tracking-[-0.01em]">dataport</span>
      </div>

      <h1 class="text-base font-semibold">{t('login.title')}</h1>
      <p class="mt-1 mb-5 text-sm text-muted">{t('login.subtitle')}</p>

      {#if error}
        <p class="mb-4 flex items-start gap-2 rounded-md border border-danger/30 bg-danger-soft px-2.5 py-2 text-xs text-danger" role="alert">
          <TriangleAlert size={14} class="mt-px shrink-0" />{error}
        </p>
      {/if}

      {#if info?.providers.length}
        <div class="space-y-2">
          {#each info.providers as p (p.name)}
            <a href={`./api/v1/auth/sso/${encodeURIComponent(p.name)}?remember=${remember && info?.remember_days ? 1 : 0}`} class="btn-outline h-8 w-full justify-center">
              <KeyRound size={14} /> {t('login.continueWith', { name: p.label })}
            </a>
          {/each}
        </div>
        {#if info.password}
          <div class="my-4 flex items-center gap-3 text-xs text-faint">
            <span class="h-px flex-1 bg-line"></span>{t('login.or')}<span class="h-px flex-1 bg-line"></span>
          </div>
        {/if}
      {/if}

      {#snippet rememberBox()}
        {#if info?.remember_days}
          <label class="flex items-center gap-2 text-sm text-fg-2" title={t('login.rememberHint', { days: info.remember_days })}>
            <input type="checkbox" bind:checked={remember} />
            {t('login.remember')}
            <span class="text-xs text-faint">{t('login.rememberDays', { days: info.remember_days })}</span>
          </label>
        {/if}
      {/snippet}

      {#if !info || info.password}
        <form onsubmit={submit} class="space-y-3">
          <div>
            <label class="label" for="username">{t('login.username')}</label>
            <!-- svelte-ignore a11y_autofocus -->
            <input id="username" class="input h-8" autocomplete="username" autofocus bind:value={username} required />
          </div>
          <div>
            <label class="label" for="password">{t('login.password')}</label>
            <input id="password" class="input h-8" type="password" autocomplete="current-password" bind:value={password} required />
          </div>
          {@render rememberBox()}
          <button class="btn-primary h-8 w-full justify-center" disabled={busy}>
            {#if busy}<LoaderCircle size={14} class="animate-spin" />{:else}<LogIn size={14} />{/if}
            {busy ? t('login.signingIn') : t('login.signIn')}
          </button>
        </form>
      {:else}
        <div class="mt-3">{@render rememberBox()}</div>
        <p class="mt-3 text-xs text-muted">{t('login.passwordDisabled')}</p>
      {/if}
    </div>
  </div>

  <div class="flex h-10 shrink-0 items-center justify-center gap-1 text-xs text-muted">
    <Languages size={12} />
    <select class="cursor-pointer rounded-sm bg-transparent px-1 py-0.5 text-xs text-muted hover:text-fg" value={i18n.locale} onchange={(e) => setLocale(e.currentTarget.value as Locale)} aria-label="Language">
      {#each locales as l (l.value)}
        <option value={l.value}>{l.label}</option>
      {/each}
    </select>
  </div>
</div>
