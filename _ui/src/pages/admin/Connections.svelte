<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { Plus, Pencil, Trash2, Lock, LockOpen, Users, UsersRound, KeyRound } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import ConnectionForm from '@/pages/admin/ConnectionForm.svelte';
  import DbIcon from '@/lib/components/ui/DbIcon.svelte';
  import { api } from '@/lib/api/client';
  import type { Connection } from '@/lib/api/types';
  import { admin, groupName, loadConnections, loadDrivers, loadGroups, loadUsers, userName } from '@/pages/admin/admin.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  let editing = $state<Connection | null | undefined>(undefined);
  let loaded = $state(false);

  async function load() {
    try {
      await Promise.all([loadConnections(), loadUsers(), loadGroups(), loadDrivers()]);
    } catch (e) {
      toast(errorMessage(e), 'error');
    } finally {
      loaded = true;
    }
  }

  void load();

  async function remove(c: Connection) {
    if (!confirm(t('admin.conn.deleteConfirm', { name: c.name }))) return;
    try {
      await api.del(`/admin/connections/${c.id}`);
      toast(t('admin.conn.deleted'), 'success');
      await loadConnections();
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }
</script>

<Page title={t('admin.connections')} description={t('admin.conn.description')}>
  {#snippet actions()}
    <button class="btn-primary" onclick={() => (editing = null)}><Plus size={14} /> {t('admin.conn.new')}</button>
  {/snippet}

  <table class="table">
      <thead>
        <tr>
          <th>{t('admin.conn.name')}</th>
          <th>{t('admin.conn.target')}</th>
          <th>{t('admin.conn.mode')}</th>
          <th>{t('admin.conn.access')}</th>
          <th class="w-24"></th>
        </tr>
      </thead>
      <tbody>
        {#each admin.connections as c (c.id)}
          {@const users = (c.access ?? []).filter((a) => a.subject_type === 'user')}
          {@const groups = (c.access ?? []).filter((a) => a.subject_type === 'group')}
          <tr>
            <td>
              <div class="flex items-center gap-2">
                <DbIcon type={c.db_type} dialect={c.options?.dialect ?? ''} />
                <span class="font-medium">{c.name}</span>
                {#if c.color && !c.production}<span class="size-2 rounded-full" style="background:{c.color}"></span>{/if}
              </div>
              {#if c.description}<div class="mt-0.5 text-xs text-muted">{c.description}</div>{/if}
            </td>
            <td class="font-mono text-xs text-fg-2">
              {c.username ? c.username + '@' : ''}{c.host}:{c.port || '—'}{c.database ? '/' + c.database : ''}
            </td>
            <td>
              <div class="flex flex-wrap gap-1">
                {#if c.production}<span class="pill-prod">{t('admin.production')}</span>{/if}
                {#if c.read_only}
                  <span class="pill-ro"><Lock size={10} /> {t('admin.readOnly')}</span>
                {:else}
                  <span class="pill-rw"><LockOpen size={10} /> {t('admin.readWrite')}</span>
                {/if}
                {#if c.user_credentials}<span class="pill-own" title={t('admin.conn.authPerUserHint')}><KeyRound size={10} /> {t('admin.conn.perUserChip')}</span>{/if}
              </div>
            </td>
            <td class="text-xs text-fg-2">
              {#if !c.access?.length}
                <span class="text-faint">{t('admin.conn.adminsOnly')}</span>
              {:else}
                <div class="flex flex-wrap gap-1">
                  {#each groups as a (a.subject_id)}
                    <span class="chip gap-1"><UsersRound size={11} />{groupName(a.subject_id)}{#if !a.read_only && !c.read_only}<span class="text-warn">· rw</span>{/if}</span>
                  {/each}
                  {#each users as a (a.subject_id)}
                    <span class="chip gap-1"><Users size={11} />{userName(a.subject_id)}{#if !a.read_only && !c.read_only}<span class="text-warn">· rw</span>{/if}</span>
                  {/each}
                </div>
              {/if}
            </td>
            <td class="w-20 text-right whitespace-nowrap">
              <button class="icon-btn" onclick={() => (editing = c)} aria-label={t('common.edit')}><Pencil size={14} /></button>
              <button class="icon-btn hover:text-danger" onclick={() => remove(c)} aria-label={t('common.delete')}><Trash2 size={14} /></button>
            </td>
          </tr>
        {:else}
          <tr>
            <td colspan="5" class="h-24 text-center text-muted">
              {loaded ? t('admin.conn.empty') : t('common.loading')}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
</Page>

{#if editing !== undefined}
  <ConnectionForm
    connection={editing}
    onclose={() => (editing = undefined)}
    onsaved={async () => {
      editing = undefined;
      await loadConnections();
    }}
  />
{/if}
