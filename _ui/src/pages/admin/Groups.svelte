<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { Plus, Pencil, Trash2, UsersRound } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import { api } from '@/lib/api/client';
  import type { Group } from '@/lib/api/types';
  import { admin, loadGroups, loadUsers, userName } from '@/pages/admin/admin.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let editing = $state<Group | null | undefined>(undefined);
  let form = $state({ name: '', description: '', member_ids: [] as string[] });
  let error = $state('');
  let busy = $state(false);

  void Promise.all([loadUsers(), loadGroups()]).catch((e) => toast(errorMessage(e), 'error'));

  function open(g: Group | null) {
    editing = g;
    error = '';
    form = { name: g?.name ?? '', description: g?.description ?? '', member_ids: [...(g?.member_ids ?? [])] };
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      if (editing) await api.put(`/admin/groups/${editing.id}`, form);
      else await api.post('/admin/groups', form);
      editing = undefined;
      await Promise.all([loadGroups(), loadUsers()]);
    } catch (err) {
      error = errorMessage(err);
    } finally {
      busy = false;
    }
  }

  async function remove(g: Group) {
    if (!confirm(t('admin.group.deleteConfirm', { name: g.name }))) return;
    try {
      await api.del(`/admin/groups/${g.id}`);
      await loadGroups();
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  function toggle(id: string) {
    const i = form.member_ids.indexOf(id);
    if (i >= 0) form.member_ids.splice(i, 1);
    else form.member_ids.push(id);
  }
</script>

<Page title={t('admin.groups')} description={t('admin.group.description')}>
  {#snippet actions()}
    <button class="btn-primary" onclick={() => open(null)}><Plus size={14} /> {t('admin.group.new')}</button>
  {/snippet}

  <table class="table">
    <thead>
      <tr>
        <th class="w-72">{t('admin.group.name')}</th>
        <th>{t('admin.group.members')}</th>
        <th class="w-20"></th>
      </tr>
    </thead>
    <tbody>
      {#each admin.groups as g (g.id)}
        <tr>
          <td class="py-1.5">
            <div class="flex items-center gap-2 font-medium"><UsersRound size={14} class="text-muted" />{g.name}</div>
            {#if g.description}<div class="mt-0.5 pl-[22px] text-xs text-muted">{g.description}</div>{/if}
          </td>
          <td>
            <div class="flex flex-wrap gap-1">
              {#each g.member_ids as m (m)}<span class="chip">{userName(m)}</span>{:else}<span class="text-xs text-faint">{t('admin.group.noMembers')}</span>{/each}
            </div>
          </td>
          <td class="w-20 text-right whitespace-nowrap">
            <button class="icon-btn" onclick={() => open(g)} aria-label={t('common.edit')}><Pencil size={14} /></button>
            <button class="icon-btn hover:text-danger" onclick={() => remove(g)} aria-label={t('common.delete')}><Trash2 size={14} /></button>
          </td>
        </tr>
      {:else}
        <tr><td colspan="3" class="h-24 text-center text-muted">{t('admin.group.empty')}</td></tr>
      {/each}
    </tbody>
  </table>
</Page>

{#if editing !== undefined}
  <Modal title={editing ? t('admin.group.edit', { name: editing.name }) : t('admin.group.new')} onclose={() => (editing = undefined)}>
    <form id="group-form" onsubmit={submit} class="space-y-3">
      <div>
        <label class="label" for="g-name">{t('admin.group.name')}</label>
        <input id="g-name" class="input" bind:value={form.name} required maxlength="100" />
      </div>
      <div>
        <label class="label" for="g-desc">{t('admin.group.descriptionLabel')}</label>
        <input id="g-desc" class="input" bind:value={form.description} />
      </div>
      <div>
        <span class="label">{t('admin.group.members')}</span>
        <div class="max-h-60 overflow-auto rounded-md border border-line">
          {#each admin.users as u (u.id)}
            <label class="flex items-center gap-2 border-b border-line-soft px-3 py-1.5 text-sm last:border-0 hover:bg-hover">
              <input type="checkbox" checked={form.member_ids.includes(u.id)} onchange={() => toggle(u.id)} />
              {u.display_name || u.username}
              <span class="text-xs text-faint">{u.username}</span>
            </label>
          {/each}
        </div>
      </div>
      {#if error}<p class="rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">{error}</p>{/if}
    </form>

    {#snippet footer()}
      <button class="btn-outline" onclick={() => (editing = undefined)}>{t('common.cancel')}</button>
      <button class="btn-primary" form="group-form" disabled={busy}>{editing ? t('common.save') : t('admin.group.create')}</button>
    {/snippet}
  </Modal>
{/if}
