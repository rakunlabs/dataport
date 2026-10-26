<script lang="ts">
  import { link } from 'svelte-spa-router';
  import { FilePlus2, History, Bookmark, Moon, Sun, Monitor, Settings, LogOut, KeyRound, ChevronDown, Check, Bot } from 'lucide-svelte';
  import Logo from '@/lib/components/Logo.svelte';
  import { logout, session } from '@/lib/stores/session.svelte';
  import { setTheme, theme, type ThemeMode } from '@/lib/stores/theme.svelte';
  import { activeTab, openQueryTab, openSettings, ws } from '@/lib/stores/workspace.svelte';
  import { i18n, locales, setLocale, t } from '@/lib/i18n/index.svelte';
  import PasswordDialog from '@/lib/components/PasswordDialog.svelte';

  let menuOpen = $state(false);
  let passwordOpen = $state(false);

  const themes: { mode: ThemeMode; icon: typeof Sun; key: string }[] = [
    { mode: 'system', icon: Monitor, key: 'app.themeSystem' },
    { mode: 'light', icon: Sun, key: 'app.themeLight' },
    { mode: 'dark', icon: Moon, key: 'app.themeDark' },
  ];

  const name = $derived(session.user?.display_name || session.user?.username || '');

  // One click moves to the next theme: system, light, dark.
  const current = $derived(themes.find((th) => th.mode === theme.mode) ?? themes[0]);

  function nextTheme() {
    const i = themes.findIndex((th) => th.mode === theme.mode);
    setTheme(themes[(i + 1) % themes.length].mode);
  }

  function newQuery() {
    const conn = activeTab()?.connectionId ?? ws.connections[0]?.id;
    if (conn) openQueryTab(conn);
  }

  function togglePanel(p: 'history' | 'saved') {
    ws.rightPanel = ws.rightPanel === p ? '' : p;
  }
</script>

<svelte:window onclick={() => (menuOpen = false)} onkeydown={(e) => e.key === 'Escape' && (menuOpen = false)} />

<header class="flex h-[38px] shrink-0 items-center gap-1 border-b border-line bg-panel pr-2 pl-3">
  <a href="/" use:link class="mr-2 flex items-center gap-2 rounded-md py-1 pr-1.5 text-sm font-semibold tracking-[-0.01em] text-fg">
    <Logo size={20} />
    dataport
  </a>

  <button class="btn" onclick={newQuery} disabled={ws.connections.length === 0}>
    <FilePlus2 size={14} /> {t('app.newQuery')}
  </button>

  <div class="flex-1"></div>

  <button
    class="icon-btn"
    aria-pressed={ws.rightPanel === 'history'}
    class:bg-active={ws.rightPanel === 'history'}
    onclick={() => togglePanel('history')}
    title={t('app.history')}
    aria-label={t('app.history')}
  >
    <History size={15} />
  </button>
  <button
    class="icon-btn"
    aria-pressed={ws.rightPanel === 'saved'}
    class:bg-active={ws.rightPanel === 'saved'}
    onclick={() => togglePanel('saved')}
    title={t('app.saved')}
    aria-label={t('app.saved')}
  >
    <Bookmark size={15} />
  </button>
  <button class="icon-btn" onclick={nextTheme} title="{t('app.theme')}: {t(current.key)}" aria-label="{t('app.theme')}: {t(current.key)}">
    <current.icon size={15} />
  </button>
  <button
    class="icon-btn"
    aria-pressed={activeTab()?.kind === 'settings'}
    class:bg-active={activeTab()?.kind === 'settings'}
    onclick={() => openSettings()}
    title={t('settings.title')}
    aria-label={t('settings.title')}
  >
    <Settings size={15} />
  </button>

  <div class="toolbar-sep"></div>

  <div class="relative">
    <button
      class="flex h-[26px] items-center gap-1 rounded-md pr-1 pl-2 text-sm text-fg-2 hover:bg-hover hover:text-fg"
      aria-haspopup="menu"
      aria-expanded={menuOpen}
      onclick={(e) => {
        e.stopPropagation();
        menuOpen = !menuOpen;
      }}
    >
      <span class="max-w-40 truncate">{name}</span>
      <ChevronDown size={13} class="text-faint" />
    </button>

    {#if menuOpen}
      <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
      <div class="popover absolute top-8 right-0 z-40 w-64" role="menu" tabindex="-1" onclick={(e) => e.stopPropagation()}>
        <div class="px-3 pt-1.5 pb-2">
          <div class="truncate text-sm font-medium">{name}</div>
          <div class="truncate text-xs text-muted">
            {session.user?.username} · {session.user?.role === 'admin' ? t('admin.user.roleAdmin') : t('admin.user.roleUser')}
          </div>
        </div>
        <div class="menu-sep"></div>

        <div class="px-3 py-1.5">
          <div class="mb-1.5 text-xs text-muted">{t('app.language')}</div>
          <div class="segmented w-full">
            {#each locales as l (l.value)}
              <button class="flex-1 justify-center" aria-pressed={i18n.locale === l.value} onclick={() => setLocale(l.value)}>
                {#if i18n.locale === l.value}<Check size={12} />{/if}
                {l.label}
              </button>
            {/each}
          </div>
        </div>

        <div class="menu-sep"></div>
        <button
          class="menu-item"
          role="menuitem"
          onclick={() => {
            passwordOpen = true;
            menuOpen = false;
          }}
        >
          <KeyRound size={14} /> {t('app.changePassword')}
        </button>
        <button
          class="menu-item"
          role="menuitem"
          onclick={() => {
            openSettings('mcp');
            menuOpen = false;
          }}
        >
          <Bot size={14} /> {t('app.mcpAccess')}
        </button>
        <button class="menu-item" role="menuitem" onclick={logout}>
          <LogOut size={14} /> {t('app.signOut')}
        </button>
      </div>
    {/if}
  </div>
</header>

{#if passwordOpen}
  <PasswordDialog onclose={() => (passwordOpen = false)} />
{/if}

