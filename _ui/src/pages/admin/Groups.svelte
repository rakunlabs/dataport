<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { Plus, Pencil, Trash2, UsersRound, Search, X } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import Modal from '@/lib/components/ui/Modal.svelte';
  import Pager from '@/lib/components/ui/Pager.svelte';
  import { api } from '@/lib/api/client';
  import type { Group } from '@/lib/api/types';
  import { admin, loadGroups, loadUsers, matchesUser, pageSize, userName } from '@/pages/admin/admin.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let editing = $state<Group | null | undefined>(undefined);
  let form = $state({ name: '', description: '', member_ids: [] as string[], features: { notebooks: false, build_docs: false } });
  // Whether each feature is limited to groups at all; a group's choice only
  // matters then.
  let limited = $state({ notebooks: false, build_docs: false });

  async function loadLimits() {
    const [nb, docs] = await Promise.all([
      api.get<{ restricted: boolean }>('/admin/notebook-access'),
      api.get<{ restricted: boolean }>('/admin/doc-access'),
    ]);
    limited = { notebooks: nb.restricted, build_docs: docs.restricted };
  }
  let error = $state('');
  let busy = $state(false);

  void Promise.all([loadUsers(), loadGroups(), loadLimits()]).catch((e) => toast(errorMessage(e), 'error'));

  // The list shows a few members per group; the rest are counted.
  const chipsShown = 8;

  let query = $state('');
  let page = $state(0);
  const filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    return q ? admin.groups.filter((g) => g.name.toLowerCase().includes(q) || g.description?.toLowerCase().includes(q)) : admin.groups;
  });
  const shown = $derived(filtered.slice(page * pageSize, (page + 1) * pageSize));
  $effect(() => {
    void query;
    page = 0;
  });
  $effect(() => {
    const last = Math.max(0, Math.ceil(filtered.length / pageSize) - 1);
    if (page > last) page = last;
  });

  // The member picker of the dialog: search, only the members, and a page
  // of matching users at a time.
  let memberQuery = $state('');
  let onlyMembers = $state(false);
  let memberPage = $state(0);
  const memberSize = 100;
  const candidates = $derived.by(() => {
    const q = memberQuery.trim().toLowerCase();
    const set = new Set(form.member_ids);
    return admin.users.filter((u) => matchesUser(u, q) && (!onlyMembers || set.has(u.id)));
  });
  const candidatesShown = $derived(candidates.slice(memberPage * memberSize, (memberPage + 1) * memberSize));
  $effect(() => {
    void memberQuery;
    void onlyMembers;
    memberPage = 0;
  });

  // Adds or removes every user the search matches.
  function setMatching(on: boolean) {
    const set = new Set(form.member_ids);
    for (const u of candidates) {
      if (on) set.add(u.id);
      else set.delete(u.id);
    }
    form.member_ids = [...set];
  }

  function open(g: Group | null) {
    editing = g;
    error = '';
    form = {
      name: g?.name ?? '',
      description: g?.description ?? '',
      member_ids: [...(g?.member_ids ?? [])],
      features: { notebooks: g?.features?.notebooks ?? false, build_docs: g?.features?.build_docs ?? false },
    };
    memberQuery = '';
    onlyMembers = false;
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
    <div class="relative">
      <Search size={13} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
      <input class="input h-7 w-56 pr-6 pl-7 text-xs" placeholder={t('admin.group.search')} aria-label={t('admin.group.search')} bind:value={query} />
      {#if query}<button class="absolute top-1/2 right-1.5 -translate-y-1/2 text-faint hover:text-fg" onclick={() => (query = '')} aria-label={t('common.clear')}><X size={12} /></button>{/if}
    </div>
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
      {#each shown as g (g.id)}
        <tr>
          <td class="py-1.5">
            <div class="flex items-center gap-2 font-medium">
              <UsersRound size={14} class="text-muted" />{g.name}
              {#if g.features?.notebooks}<span class="pill bg-accent-soft text-accent">{t('admin.group.featureNotebooks')}</span>{/if}
              {#if g.features?.build_docs}<span class="pill bg-accent-soft text-accent">{t('admin.group.featureDocs')}</span>{/if}
            </div>
            {#if g.description}<div class="mt-0.5 pl-[22px] text-xs text-muted">{g.description}</div>{/if}
          </td>
          <td>
            <div class="flex flex-wrap gap-1">
              {#each g.member_ids.slice(0, chipsShown) as m (m)}<span class="chip">{userName(m)}</span>{:else}<span class="text-xs text-faint">{t('admin.group.noMembers')}</span>{/each}
              {#if g.member_ids.length > chipsShown}
                <button class="chip text-muted hover:text-fg" onclick={() => open(g)} title={t('admin.group.allMembers', { count: g.member_ids.length })}>{t('admin.group.more', { count: g.member_ids.length - chipsShown })}</button>
              {/if}
            </div>
          </td>
          <td class="w-20 text-right whitespace-nowrap">
            <button class="icon-btn" onclick={() => open(g)} aria-label={t('common.edit')}><Pencil size={14} /></button>
            <button class="icon-btn hover:text-danger" onclick={() => remove(g)} aria-label={t('common.delete')}><Trash2 size={14} /></button>
          </td>
        </tr>
      {:else}
        <tr><td colspan="3" class="h-24 text-center text-muted">{query ? t('admin.group.noMatch') : t('admin.group.empty')}</td></tr>
      {/each}
    </tbody>
  </table>
  <Pager bind:page size={pageSize} total={filtered.length} />
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
      <fieldset class="space-y-1.5">
        <legend class="label">{t('admin.group.features')}</legend>
        {#each [{ key: 'notebooks', label: 'featureNotebooks', hint: 'featureNotebooksHint' }, { key: 'build_docs', label: 'featureDocs', hint: 'featureDocsHint' }] as f (f.key)}
          {@const key = f.key as 'notebooks' | 'build_docs'}
          <label class="flex items-start gap-2 text-sm">
            <input type="checkbox" class="mt-0.5" bind:checked={form.features[key]} />
            <span>
              {t(`admin.group.${f.label}`)}
              {#if !limited[key]}<span class="ml-1 text-xs text-faint">({t('admin.group.featureOpen')})</span>{/if}
              <span class="block text-xs text-muted">{t(`admin.group.${f.hint}`)}</span>
            </span>
          </label>
        {/each}
      </fieldset>
      <div>
        <div class="mb-1.5 flex items-center gap-2">
          <span class="label !mb-0">{t('admin.group.members')}</span>
          <span class="text-xs text-faint tabular-nums">{t('admin.group.selected', { count: form.member_ids.length })}</span>
        </div>
        <div class="mb-1.5 flex flex-wrap items-center gap-2">
          <div class="relative min-w-48 flex-1">
            <Search size={13} class="pointer-events-none absolute top-1/2 left-2 -translate-y-1/2 text-faint" />
            <input class="input h-7 w-full pl-7 text-xs" placeholder={t('admin.user.search')} aria-label={t('admin.user.search')} bind:value={memberQuery} />
          </div>
          <label class="flex items-center gap-1.5 text-xs text-fg-2"><input type="checkbox" bind:checked={onlyMembers} /> {t('admin.group.onlyMembers')}</label>
        </div>
        {#if memberQuery.trim() && candidates.length}
          <div class="mb-1.5 flex items-center gap-3 text-xs">
            <button type="button" class="text-accent hover:underline" onclick={() => setMatching(true)}>{t('admin.group.addMatching', { count: candidates.length })}</button>
            <button type="button" class="text-muted hover:text-danger hover:underline" onclick={() => setMatching(false)}>{t('admin.group.removeMatching')}</button>
          </div>
        {/if}
        <div class="max-h-72 overflow-auto rounded-md border border-line">
          {#each candidatesShown as u (u.id)}
            <label class="flex items-center gap-2 border-b border-line-soft px-3 py-1.5 text-sm last:border-0 hover:bg-hover">
              <input type="checkbox" checked={form.member_ids.includes(u.id)} onchange={() => toggle(u.id)} />
              {u.display_name || u.username}
              <span class="text-xs text-faint">{u.username}{u.email ? ` · ${u.email}` : ''}</span>
            </label>
          {:else}
            <p class="px-3 py-3 text-xs text-muted">{t('admin.user.noMatch')}</p>
          {/each}
        </div>
        <Pager bind:page={memberPage} size={memberSize} total={candidates.length} />
      </div>
      {#if error}<p class="rounded-md bg-danger-soft px-3 py-2 text-xs text-danger">{error}</p>{/if}
    </form>

    {#snippet footer()}
      <button class="btn-outline" onclick={() => (editing = undefined)}>{t('common.cancel')}</button>
      <button class="btn-primary" form="group-form" disabled={busy}>{editing ? t('common.save') : t('admin.group.create')}</button>
    {/snippet}
  </Modal>
{/if}
