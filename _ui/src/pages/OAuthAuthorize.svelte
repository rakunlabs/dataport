<script lang="ts">
  // An MCP client asks to act as the user (OAuth). The user picks what it
  // may reach and approves; the browser then returns to the client.
  import { router } from 'svelte-spa-router';
  import { Bot, Check, X, TriangleAlert, LoaderCircle, Lock, LockOpen } from 'lucide-svelte';
  import Logo from '@/lib/components/Logo.svelte';
  import DbIcon from '@/lib/components/DbIcon.svelte';
  import { api } from '@/lib/api/client';
  import { session } from '@/lib/stores/session.svelte';
  import { errorMessage } from '@/lib/stores/toast.svelte';
  import { t } from '@/lib/i18n/index.svelte';

  interface Info {
    client: string;
    redirect_uri: string;
    connections: { id: string; name: string; db_type: string; writable: boolean }[];
    // Set when the client asked for one connection; it cannot be changed.
    connection?: string;
    // Set when the request is wrong in a way the client should hear about.
    error?: string;
    redirect?: string;
  }

  const query = new URLSearchParams(router.querystring ?? '');

  let info = $state<Info | null>(null);
  let error = $state('');
  let busy = $state(false);
  let done = $state('');
  let connection = $state('');
  let readOnly = $state(true);
  let remember = $state(false);

  api
    .get<Info>(`/oauth/authorize?${query.toString()}`)
    .then((v) => {
      // A client the user remembered is approved at once.
      if ((v as Info & { remembered?: boolean }).remembered && v.redirect) {
        done = t('oauth.approved');
        window.location.href = v.redirect;
        return;
      }
      info = v;
      connection = v.connection ?? '';
    })
    .catch((e) => (error = errorMessage(e)));

  const chosen = $derived(info?.connections.find((c) => c.id === connection));
  // Writing is only offered where the user may write.
  const canWrite = $derived(connection ? !!chosen?.writable : !!info?.connections.some((c) => c.writable));

  // Where the client listens, shown so the user can tell a real client
  // from a page that only claims a name.
  const target = $derived.by(() => {
    try {
      const u = new URL(info?.redirect_uri ?? '');
      return u.protocol === 'http:' || u.protocol === 'https:' ? u.host : u.protocol.replace(/:$/, '');
    } catch {
      return '';
    }
  });

  async function answer(approve: boolean) {
    busy = true;
    try {
      const body: Record<string, unknown> = { approve, connection_id: connection, read_only: readOnly || !canWrite, remember: approve && remember };
      for (const [k, v] of query) body[k] = v;
      const res = await api.post<{ redirect: string }>('/oauth/authorize', body);
      done = approve ? t('oauth.approved') : t('oauth.declined');
      window.location.href = res.redirect;
    } catch (e) {
      error = errorMessage(e);
    } finally {
      busy = false;
    }
  }
</script>

<div class="flex h-full items-center justify-center bg-panel px-4">
  <div class="w-full max-w-[420px] rounded-lg border border-line bg-bg p-6 shadow-pop">
    <div class="mb-5 flex items-center gap-2.5">
      <Logo size={24} />
      <span class="font-semibold">dataport</span>
    </div>

    {#if done && !info}
      <p class="flex items-center gap-2 text-sm"><Check size={15} class="text-ok" /> {done}</p>
    {:else if error}
      <p class="flex items-start gap-2 rounded-md border border-danger/30 bg-danger-soft px-2.5 py-2 text-sm text-danger" role="alert">
        <TriangleAlert size={15} class="mt-0.5 shrink-0" />{error}
      </p>
    {:else if !info}
      <p class="flex items-center gap-2 text-sm text-muted"><LoaderCircle size={14} class="animate-spin" /> {t('common.loading')}</p>
    {:else if info.error}
      <p class="mb-4 text-sm text-danger">{info.error}</p>
      <a class="btn-outline" href={info.redirect}>{t('oauth.back')}</a>
    {:else if done}
      <p class="flex items-center gap-2 text-sm"><Check size={15} class="text-ok" /> {done}</p>
      <p class="mt-1 text-xs text-muted">{t('oauth.closeHint')}</p>
    {:else}
      <div class="mb-4 flex items-start gap-3">
        <span class="flex size-9 shrink-0 items-center justify-center rounded-md bg-accent-soft text-accent"><Bot size={18} /></span>
        <div class="min-w-0">
          <h1 class="text-base font-semibold">{t('oauth.title', { client: info.client })}</h1>
          <p class="mt-0.5 text-sm text-muted">{t('oauth.subtitle', { user: session.user?.display_name || session.user?.username || '' })}</p>
          {#if target}<p class="mt-1 truncate font-mono text-xs text-faint" title={info.redirect_uri}>{t('oauth.returnsTo', { target })}</p>{/if}
        </div>
      </div>

      <label class="label" for="oauth-conn">{t('oauth.connections')}</label>
      {#if info.connection}
        <div class="mb-3 flex h-8 items-center gap-2 rounded-md border border-line bg-sunken px-2.5 text-sm">
          {#if chosen}<DbIcon type={chosen.db_type} />{chosen.name}{/if}
        </div>
      {:else}
        <select id="oauth-conn" class="input mb-3" bind:value={connection}>
          <option value="">{t('oauth.allConnections', { count: info.connections.length })}</option>
          {#each info.connections as c (c.id)}
            <option value={c.id}>{c.name} · {c.db_type}</option>
          {/each}
        </select>
      {/if}

      <div class="mb-4 space-y-1.5 text-sm">
        <label class="flex items-start gap-2">
          <input type="radio" name="mode" class="mt-1" checked={readOnly || !canWrite} onchange={() => (readOnly = true)} />
          <span><span class="flex items-center gap-1 font-medium"><Lock size={12} /> {t('oauth.readOnly')}</span><span class="text-xs text-muted">{t('oauth.readOnlyHint')}</span></span>
        </label>
        <label class="flex items-start gap-2 {canWrite ? '' : 'opacity-50'}">
          <input type="radio" name="mode" class="mt-1" checked={!readOnly && canWrite} disabled={!canWrite} onchange={() => (readOnly = false)} />
          <span><span class="flex items-center gap-1 font-medium"><LockOpen size={12} /> {t('oauth.readWrite')}</span><span class="text-xs text-muted">{t('oauth.readWriteHint')}</span></span>
        </label>
      </div>

      <label class="mb-3 flex items-start gap-2 text-sm">
        <input type="checkbox" class="mt-1" bind:checked={remember} />
        <span><span class="font-medium">{t('oauth.remember')}</span><span class="block text-xs text-muted">{t('oauth.rememberHint', { client: info.client })}</span></span>
      </label>

      <p class="mb-4 text-xs text-muted">{t('oauth.note')}</p>

      <div class="flex justify-end gap-2">
        <button class="btn-outline" disabled={busy} onclick={() => answer(false)}><X size={14} /> {t('oauth.deny')}</button>
        <button class="btn-primary" disabled={busy} onclick={() => answer(true)}>
          {#if busy}<LoaderCircle size={14} class="animate-spin" />{:else}<Check size={14} />{/if}
          {t('oauth.approve')}
        </button>
      </div>
    {/if}
  </div>
</div>
