<script lang="ts">
  import { t, i18n, locales, setLocale } from '@/lib/i18n/index.svelte';
  import { Palette, Code2, UserRound, Bot, Database, Users, UsersRound, KeyRound, ScrollText, BookOpen, Timer, Moon, Sun, Monitor, Check, Info, SlidersHorizontal, PlugZap, Network } from 'lucide-svelte';
  import { session } from '@/lib/stores/session.svelte';
  import { setTheme, theme, type ThemeMode } from '@/lib/stores/theme.svelte';
  import { ws } from '@/lib/stores/workspace.svelte';
  import PasswordDialog from '@/lib/components/dialogs/PasswordDialog.svelte';
  import McpPanel from '@/lib/components/settings/McpPanel.svelte';
  import EditorSettings from '@/pages/settings/EditorSettings.svelte';
  import About from '@/pages/settings/About.svelte';
  import Connections from '@/pages/admin/Connections.svelte';
  import UsersPage from '@/pages/admin/Users.svelte';
  import Groups from '@/pages/admin/Groups.svelte';
  import Logs from '@/pages/admin/Logs.svelte';
  import Sso from '@/pages/admin/Sso.svelte';
  import OAuthClients from '@/pages/admin/OAuthClients.svelte';
  import DocsSchedule from '@/pages/admin/Docs.svelte';
  import Sessions from '@/pages/admin/Sessions.svelte';
  import ServerSettings from '@/pages/admin/ServerSettings.svelte';
  import Cluster from '@/pages/admin/Cluster.svelte';
  import MyConnections from '@/pages/settings/MyConnections.svelte';

  type Section = 'appearance' | 'editor' | 'account' | 'mcp' | 'myConnections' | 'about' | 'connections' | 'users' | 'groups' | 'sso' | 'oauthClients' | 'sessions' | 'server' | 'cluster' | 'docs' | 'logs';

  const isAdmin = $derived(session.user?.role === 'admin');

  // Own connections show up while an admin lets users create them.
  const canCreate = $derived(!!session.user?.can_create_connections);

  const personal = $derived<{ key: Section; label: string; icon: typeof Palette }[]>([
    { key: 'appearance', label: 'settings.appearance', icon: Palette },
    { key: 'editor', label: 'settings.editor', icon: Code2 },
    { key: 'account', label: 'settings.account', icon: UserRound },
    { key: 'mcp', label: 'settings.mcp', icon: Bot },
    ...(canCreate ? [{ key: 'myConnections' as Section, label: 'myConn.title', icon: PlugZap }] : []),
  ]);

  const adminItems: { key: Section; label: string; icon: typeof Palette }[] = [
    { key: 'connections', label: 'admin.connections', icon: Database },
    { key: 'users', label: 'admin.users', icon: Users },
    { key: 'groups', label: 'admin.groups', icon: UsersRound },
    { key: 'sso', label: 'admin.sso', icon: KeyRound },
    { key: 'oauthClients', label: 'admin.oauthClients.title', icon: Bot },
    { key: 'sessions', label: 'admin.sessions.title', icon: Timer },
    { key: 'server', label: 'admin.server.title', icon: SlidersHorizontal },
    { key: 'cluster', label: 'admin.cluster.title', icon: Network },
    { key: 'docs', label: 'admin.docs.title', icon: BookOpen },
    { key: 'logs', label: 'admin.logs', icon: ScrollText },
  ];

  const adminSections = adminItems.map((i) => i.key);

  // The section lives in the workspace, so it survives switching tabs.
  const section = $derived<Section>(
    (!isAdmin && adminSections.includes(ws.settingsSection as Section)) || (!canCreate && ws.settingsSection === 'myConnections')
      ? 'appearance'
      : ((ws.settingsSection as Section) || 'appearance'),
  );

  const themes: { mode: ThemeMode; icon: typeof Sun; key: string }[] = [
    { mode: 'light', icon: Sun, key: 'app.themeLight' },
    { mode: 'dark', icon: Moon, key: 'app.themeDark' },
    { mode: 'system', icon: Monitor, key: 'app.themeSystem' },
  ];

  let passwordOpen = $state(false);

</script>

<div class="flex h-full min-h-0">
  <nav class="w-52 shrink-0 overflow-y-auto border-r border-line bg-panel p-1.5" aria-label={t('settings.title')}>
    <p class="flex h-7 items-center px-2 text-xs font-semibold text-fg-2">{t('settings.personal')}</p>
    {#each personal as item (item.key)}
      {@const active = section === item.key}
      <button
        class="mb-px flex h-7 w-full items-center gap-2 rounded-md px-2 text-sm {active ? 'bg-row-active font-medium text-fg' : 'text-fg-2 hover:bg-hover'}"
        aria-current={active ? 'page' : undefined}
        onclick={() => (ws.settingsSection = item.key)}
      >
        <item.icon size={14} class={active ? 'text-accent' : 'text-muted'} />
        {t(item.label)}
      </button>
    {/each}

    {#if isAdmin}
      <p class="mt-3 flex h-7 items-center px-2 text-xs font-semibold text-fg-2">{t('app.admin')}</p>
      {#each adminItems as item (item.key)}
        {@const active = section === item.key}
        <button
          class="mb-px flex h-7 w-full items-center gap-2 rounded-md px-2 text-sm {active ? 'bg-row-active font-medium text-fg' : 'text-fg-2 hover:bg-hover'}"
          aria-current={active ? 'page' : undefined}
          onclick={() => (ws.settingsSection = item.key)}
        >
          <item.icon size={14} class={active ? 'text-accent' : 'text-muted'} />
          {t(item.label)}
        </button>
      {/each}
    {/if}

    <div class="mt-3 border-t border-line-soft pt-3">
      <button
        class="mb-px flex h-7 w-full items-center gap-2 rounded-md px-2 text-sm {section === 'about' ? 'bg-row-active font-medium text-fg' : 'text-fg-2 hover:bg-hover'}"
        aria-current={section === 'about' ? 'page' : undefined}
        onclick={() => (ws.settingsSection = 'about')}
      >
        <Info size={14} class={section === 'about' ? 'text-accent' : 'text-muted'} />
        {t('about.title')}
      </button>
    </div>
  </nav>

  <div class="min-w-0 flex-1 overflow-hidden bg-bg">
    {#if section === 'connections'}
      <Connections />
    {:else if section === 'myConnections'}
      <MyConnections />
    {:else if section === 'users'}
      <UsersPage />
    {:else if section === 'groups'}
      <Groups />
    {:else if section === 'sso'}
      <Sso />
    {:else if section === 'oauthClients'}
      <OAuthClients />
    {:else if section === 'sessions'}
      <Sessions />
    {:else if section === 'server'}
      <ServerSettings />
    {:else if section === 'cluster'}
      <Cluster />
    {:else if section === 'docs'}
      <DocsSchedule />
    {:else if section === 'logs'}
      <Logs />
    {:else}
      <div class="h-full overflow-y-auto">
        <div class="space-y-6 px-6 py-5 {section === 'editor' ? 'max-w-6xl' : 'max-w-3xl'}">
          {#if section === 'appearance'}
            <h2 class="text-base font-semibold">{t('settings.appearance')}</h2>
            <section>
              <span class="label">{t('app.theme')}</span>
              <div class="flex gap-2">
                {#each themes as th (th.mode)}
                  <button
                    class="flex h-9 items-center gap-2 rounded-md border px-3 text-sm {theme.mode === th.mode ? 'border-accent bg-accent-soft font-medium' : 'border-line hover:bg-hover'}"
                    aria-pressed={theme.mode === th.mode}
                    onclick={() => setTheme(th.mode)}
                  >
                    <th.icon size={14} />
                    {t(th.key)}
                  </button>
                {/each}
              </div>
            </section>
            <section>
              <span class="label">{t('app.language')}</span>
              <div class="flex flex-wrap gap-2">
                {#each locales as l (l.value)}
                  <button
                    class="flex h-9 items-center gap-2 rounded-md border px-3 text-sm {i18n.locale === l.value ? 'border-accent bg-accent-soft font-medium' : 'border-line hover:bg-hover'}"
                    aria-pressed={i18n.locale === l.value}
                    onclick={() => setLocale(l.value)}
                  >
                    {#if i18n.locale === l.value}<Check size={14} />{/if}
                    {l.label}
                  </button>
                {/each}
              </div>
            </section>
          {:else if section === 'editor'}
            <EditorSettings />
          {:else if section === 'account'}
            <h2 class="text-base font-semibold">{t('settings.account')}</h2>
            <dl class="grid max-w-md grid-cols-[8rem_1fr] gap-y-2 text-sm">
              <dt class="text-muted">{t('login.username')}</dt>
              <dd class="font-mono">{session.user?.username}</dd>
              <dt class="text-muted">{t('admin.user.displayName')}</dt>
              <dd>{session.user?.display_name || '—'}</dd>
              <dt class="text-muted">{t('admin.user.role')}</dt>
              <dd>{isAdmin ? t('admin.user.roleAdmin') : t('admin.user.roleUser')}</dd>
            </dl>
            <button class="btn-outline" onclick={() => (passwordOpen = true)}><KeyRound size={14} /> {t('app.changePassword')}</button>
          {:else if section === 'about'}
            <About />
          {:else if section === 'mcp'}
            <h2 class="text-base font-semibold">{t('mcp.title')}</h2>
            <McpPanel />
          {/if}
        </div>
      </div>
    {/if}
  </div>
</div>

{#if passwordOpen}
  <PasswordDialog onclose={() => (passwordOpen = false)} />
{/if}
