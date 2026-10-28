<script lang="ts">
  import { t } from '@/lib/i18n/index.svelte';
  import { Plus, Pencil, Trash2, Lock, LockOpen } from 'lucide-svelte';
  import Page from '@/pages/admin/Page.svelte';
  import ConnectionForm from '@/pages/admin/ConnectionForm.svelte';
  import DbIcon from '@/lib/components/ui/DbIcon.svelte';
  import { api } from '@/lib/api/client';
  import type { Connection, PersonalConnectionList } from '@/lib/api/types';
  import { loadConnections } from '@/lib/stores/workspace.svelte';
  import { errorMessage, toast } from '@/lib/stores/toast.svelte';

  // Connections the user created for themselves, while an admin allows it.
  let list = $state<PersonalConnectionList | null>(null);
  let editing = $state<Connection | null | undefined>(undefined);

  async function load() {
    try {
      list = await api.get<PersonalConnectionList>('/my-connections');
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }

  void load();

  async function reload() {
    await load();
    void loadConnections();
  }

  async function remove(c: Connection) {
    if (!confirm(t('myConn.deleteConfirm', { name: c.name }))) return;
    try {
      await api.del(`/my-connections/${c.id}`);
      toast(t('admin.conn.deleted'), 'success');
      await reload();
    } catch (e) {
      toast(errorMessage(e), 'error');
    }
  }
</script>

<Page title={t('myConn.title')} description={t('myConn.description')}>
  {#snippet actions()}
    {#if list?.enabled}
      <button class="btn-primary" onclick={() => (editing = null)}><Plus size={14} /> {t('admin.conn.new')}</button>
    {/if}
  {/snippet}

  {#if list && !list.enabled}
    <p class="m-4 rounded-md bg-warn-soft px-3 py-2 text-sm text-warn">{t('myConn.disabled')}</p>
  {:else}
    <table class="table">
      <thead>
        <tr>
          <th>{t('admin.conn.name')}</th>
          <th>{t('admin.conn.target')}</th>
          <th>{t('admin.conn.mode')}</th>
          <th class="w-24"></th>
        </tr>
      </thead>
      <tbody>
        {#each list?.connections ?? [] as c (c.id)}
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
              </div>
            </td>
            <td class="w-20 text-right whitespace-nowrap">
              <button class="icon-btn" onclick={() => (editing = c)} aria-label={t('common.edit')}><Pencil size={14} /></button>
              <button class="icon-btn hover:text-danger" onclick={() => remove(c)} aria-label={t('common.delete')}><Trash2 size={14} /></button>
            </td>
          </tr>
        {:else}
          <tr>
            <td colspan="4" class="h-24 text-center text-muted">{list ? t('myConn.empty') : t('common.loading')}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
</Page>

{#if editing !== undefined}
  <ConnectionForm
    connection={editing}
    personal
    onclose={() => (editing = undefined)}
    onsaved={async () => {
      editing = undefined;
      await reload();
    }}
  />
{/if}
