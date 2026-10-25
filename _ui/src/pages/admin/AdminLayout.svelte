<script lang="ts">
  import { link, router } from 'svelte-spa-router';
  import { Database, Users, UsersRound, ScrollText, KeyRound } from 'lucide-svelte';
  import { t } from '@/lib/i18n/index.svelte';
  import TopBar from '@/lib/components/TopBar.svelte';
  import Connections from '@/pages/admin/Connections.svelte';
  import UsersPage from '@/pages/admin/Users.svelte';
  import Groups from '@/pages/admin/Groups.svelte';
  import Logs from '@/pages/admin/Logs.svelte';
  import Sso from '@/pages/admin/Sso.svelte';

  const items = [
    { path: '/admin/connections', key: 'admin.connections', icon: Database },
    { path: '/admin/users', key: 'admin.users', icon: Users },
    { path: '/admin/groups', key: 'admin.groups', icon: UsersRound },
    { path: '/admin/sso', key: 'admin.sso', icon: KeyRound },
    { path: '/admin/logs', key: 'admin.logs', icon: ScrollText },
  ];

  const section = $derived(router.location.split('/')[2] || 'connections');
</script>

<div class="flex h-full flex-col">
  <TopBar admin />
  <div class="flex min-h-0 flex-1">
    <nav class="w-52 shrink-0 border-r border-line bg-panel p-1.5" aria-label={t('app.admin')}>
      <p class="flex h-7 items-center px-2 text-xs font-semibold text-fg-2">{t('app.admin')}</p>
      {#each items as item (item.path)}
        {@const active = item.path.endsWith('/' + section)}
        <a
          href={item.path}
          use:link
          class="mb-px flex h-7 items-center gap-2 rounded-md px-2 text-sm {active ? 'bg-row-active font-medium text-fg' : 'text-fg-2 hover:bg-hover'}"
          aria-current={active ? 'page' : undefined}
        >
          <item.icon size={14} class={active ? 'text-accent' : 'text-muted'} />
          {t(item.key)}
        </a>
      {/each}
    </nav>
    <main class="min-w-0 flex-1 overflow-hidden">
      {#if section === 'users'}
        <UsersPage />
      {:else if section === 'groups'}
        <Groups />
      {:else if section === 'logs'}
        <Logs />
      {:else if section === 'sso'}
        <Sso />
      {:else}
        <Connections />
      {/if}
    </main>
  </div>
</div>
