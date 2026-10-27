<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { Plus, Pencil, Trash2, ShieldCheck, KeyRound } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import { api } from '@/lib/api/client';
  import type { Role, User } from '@/lib/api/types';
  import { admin, groupName, loadGroups, loadUsers } from '@/pages/admin/admin.svelte';
  import { session } from '@/lib/stores/session.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { formatTime } from '@/lib/helper/format';

  let editing = $state<User | null | undefined>(undefined);
  let form = $state({ username: '', display_name: '', email: '', password: '', role: 'user' as Role, disabled: false, group_ids: [] as string[] });
  let error = $state('');
  let busy = $state(false);

  void Promise.all([loadUsers(), loadGroups()]).catch((e) => toast(errorMessage(e), 'error'));

  function open(u: User | null) {
    editing = u;
    error = '';
    form = {
      username: u?.username ?? '',
      display_name: u?.display_name ?? '',
      email: u?.email ?? '',
      password: '',
      role: u?.role ?? 'user',
      disabled: u?.disabled ?? false,
      group_ids: [...(u?.group_ids ?? [])],
    };
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      if (editing) await api.put(`/admin/users/${editing.id}`, form);
      else await api.post('/admin/users', form);
      toast(editing ? t('admin.user.updated') : t('admin.user.created'), 'success');
      editing = undefined;
      await Promise.all([loadUsers(), loadGroups()]);
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }

  async function remove(u: User) {
    if (!confirm(t('admin.user.deleteConfirm', { name: u.username }))) return;
    try {
      await api.del(`/admin/users/${u.id}`);
      await loadUsers();
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  async function unlink(u: User) {
    if (!confirm(t('admin.user.unlinkConfirm', { name: u.username }))) return;
    try {
      await api.del(`/admin/users/${u.id}/identities`);
      await loadUsers();
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  function toggleGroup(id: string) {
    const i = form.group_ids.indexOf(id);
    if (i >= 0) form.group_ids.splice(i, 1);
    else form.group_ids.push(id);
  }
</script>

<Page title={t('admin.users')} description={t('admin.user.description')}>
  {#snippet actions()}
    <button class="btn-primary" onclick={() => open(null)}><Plus size={14} /> {t('admin.user.new')}</button>
  {/snippet}

  <table class="table">
      <thead>
        <tr>
          <th>{t('admin.user.user')}</th>
          <th>{t('admin.user.role')}</th>
          <th>{t('admin.user.groups')}</th>
          <th>{t('admin.user.lastSignIn')}</th>
          <th class="w-24"></th>
        </tr>
      </thead>
      <tbody>
        {#each admin.users as u (u.id)}
          <tr class="{u.disabled ? 'opacity-50' : ''}">
            <td class="py-1.5">
              <div class="font-medium">{u.display_name || u.username}</div>
              <div class="text-xs text-muted">{u.username}{u.email ? ` · ${u.email}` : ''}{u.disabled ? ` · ${t('admin.user.disabled')}` : ''}</div>
              {#if u.providers?.length}
                <div class="mt-1 flex items-center gap-1">
                  {#each u.providers as p (p)}<span class="pill-own"><KeyRound size={10} />{p}</span>{/each}
                  <button class="text-2xs text-muted underline hover:text-danger" onclick={() => unlink(u)}>{t('admin.user.unlink')}</button>
                </div>
              {/if}
            </td>
            <td>
              {#if u.role === 'admin'}
                <span class="pill bg-accent-soft text-accent"><ShieldCheck size={11} /> {t('admin.user.roleAdmin')}</span>
              {:else}
                <span class="text-xs text-muted">{t('admin.user.roleUser')}</span>
              {/if}
            </td>
            <td>
              <div class="flex flex-wrap gap-1">
                {#each u.group_ids as g (g)}<span class="chip">{groupName(g)}</span>{:else}<span class="text-xs text-faint">—</span>{/each}
              </div>
            </td>
            <td class="text-xs text-muted">{formatTime(u.last_login_at)}</td>
            <td class="w-20 text-right whitespace-nowrap">
              <button class="icon-btn" onclick={() => open(u)} aria-label={t('common.edit')}><Pencil size={14} /></button>
              <button class="icon-btn hover:text-danger" onclick={() => remove(u)} disabled={u.id === session.user?.id} aria-label={t('common.delete')}><Trash2 size={14} /></button>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
</Page>

{#if editing !== undefined}
  <Modal title={editing ? t('admin.user.edit', { name: editing.username }) : t('admin.user.new')} onclose={() => (editing = undefined)}>
    <form id="user-form" onsubmit={submit} class="space-y-3">
      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="label" for="u-name">{t('admin.user.username')}</label>
          <input id="u-name" class="input" bind:value={form.username} required autocomplete="off" />
        </div>
        <div>
          <label class="label" for="u-display">{t('admin.user.displayName')}</label>
          <input id="u-display" class="input" bind:value={form.display_name} />
        </div>
      </div>
      <div>
        <label class="label" for="u-email">{t('admin.user.email')} <span class="font-normal text-faint">{t('admin.user.emailHint')}</span></label>
        <input id="u-email" class="input" type="email" bind:value={form.email} autocomplete="off" />
      </div>
      <div>
        <label class="label" for="u-pass">{editing ? t('admin.user.newPassword') : t('admin.user.password')}</label>
        <input id="u-pass" class="input" type="password" bind:value={form.password} minlength="8" required={!editing} autocomplete="new-password" />
      </div>
      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="label" for="u-role">{t('admin.user.role')}</label>
          <select id="u-role" class="input" bind:value={form.role}>
            <option value="user">{t('admin.user.roleUser')}</option>
            <option value="admin">{t('admin.user.roleAdmin')}</option>
          </select>
        </div>
        <div>
          <span class="label">{t('admin.user.status')}</span>
          <label class="flex h-8 items-center gap-2 text-sm">
            <input type="checkbox" bind:checked={form.disabled} /> {t('admin.user.disabledLabel')}
          </label>
        </div>
      </div>
      {#if admin.groups.length}
        <div>
          <span class="label">{t('admin.user.groups')}</span>
          <div class="flex flex-wrap gap-1.5">
            {#each admin.groups as g (g.id)}
              {@const on = form.group_ids.includes(g.id)}
              <button type="button" class="chip {on ? 'border-accent bg-accent-soft text-accent' : ''}" onclick={() => toggleGroup(g.id)}>{g.name}</button>
            {/each}
          </div>
        </div>
      {/if}
      {#if error}<p class="rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">{error}</p>{/if}
    </form>

    {#snippet footer()}
      <button class="btn-outline" onclick={() => (editing = undefined)}>{t('common.cancel')}</button>
      <button class="btn-primary" form="user-form" disabled={busy}>{editing ? t('common.save') : t('admin.user.create')}</button>
    {/snippet}
  </Modal>
{/if}
