<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { UsersRound } from 'lucide-svelte';
  import { api } from '@/lib/api/client';
  import { admin, loadGroups } from '@/pages/admin/admin.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';
  import { loadSession } from '@/lib/stores/session.svelte';

  // Who may use notebooks at all; sharing still decides each notebook.
  interface Access {
    restricted: boolean;
    group_ids: string[];
  }

  let saved = $state<Access | null>(null);
  let form = $state<Access>({ restricted: false, group_ids: [] });
  let error = $state('');
  let saving = $state(false);

  const norm = (a: Access) => JSON.stringify({ restricted: a.restricted, group_ids: [...a.group_ids].sort() });
  const dirty = $derived(!!saved && norm(form) !== norm(saved));

  function apply(v: Access) {
    saved = { restricted: v.restricted, group_ids: [...v.group_ids] };
    form = { restricted: v.restricted, group_ids: [...v.group_ids] };
  }

  void Promise.all([api.get<Access>('/admin/notebook-access').then(apply), loadGroups()]).catch((e) => toast(errorMessage(e), 'error'));

  function toggle(id: string) {
    const i = form.group_ids.indexOf(id);
    if (i >= 0) form.group_ids.splice(i, 1);
    else form.group_ids.push(id);
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    saving = true;
    error = '';
    try {
      apply(await api.put<Access>('/admin/notebook-access', form));
      toast(t('admin.notebookAccess.saved'), 'success');
      void loadSession();
    } catch (err) {
      error = errorMessage(err);
    } finally {
      saving = false;
    }
  }
</script>

<form class="max-w-3xl space-y-3 border-t border-line px-6 py-5" onsubmit={save}>
  <div>
    <h2 class="text-sm font-semibold">{t('admin.notebookAccess.title')}</h2>
    <p class="hint mt-0.5">{t('admin.notebookAccess.description')}</p>
  </div>

  <div class="space-y-1.5">
    <label class="flex items-center gap-2 text-sm">
      <input type="radio" name="nb-access" checked={!form.restricted} onchange={() => (form.restricted = false)} disabled={!saved || saving} />
      {t('admin.notebookAccess.everyone')}
    </label>
    <label class="flex items-center gap-2 text-sm">
      <input type="radio" name="nb-access" checked={form.restricted} onchange={() => (form.restricted = true)} disabled={!saved || saving} />
      {t('admin.notebookAccess.groups')}
    </label>
  </div>

  {#if form.restricted}
    <div class="max-h-60 overflow-auto rounded-md border border-line">
      {#each admin.groups as g (g.id)}
        <label class="flex items-center gap-2 border-b border-line-soft px-3 py-1.5 text-sm last:border-0 hover:bg-hover">
          <input type="checkbox" checked={form.group_ids.includes(g.id)} onchange={() => toggle(g.id)} disabled={saving} />
          <UsersRound size={13} class="text-muted" />
          {g.name}
          <span class="text-xs text-faint">{t('admin.notebookAccess.members', { count: g.member_ids.length })}</span>
        </label>
      {:else}
        <p class="px-3 py-3 text-xs text-muted">{t('admin.notebookAccess.noGroups')}</p>
      {/each}
    </div>
    <p class="hint">
      {form.group_ids.length ? t('admin.notebookAccess.restrictedHint') : t('admin.notebookAccess.adminsOnly')}
    </p>
  {/if}

  {#if error}<p role="alert" class="text-xs text-danger">{error}</p>{/if}
  <button type="submit" class="btn-primary" disabled={!dirty || saving}>{saving ? t('common.loading') : t('common.save')}</button>
</form>
