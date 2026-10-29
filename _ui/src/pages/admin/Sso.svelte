<script lang="ts">
  import PasswordInput from '@/lib/components/ui/PasswordInput.svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import { Plus, Pencil, Trash2, Copy, KeyRound, TriangleAlert, X } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import { api } from '@/lib/api/client';
  import type { SsoProvider } from '@/lib/api/types';
  import { admin, loadGroups } from '@/pages/admin/admin.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { copyText } from '@/lib/helper/clipboard';

  type Row = { provider: SsoProvider; status: string };

  let list = $state<Row[]>([]);
  let editing = $state<SsoProvider | null | undefined>(undefined);
  let original = '';
  let form = $state(blank());
  let secret = $state('');
  let changeSecret = $state(false);
  let mapping = $state<{ claim: string; group: string }[]>([]);
  let error = $state('');
  let busy = $state(false);

  function blank() {
    return {
      name: '',
      display_name: '',
      enabled: true,
      issuer_url: '',
      auth_url: '',
      token_url: '',
      userinfo_url: '',
      jwks_url: '',
      client_id: '',
      scopes: 'openid email profile',
      token_auth_method: '',
      disable_pkce: false,
      auto_create: false,
      link_by_email: true,
      allowed_domains: '',
      roles_claim: '',
      admin_roles: '',
    };
  }

  async function load() {
    try {
      list = await api.get<Row[]>('/admin/sso');
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  void load();
  void loadGroups().catch(() => {});

  function open(p: SsoProvider | null) {
    editing = p;
    original = p?.name ?? '';
    error = '';
    secret = '';
    changeSecret = !p;
    form = p
      ? {
          name: p.name,
          display_name: p.display_name,
          enabled: p.enabled,
          issuer_url: p.issuer_url,
          auth_url: p.auth_url,
          token_url: p.token_url,
          userinfo_url: p.userinfo_url,
          jwks_url: p.jwks_url,
          client_id: p.client_id,
          scopes: (p.scopes ?? []).join(' '),
          token_auth_method: p.token_auth_method,
          disable_pkce: p.disable_pkce,
          auto_create: p.auto_create,
          link_by_email: p.link_by_email,
          allowed_domains: (p.allowed_domains ?? []).join(', '),
          roles_claim: p.roles_claim,
          admin_roles: (p.admin_roles ?? []).join(', '),
        }
      : blank();
    mapping = Object.entries(p?.group_mapping ?? {}).map(([claim, group]) => ({ claim, group }));
  }

  const split = (v: string, sep: RegExp) => v.split(sep).map((s) => s.trim()).filter(Boolean);

  const callback = $derived(new URL(`./api/v1/auth/sso/callback/${form.name || '<name>'}`, location.href).href);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    const body = {
      ...form,
      scopes: split(form.scopes, /[\s,]+/),
      allowed_domains: split(form.allowed_domains, /[\s,]+/),
      admin_roles: split(form.admin_roles, /,/),
      group_mapping: Object.fromEntries(mapping.filter((m) => m.claim && m.group).map((m) => [m.claim.trim(), m.group])),
      client_secret: changeSecret ? secret : null,
    };
    try {
      if (editing) await api.put(`/admin/sso/${encodeURIComponent(original)}`, body);
      else await api.post('/admin/sso', body);
      editing = undefined;
      await load();
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }

  async function remove(p: SsoProvider) {
    if (!confirm(t('admin.ssoPage.deleteConfirm', { name: p.name }))) return;
    try {
      await api.del(`/admin/sso/${encodeURIComponent(p.name)}`);
      await load();
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }
</script>

<Page title={t('admin.sso')} description={t('admin.ssoPage.description')}>
  {#snippet actions()}
    <button class="btn-primary" onclick={() => open(null)}><Plus size={14} /> {t('admin.ssoPage.new')}</button>
  {/snippet}

  <table class="table">
    <thead>
      <tr>
        <th class="w-72">{t('admin.ssoPage.name')}</th>
        <th>{t('admin.ssoPage.issuer')}</th>
        <th>{t('admin.ssoPage.whoCanSignIn')}</th>
        <th class="w-20"></th>
      </tr>
    </thead>
    <tbody>
      {#each list as row (row.provider.name)}
        {@const p = row.provider}
        <tr class={p.enabled ? '' : 'text-muted'}>
          <td class="py-1.5">
            <div class="flex items-center gap-2 font-medium"><KeyRound size={14} class={p.enabled ? 'text-accent' : 'text-faint'} />{p.display_name || p.name}</div>
            <div class="mt-0.5 pl-[22px] font-mono text-2xs text-muted">{p.name}</div>
          </td>
          <td class="max-w-0">
            <div class="truncate font-mono text-xs text-fg-2" title={p.issuer_url || p.auth_url}>{p.issuer_url || p.auth_url}</div>
            {#if row.status}<p class="mt-0.5 flex items-center gap-1.5 text-xs text-danger"><TriangleAlert size={12} /> {row.status}</p>{/if}
          </td>
          <td>
            <div class="flex flex-wrap gap-1">
              {#if !p.enabled}<span class="pill-ro">{t('admin.ssoPage.chipDisabled')}</span>{/if}
              {#if p.link_by_email}<span class="chip">{t('admin.ssoPage.chipLinks')}</span>{/if}
              {#if p.auto_create}<span class="pill-rw">{t('admin.ssoPage.chipCreates')}</span>{/if}
              {#if p.allowed_domains?.length}<span class="chip">@{p.allowed_domains.join(', @')}</span>{/if}
              {#if p.admin_roles?.length || Object.keys(p.group_mapping ?? {}).length}<span class="chip">{t('admin.ssoPage.chipRoles', { paths: ['roles', p.roles_claim].filter(Boolean).join(', ') })}</span>{/if}
            </div>
          </td>
          <td class="w-20 text-right whitespace-nowrap">
            <button class="icon-btn" onclick={() => open(p)} aria-label={t('common.edit')}><Pencil size={14} /></button>
            <button class="icon-btn hover:text-danger" onclick={() => remove(p)} aria-label={t('common.delete')}><Trash2 size={14} /></button>
          </td>
        </tr>
      {:else}
        <tr><td colspan="4" class="h-24 text-center text-muted">{t('admin.ssoPage.empty')}</td></tr>
      {/each}
    </tbody>
  </table>
</Page>

{#if editing !== undefined}
  <Modal title={editing ? t('admin.ssoPage.edit', { name: editing.name }) : t('admin.ssoPage.newTitle')} onclose={() => (editing = undefined)} width="max-w-2xl">
    <form id="sso-form" onsubmit={submit} class="space-y-5">
      <section class="grid grid-cols-2 gap-3">
        <div>
          <label class="label" for="s-name">{t('admin.ssoPage.name')} <span class="font-normal text-faint">{t('admin.ssoPage.nameHint')}</span></label>
          <input id="s-name" class="input font-mono" bind:value={form.name} required pattern="[a-z0-9][a-z0-9_\-]*" placeholder="keycloak" />
        </div>
        <div>
          <label class="label" for="s-label">{t('admin.ssoPage.label')}</label>
          <input id="s-label" class="input" bind:value={form.display_name} placeholder={t('admin.ssoPage.labelPlaceholder')} />
        </div>
        <div class="col-span-2">
          <span class="label">{t('admin.ssoPage.redirect')}</span>
          <div class="flex gap-2">
            <code class="input flex items-center overflow-x-auto bg-sunken font-mono text-xs whitespace-nowrap">{callback}</code>
            <button type="button" class="btn-outline" onclick={() => copyText(callback)}><Copy size={13} /></button>
          </div>
        </div>
      </section>

      <section class="grid grid-cols-2 gap-3">
        <div class="col-span-2">
          <label class="label" for="s-issuer">{t('admin.ssoPage.issuer')} <span class="font-normal text-faint">{t('admin.ssoPage.issuerHint')}</span></label>
          <input id="s-issuer" class="input font-mono" bind:value={form.issuer_url} placeholder="https://idp.example.com/realms/main" />
        </div>
        <div>
          <label class="label" for="s-client">{t('admin.ssoPage.clientId')}</label>
          <input id="s-client" class="input font-mono" bind:value={form.client_id} required autocomplete="off" />
        </div>
        <div>
          <label class="label" for="s-secret">{t('admin.ssoPage.clientSecret')}</label>
          {#if changeSecret}
            <PasswordInput id="s-secret" class="input font-mono" bind:value={secret} autocomplete="new-password" />
          {:else}
            <button type="button" class="input text-left text-muted" onclick={() => (changeSecret = true)}>
              {editing?.has_secret ? t('admin.unchanged') : t('admin.notSet')}
            </button>
          {/if}
        </div>
        <div>
          <label class="label" for="s-scopes">{t('admin.ssoPage.scopes')}</label>
          <input id="s-scopes" class="input font-mono" bind:value={form.scopes} />
        </div>
        <div>
          <label class="label" for="s-method">{t('admin.ssoPage.tokenMethod')}</label>
          <select id="s-method" class="input" bind:value={form.token_auth_method}>
            <option value="">{t('admin.conn.default', { value: 'client_secret_basic' })}</option>
            <option value="post">client_secret_post</option>
            <option value="bearer">bearer</option>
          </select>
        </div>
        <details class="col-span-2 rounded-md border border-line px-3 py-2">
          <summary class="cursor-pointer text-xs text-muted">{t('admin.ssoPage.manual')}</summary>
          <div class="mt-3 grid grid-cols-2 gap-3">
            <div><label class="label" for="s-auth">{t('admin.ssoPage.authUrl')}</label><input id="s-auth" class="input font-mono" bind:value={form.auth_url} /></div>
            <div><label class="label" for="s-token">{t('admin.ssoPage.tokenUrl')}</label><input id="s-token" class="input font-mono" bind:value={form.token_url} /></div>
            <div><label class="label" for="s-ui">{t('admin.ssoPage.userinfoUrl')}</label><input id="s-ui" class="input font-mono" bind:value={form.userinfo_url} /></div>
            <div><label class="label" for="s-jwks">{t('admin.ssoPage.jwksUrl')}</label><input id="s-jwks" class="input font-mono" bind:value={form.jwks_url} /></div>
            <label class="col-span-2 flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={form.disable_pkce} /> {t('admin.ssoPage.disablePkce')}</label>
          </div>
        </details>
      </section>

      <section class="space-y-2">
        <span class="label">{t('admin.ssoPage.whoCanSignIn')}</span>
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={form.enabled} /> {t('admin.ssoPage.enabled')}</label>
        <label class="flex items-start gap-2 text-sm">
          <input type="checkbox" bind:checked={form.link_by_email} class="mt-1" />
          <span>{t('admin.ssoPage.linkByEmail')} <span class="text-muted">{t('admin.ssoPage.linkByEmailHint')}</span></span>
        </label>
        <label class="flex items-start gap-2 text-sm">
          <input type="checkbox" bind:checked={form.auto_create} class="mt-1" />
          <span>{t('admin.ssoPage.autoCreate')} <span class="text-muted">{t('admin.ssoPage.autoCreateHint')}</span></span>
        </label>
        <div>
          <label class="label" for="s-domains">{t('admin.ssoPage.domains')} <span class="font-normal text-faint">{t('admin.ssoPage.domainsHint')}</span></label>
          <input id="s-domains" class="input font-mono" bind:value={form.allowed_domains} placeholder="example.com, example.org" />
        </div>
      </section>

      <section class="space-y-3">
        <div>
          <span class="label">{t('admin.ssoPage.rolesTitle')}</span>
          <p class="text-xs text-muted">{t('admin.ssoPage.rolesHint')}</p>
        </div>
        <div>
          <label class="label" for="s-claim">{t('admin.ssoPage.rolesClaim')} <span class="font-normal text-faint">{t('admin.ssoPage.rolesClaimHint')}</span></label>
          <div class="flex items-center gap-2">
            <code class="chip shrink-0 font-mono">roles</code>
            <span class="text-faint">+</span>
            <input id="s-claim" class="input font-mono" bind:value={form.roles_claim} placeholder="realm_access.roles, resource_access.*.roles, groups" />
          </div>
        </div>
        <div>
          <label class="label" for="s-admin">{t('admin.ssoPage.adminRoles')}</label>
          <input id="s-admin" class="input font-mono" bind:value={form.admin_roles} placeholder="dataport-admin" />
        </div>
          <div>
            <span class="label">{t('admin.ssoPage.groupMapping')}</span>
            <div class="space-y-1.5">
              {#each mapping as m, i (i)}
                <div class="flex gap-2">
                  <input class="input font-mono" bind:value={m.claim} placeholder={t('admin.ssoPage.role')} />
                  <select class="input" bind:value={m.group}>
                    <option value="">{t('admin.ssoPage.groupPick')}</option>
                    {#each admin.groups as g (g.id)}<option value={g.id}>{g.name}</option>{/each}
                  </select>
                  <button type="button" class="icon-btn shrink-0" onclick={() => mapping.splice(i, 1)} aria-label={t('admin.remove')}><X size={13} /></button>
                </div>
              {/each}
              <button type="button" class="btn-outline" onclick={() => mapping.push({ claim: '', group: '' })}><Plus size={13} /> {t('admin.ssoPage.addMapping')}</button>
            </div>
          </div>
      </section>

      {#if error}<p class="rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">{error}</p>{/if}
    </form>

    {#snippet footer()}
      <button class="btn-outline" onclick={() => (editing = undefined)}>{t('common.cancel')}</button>
      <button class="btn-primary" form="sso-form" disabled={busy}>{editing ? t('common.save') : t('admin.ssoPage.create')}</button>
    {/snippet}
  </Modal>
{/if}
