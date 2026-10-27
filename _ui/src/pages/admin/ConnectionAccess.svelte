<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { X, Users, UsersRound } from 'lucide-svelte';
  import type { Access } from '@/lib/api/types';
  import { admin, groupName, userName } from '@/pages/admin/admin.svelte';

  let { access = $bindable(), readOnly }: { access: Access[]; readOnly: boolean } = $props();

  let pick = $state('');

  function addAccess() {
    if (!pick) return;
    const [subject_type, subject_id] = pick.split(':') as ['user' | 'group', string];
    if (!access.some((a) => a.subject_type === subject_type && a.subject_id === subject_id)) {
      access.push({ subject_type, subject_id, read_only: true });
    }
    pick = '';
  }

  const available = $derived({
    groups: admin.groups.filter((g) => !access.some((a) => a.subject_type === 'group' && a.subject_id === g.id)),
    users: admin.users.filter((u) => u.role !== 'admin' && !access.some((a) => a.subject_type === 'user' && a.subject_id === u.id)),
  });
</script>

<section>
  <span class="label">{t('admin.conn.whoCanUse')}</span>
  <p class="mb-2 text-xs text-muted">
    {t('admin.conn.whoCanUseHint')}
  </p>
  <div class="mb-2 flex gap-2">
    <select class="input" bind:value={pick}>
      <option value="">{t('admin.conn.addSubject')}</option>
      {#if available.groups.length}
        <optgroup label={t('admin.groups')}>
          {#each available.groups as g (g.id)}<option value={'group:' + g.id}>{g.name}</option>{/each}
        </optgroup>
      {/if}
      {#if available.users.length}
        <optgroup label={t('admin.users')}>
          {#each available.users as u (u.id)}<option value={'user:' + u.id}>{u.display_name || u.username}</option>{/each}
        </optgroup>
      {/if}
    </select>
    <button type="button" class="btn-outline" onclick={addAccess} disabled={!pick}>{t('common.add')}</button>
  </div>
  <div class="divide-y divide-line-soft rounded-md border border-line">
    {#each access as a, i (a.subject_type + a.subject_id)}
      <div class="flex items-center gap-2 px-3 py-1.5 text-sm">
        {#if a.subject_type === 'group'}<UsersRound size={14} class="text-muted" />{:else}<Users size={14} class="text-muted" />{/if}
        <span class="flex-1">{a.subject_type === 'group' ? groupName(a.subject_id) : userName(a.subject_id)}</span>
        <select class="h-7 rounded border border-line bg-bg px-1.5 text-xs" bind:value={a.read_only} disabled={readOnly}>
          <option value={true}>{t('admin.readOnly')}</option>
          <option value={false}>{t('admin.readWrite')}</option>
        </select>
        <button type="button" class="icon-btn size-6" onclick={() => access.splice(i, 1)} aria-label={t('admin.remove')}><X size={13} /></button>
      </div>
    {:else}
      <p class="px-3 py-3 text-center text-xs text-faint">{t('admin.conn.nobody')}</p>
    {/each}
  </div>
</section>
